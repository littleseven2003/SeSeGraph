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
	"github.com/littleseven2003/SeSeGraph/backend/internal/backup"
	"github.com/littleseven2003/SeSeGraph/backend/internal/botpool"
	"github.com/littleseven2003/SeSeGraph/backend/internal/config"
	"github.com/littleseven2003/SeSeGraph/backend/internal/db"
	"github.com/littleseven2003/SeSeGraph/backend/internal/netclient"
	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
	"github.com/littleseven2003/SeSeGraph/backend/internal/provider"
	"github.com/littleseven2003/SeSeGraph/backend/internal/publisher"
	"github.com/littleseven2003/SeSeGraph/backend/internal/publishgate"
	"github.com/littleseven2003/SeSeGraph/backend/internal/settings"
	"github.com/littleseven2003/SeSeGraph/backend/internal/storage"
	"github.com/littleseven2003/SeSeGraph/backend/internal/telegraph"
	"github.com/littleseven2003/SeSeGraph/backend/internal/traffic"
	"github.com/littleseven2003/SeSeGraph/backend/internal/views"
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
		TelegramToken:     envValue(lookupEnv, "TELEGRAM_BOT_TOKEN"),
		TelegramChannel:   envValue(lookupEnv, "TELEGRAM_CHANNEL_ID"),
		TelegramAPIBase:   envValue(lookupEnv, "TELEGRAM_API_BASE"),
		TelegraphToken:    envValue(lookupEnv, "TELEGRAPH_ACCESS_TOKEN"),
		TelegraphAuthor:   envValue(lookupEnv, "TELEGRAPH_AUTHOR_NAME"),
		TelegraphAPIBase:  envValue(lookupEnv, "TELEGRAPH_API_BASE"),
		ProxyMode:         envValue(lookupEnv, "PROXY_MODE"),
		ProxyURL:          envValue(lookupEnv, "PROXY_URL"),
		ProxyTelegram:     envValue(lookupEnv, "PROXY_FOR_TELEGRAM"),
		ProxyTelegraph:    envValue(lookupEnv, "PROXY_FOR_TELEGRAPH"),
		UploadMode:        envValue(lookupEnv, "UPLOAD_MODE"),
		TelegraphPathMode: envValue(lookupEnv, "TELEGRAPH_PATH_MODE"),
		TrafficAlertBytes: envNumberString(readGigabytes(lookupEnv, "TRAFFIC_ALERT_GB")),
		TrafficLimitBytes: envNumberString(readGigabytes(lookupEnv, "TRAFFIC_LIMIT_GB")),
		BackupEnabled:     envValue(lookupEnv, "BACKUP_ENABLED"),
		BackupTime:        backupTimeEnv(lookupEnv),
		BackupKeep:        envValue(lookupEnv, "BACKUP_KEEP"),
		MediaThumbLimit:   envValue(lookupEnv, "MEDIA_THUMB_LIMIT_BYTES"),
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

	// 浏览量同步：低频拉取 Telegraph 浏览量写回本地缓存，接口只读缓存。
	// 浏览量读取不需要发布令牌，单独按最新设置构建无令牌客户端，沿用同一套代理与超时。
	viewsService, viewsErr := views.New(views.Options{
		Store: repository,
		Client: func(ctx context.Context) (views.Fetcher, error) {
			current, resolveErr := resolver.Effective(ctx)
			if resolveErr != nil {
				return nil, resolveErr
			}
			return telegraph.New(telegraph.Config{
				HTTPClient: telegraphClient(current),
				APIBase:    current.TelegraphAPIBase.Value,
			}), nil
		},
		Interval: readViewsInterval(lookupEnv),
		Cooldown: readViewsCooldown(lookupEnv),
	})
	if viewsErr != nil {
		log.Print("浏览量同步初始化失败")
	}
	var stopViews func()
	if viewsService != nil {
		viewsCtx, viewsCancel := context.WithCancel(ctx)
		go viewsService.Run(viewsCtx)
		stopViews = viewsCancel
	} else {
		stopViews = func() {}
	}
	defer stopViews()

	// 月度流量统计：内存累加 + 定时刷盘，越过预警阈值时用 Bot 发一条通知。
	// 阈值来自生效配置（环境变量优先，其次设置页保存值）。
	alertBytes := parseSettingBytes(effective.TrafficAlertBytes.Value)
	limitBytes := parseSettingBytes(effective.TrafficLimitBytes.Value)
	trafficMeter := traffic.New(store, traffic.Options{
		AlertBytes: alertBytes,
		LimitBytes: limitBytes,
		Alert: func(month string, bytesOut, threshold int64) error {
			return notifyTrafficAlert(ctx, switcher, month, bytesOut, threshold)
		},
	})
	// 空闲时也按间隔落盘，避免低流量下最后一笔一直留在内存里。
	stopTrafficFlush := trafficMeter.Start(ctx)
	defer stopTrafficFlush()
	defer func() { _ = trafficMeter.Close(context.Background()) }()

	// 自动备份：本地保留最近若干份，可按配置发送到频道。
	backupManager := backup.New(backup.Options{
		DataDir:       cfg.DataDir,
		Handle:        store,
		IncludeThumbs: !isFalse(envValue(lookupEnv, "BACKUP_INCLUDE_THUMBS")),
		Keep:          int(parseSettingBytes(effective.BackupKeep.Value)),
		Sender:        backupSender(resolver, lookupEnv, switcher),
	})
	backupState := newBackupScheduleState(effective)
	backupSchedule := backupState.text()

	botPool := botpool.New(settingsStore)
	// reloadBotsFn 在队列装配完成后才有实现，路由先挂一个可延后填充的入口。
	var reloadBotsFn func(context.Context) error
	router := api.NewRouter(api.Deps{
		Version:           version,
		Commit:            commit,
		Auth:              authService,
		Sessions:          auth.NewSessions(secret, auth.DefaultSessionTTL, isHTTPS(cfg.PublicURL)),
		SetupToken:        readSetupToken(lookupEnv),
		WebFS:             assets,
		DataDir:           cfg.DataDir,
		Posts:             repository,
		Streams:           switcher,
		Telegraph:         telegraphManager,
		PublicURL:         cfg.PublicURL,
		RSSEnabled:        envEnabled(lookupEnv, "RSS_ENABLED"),
		Editor:            editor,
		Settings:          resolver,
		Reload:            reloadSettings,
		PrepareProvider:   prepareProvider,
		CommitProvider:    commitProvider,
		SettingsLock:      settingsLock,
		ImageConcurrency:  readImageConcurrency(lookupEnv),
		ImageQueueDepth:   readImageQueueDepth(lookupEnv),
		ImageQueueWait:    readImageQueueWait(lookupEnv),
		UploadConcurrency: readUploadConcurrency(lookupEnv),
		Traffic:           trafficMeter,
		Backup:            backupManager,
		BackupSchedule:    backupSchedule,
		ApplyBackupSchedule: func(next settings.Effective) error {
			backupState.apply(next)
			return nil
		},
		Views: viewsService,
		Bots:  botPool,
		ReloadBots: func(callCtx context.Context) error {
			if reloadBotsFn == nil {
				return nil
			}
			return reloadBotsFn(callCtx)
		},
	})

	runtime := storage.NewDynamic(switcher)

	// buildEnqueue 按当前 Bot 列表给出上传所用的提供方：零个可用保持等待，一个直接使用，多个按序切换。
	buildEnqueue := func(callCtx context.Context) (storage.Provider, *storage.Pool, error) {
		effective, resolveErr := resolver.Effective(callCtx)
		if resolveErr != nil {
			return nil, nil, resolveErr
		}
		bots, listErr := botPool.List(callCtx)
		if listErr != nil {
			return nil, nil, listErr
		}
		usable := botpool.Usable(bots)
		if len(usable) == 0 {
			// 列表为空或全部不可写：保持等待，不回退旧令牌，避免停用后仍在发送。
			return nil, nil, nil
		}
		if len(usable) == 1 {
			provider, buildErr := newProviderWithToken(effective, usable[0].Token)
			if buildErr != nil || provider == nil {
				return nil, nil, buildErr
			}
			return provider, nil, nil
		}
		pool, buildErr := buildBotPool(callCtx, botPool, effective)
		if buildErr != nil {
			return nil, nil, buildErr
		}
		if pool == nil {
			return nil, nil, nil
		}
		return pool, pool, nil
	}

	// 故障切换始终挂上：运行中加入第二个 Bot 后无需重启即可生效。
	nextProvider := func(callCtx context.Context, failed storage.Provider, _ error) (storage.Provider, bool) {
		// 优先用本次请求实际使用的 Bot 编号，避免热替换后标错对象。
		actualID := int64(0)
		if origin, ok := failed.(storage.Origin); ok {
			actualID, _ = origin.Origin()
		}
		failedVersion := ""
		if versioned, ok := failed.(interface{ CredentialVersion() string }); ok {
			failedVersion = versioned.CredentialVersion()
		}
		failedBotID, next, ok := runtime.Failover(uploadFrozenNote)
		if actualID > 0 {
			failedBotID = actualID
		}
		// 只有失败请求所用凭据仍是列表里的那一版时才标冻结，避免把刚轮换的新令牌误标。
		if failedBotID > 0 {
			current, listErr := botPool.List(callCtx)
			freeze := true
			if listErr == nil && failedVersion != "" {
				freeze = false
				effective, resolveErr := resolver.Effective(callCtx)
				for _, bot := range current {
					if err := resolveErr; err != nil {
						break
					}
					if bot.BotID == failedBotID && storage.CredentialVersion(bot.Token, effective.TelegramAPIBase.Value) == failedVersion {
						freeze = true
						break
					}
				}
			}
			if freeze {
				if err := botPool.MarkFrozen(callCtx, failedBotID, uploadFrozenNote); err != nil {
					log.Print("记录 Bot 冻结状态失败")
				}
			}
		}
		if !ok {
			return nil, false
		}
		return next, true
	}
	uploadQ := startUploadQueue(ctx, repository, cfg.DataDir, runtime, gate, lookupEnv, publishReady(telegraphManager, cfg.PublicURL), nextProvider)
	defer uploadQ.Stop()

	reloadBots := func(callCtx context.Context) error {
		provider, pool, buildErr := buildEnqueue(callCtx)
		if buildErr != nil {
			return buildErr
		}
		runtime.Set(provider, pool)
		return nil
	}
	reloadBotsFn = reloadBots
	// 启动时先按现有列表装配；旧单 Token 配置在列表为空时并入列表，保证升级后行为不变。
	go func() {
		seedCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
		defer cancel()
		if err := seedBotPool(seedCtx, botPool, resolver, settingsStore); err != nil {
			log.Print("旧 Bot 配置并入列表失败")
		}
		if err := reloadBots(seedCtx); err != nil {
			log.Print("按 Bot 列表装配运行配置失败")
		}
	}()

	stopPublisher := startPublisher(ctx, repository, telegraphManager, cfg.PublicURL)
	defer stopPublisher()

	stopBackup := startBackupScheduler(ctx, backupManager, backupState)
	defer stopBackup()

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

// uploadFrozenNote 是多 Bot 池里某个 Bot 被上游拒绝时的冻结说明，只写固定说法。
const uploadFrozenNote = "上游拒绝，账号受限"

// seedBotPool 把升级前的单 Bot 配置幂等并入列表：列表为空且环境里有令牌时才写入。
// 编号用 getMe 的真实结果，重复启动不会产生第二条。
func seedBotPool(ctx context.Context, pool *botpool.Pool, resolver *settings.Resolver, store *settings.Store) error {
	// 迁移只做一次：用户把旧 Bot 从列表移除后，重启不会再被加回来。
	if _, done, err := store.Get(ctx, "telegram.bots.migrated"); err == nil && done {
		return nil
	}
	defer func() { _ = store.Set(ctx, "telegram.bots.migrated", "1") }()
	existing, err := pool.List(ctx)
	if err != nil {
		return err
	}
	effective, err := resolver.Effective(ctx)
	if err != nil {
		return err
	}
	token := strings.TrimSpace(effective.TelegramToken.Value)
	if token == "" {
		return nil
	}
	for _, bot := range existing {
		if bot.Token == token {
			return nil
		}
	}
	provider, err := newProviderWithToken(effective, token)
	if err != nil || provider == nil {
		return nil
	}
	botID, err := provider.BotID(ctx)
	if err != nil {
		return nil
	}
	bots := append(existing, botpool.Bot{BotID: botID, Alias: "默认", Token: token, Status: botpool.StatusOK})
	return pool.Replace(ctx, bots)
}

// buildBotPool 把配置里的可写入 Bot 装配成提供方池；少于两个时返回 nil，保持单 Bot 行为。
// 令牌只用于构建客户端，不写日志、不回显。
func buildBotPool(ctx context.Context, pool *botpool.Pool, effective settings.Effective) (*storage.Pool, error) {
	bots, err := pool.List(ctx)
	if err != nil {
		return nil, err
	}
	usable := botpool.Usable(bots)
	if len(usable) < 2 {
		return nil, nil
	}
	entries := make([]storage.PoolEntry, 0, len(usable))
	for _, bot := range usable {
		if strings.TrimSpace(bot.Token) == "" {
			continue
		}
		provider, buildErr := newProviderWithToken(effective, bot.Token)
		if buildErr != nil || provider == nil {
			continue
		}
		entries = append(entries, storage.PoolEntry{
			BotID:    bot.BotID,
			ChatID:   effective.TelegramChannel.Value,
			Alias:    bot.Alias,
			Provider: provider,
		})
	}
	if len(entries) < 2 {
		return nil, nil
	}
	return storage.NewPool(entries)
}

// newProvider 按生效配置创建图床提供方；未配置时返回 nil，由调用方决定如何呈现。
func newProvider(effective settings.Effective) (*storage.Telegram, error) {
	return newProviderWithToken(effective, effective.TelegramToken.Value)
}

// newProviderWithToken 用指定令牌创建提供方，频道与代理沿用生效配置，供多 Bot 池逐条构建。
func newProviderWithToken(effective settings.Effective, token string) (*storage.Telegram, error) {
	client, err := netclient.NewClient(effective.ProxyConfig(), netclient.ServiceTelegram)
	if err != nil {
		return nil, err
	}
	provider, err := storage.NewTelegram(storage.TelegramConfig{
		Token:      token,
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

// isTrue 判断环境变量是否表示开启。
func isTrue(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// isFalse 判断环境变量是否明确表示关闭；未设置时按开启处理。
func isFalse(raw string) bool {
	value := strings.ToLower(strings.TrimSpace(raw))
	return value == "0" || value == "false" || value == "no" || value == "off"
}

// backupSender 按当前生效配置把备份发送到指定频道；频道未单独指定时使用图床频道。
func backupSender(resolver *settings.Resolver, lookupEnv func(string) (string, bool), switcher *storage.Switcher) backup.Sender {
	channel, _ := lookupEnv("BACKUP_CHANNEL_ID")
	channel = strings.TrimSpace(channel)
	return backupSenderFunc(func(ctx context.Context, path, caption string) error {
		effective, err := resolver.Effective(ctx)
		if err != nil {
			return err
		}
		if channel == "" {
			// 未指定独立频道时复用当前图床频道：先固定提供方实例，
			// 让校验与发送使用同一份令牌与目标，避免中途保存设置换了频道。
			provider, err := switcher.Current()
			if err != nil {
				return backup.ErrTargetUnverified
			}
			if err := ensurePrivateTarget(ctx, provider); err != nil {
				return err
			}
			return provider.SendDocument(ctx, path, caption)
		}
		client, err := netclient.NewClient(effective.ProxyConfig(), netclient.ServiceTelegram)
		if err != nil {
			return err
		}
		provider, err := storage.NewTelegram(storage.TelegramConfig{
			Token:      effective.TelegramToken.Value,
			ChatID:     channel,
			APIBase:    effective.TelegramAPIBase.Value,
			HTTPClient: client,
		})
		if err != nil {
			return err
		}
		if err := ensurePrivateTarget(ctx, provider); err != nil {
			return err
		}
		return uploadDocument(ctx, provider, path, caption)
	})
}

// chatInspector 读取目标会话属性，Telegram 提供方与切换器都实现它。
type chatInspector interface {
	ChatInfo(ctx context.Context) (storage.ChatInfo, error)
}

// ensurePrivateTarget 确认备份目标不是公开会话。
// 备份包含数据库里的凭据，公开频道或无法确认时只保留本地文件。
func ensurePrivateTarget(ctx context.Context, target chatInspector) error {
	info, err := target.ChatInfo(ctx)
	if err != nil {
		return backup.ErrTargetUnverified
	}
	if !info.Private() {
		return backup.ErrTargetPublic
	}
	return nil
}

// backupSenderFunc 让普通函数满足备份发送接口。
type backupSenderFunc func(context.Context, string, string) error

// SendDocument 调用底层函数发送备份文件。
func (f backupSenderFunc) SendDocument(ctx context.Context, path, caption string) error {
	return f(ctx, path, caption)
}

// uploadDocument 打开本地文件并按文档模式上传。
func uploadDocument(ctx context.Context, provider *storage.Telegram, path, caption string) error {
	file, err := os.Open(path)
	if err != nil {
		return errors.New("读取备份文件失败")
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || info.IsDir() {
		return errors.New("读取备份文件失败")
	}
	_, err = provider.Upload(ctx, storage.UploadRequest{
		FileName: filepath.Base(path),
		MIMEType: "application/zip",
		Size:     info.Size(),
		Mode:     storage.ModeDocument,
		Caption:  caption,
		Reader:   file,
	})
	return err
}

// backupScheduleState 保存当前自动备份计划，设置保存后可热更新。
type backupScheduleState struct {
	mu      sync.Mutex
	enabled bool
	hour    int
	minute  int
}

// newBackupScheduleState 用生效配置初始化计划。
func newBackupScheduleState(effective settings.Effective) *backupScheduleState {
	state := &backupScheduleState{}
	state.apply(effective)
	return state
}

// apply 把生效配置写入计划；时刻非法时回落到默认三点。
func (s *backupScheduleState) apply(effective settings.Effective) {
	hour, minute := 3, 0
	if parsedHour, parsedMinute, err := parseClock(effective.BackupTime.Value); err == nil {
		hour, minute = parsedHour, parsedMinute
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enabled = strings.EqualFold(strings.TrimSpace(effective.BackupEnabled.Value), "true")
	s.hour = hour
	s.minute = minute
}

// text 返回计划的中文说明，未启用时为空串。
func (s *backupScheduleState) text() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.enabled {
		return ""
	}
	return backup.DailyText(s.hour, s.minute)
}

// snapshot 返回当前计划，供调度协程判断是否到点。
func (s *backupScheduleState) snapshot() (bool, int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.enabled, s.hour, s.minute
}

// startBackupScheduler 按当前计划每天触发一次热备，计划可热更新。
func startBackupScheduler(ctx context.Context, manager *backup.Manager, state *backupScheduleState) func() {
	if manager == nil || state == nil {
		log.Print("定时备份未启用")
		return func() {}
	}

	schedulerCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		lastRun := ""
		for {
			enabled, hour, minute := state.snapshot()
			now := time.Now()
			if enabled && now.Hour() == hour && now.Minute() == minute && lastRun != now.Format("2006-01-02") {
				lastRun = now.Format("2006-01-02")
				if err := manager.Start(); err != nil {
					log.Print("上一次备份仍在进行，本次跳过")
				} else {
					runCtx, runCancel := context.WithTimeout(schedulerCtx, 15*time.Minute)
					result, runErr := manager.Run(runCtx)
					runCancel()
					if runErr != nil {
						log.Print("定时备份未完成，请检查数据目录与图床配置")
					} else {
						log.Printf("定时备份完成：%s（%.1f MB）", result.Name, float64(result.Size)/(1<<20))
					}
				}
			}
			select {
			case <-schedulerCtx.Done():
				return
			case <-time.After(20 * time.Second):
			}
		}
	}()

	return func() {
		cancel()
		<-done
	}
}

// parseClock 解析 HH:MM 时刻。
func parseClock(raw string) (int, int, error) {
	parts := strings.SplitN(strings.TrimSpace(raw), ":", 2)
	if len(parts) != 2 {
		return 0, 0, errors.New("时刻格式不正确")
	}
	hour, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, err
	}
	minute, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, err
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, 0, errors.New("时刻超出范围")
	}
	return hour, minute, nil
}

// parseSettingBytes 把配置中的十进制字节数转成整数，非法值按 0 处理。
func parseSettingBytes(raw string) int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || value < 0 {
		return 0
	}
	return value
}

// envNumberString 把环境变量里的字节数转成配置字符串，未配置时为空。
func envNumberString(value int64) string {
	if value <= 0 {
		return ""
	}
	return strconv.FormatInt(value, 10)
}

// backupTimeEnv 把 BACKUP_CRON 里的每日时刻转成 HH:MM。
func backupTimeEnv(lookupEnv func(string) (string, bool)) string {
	spec := envValue(lookupEnv, "BACKUP_CRON")
	if spec == "" {
		return ""
	}
	hour, minute, err := backup.ParseDaily(spec)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%02d:%02d", hour, minute)
}

// nextBackupTime 计算下一次触发时刻。
func nextBackupTime(now time.Time, hour, minute int) time.Time {
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return next
}

// readGigabytes 读取以 GB 为单位的阈值，支持小数；非法或非正值视为未配置。
func readGigabytes(lookupEnv func(string) (string, bool), key string) int64 {
	raw, ok := lookupEnv(key)
	if !ok {
		return 0
	}
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || value <= 0 {
		return 0
	}
	return int64(value * float64(traffic.GB))
}

// notifyTrafficAlert 用一条纯文本消息提醒当月流量越过预警阈值。
// 返回错误表示没有送达，由统计器稍后重试；消息里不出现令牌与原始地址。
func notifyTrafficAlert(ctx context.Context, switcher *storage.Switcher, month string, bytesOut, threshold int64) error {
	notifyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	text := fmt.Sprintf("SeSeGraph 流量提醒：%s 已出站 %.2f GB，超过预警阈值 %.2f GB。",
		month, float64(bytesOut)/float64(traffic.GB), float64(threshold)/float64(traffic.GB))
	if err := switcher.SendMessage(notifyCtx, text); err != nil {
		log.Print("流量预警通知发送失败，图床可能未配置或不可用")
		return err
	}
	return nil
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

// readUploadConcurrency 读取同时接收上传的上限，未配置时使用默认值。
func readUploadConcurrency(lookupEnv func(string) (string, bool)) int {
	raw, ok := lookupEnv("UPLOAD_CONCURRENCY")
	if !ok {
		return api.DefaultUploadConcurrency
	}
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return api.DefaultUploadConcurrency
	}
	if value > 10 {
		return 10
	}
	return value
}

// readImageQueueDepth 读取图片网关的排队人数上限，未配置时使用默认值。
func readImageQueueDepth(lookupEnv func(string) (string, bool)) int {
	raw, ok := lookupEnv("PROXY_QUEUE_DEPTH")
	if !ok {
		return api.DefaultImageQueueDepth
	}
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return api.DefaultImageQueueDepth
	}
	if value > 1000 {
		return 1000
	}
	return value
}

// readImageQueueWait 读取单个请求的最长排队时间（秒），未配置时使用默认值。
func readImageQueueWait(lookupEnv func(string) (string, bool)) time.Duration {
	raw, ok := lookupEnv("PROXY_QUEUE_WAIT")
	if !ok {
		return api.DefaultImageQueueWait
	}
	seconds, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || seconds <= 0 {
		return api.DefaultImageQueueWait
	}
	if seconds > 60 {
		seconds = 60
	}
	return time.Duration(seconds) * time.Second
}

// startUploadQueue 启动上传队列，返回停止函数。
// 队列始终运行：图床未配置时按提供方的就绪状态等待，配置保存后自动接手。
type uploadQueue struct {
	Stop func()
	// SetProvider 在配置保存后热替换发送所用提供方。
	SetProvider func(storage.Provider)
}

func startUploadQueue(ctx context.Context, repository *posts.Repository, dataDir string, provider storage.Provider, gate *storage.UploadGate, lookupEnv func(string) (string, bool), ready func(ctx context.Context) error, nextProvider func(ctx context.Context, failed storage.Provider, cause error) (storage.Provider, bool)) *uploadQueue {
	interval := readUploadInterval(lookupEnv)

	queue, err := worker.New(worker.Options{
		Repository:   repository,
		Provider:     provider,
		DataDir:      dataDir,
		Interval:     interval,
		Gate:         gate,
		PublishReady: ready,
		NextProvider: nextProvider,
	})
	if err != nil {
		log.Print("上传队列初始化失败")
		return &uploadQueue{Stop: func() {}, SetProvider: func(storage.Provider) {}}
	}

	if _, err := queue.Recover(ctx); err != nil {
		log.Print("上传队列恢复失败")
	}

	queueCtx, cancel := context.WithCancel(ctx)
	go queue.Run(queueCtx)
	log.Print("上传队列已启动")
	return &uploadQueue{Stop: cancel, SetProvider: queue.SetProvider}
}

// readViewsInterval 读取浏览量同步间隔，未配置或取值不合法时使用默认值。
func readViewsInterval(lookupEnv func(string) (string, bool)) time.Duration {
	raw, ok := lookupEnv("VIEWS_SYNC_MINUTES")
	if !ok {
		return views.DefaultInterval
	}
	minutes, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || minutes <= 0 {
		return views.DefaultInterval
	}
	return time.Duration(minutes) * time.Minute
}

// readViewsCooldown 读取手动刷新冷却，未配置或取值不合法时使用默认值。
func readViewsCooldown(lookupEnv func(string) (string, bool)) time.Duration {
	raw, ok := lookupEnv("VIEWS_COOLDOWN_SECONDS")
	if !ok {
		return views.DefaultCooldown
	}
	seconds, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || seconds <= 0 {
		return views.DefaultCooldown
	}
	return time.Duration(seconds) * time.Second
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

// envEnabled 把 1/true/yes/on 视为开启，其余取值与未配置都按关闭处理。
func envEnabled(lookupEnv func(string) (string, bool), key string) bool {
	raw, ok := lookupEnv(key)
	if !ok {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
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

// publishReady 把发布前置检查装配成闭包，手动发布与同步收尾共用同一口径。
func publishReady(manager *telegraph.Manager, publicURL string) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		return publishgate.Check(ctx, manager, publicURL)
	}
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
