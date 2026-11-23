package db

import (
	"context"
	"path/filepath"
	"testing"
)

// TestMigrateAddsSessionVersion 验证 v8 旧库升级后补上会话版本列：
// 已有管理员记录默认 0，升级前的令牌因此仍然有效。
func TestMigrateAddsSessionVersion(t *testing.T) {
	handle, err := Open(filepath.Join(t.TempDir(), "legacy-v8.db"))
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	ctx := context.Background()

	if _, err := handle.ExecContext(ctx, `CREATE TABLE admin_users (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		username TEXT NOT NULL,
		password_hash TEXT NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		t.Fatalf("创建旧表失败: %v", err)
	}
	if _, err := handle.ExecContext(ctx,
		`INSERT INTO admin_users (id, username, password_hash) VALUES (1, 'admin', 'legacy-hash')`); err != nil {
		t.Fatalf("写入旧记录失败: %v", err)
	}
	if _, err := handle.ExecContext(ctx, "PRAGMA user_version = 8"); err != nil {
		t.Fatalf("写入旧版本号失败: %v", err)
	}

	if err := Migrate(ctx, handle); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}

	var (
		version  int
		username string
		session  int
	)
	if err := handle.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("读取版本失败: %v", err)
	}
	if version != SchemaVersion {
		t.Fatalf("版本 = %d，期望 %d", version, SchemaVersion)
	}
	if err := handle.QueryRowContext(ctx,
		"SELECT username, session_version FROM admin_users WHERE id = 1").Scan(&username, &session); err != nil {
		t.Fatalf("读取会话版本失败: %v", err)
	}
	if username != "admin" || session != 0 {
		t.Errorf("旧记录升级后 = %q/%d，期望 admin/0", username, session)
	}
}
