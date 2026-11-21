package db

import (
	"context"
	"path/filepath"
	"testing"
)

// TestMigrateAddsPublicFlagForExistingDatabase 验证 v4 旧库升级后补上公开标记且默认关闭。
func TestMigrateAddsPublicFlagForExistingDatabase(t *testing.T) {
	handle, err := Open(filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	ctx := context.Background()

	// 构造 v4 结构：posts 表没有 is_public 列，且已有一条存图完成的旧任务。
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
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		t.Fatalf("创建旧表失败: %v", err)
	}
	if _, err := handle.ExecContext(ctx,
		`INSERT INTO posts (title, image_count, status) VALUES ('升级前的图集', 1, 'completed')`); err != nil {
		t.Fatalf("写入旧数据失败: %v", err)
	}
	if _, err := handle.ExecContext(ctx, "PRAGMA user_version = 4"); err != nil {
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

	columns := map[string]bool{}
	rows, err := handle.QueryContext(ctx, "PRAGMA table_info(posts)")
	if err != nil {
		t.Fatalf("读取表结构失败: %v", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var (
			cid        int
			name       string
			typeName   string
			notNull    int
			defaultVal any
			primaryKey int
		)
		if err := rows.Scan(&cid, &name, &typeName, &notNull, &defaultVal, &primaryKey); err != nil {
			t.Fatalf("解析表结构失败: %v", err)
		}
		columns[name] = true
	}
	if !columns["is_public"] {
		t.Fatal("迁移后缺少 is_public 列")
	}

	var public int
	if err := handle.QueryRowContext(ctx, `SELECT is_public FROM posts WHERE title = '升级前的图集'`).Scan(&public); err != nil {
		t.Fatalf("读取公开标记失败: %v", err)
	}
	if public != 0 {
		t.Errorf("升级后旧任务 is_public = %d，期望默认关闭 0", public)
	}

	// 重复迁移应保持幂等。
	if err := Migrate(ctx, handle); err != nil {
		t.Fatalf("重复迁移失败: %v", err)
	}
}
