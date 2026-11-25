package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// TestMigrateAddsImageOriginAndFileRefs 验证 v15 旧库升级：图片补来源列，
// 新建跨 Bot 文件引用表，并按升级前的全局归属回填已存图片。
func TestMigrateAddsImageOriginAndFileRefs(t *testing.T) {
	handle, err := Open(filepath.Join(t.TempDir(), "legacy-v14.db"))
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	ctx := context.Background()

	if _, err := handle.ExecContext(ctx, `CREATE TABLE settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		t.Fatalf("创建配置表失败: %v", err)
	}
	if _, err := handle.ExecContext(ctx, `CREATE TABLE posts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		title TEXT NOT NULL,
		status TEXT DEFAULT 'completed',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		t.Fatalf("创建旧帖表失败: %v", err)
	}
	if _, err := handle.ExecContext(ctx, `CREATE TABLE post_images (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		post_id INTEGER NOT NULL,
		sort_order INTEGER NOT NULL,
		short_code TEXT NOT NULL UNIQUE,
		remote_file_id TEXT DEFAULT '',
		remote_message_id INTEGER DEFAULT 0,
		mime_type TEXT DEFAULT 'image/jpeg',
		file_size INTEGER DEFAULT 0,
		temp_file_path TEXT DEFAULT '',
		status TEXT DEFAULT 'pending',
		error_message TEXT DEFAULT ''
	)`); err != nil {
		t.Fatalf("创建旧图片表失败: %v", err)
	}
	if _, err := handle.ExecContext(ctx, `INSERT INTO posts (title) VALUES ('旧图集')`); err != nil {
		t.Fatalf("写入旧帖失败: %v", err)
	}
	if _, err := handle.ExecContext(ctx, `INSERT INTO post_images (post_id, sort_order, short_code, status)
		VALUES (1, 1, 'abc123', 'stored'), (1, 2, 'def456', 'uploaded')`); err != nil {
		t.Fatalf("写入旧图片失败: %v", err)
	}
	if _, err := handle.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES ('telegram.bot_id', '9001'), ('telegram.channel_id', '-100777')`); err != nil {
		t.Fatalf("写入旧归属失败: %v", err)
	}
	if _, err := handle.ExecContext(ctx, "PRAGMA user_version = 14"); err != nil {
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

	// 已存图片按旧全局归属回填，未存的图片保持空值等待真正写入时记录。
	var (
		storedBot   int64
		storedChat  string
		pendingBot  int64
		pendingChat string
	)
	if err := handle.QueryRowContext(ctx,
		`SELECT source_bot_id, source_chat_id FROM post_images WHERE short_code = 'abc123'`).
		Scan(&storedBot, &storedChat); err != nil {
		t.Fatalf("读取已存图片来源失败: %v", err)
	}
	if storedBot != 9001 || storedChat != "-100777" {
		t.Fatalf("已存图片来源 = %d/%q，期望 9001/-100777", storedBot, storedChat)
	}
	if err := handle.QueryRowContext(ctx,
		`SELECT source_bot_id, source_chat_id FROM post_images WHERE short_code = 'def456'`).
		Scan(&pendingBot, &pendingChat); err != nil {
		t.Fatalf("读取未存图片来源失败: %v", err)
	}
	if pendingBot != 0 || pendingChat != "" {
		t.Fatalf("未存图片来源 = %d/%q，期望保持空值", pendingBot, pendingChat)
	}

	// 引用表可用，且同一图片与同一 Bot 只有一条记录。
	if _, err := handle.ExecContext(ctx,
		`INSERT INTO image_file_refs (image_id, bot_id, file_id) VALUES (1, 9002, 'ref-file')`); err != nil {
		t.Fatalf("写入引用失败: %v", err)
	}
	if _, err := handle.ExecContext(ctx,
		`INSERT INTO image_file_refs (image_id, bot_id, file_id) VALUES (1, 9002, 'ref-dup')`); err == nil {
		t.Fatal("同一图片与 Bot 的重复引用未被拒绝")
	}

	if err := Migrate(ctx, handle); err != nil {
		t.Fatalf("重复迁移失败: %v", err)
	}
	var kept int
	if err := handle.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM image_file_refs WHERE image_id = 1 AND bot_id = 9002`).Scan(&kept); err != nil {
		t.Fatalf("读取引用失败: %v", err)
	}
	if kept != 1 {
		t.Fatalf("重复迁移后引用条数 = %d，期望 1", kept)
	}
	_ = sql.ErrNoRows
}
