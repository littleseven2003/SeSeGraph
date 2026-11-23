package posts

import (
	"context"
	"errors"
	"testing"
)

func TestMarkPendingDeleteDisablesAndReturnsPaths(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	id, err := repo.Create(ctx, Post{Title: "待删除", ImageCount: 1})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	if err := repo.MarkPostFinished(ctx, id, StatusCompleted, ""); err != nil {
		t.Fatalf("标记完成失败: %v", err)
	}
	if err := repo.RequestPublish(ctx, id); err != nil {
		t.Fatalf("请求发布失败: %v", err)
	}
	if err := repo.MarkPublished(ctx, id,
		[]string{"Page-1", "Page-2"},
		[]string{"https://telegra.ph/Page-1", "https://telegra.ph/Page-2"}); err != nil {
		t.Fatalf("标记发布失败: %v", err)
	}

	paths, err := repo.MarkPendingDelete(ctx, id)
	if err != nil {
		t.Fatalf("标记删除失败: %v", err)
	}
	if len(paths) != 2 || paths[0] != "Page-1" {
		t.Errorf("返回路径 = %v", paths)
	}

	pending, err := repo.PendingDelete(ctx, id)
	if err != nil || !pending {
		t.Fatalf("应处于待删除状态: pending=%v err=%v", pending, err)
	}
	if _, err := repo.PublicImage(ctx, ""); !errors.Is(err, ErrNotFound) {
		t.Logf("空短码应 404: %v", err)
	}

	// 重复调用保持幂等，仍返回待清空的路径。
	again, err := repo.MarkPendingDelete(ctx, id)
	if err != nil || len(again) != 2 {
		t.Errorf("重复标记 = %v, %v", again, err)
	}
}

func TestMarkPendingDeleteRejectsRunningTask(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	for _, status := range []string{StatusUploading, StatusQueued, StatusPublishing} {
		id, err := repo.Create(ctx, Post{Title: "进行中", ImageCount: 1, Status: status})
		if err != nil {
			t.Fatalf("创建帖子失败: %v", err)
		}
		if _, err := repo.MarkPendingDelete(ctx, id); !errors.Is(err, ErrTaskRunning) {
			t.Errorf("%s 状态应拒绝标记删除，实际: %v", status, err)
		}
	}
}

func TestMarkPendingDeleteMissingPost(t *testing.T) {
	repo := newTestRepository(t)
	if _, err := repo.MarkPendingDelete(context.Background(), 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("不存在的帖子应返回 ErrNotFound，实际: %v", err)
	}
}
