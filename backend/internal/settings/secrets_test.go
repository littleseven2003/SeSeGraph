package settings

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// rawValue 直接读库，绕过解密，用于确认磁盘上是密文。
func rawValue(t *testing.T, store *Store, key string) string {
	t.Helper()
	var value string
	if err := store.handle.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&value); err != nil {
		t.Fatalf("读原始值失败: %v", err)
	}
	return value
}

func TestStoreSealsCredentialKeysAtRest(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()

	if err := store.Set(ctx, KeyTelegramToken, "111:first-token"); err != nil {
		t.Fatalf("写入令牌失败: %v", err)
	}
	raw := rawValue(t, store, KeyTelegramToken)
	if !IsSealed(raw) || strings.Contains(raw, "first-token") {
		t.Fatalf("令牌未加密落库: %q", raw)
	}
	if value, _, err := store.Get(ctx, KeyTelegramToken); err != nil || value != "111:first-token" {
		t.Fatalf("读取令牌 = %q err=%v", value, err)
	}

	poolJSON := `[{"bot_id":111,"alias":"默认","token":"111:pool-token","priority":0}]`
	if err := store.Set(ctx, KeyTelegramBots, poolJSON); err != nil {
		t.Fatalf("写入 Bot 池失败: %v", err)
	}
	rawPool := rawValue(t, store, KeyTelegramBots)
	if !IsSealed(rawPool) || strings.Contains(rawPool, "pool-token") {
		t.Fatalf("Bot 池未加密落库: %q", rawPool)
	}
	snapshot, err := store.Snapshot(ctx)
	if err != nil || snapshot[KeyTelegramBots] != poolJSON {
		t.Fatalf("快照 Bot 池 = %q err=%v", snapshot[KeyTelegramBots], err)
	}

	// 非敏感配置保持明文，便于排查。
	if err := store.Set(ctx, KeyTelegramChannel, "@channel"); err != nil {
		t.Fatalf("写入频道失败: %v", err)
	}
	if got := rawValue(t, store, KeyTelegramChannel); got != "@channel" {
		t.Fatalf("频道应保持明文，实际 %q", got)
	}
}

func TestStoreRejectsCredentialWithoutCipher(t *testing.T) {
	store := newStoreWithoutCipher(t)
	ctx := context.Background()

	if err := store.Set(ctx, KeyTelegramToken, "111:plain"); err == nil || !errors.Is(err, ErrSecretKeyMissing) {
		t.Fatalf("未配置密钥写入令牌应拒绝，实际 err=%v", err)
	}
	if err := store.Set(ctx, KeyTelegraphToken, "telegraph-token"); err == nil || !errors.Is(err, ErrSecretKeyMissing) {
		t.Fatalf("未配置密钥写入 Telegraph 令牌应拒绝，实际 err=%v", err)
	}
	if err := store.Set(ctx, KeyTelegramChannel, "@channel"); err != nil {
		t.Fatalf("非敏感配置不应受影响: %v", err)
	}
}

func TestSanitizeSecretsEncryptsLegacyPlaintext(t *testing.T) {
	store := newStoreWithoutCipher(t)
	ctx := context.Background()
	// 模拟历史库：直接写入明文（绕过加密层）。
	legacy := map[string]string{
		KeyTelegramToken:  "111:legacy-token",
		KeyTelegraphToken: "telegraph-legacy",
		KeyTelegramBots:   `[{"bot_id":111,"alias":"默认","token":"111:legacy-token","priority":0}]`,
	}
	for key, value := range legacy {
		if _, err := store.handle.ExecContext(ctx,
			`INSERT INTO settings (key, value) VALUES (?, ?)`, key, value); err != nil {
			t.Fatalf("写入历史明文失败: %v", err)
		}
	}

	// 没密钥时必须报错，且不改动数据。
	if _, err := SanitizeSecrets(ctx, store); !errors.Is(err, ErrSecretKeyMissing) {
		t.Fatalf("缺密钥时应返回 ErrSecretKeyMissing，实际 %v", err)
	}
	if got := rawValue(t, store, KeyTelegramToken); got != "111:legacy-token" {
		t.Fatalf("缺密钥时不应改动数据，实际 %q", got)
	}

	// 注入密钥后一次清理：全部变密文，读出来仍是原值。
	store.SetCipher(testCipher(t))
	count, err := SanitizeSecrets(ctx, store)
	if err != nil || count != 3 {
		t.Fatalf("清理 = %d err=%v，期望 3 项", count, err)
	}
	for key := range legacy {
		if raw := rawValue(t, store, key); !IsSealed(raw) {
			t.Fatalf("%s 仍是明文: %q", key, raw)
		}
	}
	if value, _, err := store.Get(ctx, KeyTelegraphToken); err != nil || value != "telegraph-legacy" {
		t.Fatalf("清理后读取 = %q err=%v", value, err)
	}

	// 幂等：再跑一次没有可改写的项。
	if again, err := SanitizeSecrets(ctx, store); err != nil || again != 0 {
		t.Fatalf("重复清理 = %d err=%v，期望 0", again, err)
	}
	// 空值不算明文项。
	if err := store.Set(ctx, KeyTelegramToken, ""); err != nil {
		t.Fatalf("清空令牌失败: %v", err)
	}
	if left, err := SanitizeSecrets(ctx, store); err != nil || left != 0 {
		t.Fatalf("空值清理 = %d err=%v，期望 0", left, err)
	}
}
