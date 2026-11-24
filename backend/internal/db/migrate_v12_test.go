package db

import (
	"context"
	"path/filepath"
	"regexp"
	"testing"
)

// TestMigrateBackfillsMediaVersion 验证从 v11 升级时，
// 已有媒体记录会补上稳定的 12 位内容版本，抽屉才能据此回存小图。
func TestMigrateBackfillsMediaVersion(t *testing.T) {
	handle, err := Open(filepath.Join(t.TempDir(), "legacy-v11.db"))
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
		`INSERT INTO post_images (post_id, sort_order, short_code, status) VALUES (1, 1, 'old-1', 'stored')`,
		`INSERT INTO post_images (post_id, sort_order, short_code, status) VALUES (1, 2, 'old-2', 'stored')`,
		// 模拟升级前没有内容版本的记录。
		`UPDATE post_images SET media_version = ''`,
		`PRAGMA user_version = 11`,
	} {
		if _, err := handle.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("准备旧库失败: %v", err)
		}
	}

	if err := Migrate(ctx, handle); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}

	pattern := regexp.MustCompile(`^[0-9a-f]{12}$`)
	rows, err := handle.QueryContext(ctx, `SELECT sort_order, media_version FROM post_images ORDER BY sort_order`)
	if err != nil {
		t.Fatalf("读取版本失败: %v", err)
	}
	defer func() { _ = rows.Close() }()
	versions := map[int]string{}
	for rows.Next() {
		var (
			sortOrder int
			version   string
		)
		if err := rows.Scan(&sortOrder, &version); err != nil {
			t.Fatalf("扫描失败: %v", err)
		}
		if !pattern.MatchString(version) {
			t.Errorf("序号 %d 的版本 %q 不是 12 位十六进制", sortOrder, version)
		}
		versions[sortOrder] = version
	}
	if len(versions) != 2 {
		t.Fatalf("应回填 2 条记录，实际 %d", len(versions))
	}

	// 再次升级不能改动已写入的版本，保证客户端上传的版本持续有效。
	if _, err := handle.ExecContext(ctx, `PRAGMA user_version = 11`); err != nil {
		t.Fatalf("重置版本号失败: %v", err)
	}
	if err := Migrate(ctx, handle); err != nil {
		t.Fatalf("二次迁移失败: %v", err)
	}
	for sortOrder, want := range versions {
		var got string
		if err := handle.QueryRowContext(ctx,
			`SELECT media_version FROM post_images WHERE sort_order = ?`, sortOrder).Scan(&got); err != nil {
			t.Fatalf("二次读取失败: %v", err)
		}
		if got != want {
			t.Errorf("序号 %d 的版本被改写：%q → %q", sortOrder, want, got)
		}
	}
}
