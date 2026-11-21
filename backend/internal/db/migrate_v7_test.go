package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// TestMigrateBackfillsPublishedAt 验证 v6 旧库升级后补上发布时间：
// 已发布帖用升级前的更新时间回填，未发布帖留空。
func TestMigrateBackfillsPublishedAt(t *testing.T) {
	handle, err := Open(filepath.Join(t.TempDir(), "legacy-v6.db"))
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
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		t.Fatalf("创建旧表失败: %v", err)
	}
	if _, err := handle.ExecContext(ctx,
		`INSERT INTO posts (title, status, is_public, publish_state, updated_at)
		 VALUES ('已发布图集', 'completed', 1, 'published', '2026-11-20 10:00:00')`); err != nil {
		t.Fatalf("写入已发布帖失败: %v", err)
	}
	if _, err := handle.ExecContext(ctx,
		`INSERT INTO posts (title, status, publish_state, updated_at)
		 VALUES ('未发布图集', 'completed', 'idle', '2026-11-21 10:00:00')`); err != nil {
		t.Fatalf("写入未发布帖失败: %v", err)
	}
	if _, err := handle.ExecContext(ctx, "PRAGMA user_version = 6"); err != nil {
		t.Fatalf("写入旧版本号失败: %v", err)
	}

	if err := Migrate(ctx, handle); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}

	var version int
	if err := handle.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("读取版本失败: %v", err)
	}
	if version != SchemaVersion {
		t.Errorf("版本 = %d，期望 %d", version, SchemaVersion)
	}

	var publishedAt sql.NullString
	if err := handle.QueryRowContext(ctx,
		`SELECT published_at FROM posts WHERE title = '已发布图集'`).Scan(&publishedAt); err != nil {
		t.Fatalf("读取发布时间失败: %v", err)
	}
	if !publishedAt.Valid || publishedAt.String != "2026-11-20T10:00:00Z" {
		t.Errorf("已发布帖发布时间 = %v，期望回填更新时间", publishedAt)
	}

	var idleAt sql.NullString
	if err := handle.QueryRowContext(ctx,
		`SELECT published_at FROM posts WHERE title = '未发布图集'`).Scan(&idleAt); err != nil {
		t.Fatalf("读取发布时间失败: %v", err)
	}
	if idleAt.Valid && idleAt.String != "" {
		t.Errorf("未发布帖发布时间应为空，实际 %v", idleAt)
	}

	// 重复迁移不应再次改写已有发布时间。
	if _, err := handle.ExecContext(ctx,
		`UPDATE posts SET published_at = '2026-11-19 09:00:00' WHERE title = '已发布图集'`); err != nil {
		t.Fatalf("改写发布时间失败: %v", err)
	}
	if _, err := handle.ExecContext(ctx, "PRAGMA user_version = 7"); err != nil {
		t.Fatalf("写入版本号失败: %v", err)
	}
	if err := Migrate(ctx, handle); err != nil {
		t.Fatalf("重复迁移失败: %v", err)
	}
	if err := handle.QueryRowContext(ctx,
		`SELECT published_at FROM posts WHERE title = '已发布图集'`).Scan(&publishedAt); err != nil {
		t.Fatalf("读取发布时间失败: %v", err)
	}
	if publishedAt.String != "2026-11-19T09:00:00Z" {
		t.Errorf("重复迁移不应改写发布时间，实际 %q", publishedAt.String)
	}
}
