package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/littleseven2003/SeSeGraph/backend/internal/db"
	"github.com/littleseven2003/SeSeGraph/backend/internal/settings"
	"github.com/littleseven2003/SeSeGraph/backend/internal/storage"
)

// fakeBotAPI 按令牌返回不同的 Bot 编号，用于验证换 Bot 判断。
func fakeBotAPI(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.URL.Path, "/bot")
		token = strings.SplitN(token, "/", 2)[0]
		w.Header().Set("Content-Type", "application/json")
		ids := map[string]int64{"111:token-a": 111, "333:token-a2": 111, "111:token-b": 222}
		id, ok := ids[token]
		if !ok || id == 0 {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error_code": 401})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"id": id}})
	}))
	t.Cleanup(server.Close)
	return server
}

// newGuardEnv 装配守卫、切换器与独立配置库。
func newGuardEnv(t *testing.T, apiBase string, count int64) (*Guard, *storage.Switcher, *settings.Store, *int64) {
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
	images := count
	guard := NewGuard(switcher, store, func(context.Context) (int64, error) { return images, nil })
	return guard, switcher, store, &images
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

func TestGuardActivatesAndPersistsBotID(t *testing.T) {
	api := fakeBotAPI(t)
	guard, switcher, store, _ := newGuardEnv(t, api.URL, 0)
	ctx := context.Background()

	if err := guard.Activate(ctx, newCandidate(t, api.URL, "111:token-a")); err != nil {
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
	api := fakeBotAPI(t)
	guard, switcher, _, images := newGuardEnv(t, api.URL, 5)
	ctx := context.Background()
	*images = 5

	if err := guard.Activate(ctx, newCandidate(t, api.URL, "111:token-a")); err != nil {
		t.Fatalf("首次激活失败: %v", err)
	}
	// 同一 Bot 的另一个令牌（编号同为 111）允许切换。
	if _, err := guard.Validate(ctx, newCandidate(t, api.URL, "333:token-a2")); err != nil {
		t.Errorf("同 Bot 换令牌应允许: %v", err)
	}
	if err := guard.Activate(ctx, newCandidate(t, api.URL, "333:token-a2")); err != nil {
		t.Errorf("同 Bot 换令牌激活失败: %v", err)
	}
	if !switcher.Ready() {
		t.Error("同一 Bot 切换后应继续就绪")
	}
}

func TestGuardRejectsBotSwitchWithStoredImages(t *testing.T) {
	api := fakeBotAPI(t)
	guard, switcher, store, images := newGuardEnv(t, api.URL, 2)
	ctx := context.Background()
	*images = 2

	if err := guard.Activate(ctx, newCandidate(t, api.URL, "111:token-a")); err != nil {
		t.Fatalf("首次激活失败: %v", err)
	}
	err := guard.Activate(ctx, newCandidate(t, api.URL, "111:token-b"))
	if !errors.Is(err, ErrBotMismatch) {
		t.Fatalf("错误 = %v，期望 ErrBotMismatch", err)
	}
	if !switcher.Ready() || guard.BotID() != 111 {
		t.Error("拒绝切换后应保留原提供方")
	}
	if persisted, _, _ := store.Get(ctx, settings.KeyTelegramBotID); persisted != "111" {
		t.Errorf("拒绝后 Bot 编号 = %q，期望保持 111", persisted)
	}
}

func TestGuardAllowsBotSwitchWhenLibraryEmpty(t *testing.T) {
	api := fakeBotAPI(t)
	guard, switcher, store, images := newGuardEnv(t, api.URL, 0)
	ctx := context.Background()
	*images = 0

	if err := guard.Activate(ctx, newCandidate(t, api.URL, "111:token-a")); err != nil {
		t.Fatalf("首次激活失败: %v", err)
	}
	if err := guard.Activate(ctx, newCandidate(t, api.URL, "111:token-b")); err != nil {
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
	api := fakeBotAPI(t)
	guard, switcher, _, _ := newGuardEnv(t, api.URL, 0)

	err := guard.Activate(context.Background(), newCandidate(t, api.URL, "999:bad-token"))
	if !errors.Is(err, ErrVerify) {
		t.Fatalf("错误 = %v，期望 ErrVerify", err)
	}
	if switcher.Ready() {
		t.Error("校验失败时不应切换")
	}
}

// TestGuardHonorsPersistedBotID 验证重启后仍按数据库记录的 Bot 归属判断。
func TestGuardHonorsPersistedBotID(t *testing.T) {
	api := fakeBotAPI(t)
	_, switcher, store, images := newGuardEnv(t, api.URL, 3)
	ctx := context.Background()
	*images = 3
	if err := store.Set(ctx, settings.KeyTelegramBotID, "111"); err != nil {
		t.Fatalf("写入归属失败: %v", err)
	}

	err := NewGuard(switcher, store, func(context.Context) (int64, error) { return *images, nil }).
		Activate(ctx, newCandidate(t, api.URL, "111:token-b"))
	if !errors.Is(err, ErrBotMismatch) {
		t.Fatalf("错误 = %v，期望按持久化归属判定为不匹配", err)
	}
	if switcher.Ready() {
		t.Error("重启后不匹配时不应激活")
	}
}
