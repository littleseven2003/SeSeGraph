package db

import (
	"context"
	"path/filepath"
	"testing"
)

func TestMigrateCreatesV3Indexes(t *testing.T) {
	handle, err := Open(filepath.Join(t.TempDir(), "sesegraph.db"))
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	defer handle.Close()

	if err := Migrate(context.Background(), handle); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}

	for _, name := range []string{"idx_posts_status_created", "idx_post_images_status"} {
		var found string
		if err := handle.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='index' AND name=?", name,
		).Scan(&found); err != nil {
			t.Errorf("缺少索引 %s: %v", name, err)
		}
	}

	var version int
	if err := handle.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("读取结构版本失败: %v", err)
	}
	if version != SchemaVersion {
		t.Errorf("user_version = %d，期望 %d", version, SchemaVersion)
	}
}

func TestMigrateUpgradesV2DatabaseKeepingData(t *testing.T) {
	handle, err := Open(filepath.Join(t.TempDir(), "sesegraph.db"))
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	defer handle.Close()
	ctx := context.Background()

	if err := Migrate(ctx, handle); err != nil {
		t.Fatalf("首次迁移失败: %v", err)
	}

	// 还原成 v0.1.0 的结构：去掉 v3 新增索引并把版本改回 2。
	for _, name := range []string{"idx_posts_status_created", "idx_post_images_status"} {
		if _, err := handle.Exec("DROP INDEX IF EXISTS " + name); err != nil {
			t.Fatalf("删除索引失败: %v", err)
		}
	}
	if _, err := handle.Exec("PRAGMA user_version = 2"); err != nil {
		t.Fatalf("回退版本失败: %v", err)
	}
	if _, err := handle.Exec("INSERT INTO posts (title, image_count) VALUES ('旧库帖子', 2)"); err != nil {
		t.Fatalf("写入旧数据失败: %v", err)
	}

	if err := Migrate(ctx, handle); err != nil {
		t.Fatalf("升级迁移失败: %v", err)
	}

	var version int
	if err := handle.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("读取结构版本失败: %v", err)
	}
	if version != SchemaVersion {
		t.Errorf("user_version = %d，期望 %d", version, SchemaVersion)
	}

	var title string
	if err := handle.QueryRow("SELECT title FROM posts LIMIT 1").Scan(&title); err != nil {
		t.Fatalf("升级后读取旧数据失败: %v", err)
	}
	if title != "旧库帖子" {
		t.Errorf("旧数据被改动: %q", title)
	}

	for _, name := range []string{"idx_posts_status_created", "idx_post_images_status"} {
		var found string
		if err := handle.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='index' AND name=?", name,
		).Scan(&found); err != nil {
			t.Errorf("升级后缺少索引 %s: %v", name, err)
		}
	}
}
