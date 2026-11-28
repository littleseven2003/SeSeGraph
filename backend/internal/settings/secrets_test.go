package settings

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/littleseven2003/SeSeGraph/backend/internal/db"
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

func dbFilePath(t *testing.T, store *Store) string {
	t.Helper()
	var seq int
	var name, file string
	if err := store.handle.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &file); err != nil {
		t.Fatalf("读取数据库路径失败: %v", err)
	}
	return file
}

func TestSanitizeSecretsCoversProxyCredentials(t *testing.T) {
	store := newStoreWithoutCipher(t)
	ctx := context.Background()
	proxyURL := "http://user:pass@127.0.0.1:7890"
	if _, err := store.handle.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES (?, ?)`, KeyProxyURL, proxyURL); err != nil {
		t.Fatalf("写入历史代理失败: %v", err)
	}
	store.SetCipher(testCipher(t))
	count, err := SanitizeSecrets(ctx, store)
	if err != nil || count != 1 {
		t.Fatalf("清理 = %d err=%v，期望 1 项", count, err)
	}
	if raw := rawValue(t, store, KeyProxyURL); !IsSealed(raw) {
		t.Fatalf("代理地址未加密: %q", raw)
	}
	if value, _, err := store.Get(ctx, KeyProxyURL); err != nil || value != proxyURL {
		t.Fatalf("代理地址解密 = %q err=%v", value, err)
	}
}

func TestSanitizeSecretsWaitsForCheckpointThenRecovers(t *testing.T) {
	oldAttempts, oldDelay := cleanupAttempts, cleanupDelay
	cleanupAttempts, cleanupDelay = 3, 5*time.Millisecond
	defer func() {
		cleanupAttempts, cleanupDelay = oldAttempts, oldDelay
	}()

	store := newStoreWithoutCipher(t)
	ctx := context.Background()
	secret := "111:legacy-token-zzz"
	if _, err := store.handle.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES (?, ?)`, KeyTelegramToken, secret); err != nil {
		t.Fatalf("写入历史令牌失败: %v", err)
	}
	path := dbFilePath(t, store)
	store.SetCipher(testCipher(t))

	// 另一条连接持有读事务：WAL 检查点会被挡住，清理不能算完成。
	reader, err := db.Open(path)
	if err != nil {
		t.Fatalf("打开第二连接失败: %v", err)
	}
	defer func() { _ = reader.Close() }()
	readTx, err := reader.Begin()
	if err != nil {
		t.Fatalf("开启读事务失败: %v", err)
	}
	var rows int
	if err := readTx.QueryRow(`SELECT count(*) FROM settings`).Scan(&rows); err != nil {
		t.Fatalf("读取快照失败: %v", err)
	}
	if _, err := SanitizeSecrets(ctx, store); !errors.Is(err, ErrCleanupIncomplete) {
		t.Fatalf("存在读者时应返回 ErrCleanupIncomplete，实际 %v", err)
	}
	if _, marked, _ := store.Get(ctx, KeySecretCleanupPending); !marked {
		t.Fatal("清理未完成时待清理标记必须保留")
	}

	// 释放读者后重跑：标记清除，主库不再有明文字节。
	if err := readTx.Rollback(); err != nil {
		t.Fatalf("回滚读事务失败: %v", err)
	}
	if _, err := SanitizeSecrets(ctx, store); err != nil {
		t.Fatalf("释放读者后清理失败: %v", err)
	}
	if _, marked, _ := store.Get(ctx, KeySecretCleanupPending); marked {
		t.Fatal("清理完成后应清除待清理标记")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取数据库文件失败: %v", err)
	}
	if bytes.Contains(content, []byte(secret)) {
		t.Fatal("主库文件仍含历史明文")
	}
}
