package main

import (
	"bytes"
	"context"
	"errors"
	"github.com/littleseven2003/SeSeGraph/backend/internal/api"
	"github.com/littleseven2003/SeSeGraph/backend/internal/botpool"
	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
	"github.com/littleseven2003/SeSeGraph/backend/internal/settings"
	"github.com/littleseven2003/SeSeGraph/backend/internal/storage"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/littleseven2003/SeSeGraph/backend/internal/auth"
	"github.com/littleseven2003/SeSeGraph/backend/internal/db"
	"github.com/littleseven2003/SeSeGraph/backend/internal/worker"
)

// noEnv 模拟没有任何环境变量参与配置。
func noEnv(string) (string, bool) { return "", false }

func occupyPort(t *testing.T) (addr string, closeFn func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("无法占用测试端口: %v", err)
	}
	return ln.Addr().String(), func() { _ = ln.Close() }
}

func TestRunHidesConfigOnListenFailure(t *testing.T) {
	addr, closeFn := occupyPort(t)
	defer closeFn()

	dataDir := t.TempDir()
	err := run([]string{"--addr", addr, "--data", dataDir}, noEnv)
	if err == nil {
		t.Fatal("端口已被占用时应返回错误")
	}
	msg := err.Error()
	for _, value := range []string{addr, dataDir} {
		if strings.Contains(msg, value) {
			t.Errorf("错误信息不应回显配置原值 %q，实际: %s", value, msg)
		}
	}
}

func TestStartupLogHidesConfig(t *testing.T) {
	addr, closeFn := occupyPort(t)
	defer closeFn()

	var buf bytes.Buffer
	original := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(original)

	dataDir := t.TempDir()
	_ = run([]string{"--addr", addr, "--data", dataDir}, noEnv)

	logged := buf.String()
	for _, value := range []string{addr, dataDir} {
		if strings.Contains(logged, value) {
			t.Errorf("启动日志不应包含配置原值 %q，实际: %s", value, logged)
		}
	}
}

func TestRunInitializesDatabase(t *testing.T) {
	addr, closeFn := occupyPort(t)
	defer closeFn()

	dataDir := t.TempDir()
	if err := run([]string{"--addr", addr, "--data", dataDir}, noEnv); err == nil {
		t.Fatal("端口已被占用时应返回错误")
	}

	store, err := db.Open(filepath.Join(dataDir, databaseFileName))
	if err != nil {
		t.Fatalf("启动后应生成可打开的数据库文件: %v", err)
	}
	defer store.Close()

	var version int
	if err := store.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("读取结构版本失败: %v", err)
	}
	if version != db.SchemaVersion {
		t.Errorf("user_version = %d，期望 %d", version, db.SchemaVersion)
	}

	var table string
	if err := store.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='table' AND name='posts'",
	).Scan(&table); err != nil {
		t.Errorf("启动后应存在 posts 表: %v", err)
	}
}

func TestRunKeepsDatabaseAcrossRestarts(t *testing.T) {
	addr, closeFn := occupyPort(t)
	defer closeFn()
	dataDir := t.TempDir()

	if err := run([]string{"--addr", addr, "--data", dataDir}, noEnv); err == nil {
		t.Fatal("首次启动应因端口占用失败")
	}
	store, err := db.Open(filepath.Join(dataDir, databaseFileName))
	if err != nil {
		t.Fatalf("首次启动后打开数据库失败: %v", err)
	}
	if _, err := store.Exec("INSERT INTO posts (title) VALUES ('重启前写入')"); err != nil {
		t.Fatalf("写入帖子失败: %v", err)
	}
	_ = store.Close()

	if err := run([]string{"--addr", addr, "--data", dataDir}, noEnv); err == nil {
		t.Fatal("再次启动应因端口占用失败")
	}

	again, err := db.Open(filepath.Join(dataDir, databaseFileName))
	if err != nil {
		t.Fatalf("再次启动后打开数据库失败: %v", err)
	}
	defer again.Close()

	var title string
	if err := again.QueryRow("SELECT title FROM posts LIMIT 1").Scan(&title); err != nil {
		t.Fatalf("重复启动后应保留已有数据: %v", err)
	}
	if title != "重启前写入" {
		t.Errorf("title = %q，期望保留原数据", title)
	}
}

func TestRunHidesDatabaseFailure(t *testing.T) {
	base := t.TempDir()
	blocker := filepath.Join(base, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("准备文件失败: %v", err)
	}
	dataDir := filepath.Join(blocker, "data")

	err := run([]string{"--addr", "127.0.0.1:0", "--data", dataDir}, noEnv)
	if err == nil {
		t.Fatal("数据目录不可用时应返回错误")
	}
	if err.Error() != errDatabase.Error() {
		t.Errorf("错误信息 = %q，期望固定说明且不回显路径", err.Error())
	}
	if strings.Contains(err.Error(), dataDir) {
		t.Errorf("错误信息不应回显数据目录: %s", err.Error())
	}
}

func TestRunReportsInvalidConfig(t *testing.T) {
	if err := run([]string{"--public-url", "ftp://example.com"}, noEnv); err == nil {
		t.Fatal("非法协议应返回错误")
	}
	if err := run([]string{"--addr", ""}, noEnv); err == nil {
		t.Fatal("空监听地址应返回错误")
	}
}

func envLookup(pairs map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := pairs[key]
		return value, ok
	}
}

func TestEnvBootstrapCreatesAdminWithoutLoggingPassword(t *testing.T) {
	addr, closeFn := occupyPort(t)
	defer closeFn()
	dataDir := t.TempDir()
	password := "env-bootstrap-pass"

	var buf bytes.Buffer
	original := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(original)

	env := envLookup(map[string]string{"ADMIN_USERNAME": "admin", "ADMIN_PASSWORD": password})
	if err := run([]string{"--addr", addr, "--data", dataDir}, env); err == nil {
		t.Fatal("端口被占用时应返回错误")
	}

	if strings.Contains(buf.String(), password) {
		t.Errorf("启动日志不应包含密码，实际: %s", buf.String())
	}

	store, err := db.Open(filepath.Join(dataDir, databaseFileName))
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	defer store.Close()

	var name, hash string
	if err := store.QueryRow("SELECT username, password_hash FROM admin_users WHERE id = 1").Scan(&name, &hash); err != nil {
		t.Fatalf("读取管理员失败: %v", err)
	}
	if name != "admin" {
		t.Errorf("账号名 = %q，期望 admin", name)
	}
	if strings.Contains(hash, password) {
		t.Error("密码必须以摘要形式保存")
	}

	service := auth.NewService(store)
	if _, err := service.Authenticate(context.Background(), "admin", password); err != nil {
		t.Errorf("环境变量初始化的账号应可登录: %v", err)
	}
}

func TestEnvBootstrapRejectsWeakPassword(t *testing.T) {
	env := envLookup(map[string]string{"ADMIN_USERNAME": "admin", "ADMIN_PASSWORD": "short"})
	err := run([]string{"--addr", "127.0.0.1:0", "--data", t.TempDir()}, env)
	if err == nil {
		t.Fatal("弱密码应返回错误")
	}
	if err != errAdminEnv {
		t.Errorf("错误 = %v，期望固定说明", err)
	}
	if strings.Contains(err.Error(), "short") {
		t.Errorf("错误信息不应回显密码: %s", err.Error())
	}
}

func TestEnvBootstrapKeepsExistingAdmin(t *testing.T) {
	addr, closeFn := occupyPort(t)
	defer closeFn()
	dataDir := t.TempDir()

	first := envLookup(map[string]string{"ADMIN_USERNAME": "admin", "ADMIN_PASSWORD": "first-password"})
	_ = run([]string{"--addr", addr, "--data", dataDir}, first)

	second := envLookup(map[string]string{"ADMIN_USERNAME": "intruder", "ADMIN_PASSWORD": "second-password"})
	_ = run([]string{"--addr", addr, "--data", dataDir}, second)

	store, err := db.Open(filepath.Join(dataDir, databaseFileName))
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	defer store.Close()

	service := auth.NewService(store)
	if _, err := service.Authenticate(context.Background(), "admin", "first-password"); err != nil {
		t.Errorf("应保留首次初始化的账号: %v", err)
	}
	if _, err := service.Authenticate(context.Background(), "intruder", "second-password"); err == nil {
		t.Error("不应被后一次环境变量覆盖")
	}
}

func TestIsHTTPS(t *testing.T) {
	cases := map[string]bool{
		"https://img.example.com": true,
		"HTTPS://img.example.com": true,
		"http://img.example.com":  false,
		"":                        false,
	}
	for input, want := range cases {
		if got := isHTTPS(input); got != want {
			t.Errorf("isHTTPS(%q) = %v，期望 %v", input, got, want)
		}
	}
}

func TestReadSetupToken(t *testing.T) {
	if got := readSetupToken(noEnv); got != "" {
		t.Errorf("未配置时应为空，实际 %q", got)
	}
	env := envLookup(map[string]string{"ADMIN_SETUP_TOKEN": "  abc123  "})
	if got := readSetupToken(env); got != "abc123" {
		t.Errorf("应去除首尾空白，实际 %q", got)
	}
}

func TestReadUploadInterval(t *testing.T) {
	if got := readUploadInterval(noEnv); got != worker.DefaultInterval {
		t.Errorf("默认间隔 = %s，期望 %s", got, worker.DefaultInterval)
	}
	env := envLookup(map[string]string{"UPLOAD_INTERVAL_MS": "2500"})
	if got := readUploadInterval(env); got != 2500*time.Millisecond {
		t.Errorf("间隔 = %s，期望 2.5s", got)
	}
	invalid := envLookup(map[string]string{"UPLOAD_INTERVAL_MS": "abc"})
	if got := readUploadInterval(invalid); got != worker.DefaultInterval {
		t.Errorf("非法间隔应回落到默认值，实际 %s", got)
	}
}

func TestEnvValueTrimsSpace(t *testing.T) {
	if got := envValue(noEnv, "TELEGRAM_API_BASE"); got != "" {
		t.Errorf("未配置时应为空，实际 %q", got)
	}
	env := envLookup(map[string]string{"TELEGRAM_API_BASE": " http://127.0.0.1:9090 "})
	if got := envValue(env, "TELEGRAM_API_BASE"); got != "http://127.0.0.1:9090" {
		t.Errorf("应去除首尾空白，实际 %q", got)
	}
}

// TestMatchBotCredentialRejectsRotatedToken 验证迟到结果只在凭据版本仍一致时才算数。
func TestMatchBotCredentialRejectsRotatedToken(t *testing.T) {
	bots := []botpool.Bot{{BotID: 111, Token: "111:old"}}
	version := storage.CredentialVersion("111:old", "https://api.telegram.org")
	if !matchBotCredential(bots, 111, version, "https://api.telegram.org") {
		t.Fatal("同一令牌与上游地址应判定为同一版凭据")
	}
	if matchBotCredential(bots, 111, version, "https://mirror.example") {
		t.Error("上游地址变化后不应判定为同一版凭据")
	}
	rotated := []botpool.Bot{{BotID: 111, Token: "111:new"}}
	if matchBotCredential(rotated, 111, version, "https://api.telegram.org") {
		t.Error("换过令牌后不应再匹配旧请求的凭据版本")
	}
	if matchBotCredential(bots, 222, version, "https://api.telegram.org") {
		t.Error("编号不存在时不应匹配")
	}
	if matchBotCredential(bots, 111, "", "https://api.telegram.org") {
		t.Error("没有凭据版本时不应匹配")
	}
}

// TestSeedBotPoolWritesMarkerOnlyAfterSuccess 验证迁移标记只在真正完成后写入，
// 失败时保留重试机会，避免用户移除旧 Bot 后又被加回来或被永久跳过。
func TestSeedBotPoolWritesMarkerOnlyAfterSuccess(t *testing.T) {
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"ok":false}`))
	}))
	t.Cleanup(failing.Close)

	setup := func(env settings.Env) (*botpool.Pool, *settings.Resolver, *settings.Store) {
		handle, err := db.Open(filepath.Join(t.TempDir(), "seed.db"))
		if err != nil {
			t.Fatalf("打开数据库失败: %v", err)
		}
		t.Cleanup(func() { _ = handle.Close() })
		if err := db.Migrate(context.Background(), handle); err != nil {
			t.Fatalf("迁移失败: %v", err)
		}
		store := settings.NewStore(handle)
		return botpool.New(store), settings.NewResolver(store, env), store
	}

	// 没有旧令牌：无需迁移，也不写标记。
	pool, resolver, store := setup(settings.Env{})
	if err := seedBotPool(context.Background(), pool, resolver, store); err != nil {
		t.Fatalf("无令牌时迁移失败: %v", err)
	}
	if _, done, _ := store.Get(context.Background(), "telegram.bots.migrated"); done {
		t.Fatal("没有可迁移令牌时不应写完成标记")
	}

	// 探测失败：列表未写入，标记也不能写。
	pool, resolver, store = setup(settings.Env{TelegramToken: "111:legacy", TelegramAPIBase: failing.URL})
	if err := seedBotPool(context.Background(), pool, resolver, store); err != nil {
		t.Fatalf("探测失败时应返回可重试错误，实际: %v", err)
	}
	if _, done, _ := store.Get(context.Background(), "telegram.bots.migrated"); done {
		t.Fatal("探测失败时不应写完成标记")
	}
	if bots, err := pool.List(context.Background()); err != nil || len(bots) != 0 {
		t.Fatalf("探测失败后列表 = %+v（err=%v），期望为空", bots, err)
	}

	// 旧令牌已在列表里：并入完成，写下标记，之后移除不会再被加回。
	pool, resolver, store = setup(settings.Env{TelegramToken: "111:legacy"})
	if err := pool.Replace(context.Background(), []botpool.Bot{{BotID: 111, Token: "111:legacy"}}); err != nil {
		t.Fatalf("写入列表失败: %v", err)
	}
	if err := seedBotPool(context.Background(), pool, resolver, store); err != nil {
		t.Fatalf("已并入时失败: %v", err)
	}
	if _, done, _ := store.Get(context.Background(), "telegram.bots.migrated"); !done {
		t.Fatal("已并入旧令牌后应写完成标记")
	}
}

// TestPickImageSourceFallsBackToSourceBot 验证读图选源：优先用持有引用的 Bot，
// 没有引用时回到存下这张图的 Bot 直读原编号，两者都不可用时才回落到全局图床。
func TestPickImageSourceFallsBackToSourceBot(t *testing.T) {
	usable := []botpool.Bot{{BotID: 111}, {BotID: 222}}
	refs := []posts.ImageFileRef{{BotID: 222, FileID: "file-b"}}

	if botID, fileID, ok := pickImageSource(usable, refs, 111, "file-a"); !ok || botID != 222 || fileID != "file-b" {
		t.Fatalf("有引用时 = %d/%q/%v，期望 222/file-b", botID, fileID, ok)
	}
	// 引用的 Bot 已被移除：回到来源 Bot 直读原编号，读成功后由网关登记替代引用。
	if botID, fileID, ok := pickImageSource([]botpool.Bot{{BotID: 111}}, refs, 111, "file-a"); !ok || botID != 111 || fileID != "file-a" {
		t.Fatalf("引用不可用时 = %d/%q/%v，期望 111/file-a", botID, fileID, ok)
	}
	// 连来源 Bot 也不在池里：交给全局图床，不再猜编号。
	if _, _, ok := pickImageSource(usable, nil, 333, "file-c"); ok {
		t.Fatal("来源与引用都不可用时不应选出 Bot")
	}
	if _, _, ok := pickImageSource(nil, nil, 111, "file-a"); ok {
		t.Fatal("没有可用 Bot 时不应选出 Bot")
	}
}

// TestNextProviderSkipsRotatedCredential 验证发送在途时换过配置的旧请求失败后，
// 既不会切换当前池，也不会把新令牌标成冻结；同一版凭据才执行切换。
func TestNextProviderSkipsRotatedCredential(t *testing.T) {
	oldVersion := storage.CredentialVersion("111:old", "https://api.telegram.org")
	next := &storage.Telegram{}
	failovers, frozen := 0, 0
	deps := failoverDeps{
		list: func(context.Context) ([]botpool.Bot, error) {
			// 列表里已是新令牌，旧请求携带的是旧版本。
			return []botpool.Bot{{BotID: 111, Token: "111:new"}}, nil
		},
		apiBase: func(context.Context) (string, error) { return "https://api.telegram.org", nil },
		failover: func(int64) (storage.Provider, bool) {
			failovers++
			return next, true
		},
		markFrozen: func(context.Context, int64, string) error { frozen++; return nil },
	}
	provider, ok := newNextProvider(deps)(context.Background(),
		storage.SendOrigin{BotID: 111, CredentialVersion: oldVersion}, nil)
	if ok || provider != nil {
		t.Fatal("换过令牌后不应切换或返回新提供方")
	}
	if failovers != 0 || frozen != 0 {
		t.Fatalf("换过令牌后不应动运行态：failover=%d frozen=%d", failovers, frozen)
	}

	// 同一版凭据：先切换运行态，再落冻结标记。
	deps.list = func(context.Context) ([]botpool.Bot, error) {
		return []botpool.Bot{{BotID: 111, Token: "111:old"}}, nil
	}
	provider, ok = newNextProvider(deps)(context.Background(),
		storage.SendOrigin{BotID: 111, CredentialVersion: oldVersion}, nil)
	if !ok || provider != next {
		t.Fatalf("同一版凭据应切换到下一个 Bot，实际 ok=%v", ok)
	}
	if failovers != 1 || frozen != 1 {
		t.Fatalf("切换次数 = %d/%d，期望各 1 次", failovers, frozen)
	}
}

// takeoverFake 模拟能转发原消息的读图提供方，记录被要求接管的原消息位置。
type takeoverFake struct {
	botID int64
	taken int
}

func (f *takeoverFake) Name() string { return "takeover-fake" }

func (f *takeoverFake) Upload(context.Context, storage.UploadRequest) (storage.UploadResult, error) {
	return storage.UploadResult{}, nil
}

func (f *takeoverFake) OpenStream(_ context.Context, fileID string) (storage.Stream, error) {
	if fileID != "file-b" {
		return storage.Stream{}, storage.ErrFileUnavailable
	}
	return storage.Stream{Body: io.NopCloser(strings.NewReader("ok")), ContentType: "image/jpeg", Size: 2}, nil
}

func (f *takeoverFake) ChatInfoOf(_ context.Context, chatID string) (storage.ChatInfo, error) {
	switch strings.TrimSpace(chatID) {
	case "-100999":
		return storage.ChatInfo{ID: -100999, Type: "channel", Title: "维护"}, nil
	case "-100777":
		return storage.ChatInfo{ID: -100777, Type: "channel", Title: "图床"}, nil
	}
	return storage.ChatInfo{}, errors.New("会话不符")
}

func (f *takeoverFake) TakeoverFile(_ context.Context, destChatID, fromChatID string, messageID int64) (string, error) {
	if destChatID != "-100999" || fromChatID != "-100777" || messageID != 55 {
		return "", errors.New("转发位置不符")
	}
	f.taken++
	return "file-b", nil
}

// TestImageResolverTakesOverMissingRef 验证从缺失引用开始的真实接管路径：
// 原 Bot 已移出池时由另一个 Bot 转发原消息取得自己的文件编号，
// 登记成功后再次读取直接复用引用，且原 Bot 的删除保护随之解除。
func TestImageResolverTakesOverMissingRef(t *testing.T) {
	handle, err := db.Open(filepath.Join(t.TempDir(), "resolve.db"))
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	if err := db.Migrate(context.Background(), handle); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	repository := posts.NewRepository(handle)
	ctx := context.Background()
	postID, err := repository.Create(ctx, posts.Post{Title: "接管", ImageCount: 1, UploadMode: storage.ModePhoto})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	if _, err := repository.AttachImage(ctx, posts.Image{
		PostID: postID, SortOrder: 1, Status: posts.ImageStatusUploaded,
		MimeType: "image/jpeg", FileSize: 10, TempFilePath: "tmp/a.part",
	}); err != nil {
		t.Fatalf("保存图片失败: %v", err)
	}
	images, err := repository.ListImages(ctx, postID)
	if err != nil || len(images) != 1 {
		t.Fatalf("读取图片失败: %v", err)
	}
	imageID := images[0].ID
	if err := repository.MarkImageStoredWithOrigin(ctx, imageID, "file-a", 55, "telegram", 7, "-100777"); err != nil {
		t.Fatalf("写入逐图来源失败: %v", err)
	}

	taker := &takeoverFake{botID: 8}
	resolver := newImageResolver(imageResolverDeps{
		repo: repository,
		// 7 号 Bot 已被移出池，只剩 8 号。
		bots: func(context.Context) ([]botpool.Bot, error) {
			return []botpool.Bot{{BotID: 8, Token: "888:ok", Status: botpool.StatusOK}}, nil
		},
		effective: func(context.Context) (settings.Effective, error) { return settings.Effective{}, nil },
		provider: func(bot botpool.Bot, _ settings.Effective) storage.StreamProvider {
			if bot.BotID != 8 {
				return nil
			}
			return taker
		},
	})

	image := &posts.PublicImage{
		ImageID: imageID, RemoteFileID: "file-a", MIMEType: "image/jpeg",
		UploadMode: storage.ModePhoto, SourceBotID: 7, SourceChatID: "-100777", SourceMessageID: 55,
	}
	// 公开读图只登记接管，不同步转发；第一次选源因此返回 false。
	queued := 0
	queue := newTakeoverQueue(takeoverDeps{
		repo:   repository,
		chatID: "-100999",
		bots: func(context.Context) ([]botpool.Bot, error) {
			return []botpool.Bot{{BotID: 8, Token: "888:ok", Status: botpool.StatusOK}}, nil
		},
		effective: func(context.Context) (settings.Effective, error) { return settings.Effective{}, nil },
		provider: func(bot botpool.Bot, _ settings.Effective) storage.StreamProvider {
			if bot.BotID != 8 {
				return nil
			}
			return taker
		},
	})
	resolveWithQueue := newImageResolver(imageResolverDeps{
		repo: repository,
		bots: func(context.Context) ([]botpool.Bot, error) {
			return []botpool.Bot{{BotID: 8, Token: "888:ok", Status: botpool.StatusOK}}, nil
		},
		effective: func(context.Context) (settings.Effective, error) { return settings.Effective{}, nil },
		provider: func(bot botpool.Bot, _ settings.Effective) storage.StreamProvider {
			if bot.BotID != 8 {
				return nil
			}
			return taker
		},
		queue: func(*posts.PublicImage) { queued++ },
	})
	if _, ok := resolveWithQueue(ctx, image); ok {
		t.Fatal("来源 Bot 不在池里时公开读图不应同步接管")
	}
	if queued != 1 {
		t.Fatalf("后台接管登记次数 = %d，期望 1", queued)
	}
	if taker.taken != 0 {
		t.Fatalf("公开读图阶段不应转发，实际 %d 次", taker.taken)
	}
	if count, err := repository.CountImagesExclusiveToRemoval(ctx, 7, []int64{8}); err != nil || count != 1 {
		t.Fatalf("接管前独占计数 = %d（err=%v），期望 1", count, err)
	}

	// 后台接管完成后才有引用；此后的读取直接复用，不再转发。
	queue.handle(ctx, image)
	if taker.taken != 1 {
		t.Fatalf("接管次数 = %d，期望 1", taker.taken)
	}

	source, ok := resolver(ctx, image)
	if !ok || source.BotID != 8 || source.FileID != "file-b" || !source.FromRef {
		t.Fatalf("再次选源 = %+v ok=%v，期望直接复用已登记引用", source, ok)
	}
	if taker.taken != 1 {
		t.Fatalf("已有引用时不应再次接管，实际 %d 次", taker.taken)
	}
	if count, err := repository.CountImagesExclusiveToRemoval(ctx, 7, []int64{8}); err != nil || count != 0 {
		t.Fatalf("接管后独占计数 = %d（err=%v），期望 0", count, err)
	}
}

// dualBotFake 是接管后的读图提供方替身：只认自己取得的文件编号，并支持区间读取。
type dualBotFake struct {
	botID int64
	taken int
	body  []byte
	opens int
}

func (f *dualBotFake) Name() string { return "dual-bot-fake" }

func (f *dualBotFake) Upload(context.Context, storage.UploadRequest) (storage.UploadResult, error) {
	return storage.UploadResult{}, nil
}

func (f *dualBotFake) ChatInfoOf(_ context.Context, chatID string) (storage.ChatInfo, error) {
	switch strings.TrimSpace(chatID) {
	case "-100999":
		return storage.ChatInfo{ID: -100999, Type: "channel", Title: "维护"}, nil
	case "-100777":
		return storage.ChatInfo{ID: -100777, Type: "channel", Title: "图床"}, nil
	}
	return storage.ChatInfo{}, errors.New("会话不符")
}

func (f *dualBotFake) TakeoverFile(_ context.Context, destChatID, fromChatID string, messageID int64) (string, error) {
	if destChatID != "-100999" || fromChatID != "-100777" || messageID != 55 {
		return "", errors.New("转发位置不符")
	}
	f.taken++
	return "file-b", nil
}

func (f *dualBotFake) OpenStream(_ context.Context, fileID string) (storage.Stream, error) {
	f.opens++
	if fileID != "file-b" {
		return storage.Stream{}, storage.ErrFileUnavailable
	}
	return storage.Stream{
		Body:        io.NopCloser(bytes.NewReader(f.body)),
		ContentType: "video/mp4",
		Size:        int64(len(f.body)),
	}, nil
}

func (f *dualBotFake) Size(context.Context, string) (int64, error) { return int64(len(f.body)), nil }

func (f *dualBotFake) OpenStreamRange(_ context.Context, fileID string, r storage.ByteRange) (storage.Stream, error) {
	if fileID != "file-b" {
		return storage.Stream{}, storage.ErrFileUnavailable
	}
	start := r.Start
	end := int64(len(f.body))
	if r.Length >= 0 && start+r.Length < end {
		end = start + r.Length
	}
	if start > end || start > int64(len(f.body)) {
		return storage.Stream{}, storage.ErrFileUnavailable
	}
	return storage.Stream{
		Body:        io.NopCloser(bytes.NewReader(f.body[start:end])),
		ContentType: "video/mp4",
		Size:        int64(len(f.body)),
		Start:       start,
		End:         end - 1,
	}, nil
}

// TestGatewayTakeoverServesAfterSourceRemoved 用双 Bot 样本走完整读图链路：
// 原 Bot 移除、没有任何替代引用时由 B 转发接管，随后的 GET、HEAD 与 MP4 Range 都可用，
// 且原 Bot 的删除保护在引用登记后解除。
func TestGatewayTakeoverServesAfterSourceRemoved(t *testing.T) {
	handle, err := db.Open(filepath.Join(t.TempDir(), "dual.db"))
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	if err := db.Migrate(context.Background(), handle); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	repository := posts.NewRepository(handle)
	ctx := context.Background()
	postID, err := repository.Create(ctx, posts.Post{Title: "接管", ImageCount: 1, UploadMode: storage.ModeDocument})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	if _, err := repository.AttachImage(ctx, posts.Image{
		PostID: postID, SortOrder: 1, Status: posts.ImageStatusUploaded,
		MimeType: posts.MimeVideoMP4, FileSize: 10, TempFilePath: "tmp/a.part",
	}); err != nil {
		t.Fatalf("保存图片失败: %v", err)
	}
	images, err := repository.ListImages(ctx, postID)
	if err != nil || len(images) != 1 {
		t.Fatalf("读取图片失败: %v", err)
	}
	imageID := images[0].ID
	if err := repository.MarkImageStoredWithOrigin(ctx, imageID, "file-a", 55, "telegram", 7, "-100777"); err != nil {
		t.Fatalf("写入逐图来源失败: %v", err)
	}
	if err := repository.MarkPostFinished(ctx, postID, posts.StatusCompleted, ""); err != nil {
		t.Fatalf("标记完成失败: %v", err)
	}
	if err := repository.SetPublic(ctx, postID, true); err != nil {
		t.Fatalf("设置公开失败: %v", err)
	}
	shortCode := images[0].ShortCode

	body := []byte("0123456789")
	taker := &dualBotFake{botID: 8, body: body}
	resolver := newImageResolver(imageResolverDeps{
		repo: repository,
		// 7 号 Bot 已从池里移除，只剩 8 号。
		bots: func(context.Context) ([]botpool.Bot, error) {
			return []botpool.Bot{{BotID: 8, Token: "888:ok", Status: botpool.StatusOK}}, nil
		},
		effective: func(context.Context) (settings.Effective, error) { return settings.Effective{}, nil },
		provider: func(bot botpool.Bot, _ settings.Effective) storage.StreamProvider {
			if bot.BotID != 8 {
				return nil
			}
			return taker
		},
	})
	router := api.NewRouter(api.Deps{
		Version: "test", Commit: "abc1234", Posts: repository, Streams: taker,
		ResolveImage: resolver, Logger: log.New(io.Discard, "", 0),
	})
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	request := func(method, rangeHeader string) (*http.Response, []byte) {
		req, err := http.NewRequest(method, server.URL+"/i/"+shortCode, nil)
		if err != nil {
			t.Fatalf("构造请求失败: %v", err)
		}
		if rangeHeader != "" {
			req.Header.Set("Range", rangeHeader)
		}
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("请求失败: %v", err)
		}
		defer func() { _ = response.Body.Close() }()
		payload, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatalf("读取响应失败: %v", err)
		}
		return response, payload
	}

	// 首次读取只登记后台接管，公开路径不同步转发。
	response, _ := request(http.MethodGet, "")
	if response.StatusCode == http.StatusOK {
		t.Fatal("缺引用且来源已移除时首次读取不应同步转发")
	}
	if taker.taken != 0 {
		t.Fatalf("公开 GET 阶段不应转发，实际 %d 次", taker.taken)
	}

	// 后台接管按合并队列执行一次，随后的读取走已登记引用。
	queue := newTakeoverQueue(takeoverDeps{
		repo:   repository,
		chatID: "-100999",
		bots: func(context.Context) ([]botpool.Bot, error) {
			return []botpool.Bot{{BotID: 8, Token: "888:ok", Status: botpool.StatusOK}}, nil
		},
		effective: func(context.Context) (settings.Effective, error) { return settings.Effective{}, nil },
		provider: func(bot botpool.Bot, _ settings.Effective) storage.StreamProvider {
			if bot.BotID != 8 {
				return nil
			}
			return taker
		},
	})
	image := &posts.PublicImage{
		ImageID: imageID, RemoteFileID: "file-a", MIMEType: posts.MimeVideoMP4,
		UploadMode: storage.ModeDocument, SourceBotID: 7, SourceChatID: "-100777", SourceMessageID: 55,
	}
	queue.handle(ctx, image)
	if taker.taken != 1 {
		t.Fatalf("接管次数 = %d，期望 1", taker.taken)
	}
	response, payload := request(http.MethodGet, "")
	if response.StatusCode != http.StatusOK || string(payload) != string(body) {
		t.Fatalf("接管后 GET = %d/%q，期望 200 与完整内容", response.StatusCode, payload)
	}

	response, _ = request(http.MethodHead, "bytes=2-5")
	if response.StatusCode != http.StatusPartialContent {
		t.Fatalf("HEAD 状态码 = %d，期望 206", response.StatusCode)
	}
	response, payload = request(http.MethodGet, "bytes=2-5")
	if response.StatusCode != http.StatusPartialContent || string(payload) != "2345" {
		t.Fatalf("Range GET = %d/%q，期望 206 与 2345", response.StatusCode, payload)
	}

	// 引用已登记，后续读取不再重复接管；原 Bot 的删除保护随之解除。
	if taker.taken != 1 {
		t.Fatalf("再次读取不应重复接管，实际 %d 次", taker.taken)
	}
	if count, err := repository.CountImagesExclusiveToRemoval(ctx, 7, []int64{8}); err != nil || count != 0 {
		t.Fatalf("独占计数 = %d（err=%v），期望 0", count, err)
	}
}

// TestTakeoverQueueCoalescesSameImage 验证后台接管的合并与有界：
// 同一张图在排队期间只保留一条，队列满时丢弃并可再次登记。
func TestTakeoverQueueCoalescesSameImage(t *testing.T) {
	queue := newTakeoverQueue(takeoverDeps{})
	image := &posts.PublicImage{ImageID: 5, SourceChatID: "-100777", SourceMessageID: 9}
	queue.Enqueue(image)
	queue.Enqueue(image)
	if got := len(queue.queue); got != 1 {
		t.Fatalf("同一张图的排队条目 = %d，期望 1", got)
	}
	// 缺少原消息位置的条目不入队。
	queue.Enqueue(&posts.PublicImage{ImageID: 6})
	if got := len(queue.queue); got != 1 {
		t.Fatalf("缺少位置时排队条目 = %d，期望仍为 1", got)
	}

	// 填满队列后多余的条目被丢弃，排队标记也一并清掉，下次读取可再次登记。
	for id := int64(100); id < 100+takeoverLimit+5; id++ {
		queue.Enqueue(&posts.PublicImage{ImageID: id, SourceChatID: "-100777", SourceMessageID: 1})
	}
	if got := len(queue.queue); got != takeoverLimit {
		t.Fatalf("排队条目 = %d，期望上限 %d", got, takeoverLimit)
	}
	dropped := int64(100 + takeoverLimit + 4)
	queue.mu.Lock()
	stillPending := queue.pending[dropped]
	queue.mu.Unlock()
	if stillPending {
		t.Fatal("被丢弃的条目不应留在排队标记里")
	}
	queue.Enqueue(&posts.PublicImage{ImageID: dropped, SourceChatID: "-100777", SourceMessageID: 1})
	queue.mu.Lock()
	retried := queue.pending[dropped]
	queue.mu.Unlock()
	if retried {
		t.Fatal("队列仍满时再次登记应被丢弃")
	}
}

// takeoverProbeFake 可按需返回公开会话或读取失败，用于验证接管的前置条件与登记条件。
type takeoverProbeFake struct {
	public    bool
	failRead  bool
	emptyRead bool
	taken     int
}

func (f *takeoverProbeFake) Name() string { return "takeover-probe" }

func (f *takeoverProbeFake) Upload(context.Context, storage.UploadRequest) (storage.UploadResult, error) {
	return storage.UploadResult{}, nil
}

func (f *takeoverProbeFake) ChatInfoOf(_ context.Context, chatID string) (storage.ChatInfo, error) {
	switch strings.TrimSpace(chatID) {
	case "-100888":
		info := storage.ChatInfo{ID: -100888, Type: "channel", Title: "维护"}
		if f.public {
			info.Username = "public_channel"
		}
		return info, nil
	case "-100777":
		return storage.ChatInfo{ID: -100777, Type: "channel", Title: "图床"}, nil
	}
	return storage.ChatInfo{}, errors.New("会话不符")
}

func (f *takeoverProbeFake) TakeoverFile(context.Context, string, string, int64) (string, error) {
	f.taken++
	return "file-b", nil
}

func (f *takeoverProbeFake) OpenStream(context.Context, string) (storage.Stream, error) {
	if f.failRead {
		return storage.Stream{}, storage.ErrFileUnavailable
	}
	if f.emptyRead {
		return storage.Stream{Body: io.NopCloser(strings.NewReader("")), ContentType: "image/jpeg", Size: 0}, nil
	}
	return storage.Stream{Body: io.NopCloser(strings.NewReader("ok")), ContentType: "image/jpeg", Size: 2}, nil
}

// TestTakeoverRequiresPrivateIndependentChannel 验证接管只在独立私有维护频道进行，
// 公开会话、与图床频道相同、或清理后读不到时都不登记删除依据。
func TestTakeoverRequiresPrivateIndependentChannel(t *testing.T) {
	handle, err := db.Open(filepath.Join(t.TempDir(), "takeover-guard.db"))
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	if err := db.Migrate(context.Background(), handle); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	repository := posts.NewRepository(handle)
	ctx := context.Background()
	postID, err := repository.Create(ctx, posts.Post{Title: "接管", ImageCount: 1, UploadMode: storage.ModePhoto})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	if _, err := repository.AttachImage(ctx, posts.Image{
		PostID: postID, SortOrder: 1, Status: posts.ImageStatusUploaded,
		MimeType: "image/jpeg", FileSize: 10, TempFilePath: "tmp/a.part",
	}); err != nil {
		t.Fatalf("保存图片失败: %v", err)
	}
	images, err := repository.ListImages(ctx, postID)
	if err != nil || len(images) != 1 {
		t.Fatalf("读取图片失败: %v", err)
	}
	imageID := images[0].ID
	if err := repository.MarkImageStoredWithOrigin(ctx, imageID, "file-a", 55, "telegram", 7, "-100777"); err != nil {
		t.Fatalf("写入逐图来源失败: %v", err)
	}
	cases := []struct {
		name        string
		chatID      string
		fake        *takeoverProbeFake
		gallery     string
		sourceChat  string
		wantForward int
	}{
		{"公开会话", "-100888", &takeoverProbeFake{public: true}, "-100777", "-100777", 0},
		{"与图床频道相同", "-100777", &takeoverProbeFake{}, "-100777", "-100777", 0},
		{"与来源频道相同", "-100888", &takeoverProbeFake{}, "-100777", "-100888", 0},
		// 先转发才做回读校验：这里允许一次转发，但不能留下可作删除依据的引用。
		{"读不到文件", "-100888", &takeoverProbeFake{failRead: true}, "-100777", "-100777", 1},
		// 回读零字节同样不算读到内容。
		{"回读零字节", "-100888", &takeoverProbeFake{emptyRead: true}, "-100777", "-100777", 1},
	}
	for _, item := range cases {
		queue := newTakeoverQueue(takeoverDeps{
			repo:   repository,
			chatID: item.chatID,
			bots: func(context.Context) ([]botpool.Bot, error) {
				return []botpool.Bot{{BotID: 8, Token: "888:ok", Status: botpool.StatusOK}}, nil
			},
			effective: func(context.Context) (settings.Effective, error) {
				return settings.Effective{TelegramChannel: settings.Value{Value: item.gallery}}, nil
			},
			provider: func(botpool.Bot, settings.Effective) storage.StreamProvider { return item.fake },
		})
		image := &posts.PublicImage{
			ImageID: imageID, RemoteFileID: "file-a", MIMEType: "image/jpeg",
			UploadMode: storage.ModePhoto, SourceBotID: 7, SourceChatID: item.sourceChat, SourceMessageID: 55,
		}
		queue.handle(ctx, image)
		if item.fake.taken != item.wantForward {
			t.Errorf("%s：转发次数 = %d，期望 %d", item.name, item.fake.taken, item.wantForward)
		}
		refs, err := repository.ImageFileRefs(ctx, imageID)
		if err != nil {
			t.Fatalf("%s：读取引用失败: %v", item.name, err)
		}
		for _, ref := range refs {
			if ref.BotID == 8 {
				t.Errorf("%s：不应登记 8 号引用，实际 %+v", item.name, refs)
			}
		}
	}
}
