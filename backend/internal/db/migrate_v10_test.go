package db

import (
	"context"
	"path/filepath"
	"testing"
)

// TestMigrateAddsPendingDelete 验证 v9 旧库升级后补上待删除标记，旧记录默认 0。
func TestMigrateAddsPendingDelete(t *testing.T) {
	handle, err := Open(filepath.Join(t.TempDir(), "legacy-v9.db"))
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	ctx := context.Background()

	if _, err := handle.ExecContext(ctx, `CREATE TABLE posts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		title TEXT NOT NULL,
		description TEXT DEFAULT '',
		status TEXT DEFAULT 'completed',
		publish_state TEXT DEFAULT 'published',
		is_public INTEGER DEFAULT 1,
		is_disabled INTEGER DEFAULT 0,
		telegraph_urls TEXT DEFAULT '[]',
		telegraph_paths TEXT DEFAULT '[]',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		t.Fatalf("创建旧表失败: %v", err)
	}
	if _, err := handle.ExecContext(ctx,
		`INSERT INTO posts (title, status, publish_state, is_public) VALUES ('旧图集', 'completed', 'published', 1)`); err != nil {
		t.Fatalf("写入旧记录失败: %v", err)
	}
	if _, err := handle.ExecContext(ctx, "PRAGMA user_version = 9"); err != nil {
		t.Fatalf("写入旧版本号失败: %v", err)
	}

	if err := Migrate(ctx, handle); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}

	var (
		version int
		title   string
		pending int
	)
	if err := handle.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("读取版本失败: %v", err)
	}
	if version != SchemaVersion {
		t.Fatalf("版本 = %d，期望 %d", version, SchemaVersion)
	}
	if err := handle.QueryRowContext(ctx,
		"SELECT title, pending_delete FROM posts WHERE id = 1").Scan(&title, &pending); err != nil {
		t.Fatalf("读取待删除标记失败: %v", err)
	}
	if title != "旧图集" || pending != 0 {
		t.Errorf("旧记录升级后 = %q/%d，期望 旧图集/0", title, pending)
	}
}
