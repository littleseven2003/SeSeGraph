package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// TestMigrateAddsViewsColumns 验证 v7 旧库升级到当前版本后补上浏览量字段：
// 已有记录默认 0 且时间戳为空，表示尚未同步。
func TestMigrateAddsViewsColumns(t *testing.T) {
	handle, err := Open(filepath.Join(t.TempDir(), "legacy-v7.db"))
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	ctx := context.Background()

	if _, err := handle.ExecContext(ctx, `CREATE TABLE posts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		title TEXT NOT NULL,
		description TEXT DEFAULT '',
		author_name TEXT DEFAULT '',
		cover_thumb_path TEXT DEFAULT '',
		telegraph_urls TEXT DEFAULT '[]',
		telegraph_paths TEXT DEFAULT '[]',
		image_count INTEGER DEFAULT 0,
		upload_mode TEXT DEFAULT 'photo',
		status TEXT DEFAULT 'queued',
		error_message TEXT DEFAULT '',
		is_disabled INTEGER DEFAULT 0,
		is_public INTEGER DEFAULT 0,
		publish_state TEXT DEFAULT 'idle',
		publish_error TEXT DEFAULT '',
		published_at DATETIME DEFAULT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		t.Fatalf("创建旧表失败: %v", err)
	}
	if _, err := handle.ExecContext(ctx,
		`INSERT INTO posts (title, status, is_public, publish_state, telegraph_paths, updated_at)
		 VALUES ('已发布图集', 'completed', 1, 'published', '["Page-11-22"]', '2026-11-20 10:00:00')`); err != nil {
		t.Fatalf("写入旧记录失败: %v", err)
	}
	if _, err := handle.ExecContext(ctx, "PRAGMA user_version = 7"); err != nil {
		t.Fatalf("写入旧版本号失败: %v", err)
	}

	if err := Migrate(ctx, handle); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}

	var version int
	if err := handle.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("读取版本失败: %v", err)
	}
	if version != 9 || SchemaVersion != 9 {
		t.Fatalf("版本 = %d，SchemaVersion = %d，期望 9", version, SchemaVersion)
	}

	var count int
	var updatedAt sql.NullString
	if err := handle.QueryRowContext(ctx,
		`SELECT views_count, views_updated_at FROM posts WHERE title = '已发布图集'`).
		Scan(&count, &updatedAt); err != nil {
		t.Fatalf("读取浏览量字段失败: %v", err)
	}
	if count != 0 {
		t.Errorf("浏览量 = %d，期望 0", count)
	}
	if updatedAt.Valid {
		t.Errorf("同步时间 = %q，期望为空", updatedAt.String)
	}

	// 迁移可重复执行：再跑一次不应报错，也不应重置已有缓存。
	if _, err := handle.ExecContext(ctx,
		`UPDATE posts SET views_count = 7, views_updated_at = '2026-11-22T08:00:00Z' WHERE title = '已发布图集'`); err != nil {
		t.Fatalf("写入缓存失败: %v", err)
	}
	if err := Migrate(ctx, handle); err != nil {
		t.Fatalf("重复迁移失败: %v", err)
	}
	if err := handle.QueryRowContext(ctx,
		`SELECT views_count FROM posts WHERE title = '已发布图集'`).Scan(&count); err != nil {
		t.Fatalf("读取浏览量失败: %v", err)
	}
	if count != 7 {
		t.Fatalf("重复迁移改写了缓存：%d", count)
	}
}
