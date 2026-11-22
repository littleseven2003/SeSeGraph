package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/littleseven2003/SeSeGraph/backend/internal/api"
	"github.com/littleseven2003/SeSeGraph/backend/internal/auth"
	"github.com/littleseven2003/SeSeGraph/backend/internal/config"
	"github.com/littleseven2003/SeSeGraph/backend/internal/db"
	"github.com/littleseven2003/SeSeGraph/backend/internal/netclient"
	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
	"github.com/littleseven2003/SeSeGraph/backend/internal/provider"
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

	settingsStore := settings.NewStore(store)
	resolver := settings.NewResolver(settingsStore, settings.Env{
		TelegramToken:    envValue(lookupEnv, "TELEGRAM_BOT_TOKEN"),
		TelegramChannel:  envValue(lookupEnv, "TELEGRAM_CHANNEL_ID"),
		TelegramAPIBase:  envValue(lookupEnv, "TELEGRAM_API_BASE"),
		TelegraphToken:   envValue(lookupEnv, "TELEGRAPH_ACCESS_TOKEN"),
		TelegraphAuthor:  envValue(lookupEnv, "TELEGRAPH_AUTHOR_NAME"),
		TelegraphAPIBase: envValue(lookupEnv, "TELEGRAPH_API_BASE"),
	})
	effective, err := resolver.Effective(ctx)
	if err != nil {
		return errDatabase
	}

	telegraphManager := buildTelegraphManager(store, effective, lookupEnv)
	switcher := storage.NewSwitcher()
	// gate 由上传队列与切换守卫共用：换 Bot 前先独占，确认没有在途任务。
	gate := storage.NewUploadGate()
	guard := provider.NewGuard(switcher, settingsStore, repository.CountStoredImages, gate)
	// 旧库可能已有图片但缺少归属记录：首次绑定时用候选凭据确认旧图可读再记录归属。
	guard.SetLegacySamples(repository.SampleStoredFileIDs)

	// activate 按生效配置校验并激活图床；切换失败时保留原有提供方。
	activate := func(ctx context.Context) error {
		current, resolveErr := resolver.Effective(ctx)
		if resolveErr != nil {
			return resolveErr
		}
		candidate, buildErr := newProvider(current)
		if buildErr != nil {
			return buildErr
		}
		return guard.Activate(ctx, candidate)
	}
	if err := activate(ctx); err != nil {
		if errors.Is(err, provider.ErrBotMismatch) {
			log.Print("图库已有其他 Bot 上传的图片，图床未激活")
		} else {
			log.Print("图床配置不可用，上传队列等待配置")
		}
	}

	settingsLock := &sync.Mutex{}
	prepareProvider := func(ctx context.Context, expected settings.Effective) (*provider.Candidate, error) {
		candidate, err := newProvider(expected)
		if err != nil {
			return nil, err
		}
		return guard.Prepare(ctx, candidate)
	}
	commitProvider := func(ctx context.Context, candidate *provider.Candidate) {
		guard.Commit(ctx, candidate)
	}

	// reloadSettings 在配置落库并切换图床后收尾：按最新的代理配置重建 Telegraph 客户端，
	// 下次发帖使用新的传输层、作者名与令牌。
	reloadSettings := func(ctx context.Context) error {
		if telegraphManager == nil {
			return nil
		}
		current, resolveErr := resolver.Effective(ctx)
		if resolveErr != nil {
			return resolveErr
		}
		telegraphManager.Reload(telegraphClient(current))
		return nil
	}

	editor, editorErr := publisher.NewEditor(repository, func(ctx context.Context) (publisher.PageAPI, error) {
		if telegraphManager == nil {
			return nil, telegraph.ErrNotConfigured
		}
		return telegraphManager.Client(ctx)
	}, cfg.PublicURL)
	if editorErr != nil {
		return errEditorConfig
	}

	router := api.NewRouter(api.Deps{
		Version:          version,
		Commit:           commit,
		Auth:             authService,
		Sessions:         auth.NewSessions(secret, auth.DefaultSessionTTL, isHTTPS(cfg.PublicURL)),
		SetupToken:       readSetupToken(lookupEnv),
		WebFS:            assets,
		DataDir:          cfg.DataDir,
		Posts:            repository,
		Streams:          switcher,
		Telegraph:        telegraphManager,
		PublicURL:        cfg.PublicURL,
		Editor:           editor,
		Settings:         resolver,
		Reload:           reloadSettings,
		PrepareProvider:  prepareProvider,
		CommitProvider:   commitProvider,
		SettingsLock:     settingsLock,
		ImageConcurrency: readImageConcurrency(lookupEnv),
	})

	stopWorker := startUploadQueue(ctx, repository, cfg.DataDir, switcher, gate, lookupEnv)
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

// newProvider 按生效配置创建图床提供方；未配置时返回 nil，由调用方决定如何呈现。
func newProvider(effective settings.Effective) (*storage.Telegram, error) {
	client, err := netclient.NewClient(effective.ProxyConfig(), netclient.ServiceTelegram)
	if err != nil {
		return nil, err
	}
	provider, err := storage.NewTelegram(storage.TelegramConfig{
		Token:      effective.TelegramToken.Value,
		ChatID:     effective.TelegramChannel.Value,
		APIBase:    effective.TelegramAPIBase.Value,
		HTTPClient: client,
	})
	if errors.Is(err, storage.ErrNotConfigured) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return provider, nil
}

// telegraphClient 按代理配置构建 Telegraph 传输层，未启用代理时为直连。
func telegraphClient(effective settings.Effective) *http.Client {
	client, err := netclient.NewClient(effective.ProxyConfig(), netclient.ServiceTelegraph)
	if err != nil {
		return nil
	}
	return client
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
// 上游地址取生效配置，便于在没有环境变量时使用设置页保存的地址。
func buildTelegraphManager(store *sql.DB, effective settings.Effective, lookupEnv func(string) (string, bool)) *telegraph.Manager {
	token, _ := lookupEnv("TELEGRAPH_ACCESS_TOKEN")
	author, _ := lookupEnv("TELEGRAPH_AUTHOR_NAME")
	return telegraph.NewManager(telegraph.Config{
		AccessToken: strings.TrimSpace(token),
		AuthorName:  strings.TrimSpace(author),
		HTTPClient:  telegraphClient(effective),
		APIBase:     effective.TelegraphAPIBase.Value,
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

// startUploadQueue 启动上传队列，返回停止函数。
// 队列始终运行：图床未配置时按提供方的就绪状态等待，配置保存后自动接手。
func startUploadQueue(ctx context.Context, repository *posts.Repository, dataDir string, provider storage.Provider, gate *storage.UploadGate, lookupEnv func(string) (string, bool)) func() {
	interval := readUploadInterval(lookupEnv)

	queue, err := worker.New(worker.Options{
		Repository: repository,
		Provider:   provider,
		DataDir:    dataDir,
		Interval:   interval,
		Gate:       gate,
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

// readUploadInterval 读取上传间隔，未配置或取值不合法时使用默认值。
func readUploadInterval(lookupEnv func(string) (string, bool)) time.Duration {
	raw, ok := lookupEnv("UPLOAD_INTERVAL_MS")
	if !ok {
		return worker.DefaultInterval
	}
	ms, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || ms < 0 {
		return worker.DefaultInterval
	}
	return time.Duration(ms) * time.Millisecond
}

// envValue 读取环境变量并去除首尾空白。
func envValue(lookupEnv func(string) (string, bool), key string) string {
	value, _ := lookupEnv(key)
	return strings.TrimSpace(value)
}

// readSetupToken 读取网页初始化使用的一次性凭证，未配置时为空。
func readSetupToken(lookupEnv func(string) (string, bool)) string {
	value, _ := lookupEnv("ADMIN_SETUP_TOKEN")
	return strings.TrimSpace(value)
}

// isHTTPS 判断对外地址是否使用 HTTPS，用于决定会话 Cookie 是否带 Secure。
// 地址结构（查询串、片段、内嵌凭据）由 config 统一校验，避免拼接出无法访问的链接。
func isHTTPS(publicURL string) bool {
	return config.PublicURLIsHTTPS(publicURL)
}

// errServerStart 是启动失败时对外的固定说明，避免回显配置取值。
var errServerStart = errors.New("HTTP 服务启动失败，请检查监听地址是否可用")

// errDatabase 是数据库或鉴权初始化失败时的固定说明，不包含路径等本地信息。
var errDatabase = errors.New("数据库初始化失败，请检查数据目录是否可写")

// errEditorConfig 表示在线编辑依赖装配失败，不回显具体原因。
var errEditorConfig = errors.New("在线编辑初始化失败")

// errAdminEnv 说明环境变量中的管理员凭据不符合要求，同样不回显取值。
var errAdminEnv = errors.New("环境变量中的管理员账号或密码不符合要求")

// errAssets 表示嵌入的前端资源无法读取。
var errAssets = errors.New("前端资源加载失败")
