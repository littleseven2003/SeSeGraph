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

// TestMigrateCleansOrphanTags 验证升级时清掉历史孤立标签，共享标签保留并补上反查索引。
func TestMigrateCleansOrphanTags(t *testing.T) {
	handle, err := Open(filepath.Join(t.TempDir(), "legacy-tags.db"))
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	ctx := context.Background()
	if err := Migrate(ctx, handle); err != nil {
		t.Fatalf("初始化失败: %v", err)
	}

	for _, stmt := range []string{
		`INSERT INTO posts (title, status) VALUES ('旧图集', 'completed')`,
		`INSERT INTO tags (name) VALUES ('共享'), ('孤立')`,
		`INSERT INTO post_tags (post_id, tag_id) SELECT 1, id FROM tags WHERE name = '共享'`,
		`PRAGMA user_version = 10`,
	} {
		if _, err := handle.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("准备旧库失败: %v", err)
		}
	}

	if err := Migrate(ctx, handle); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}

	rows, err := handle.QueryContext(ctx, `SELECT name FROM tags ORDER BY name`)
	if err != nil {
		t.Fatalf("读取标签失败: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("扫描标签失败: %v", err)
		}
		names = append(names, name)
	}
	if len(names) != 1 || names[0] != "共享" {
		t.Errorf("迁移后标签 = %v，期望只剩共享", names)
	}

	indexRows, err := handle.QueryContext(ctx, `PRAGMA index_list(post_tags)`)
	if err != nil {
		t.Fatalf("读取索引失败: %v", err)
	}
	defer func() { _ = indexRows.Close() }()
	found := false
	for indexRows.Next() {
		var (
			seq     int
			name    string
			unique  int
			origin  string
			partial int
		)
		if err := indexRows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			t.Fatalf("扫描索引失败: %v", err)
		}
		if name == "idx_post_tags_tag_id" {
			found = true
		}
	}
	if !found {
		t.Error("缺少 idx_post_tags_tag_id 索引")
	}
}
