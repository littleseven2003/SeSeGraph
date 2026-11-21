package db

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
)

func migrateTemp(t *testing.T) (*sql.DB, context.Context) {
	t.Helper()
	handle, _ := openTemp(t)
	ctx := context.Background()
	if err := Migrate(ctx, handle); err != nil {
		t.Fatalf("初始迁移失败: %v", err)
	}
	return handle, ctx
}

func TestMigrateCreatesSchema(t *testing.T) {
	handle, _ := migrateTemp(t)

	tables := []string{"settings", "posts", "post_images", "tags", "post_tags", "traffic_stats"}
	for _, name := range tables {
		var got string
		err := handle.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?", name,
		).Scan(&got)
		if err != nil {
			t.Errorf("缺少表 %s: %v", name, err)
		}
	}

	indexes := []string{"idx_post_images_post_id", "idx_post_images_short_code"}
	for _, name := range indexes {
		var got string
		err := handle.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='index' AND name=?", name,
		).Scan(&got)
		if err != nil {
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

func TestMigrateIsIdempotentAndKeepsData(t *testing.T) {
	handle, ctx := migrateTemp(t)

	res, err := handle.ExecContext(ctx, "INSERT INTO posts (title) VALUES ('已存在的帖子')")
	if err != nil {
		t.Fatalf("写入帖子失败: %v", err)
	}
	postID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("读取写入编号失败: %v", err)
	}

	if err := Migrate(ctx, handle); err != nil {
		t.Fatalf("重复迁移失败: %v", err)
	}

	var (
		count  int
		title  string
		loaded int64
	)
	if err := handle.QueryRow("SELECT id, title FROM posts LIMIT 1").Scan(&loaded, &title); err != nil {
		t.Fatalf("重复迁移后读取数据失败: %v", err)
	}
	if loaded != postID || title != "已存在的帖子" {
		t.Errorf("重复迁移改动了已有数据: id=%d title=%q", loaded, title)
	}
	if err := handle.QueryRow("SELECT COUNT(*) FROM posts").Scan(&count); err != nil {
		t.Fatalf("统计数据失败: %v", err)
	}
	if count != 1 {
		t.Errorf("帖子数 = %d，期望 1", count)
	}
}

func TestForeignKeysEnforcedAndCascade(t *testing.T) {
	handle, ctx := migrateTemp(t)

	if _, err := handle.ExecContext(ctx,
		"INSERT INTO post_images (post_id, sort_order, short_code) VALUES (999, 1, 'orphan')",
	); err == nil {
		t.Error("外键应拒绝不存在的 post_id")
	}

	res, err := handle.ExecContext(ctx, "INSERT INTO posts (title) VALUES ('带图帖子')")
	if err != nil {
		t.Fatalf("写入帖子失败: %v", err)
	}
	postID, _ := res.LastInsertId()
	if _, err := handle.ExecContext(ctx,
		"INSERT INTO post_images (post_id, sort_order, short_code) VALUES (?, 1, 'code-1')", postID,
	); err != nil {
		t.Fatalf("写入图片记录失败: %v", err)
	}

	if _, err := handle.ExecContext(ctx, "DELETE FROM posts WHERE id = ?", postID); err != nil {
		t.Fatalf("删除帖子失败: %v", err)
	}
	var remaining int
	if err := handle.QueryRow("SELECT COUNT(*) FROM post_images").Scan(&remaining); err != nil {
		t.Fatalf("统计图片失败: %v", err)
	}
	if remaining != 0 {
		t.Errorf("删除帖子应级联删除图片记录，剩余 %d", remaining)
	}
}

func TestConcurrentReadsAndWrites(t *testing.T) {
	handle, ctx := migrateTemp(t)

	const (
		writers    = 6
		readers    = 3
		perRoutine = 20
	)

	var wg sync.WaitGroup
	errCh := make(chan error, writers+readers)

	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for j := 0; j < perRoutine; j++ {
				if _, err := handle.ExecContext(ctx,
					"INSERT INTO posts (title) VALUES (?)", fmt.Sprintf("worker-%d-%d", worker, j),
				); err != nil {
					errCh <- fmt.Errorf("写入失败: %w", err)
					return
				}
			}
		}(i)
	}

	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perRoutine; j++ {
				var total int
				if err := handle.QueryRowContext(ctx, "SELECT COUNT(*) FROM posts").Scan(&total); err != nil {
					errCh <- fmt.Errorf("读取失败: %w", err)
					return
				}
			}
		}()
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("并发读写出现错误: %v", err)
	}

	var total int
	if err := handle.QueryRow("SELECT COUNT(*) FROM posts").Scan(&total); err != nil {
		t.Fatalf("统计失败: %v", err)
	}
	if total != writers*perRoutine {
		t.Errorf("帖子数 = %d，期望 %d", total, writers*perRoutine)
	}
}

func TestMigrateRejectsNilHandle(t *testing.T) {
	if err := Migrate(context.Background(), nil); err == nil {
		t.Error("空连接应返回错误")
	}
}
