package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// TestMigrateAddsCompressedCount 验证 v12 旧库升级后补上压缩进度字段，
// 旧记录保持 NULL：界面据此显示「未记录」，而不是补一个猜测值。
func TestMigrateAddsCompressedCount(t *testing.T) {
	handle, err := Open(filepath.Join(t.TempDir(), "legacy-v12.db"))
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
		cover_sort_order INTEGER DEFAULT 0,
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
		publish_path_mode TEXT DEFAULT '',
		publish_slug TEXT DEFAULT '',
		control_state TEXT DEFAULT 'running',
		upload_round INTEGER DEFAULT 0,
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
	if _, err := handle.ExecContext(ctx, "PRAGMA user_version = 12"); err != nil {
		t.Fatalf("写入旧版本号失败: %v", err)
	}

	if err := Migrate(ctx, handle); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}

	var version int
	if err := handle.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("读取版本号失败: %v", err)
	}
	if version != SchemaVersion {
		t.Fatalf("版本 = %d，期望 %d", version, SchemaVersion)
	}

	// 旧记录不被回填：这项数据只由提交时的浏览器上报。
	var compressed sql.NullInt64
	if err := handle.QueryRowContext(ctx, `SELECT compressed_count FROM posts WHERE title = '旧图集'`).Scan(&compressed); err != nil {
		t.Fatalf("读取压缩进度失败: %v", err)
	}
	if compressed.Valid {
		t.Errorf("旧任务压缩进度 = %d，期望保持 NULL", compressed.Int64)
	}

	// 再次迁移应可重复执行。
	if err := Migrate(ctx, handle); err != nil {
		t.Fatalf("重复迁移失败: %v", err)
	}
}
