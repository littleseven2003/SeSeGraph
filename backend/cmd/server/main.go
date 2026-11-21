package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/littleseven2003/SeSeGraph/backend/internal/api"
	"github.com/littleseven2003/SeSeGraph/backend/internal/auth"
	"github.com/littleseven2003/SeSeGraph/backend/internal/config"
	"github.com/littleseven2003/SeSeGraph/backend/internal/db"
	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
	"github.com/littleseven2003/SeSeGraph/backend/internal/publisher"
	"github.com/littleseven2003/SeSeGraph/backend/internal/settings"
	"github.com/littleseven2003/SeSeGraph/backend/internal/storage"
	"github.com/littleseven2003/SeSeGraph/backend/internal/telegraph"
	"github.com/littleseven2003/SeSeGraph/backend/internal/web"
	"github.com/littleseven2003/SeSeGraph/backend/internal/worker"
)

// 构建时通过 -ldflags 注入，未注入时使用开发默认值。
var (
	version = "dev"
	commit  = "unknown"
)

// databaseFileName 是数据目录下的数据库文件名。
const databaseFileName = "sesegraph.db"

func main() {
	if err := run(os.Args[1:], os.LookupEnv); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, "用法: sesegraph [--addr 监听地址] [--data 数据目录] [--public-url 对外访问地址]")
			return
		}
		fmt.Fprintln(os.Stderr, "启动失败: "+err.Error())
		os.Exit(1)
	}
}

// run 加载配置、准备数据库与鉴权，然后启动 HTTP 服务。
func run(args []string, lookupEnv func(string) (string, bool)) error {
	cfg, err := config.Load(args, lookupEnv)
	if err != nil {
		return err
	}

	gin.SetMode(gin.ReleaseMode)

	store, err := db.Open(filepath.Join(cfg.DataDir, databaseFileName))
	if err != nil {
		return errDatabase
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	if err := db.Migrate(ctx, store); err != nil {
		return errDatabase
	}

	authService := auth.NewService(store)
	secret, err := authService.SessionSecret(ctx)
	if err != nil {
		return errDatabase
	}
	if err := bootstrapAdmin(ctx, authService, lookupEnv); err != nil {
		return err
	}

	repository := posts.NewRepository(store)
	assets, err := web.Assets()
	if err != nil {
		return errAssets
	}

	streams := buildStreamProvider(lookupEnv)
	telegraphManager := buildTelegraphManager(store, lookupEnv)

	router := api.NewRouter(api.Deps{
		Version:          version,
		Commit:           commit,
		Auth:             authService,
		Sessions:         auth.NewSessions(secret, auth.DefaultSessionTTL, isHTTPS(cfg.PublicURL)),
		SetupToken:       readSetupToken(lookupEnv),
		WebFS:            assets,
		DataDir:          cfg.DataDir,
		Posts:            repository,
		Streams:          streams,
		Telegraph:        telegraphManager,
		PublicURL:        cfg.PublicURL,
		ImageConcurrency: readImageConcurrency(lookupEnv),
	})

	stopWorker := startUploadQueue(ctx, repository, cfg.DataDir, streams, lookupEnv)
	defer stopWorker()

	stopPublisher := startPublisher(ctx, repository, telegraphManager, cfg.PublicURL)
	defer stopPublisher()

	// 只记录版本信息，监听地址与数据目录属于本地部署细节，不写入日志。
	log.Printf("服务启动：版本 %s（提交 %s）", version, commit)

	if err := router.Run(cfg.Addr); err != nil {
		// 监听失败的错误会带上地址等原始配置，这里统一替换为固定描述。
		return errServerStart
	}
	return nil
}

// bootstrapAdmin 在配置了环境变量账号密码时完成首次初始化。
// 已初始化时静默跳过，不覆盖已有账号。
func bootstrapAdmin(ctx context.Context, service *auth.Service, lookupEnv func(string) (string, bool)) error {
	username, _ := lookupEnv("ADMIN_USERNAME")
	password, _ := lookupEnv("ADMIN_PASSWORD")
	if strings.TrimSpace(username) == "" || password == "" {
		return nil
	}

	err := service.Create(ctx, username, password)
	switch {
	case err == nil:
		log.Print("管理员账号已通过环境变量完成初始化")
		return nil
	case errors.Is(err, auth.ErrAlreadyInitialized):
		return nil
	case errors.Is(err, auth.ErrInvalidInput):
		return errAdminEnv
	default:
		return errDatabase
	}
}

// buildStreamProvider 按环境变量创建图床提供方。未配置或配置不合法时返回空，
// 由调用方决定跳过上传队列，并让图片网关返回不可用。
func buildStreamProvider(lookupEnv func(string) (string, bool)) storage.StreamProvider {
	token, chatID, _ := readTelegramConfig(lookupEnv)
	provider, err := storage.NewTelegram(storage.TelegramConfig{
		Token:   token,
		ChatID:  chatID,
		APIBase: readTelegramAPIBase(lookupEnv),
	})
	switch {
	case errors.Is(err, storage.ErrNotConfigured):
		log.Print("图床未配置，上传队列未启动")
		return nil
	case err != nil:
		log.Print("图床配置不可用，上传队列未启动")
		return nil
	}
	return provider
}

// startPublisher 在 Telegraph 已配置且对外地址为 HTTPS 时启动发布队列。
func startPublisher(ctx context.Context, repository *posts.Repository, manager *telegraph.Manager, publicURL string) func() {
	if manager == nil {
		log.Print("Telegraph 未配置，发布队列未启动")
		return func() {}
	}
	if !isHTTPS(publicURL) {
		log.Print("对外地址不是 HTTPS，发布队列未启动")
		return func() {}
	}
	if _, err := manager.Client(ctx); err != nil {
		log.Print("Telegraph 未配置，发布队列等待建号后启动")
	}
	queue, err := publisher.New(publisher.Options{
		Repository: repository,
		PublicURL:  publicURL,
		Pages: func(ctx context.Context) (publisher.PageAPI, error) {
			return manager.Client(ctx)
		},
	})
	if err != nil {
		log.Print("发布队列初始化失败")
		return func() {}
	}
	if _, err := queue.Recover(ctx); err != nil {
		log.Print("发布队列恢复失败")
	}

	runCtx, cancel := context.WithCancel(ctx)
	go queue.Run(runCtx)
	log.Print("发布队列已启动")
	return cancel
}

// buildTelegraphManager 创建 Telegraph 账户管理，环境变量令牌优先于数据库。
func buildTelegraphManager(store *sql.DB, lookupEnv func(string) (string, bool)) *telegraph.Manager {
	token, _ := lookupEnv("TELEGRAPH_ACCESS_TOKEN")
	author, _ := lookupEnv("TELEGRAPH_AUTHOR_NAME")
	apiBase, _ := lookupEnv("TELEGRAPH_API_BASE")
	return telegraph.NewManager(telegraph.Config{
		AccessToken: strings.TrimSpace(token),
		AuthorName:  strings.TrimSpace(author),
		APIBase:     strings.TrimSpace(apiBase),
	}, settings.NewStore(store))
}

// readImageConcurrency 读取图片网关并发上限，未配置时使用默认值。
func readImageConcurrency(lookupEnv func(string) (string, bool)) int {
	raw, ok := lookupEnv("PROXY_CONCURRENCY")
	if !ok {
		return api.DefaultImageConcurrency
	}
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return api.DefaultImageConcurrency
	}
	if value > 100 {
		return 100
	}
	return value
}

// startUploadQueue 在提供方可用时启动上传队列，返回停止函数。
func startUploadQueue(ctx context.Context, repository *posts.Repository, dataDir string, provider storage.StreamProvider, lookupEnv func(string) (string, bool)) func() {
	if provider == nil {
		return func() {}
	}
	_, _, interval := readTelegramConfig(lookupEnv)

	queue, err := worker.New(worker.Options{
		Repository: repository,
		Provider:   provider,
		DataDir:    dataDir,
		Interval:   interval,
	})
	if err != nil {
		log.Print("上传队列初始化失败")
		return func() {}
	}

	if _, err := queue.Recover(ctx); err != nil {
		log.Print("上传队列恢复失败")
	}

	queueCtx, cancel := context.WithCancel(ctx)
	go queue.Run(queueCtx)
	log.Print("上传队列已启动")
	return cancel
}

// readTelegramConfig 读取图床凭据与上传间隔，未配置的项留空。
func readTelegramConfig(lookupEnv func(string) (string, bool)) (string, string, time.Duration) {
	token, _ := lookupEnv("TELEGRAM_BOT_TOKEN")
	chatID, _ := lookupEnv("TELEGRAM_CHANNEL_ID")

	interval := worker.DefaultInterval
	if raw, ok := lookupEnv("UPLOAD_INTERVAL_MS"); ok {
		if ms, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil && ms >= 0 {
			interval = time.Duration(ms) * time.Millisecond
		}
	}
	return strings.TrimSpace(token), strings.TrimSpace(chatID), interval
}

// readTelegramAPIBase 读取可选的上游地址，便于接入自建反代或本地联调。
func readTelegramAPIBase(lookupEnv func(string) (string, bool)) string {
	value, _ := lookupEnv("TELEGRAM_API_BASE")
	return strings.TrimSpace(value)
}

// readSetupToken 读取网页初始化使用的一次性凭证，未配置时为空。
func readSetupToken(lookupEnv func(string) (string, bool)) string {
	value, _ := lookupEnv("ADMIN_SETUP_TOKEN")
	return strings.TrimSpace(value)
}

// isHTTPS 判断对外地址是否使用 HTTPS，用于决定会话 Cookie 是否带 Secure。
func isHTTPS(publicURL string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(publicURL)), "https://")
}

// errServerStart 是启动失败时对外的固定说明，避免回显配置取值。
var errServerStart = errors.New("HTTP 服务启动失败，请检查监听地址是否可用")

// errDatabase 是数据库或鉴权初始化失败时的固定说明，不包含路径等本地信息。
var errDatabase = errors.New("数据库初始化失败，请检查数据目录是否可写")

// errAdminEnv 说明环境变量中的管理员凭据不符合要求，同样不回显取值。
var errAdminEnv = errors.New("环境变量中的管理员账号或密码不符合要求")

// errAssets 表示嵌入的前端资源无法读取。
var errAssets = errors.New("前端资源加载失败")
