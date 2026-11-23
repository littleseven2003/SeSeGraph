package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// SchemaVersion 是当前代码期望的数据库结构版本，写入 SQLite 的 user_version。
const SchemaVersion = 11

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
		control_state TEXT DEFAULT 'running',
		upload_round INTEGER NOT NULL DEFAULT 0,
		error_message TEXT DEFAULT '',
		is_disabled INTEGER DEFAULT 0,
		is_public INTEGER DEFAULT 0,
		cover_sort_order INTEGER DEFAULT NULL,
		publish_path_mode TEXT DEFAULT '',
		publish_slug TEXT DEFAULT '',
		publish_state TEXT DEFAULT 'idle',
		publish_error TEXT DEFAULT '',
		pending_delete INTEGER NOT NULL DEFAULT 0,
		published_at DATETIME DEFAULT NULL,
		views_count INTEGER NOT NULL DEFAULT 0,
		views_updated_at DATETIME DEFAULT NULL,
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
		session_version INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`,
	`CREATE INDEX IF NOT EXISTS idx_posts_status_created ON posts(status, created_at)`,
	`CREATE INDEX IF NOT EXISTS idx_post_images_status ON post_images(status)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_post_images_post_order ON post_images(post_id, sort_order)`,
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
	// 旧库升级：post 表补公开标记，默认关闭，需要管理员确认后才开启。
	if err := ensureColumn(ctx, tx, "posts", "is_public", "ALTER TABLE posts ADD COLUMN is_public INTEGER DEFAULT 0"); err != nil {
		return errors.New("升级帖子公开标记失败")
	}
	if err := ensureColumn(ctx, tx, "posts", "publish_state", "ALTER TABLE posts ADD COLUMN publish_state TEXT DEFAULT 'idle'"); err != nil {
		return errors.New("升级发布状态失败")
	}
	if err := ensureColumn(ctx, tx, "posts", "publish_error", "ALTER TABLE posts ADD COLUMN publish_error TEXT DEFAULT ''"); err != nil {
		return errors.New("升级发布状态失败")
	}
	// 旧库升级：补首次发布成功的时间。已发布帖用升级前的更新时间回填，未发布帖留空。
	if err := ensureColumn(ctx, tx, "posts", "published_at", "ALTER TABLE posts ADD COLUMN published_at DATETIME DEFAULT NULL"); err != nil {
		return errors.New("升级发布时间失败")
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE posts SET published_at = updated_at
		 WHERE publish_state = 'published' AND (published_at IS NULL OR published_at = '')`); err != nil {
		return errors.New("回填发布时间失败")
	}
	// 旧库升级：补浏览量缓存。visits 为空表示尚未同步，0 配合时间戳才是已确认的零浏览。
	if err := ensureColumn(ctx, tx, "posts", "views_count", "ALTER TABLE posts ADD COLUMN views_count INTEGER NOT NULL DEFAULT 0"); err != nil {
		return errors.New("升级浏览量失败")
	}
	if err := ensureColumn(ctx, tx, "posts", "views_updated_at", "ALTER TABLE posts ADD COLUMN views_updated_at DATETIME DEFAULT NULL"); err != nil {
		return errors.New("升级浏览量同步时间失败")
	}
	// 旧库升级：删除流程标记。置位后短码立即 404，远端页面清空成功才真正删本地记录。
	if err := ensureColumn(ctx, tx, "posts", "pending_delete", "ALTER TABLE posts ADD COLUMN pending_delete INTEGER NOT NULL DEFAULT 0"); err != nil {
		return errors.New("升级删除标记失败")
	}
	// 旧库升级：会话版本用于让改密后的旧 Cookie 立即失效，默认 0 与升级前的令牌兼容。
	if err := ensureColumn(ctx, tx, "admin_users", "session_version", "ALTER TABLE admin_users ADD COLUMN session_version INTEGER NOT NULL DEFAULT 0"); err != nil {
		return errors.New("升级会话版本失败")
	}
	// 旧库升级：任务控制状态与上传轮次。暂停可恢复，停止为终态。
	if err := ensureColumn(ctx, tx, "posts", "control_state", "ALTER TABLE posts ADD COLUMN control_state TEXT DEFAULT 'running'"); err != nil {
		return errors.New("升级任务控制状态失败")
	}
	if err := ensureColumn(ctx, tx, "posts", "upload_round", "ALTER TABLE posts ADD COLUMN upload_round INTEGER NOT NULL DEFAULT 0"); err != nil {
		return errors.New("升级上传轮次失败")
	}
	// 旧库升级：封面来源序号。空值表示沿用旧行为，不前置任何媒体。
	if err := ensureColumn(ctx, tx, "posts", "cover_sort_order", "ALTER TABLE posts ADD COLUMN cover_sort_order INTEGER DEFAULT NULL"); err != nil {
		return errors.New("升级封面序号失败")
	}
	// 旧库升级：一次性清掉历史遗留的孤立标签，并补上按标签反查帖子的索引。
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM tags WHERE id NOT IN (SELECT DISTINCT tag_id FROM post_tags)`); err != nil {
		return errors.New("清理孤立标签失败")
	}
	if _, err := tx.ExecContext(ctx,
		`CREATE INDEX IF NOT EXISTS idx_post_tags_tag_id ON post_tags(tag_id)`); err != nil {
		return errors.New("创建标签索引失败")
	}
	// 旧库升级：首次入队时冻结的链接生成方式与随机短码，重启后沿用同一套取值。
	if err := ensureColumn(ctx, tx, "posts", "publish_path_mode", "ALTER TABLE posts ADD COLUMN publish_path_mode TEXT DEFAULT ''"); err != nil {
		return errors.New("升级链接生成方式失败")
	}
	if err := ensureColumn(ctx, tx, "posts", "publish_slug", "ALTER TABLE posts ADD COLUMN publish_slug TEXT DEFAULT ''"); err != nil {
		return errors.New("升级链接短码失败")
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", SchemaVersion)); err != nil {
		return errors.New("写入数据库结构版本失败")
	}
	if err := tx.Commit(); err != nil {
		return errors.New("提交迁移事务失败")
	}
	return nil
}

// ensureColumn 在列缺失时执行加列语句，保证旧库升级可重复执行。
func ensureColumn(ctx context.Context, tx *sql.Tx, table, column, ddl string) error {
	rows, err := tx.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var (
			cid        int
			name       string
			typeName   string
			notNull    int
			defaultV   sql.NullString
			primaryKey int
		)
		if err := rows.Scan(&cid, &name, &typeName, &notNull, &defaultV, &primaryKey); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, ddl)
	return err
}
