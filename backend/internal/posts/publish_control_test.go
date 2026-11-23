package posts

import (
	"context"
	"errors"
	"testing"
)

func storedPost(t *testing.T, repo *Repository) int64 {
	t.Helper()
	ctx := context.Background()
	id, err := repo.Create(ctx, Post{Title: "存图完成", ImageCount: 1, Status: StatusCompleted})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	if err := repo.SetPublic(ctx, id, true); err != nil {
		t.Fatalf("设为公开失败: %v", err)
	}
	return id
}

// TestRequestPublishFreezesSlugOnce 验证首次发布原子冻结链接方式，重试不随后续设置改变。
func TestRequestPublishFreezesSlugOnce(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	id := storedPost(t, repo)

	if err := repo.RequestPublishWithPath(ctx, id, "slug", "abc1234567"); err != nil {
		t.Fatalf("首次发布失败: %v", err)
	}
	info, err := repo.PublishInfo(ctx, id)
	if err != nil {
		t.Fatalf("读取发布信息失败: %v", err)
	}
	if info.PathMode != "slug" || info.Slug != "abc1234567" {
		t.Fatalf("冻结值 = %q/%q，期望 slug/abc1234567", info.PathMode, info.Slug)
	}

	// 发布失败后重试，即使传入不同设置也沿用已冻结值。
	if _, ok, err := repo.ClaimPublishable(ctx); err != nil || !ok {
		t.Fatalf("领取发布失败: %v / %v", ok, err)
	}
	if err := repo.MarkPublishFailed(ctx, id, "上游失败"); err != nil {
		t.Fatalf("标记发布失败失败: %v", err)
	}
	if err := repo.RequestPublishWithPath(ctx, id, "title", ""); err != nil {
		t.Fatalf("重试发布失败: %v", err)
	}
	info, err = repo.PublishInfo(ctx, id)
	if err != nil {
		t.Fatalf("读取发布信息失败: %v", err)
	}
	if info.PathMode != "slug" || info.Slug != "abc1234567" {
		t.Errorf("重试后冻结值 = %q/%q，期望保持不变", info.PathMode, info.Slug)
	}
}

// TestStoppedPostCannotPublish 验证停止后不能重新发布，发布收尾也不会重开短码。
func TestStoppedPostCannotPublish(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	id := storedPost(t, repo)

	if err := repo.Stop(ctx, id); err != nil {
		t.Fatalf("停止失败: %v", err)
	}
	if err := repo.RequestPublish(ctx, id); !errors.Is(err, ErrControlInvalid) && err == nil {
		t.Errorf("停止后仍可发起发布: %v", err)
	}
	if err := repo.MarkPublished(ctx, id, []string{"Page-1"}, []string{"https://telegra.ph/Page-1"}); !errors.Is(err, ErrPublishStateChanged) {
		t.Errorf("停止后收尾错误 = %v，期望 ErrPublishStateChanged", err)
	}
	var public int
	if err := repo.handle.QueryRowContext(ctx, `SELECT is_public FROM posts WHERE id = ?`, id).Scan(&public); err != nil {
		t.Fatalf("读取公开标记失败: %v", err)
	}
	if public != 0 {
		t.Error("停止后发布收尾重新打开了短码")
	}
}

// TestPauseAllowsPublishStage 验证 completed + pending 发布阶段可以暂停并恢复。
func TestPauseAllowsPublishStage(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	id := storedPost(t, repo)
	if err := repo.RequestPublishWithPath(ctx, id, "title", ""); err != nil {
		t.Fatalf("发起发布失败: %v", err)
	}
	if err := repo.Pause(ctx, id); err != nil {
		t.Fatalf("发布阶段暂停失败: %v", err)
	}
	if state, _, err := repo.Control(ctx, id); err != nil || state != ControlPaused {
		t.Fatalf("暂停状态 = %q / %v", state, err)
	}
	if err := repo.Resume(ctx, id); err != nil {
		t.Fatalf("恢复失败: %v", err)
	}
	state, _, err := repo.Control(ctx, id)
	if err != nil || state != ControlRunning {
		t.Fatalf("恢复状态 = %q / %v", state, err)
	}
}

// TestReleasePublishAfterResume 验证暂停后立刻恢复时发布仍被原子交还，不会卡在 publishing。
func TestReleasePublishAfterResume(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	id := storedPost(t, repo)
	if err := repo.RequestPublish(ctx, id); err != nil {
		t.Fatalf("发起发布失败: %v", err)
	}
	if _, ok, err := repo.ClaimPublishable(ctx); err != nil || !ok {
		t.Fatalf("领取发布失败: %v / %v", ok, err)
	}
	if err := repo.Pause(ctx, id); err != nil {
		t.Fatalf("暂停失败: %v", err)
	}
	// 恢复抢先执行：交还时控制状态已是 running，仍应回到 pending。
	if err := repo.Resume(ctx, id); err != nil {
		t.Fatalf("恢复失败: %v", err)
	}
	if err := repo.ReleasePublish(ctx, id); err != nil {
		t.Fatalf("交还失败: %v", err)
	}
	info, err := repo.PublishInfo(ctx, id)
	if err != nil {
		t.Fatalf("读取发布信息失败: %v", err)
	}
	if info.State != PublishPending {
		t.Errorf("交还后状态 = %q，期望 pending", info.State)
	}
}

// TestReleasePublishAfterStopFails 验证停止后交还发布记为失败，不会重新开放领取。
func TestReleasePublishAfterStopFails(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	id := storedPost(t, repo)
	if err := repo.RequestPublish(ctx, id); err != nil {
		t.Fatalf("发起发布失败: %v", err)
	}
	if _, ok, err := repo.ClaimPublishable(ctx); err != nil || !ok {
		t.Fatalf("领取发布失败: %v / %v", ok, err)
	}
	if err := repo.Stop(ctx, id); err != nil {
		t.Fatalf("停止失败: %v", err)
	}
	if err := repo.ReleasePublish(ctx, id); err != nil {
		t.Fatalf("交还失败: %v", err)
	}
	info, err := repo.PublishInfo(ctx, id)
	if err != nil {
		t.Fatalf("读取发布信息失败: %v", err)
	}
	if info.State != PublishFailed {
		t.Errorf("停止后交还状态 = %q，期望 failed", info.State)
	}
	if _, ok, err := repo.ClaimPublishable(ctx); err != nil || ok {
		t.Errorf("停止后发布仍被领取: ok=%v err=%v", ok, err)
	}
}

// TestReleasePublishRacesResume 验证交还与恢复真并发时发布不会卡在 publishing。
func TestReleasePublishRacesResume(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()

	for round := 0; round < 20; round++ {
		id := storedPost(t, repo)
		if err := repo.RequestPublish(ctx, id); err != nil {
			t.Fatalf("发起发布失败: %v", err)
		}
		if _, ok, err := repo.ClaimPublishable(ctx); err != nil || !ok {
			t.Fatalf("领取发布失败: %v / %v", ok, err)
		}
		if err := repo.Pause(ctx, id); err != nil {
			t.Fatalf("暂停失败: %v", err)
		}

		start := make(chan struct{})
		done := make(chan error, 2)
		go func() {
			<-start
			done <- repo.Resume(ctx, id)
		}()
		go func() {
			<-start
			done <- repo.ReleasePublish(ctx, id)
		}()
		close(start)
		for index := 0; index < 2; index++ {
			if err := <-done; err != nil {
				t.Fatalf("并发操作失败: %v", err)
			}
		}

		info, err := repo.PublishInfo(ctx, id)
		if err != nil {
			t.Fatalf("读取发布信息失败: %v", err)
		}
		if info.State != PublishPending {
			t.Fatalf("第 %d 轮并发后状态 = %q，期望 pending", round+1, info.State)
		}
	}
}
