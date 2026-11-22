package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/littleseven2003/SeSeGraph/backend/internal/db"
	"github.com/littleseven2003/SeSeGraph/backend/internal/settings"
	"github.com/littleseven2003/SeSeGraph/backend/internal/storage"
)

// 测试用合成令牌，按令牌返回不同的 Bot 编号，用于验证换 Bot 判断。
const (
	tokenBotOne  = "111:test-one"
	tokenBotOne2 = "333:test-one-rotated"
	tokenBotTwo  = "222:test-two"
	tokenInvalid = "999:test-invalid"
)

// fakeBotAPI 模拟 getMe，并记录被调用的次数与令牌。
type fakeBotAPI struct {
	server *httptest.Server
	mu     sync.Mutex
	tokens []string
}

// newFakeBotAPI 启动模拟上游，未知令牌返回 401。
func newFakeBotAPI(t *testing.T) *fakeBotAPI {
	t.Helper()
	api := &fakeBotAPI{}
	ids := map[string]int64{tokenBotOne: 111, tokenBotOne2: 111, tokenBotTwo: 222}
	api.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.URL.Path, "/bot")
		token = strings.SplitN(token, "/", 2)[0]
		api.mu.Lock()
		api.tokens = append(api.tokens, token)
		api.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		id, ok := ids[token]
		if !ok || id == 0 {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error_code": 401})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"id": id}})
	}))
	t.Cleanup(api.server.Close)
	return api
}

// URL 返回模拟上游地址。
func (a *fakeBotAPI) URL() string { return a.server.URL }

// Calls 返回已收到的 getMe 次数。
func (a *fakeBotAPI) Calls() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.tokens)
}

// newGuardEnv 装配守卫、切换器、协调点与独立配置库。
func newGuardEnv(t *testing.T, apiBase string, count int64) (*Guard, *storage.Switcher, *settings.Store, *int64, *storage.UploadGate) {
	t.Helper()
	handle, err := db.Open(filepath.Join(t.TempDir(), "guard.db"))
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	if err := db.Migrate(context.Background(), handle); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	store := settings.NewStore(handle)
	switcher := storage.NewSwitcher()
	gate := storage.NewUploadGate()
	images := count
	guard := NewGuard(switcher, store, func(context.Context) (int64, error) { return images, nil }, gate)
	return guard, switcher, store, &images, gate
}

// newCandidate 按令牌创建候选提供方。
func newCandidate(t *testing.T, apiBase, token string) *storage.Telegram {
	t.Helper()
	provider, err := storage.NewTelegram(storage.TelegramConfig{
		Token:   token,
		ChatID:  "-1001234567890",
		APIBase: apiBase,
	})
	if err != nil {
		t.Fatalf("创建候选提供方失败: %v", err)
	}
	return provider
}

// commit 走一遍保存设置的完整路径：校验候选、落库归属、提交运行时。
func (g *Guard) testCommit(t *testing.T, ctx context.Context, store *settings.Store, candidate *storage.Telegram) error {
	t.Helper()
	prepared, err := g.Prepare(ctx, candidate)
	if err != nil {
		return err
	}
	if prepared.BotID() != 0 && store != nil {
		if err := store.Set(ctx, settings.KeyTelegramBotID, strconv.FormatInt(prepared.BotID(), 10)); err != nil {
			prepared.Abort()
			return err
		}
	}
	g.Commit(ctx, prepared)
	return nil
}

func TestGuardActivatesAndPersistsBotID(t *testing.T) {
	api := newFakeBotAPI(t)
	guard, switcher, store, _, _ := newGuardEnv(t, api.URL(), 0)
	ctx := context.Background()

	if err := guard.Activate(ctx, newCandidate(t, api.URL(), tokenBotOne)); err != nil {
		t.Fatalf("激活失败: %v", err)
	}
	if !switcher.Ready() {
		t.Fatal("激活后提供方应就绪")
	}
	if guard.BotID() != 111 {
		t.Errorf("Bot 编号 = %d，期望 111", guard.BotID())
	}
	persisted, ok, err := store.Get(ctx, settings.KeyTelegramBotID)
	if err != nil || !ok || persisted != "111" {
		t.Errorf("持久化 Bot 编号 = %q ok=%v err=%v，期望 111", persisted, ok, err)
	}

	// 清空配置保留归属记录。
	if err := guard.Activate(ctx, nil); err != nil {
		t.Fatalf("清空失败: %v", err)
	}
	if switcher.Ready() {
		t.Error("清空后不应就绪")
	}
}

func TestGuardAllowsSameBotTokenRotation(t *testing.T) {
	api := newFakeBotAPI(t)
	guard, switcher, store, images, gate := newGuardEnv(t, api.URL(), 5)
	ctx := context.Background()
	*images = 5

	if err := guard.testCommit(t, ctx, store, newCandidate(t, api.URL(), tokenBotOne)); err != nil {
		t.Fatalf("首次激活失败: %v", err)
	}
	// 同一 Bot 的另一个令牌（编号同为 111）允许切换，且不需要独占上传队列。
	if err := guard.testCommit(t, ctx, store, newCandidate(t, api.URL(), tokenBotOne2)); err != nil {
		t.Fatalf("同 Bot 换令牌失败: %v", err)
	}
	if !switcher.Ready() || guard.BotID() != 111 {
		t.Error("同一 Bot 切换后应继续就绪并使用新配置")
	}
	if release, ok := gate.EnterUpload(); !ok {
		t.Error("同 Bot 换令牌不应独占上传队列")
	} else {
		release()
	}
}

func TestGuardRejectsBotSwitchWithStoredImages(t *testing.T) {
	api := newFakeBotAPI(t)
	guard, switcher, store, images, gate := newGuardEnv(t, api.URL(), 2)
	ctx := context.Background()
	*images = 2

	if err := guard.testCommit(t, ctx, store, newCandidate(t, api.URL(), tokenBotOne)); err != nil {
		t.Fatalf("首次激活失败: %v", err)
	}
	err := guard.testCommit(t, ctx, store, newCandidate(t, api.URL(), tokenBotTwo))
	if !errors.Is(err, ErrBotMismatch) {
		t.Fatalf("错误 = %v，期望 ErrBotMismatch", err)
	}
	if !switcher.Ready() || guard.BotID() != 111 {
		t.Error("拒绝切换后应保留原提供方")
	}
	if persisted, _, _ := store.Get(ctx, settings.KeyTelegramBotID); persisted != "111" {
		t.Errorf("拒绝后 Bot 编号 = %q，期望保持 111", persisted)
	}
	// 拒绝后必须释放独占，否则上传队列会被永久挡住。
	if release, ok := gate.EnterUpload(); !ok {
		t.Error("拒绝切换后应释放上传队列")
	} else {
		release()
	}
}

func TestGuardAllowsBotSwitchWhenLibraryEmpty(t *testing.T) {
	api := newFakeBotAPI(t)
	guard, switcher, store, images, _ := newGuardEnv(t, api.URL(), 0)
	ctx := context.Background()
	*images = 0

	if err := guard.testCommit(t, ctx, store, newCandidate(t, api.URL(), tokenBotOne)); err != nil {
		t.Fatalf("首次激活失败: %v", err)
	}
	if err := guard.testCommit(t, ctx, store, newCandidate(t, api.URL(), tokenBotTwo)); err != nil {
		t.Fatalf("空图库换 Bot 应允许: %v", err)
	}
	if !switcher.Ready() || guard.BotID() != 222 {
		t.Error("换 Bot 后应使用新提供方")
	}
	if persisted, _, _ := store.Get(ctx, settings.KeyTelegramBotID); persisted != "222" {
		t.Errorf("Bot 编号 = %q，期望 222", persisted)
	}
}

func TestGuardRejectsInvalidCredential(t *testing.T) {
	api := newFakeBotAPI(t)
	guard, switcher, _, _, _ := newGuardEnv(t, api.URL(), 0)

	if _, err := guard.Prepare(context.Background(), newCandidate(t, api.URL(), tokenInvalid)); !errors.Is(err, ErrVerify) {
		t.Fatalf("错误 = %v，期望 ErrVerify", err)
	}
	if switcher.Ready() {
		t.Error("校验失败时不应切换")
	}
}

// TestGuardHonorsPersistedBotID 验证重启后仍按数据库记录的 Bot 归属判断。
func TestGuardHonorsPersistedBotID(t *testing.T) {
	api := newFakeBotAPI(t)
	_, switcher, store, images, _ := newGuardEnv(t, api.URL(), 3)
	ctx := context.Background()
	*images = 3
	if err := store.Set(ctx, settings.KeyTelegramBotID, "111"); err != nil {
		t.Fatalf("写入归属失败: %v", err)
	}

	err := NewGuard(switcher, store, func(context.Context) (int64, error) { return *images, nil }, storage.NewUploadGate()).
		Activate(ctx, newCandidate(t, api.URL(), tokenBotTwo))
	if !errors.Is(err, ErrBotMismatch) {
		t.Fatalf("错误 = %v，期望按持久化归属判定为不匹配", err)
	}
	if switcher.Ready() {
		t.Error("重启后不匹配时不应激活")
	}
}

// TestPrepareReusesVerificationAndPersistsNothing 确认候选只校验一次，
// 且 Commit 不写库：归属由调用方与配置在同一事务内落库。
func TestPrepareReusesVerificationAndPersistsNothing(t *testing.T) {
	api := newFakeBotAPI(t)
	guard, switcher, store, _, _ := newGuardEnv(t, api.URL(), 0)
	ctx := context.Background()

	prepared, err := guard.Prepare(ctx, newCandidate(t, api.URL(), tokenBotOne))
	if err != nil {
		t.Fatalf("校验失败: %v", err)
	}
	if prepared.BotID() != 111 {
		t.Errorf("候选 Bot 编号 = %d，期望 111", prepared.BotID())
	}
	if api.Calls() != 1 {
		t.Errorf("getMe 调用 = %d，期望 1", api.Calls())
	}

	guard.Commit(ctx, prepared)
	if api.Calls() != 1 {
		t.Errorf("提交后又调用了上游：getMe 调用 = %d，期望仍为 1", api.Calls())
	}
	if _, ok, _ := store.Get(ctx, settings.KeyTelegramBotID); ok {
		t.Error("提交运行时不应写库，归属应与配置同事务写入")
	}
	if !switcher.Ready() || guard.BotID() != 111 {
		t.Error("提交后应使用候选提供方")
	}
}

// TestPrepareRejectsSwitchWhileUploadInFlight 确认首图落库前尝试换 Bot 会被拒绝。
// 图库统计仍为 0，但已有在途任务，切换必须等这一帖结束。
func TestPrepareRejectsSwitchWhileUploadInFlight(t *testing.T) {
	api := newFakeBotAPI(t)
	guard, switcher, store, images, gate := newGuardEnv(t, api.URL(), 0)
	ctx := context.Background()
	*images = 0

	if err := guard.testCommit(t, ctx, store, newCandidate(t, api.URL(), tokenBotOne)); err != nil {
		t.Fatalf("首次激活失败: %v", err)
	}

	release, ok := gate.EnterUpload()
	if !ok {
		t.Fatal("上传名额应可领取")
	}
	if _, err := guard.Prepare(ctx, newCandidate(t, api.URL(), tokenBotTwo)); !errors.Is(err, ErrUploadBusy) {
		t.Fatalf("错误 = %v，期望 ErrUploadBusy", err)
	}
	if guard.BotID() != 111 || !switcher.Ready() {
		t.Error("拒绝后应保留原提供方")
	}

	// 首图落库、任务结束；图库出现图片后换 Bot 转为归属冲突。
	release()
	*images = 1
	if _, err := guard.Prepare(ctx, newCandidate(t, api.URL(), tokenBotTwo)); !errors.Is(err, ErrBotMismatch) {
		t.Fatalf("任务结束后的错误 = %v，期望 ErrBotMismatch", err)
	}
}

// TestPrepareWaitsForUploadToFinish 确认切换会等待在途上传结束再统计图片。
func TestPrepareWaitsForUploadToFinish(t *testing.T) {
	api := newFakeBotAPI(t)
	guard, _, store, images, gate := newGuardEnv(t, api.URL(), 0)
	ctx := context.Background()
	*images = 0
	guard.SetSwitchWait(2 * time.Second)

	if err := guard.testCommit(t, ctx, store, newCandidate(t, api.URL(), tokenBotOne)); err != nil {
		t.Fatalf("首次激活失败: %v", err)
	}

	release, ok := gate.EnterUpload()
	if !ok {
		t.Fatal("上传名额应可领取")
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		release()
	}()

	prepared, err := guard.Prepare(ctx, newCandidate(t, api.URL(), tokenBotTwo))
	if err != nil {
		t.Fatalf("等待在途任务后应允许切换: %v", err)
	}
	defer prepared.Abort()
	if prepared.BotID() != 222 {
		t.Errorf("候选 Bot 编号 = %d，期望 222", prepared.BotID())
	}
}

// TestPrepareBlocksNewUploadsUntilCommit 确认切换窗口内不会领取上传任务，
// 避免图片落到即将被替换的 Bot 名下。
func TestPrepareBlocksNewUploadsUntilCommit(t *testing.T) {
	api := newFakeBotAPI(t)
	guard, _, store, images, gate := newGuardEnv(t, api.URL(), 0)
	ctx := context.Background()
	*images = 0

	if err := guard.testCommit(t, ctx, store, newCandidate(t, api.URL(), tokenBotOne)); err != nil {
		t.Fatalf("首次激活失败: %v", err)
	}
	prepared, err := guard.Prepare(ctx, newCandidate(t, api.URL(), tokenBotTwo))
	if err != nil {
		t.Fatalf("空图库换 Bot 应允许: %v", err)
	}
	if _, ok := gate.EnterUpload(); ok {
		t.Fatal("切换窗口内不应领取上传任务")
	}
	*images = 0
	guard.Commit(ctx, prepared)
	release, ok := gate.EnterUpload()
	if !ok {
		t.Fatal("提交后应释放上传队列")
	}
	release()
	if guard.BotID() != 222 {
		t.Errorf("提交后 Bot 编号 = %d，期望 222", guard.BotID())
	}
}
