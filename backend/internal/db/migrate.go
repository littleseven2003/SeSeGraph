package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// SchemaVersion 是当前代码期望的数据库结构版本，写入 SQLite 的 user_version。
const SchemaVersion = 2

// schemaStatements 是首次建库所需的语句，顺序即执行顺序。
var schemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`,
	`CREATE TABLE IF NOT EXISTS posts (
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
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`,
	`CREATE TABLE IF NOT EXISTS post_images (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		post_id INTEGER NOT NULL,
		sort_order INTEGER NOT NULL,
		short_code TEXT NOT NULL UNIQUE,
		storage_provider TEXT DEFAULT 'telegram',
		remote_file_id TEXT DEFAULT '',
		remote_message_id INTEGER DEFAULT 0,
		mime_type TEXT DEFAULT 'image/jpeg',
		file_size INTEGER DEFAULT 0,
		temp_file_path TEXT DEFAULT '',
		status TEXT DEFAULT 'pending',
		error_message TEXT DEFAULT '',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (post_id) REFERENCES posts(id) ON DELETE CASCADE
	)`,
	`CREATE INDEX IF NOT EXISTS idx_post_images_post_id ON post_images(post_id, sort_order)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_post_images_short_code ON post_images(short_code)`,
	`CREATE TABLE IF NOT EXISTS tags (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL UNIQUE
	)`,
	`CREATE TABLE IF NOT EXISTS post_tags (
		post_id INTEGER NOT NULL,
		tag_id INTEGER NOT NULL,
		PRIMARY KEY (post_id, tag_id),
		FOREIGN KEY (post_id) REFERENCES posts(id) ON DELETE CASCADE,
		FOREIGN KEY (tag_id) REFERENCES tags(id) ON DELETE CASCADE
	)`,
	`CREATE TABLE IF NOT EXISTS admin_users (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		username TEXT NOT NULL,
		password_hash TEXT NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`,
	`CREATE TABLE IF NOT EXISTS traffic_stats (
		month TEXT PRIMARY KEY,
		bytes_out INTEGER DEFAULT 0,
		request_count INTEGER DEFAULT 0,
		alert_sent INTEGER DEFAULT 0,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`,
}

// Migrate 按 user_version 升级数据库结构，可重复调用。
// 结构版本已满足时直接返回，不修改任何数据。
func Migrate(ctx context.Context, handle *sql.DB) error {
	if handle == nil {
		return errors.New("数据库连接为空")
	}

	var current int
	if err := handle.QueryRowContext(ctx, "PRAGMA user_version").Scan(&current); err != nil {
		return errors.New("读取数据库结构版本失败")
	}
	if current >= SchemaVersion {
		return nil
	}

	tx, err := handle.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("开启迁移事务失败")
	}
	defer func() { _ = tx.Rollback() }()

	for _, stmt := range schemaStatements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return errors.New("执行初始迁移失败")
		}
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", SchemaVersion)); err != nil {
		return errors.New("写入数据库结构版本失败")
	}
	if err := tx.Commit(); err != nil {
		return errors.New("提交迁移事务失败")
	}
	return nil
}
