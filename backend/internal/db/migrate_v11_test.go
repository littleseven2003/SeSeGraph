package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// TestMigrateAddsCoverAndPathFields 验证 v10 旧库升级后补上封面序号与链接冻结字段，
// 旧记录保持空值，发布行为与升级前一致。
func TestMigrateAddsCoverAndPathFields(t *testing.T) {
	handle, err := Open(filepath.Join(t.TempDir(), "legacy-v10.db"))
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
		status TEXT DEFAULT 'completed',
		error_message TEXT DEFAULT '',
		is_disabled INTEGER DEFAULT 0,
		is_public INTEGER DEFAULT 1,
		publish_state TEXT DEFAULT 'published',
		publish_error TEXT DEFAULT '',
		pending_delete INTEGER NOT NULL DEFAULT 0,
		published_at DATETIME DEFAULT NULL,
		views_count INTEGER NOT NULL DEFAULT 0,
		views_updated_at DATETIME DEFAULT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		t.Fatalf("创建旧表失败: %v", err)
	}
	if _, err := handle.ExecContext(ctx,
		`INSERT INTO posts (title, status, publish_state, is_public) VALUES ('旧图集', 'completed', 'published', 1)`); err != nil {
		t.Fatalf("写入旧记录失败: %v", err)
	}
	if _, err := handle.ExecContext(ctx, "PRAGMA user_version = 10"); err != nil {
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
		t.Fatalf("版本 = %d，期望 %d", version, SchemaVersion)
	}

	var (
		title    string
		cover    sql.NullInt64
		pathMode string
		slug     string
	)
	if err := handle.QueryRowContext(ctx,
		"SELECT title, cover_sort_order, publish_path_mode, publish_slug FROM posts WHERE id = 1").
		Scan(&title, &cover, &pathMode, &slug); err != nil {
		t.Fatalf("读取升级后字段失败: %v", err)
	}
	if title != "旧图集" {
		t.Errorf("标题 = %q，期望 旧图集", title)
	}
	if cover.Valid {
		t.Errorf("封面序号 = %d，期望为空", cover.Int64)
	}
	if pathMode != "" || slug != "" {
		t.Errorf("链接字段 = %q/%q，期望空值", pathMode, slug)
	}
}

// TestMigrateFreshDatabaseHasCoverAndPathFields 验证新建库直接包含三个新字段。
func TestMigrateFreshDatabaseHasCoverAndPathFields(t *testing.T) {
	handle, err := Open(filepath.Join(t.TempDir(), "fresh.db"))
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	ctx := context.Background()
	if err := Migrate(ctx, handle); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}

	if _, err := handle.ExecContext(ctx,
		`INSERT INTO posts (title, status, cover_sort_order, publish_path_mode, publish_slug)
		 VALUES ('新图集', 'completed', 3, 'slug', 'abc12345')`); err != nil {
		t.Fatalf("写入新记录失败: %v", err)
	}
	var cover int
	var pathMode, slug string
	if err := handle.QueryRowContext(ctx,
		"SELECT cover_sort_order, publish_path_mode, publish_slug FROM posts WHERE id = 1").
		Scan(&cover, &pathMode, &slug); err != nil {
		t.Fatalf("读取新字段失败: %v", err)
	}
	if cover != 3 || pathMode != "slug" || slug != "abc12345" {
		t.Errorf("字段 = %d/%q/%q，期望 3/slug/abc12345", cover, pathMode, slug)
	}
}
