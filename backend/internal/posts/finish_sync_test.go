package posts

import (
	"context"
	"errors"
	"testing"
)

// createIntentPost 建一条已选自动发布的任务，链接口径与提交时一致。
func createIntentPost(t *testing.T, repo *Repository) int64 {
	t.Helper()
	id, err := repo.Create(context.Background(), Post{
		Title:           "自动发布任务",
		ImageCount:      1,
		Status:          StatusUploading,
		PublishIntent:   PublishIntentAuto,
		PublishPathMode: "slug",
		PublishSlug:     "frozen0001",
	})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	return id
}

// TestCreateKeepsFrozenPathWithIntent 验证发布意图与链接口径一次写入，不出现只有意图没有口径的中间态。
func TestCreateKeepsFrozenPathWithIntent(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	id := createIntentPost(t, repo)

	info, err := repo.PublishInfo(ctx, id)
	if err != nil {
		t.Fatalf("读取发布信息失败: %v", err)
	}
	if info.PathMode != "slug" || info.Slug != "frozen0001" {
		t.Fatalf("冻结值 = %q/%q，期望 slug/frozen0001", info.PathMode, info.Slug)
	}
}

// TestFinishSyncEnqueuesAutoIntent 验证条件齐备时收尾与入队同事务完成。
func TestFinishSyncEnqueuesAutoIntent(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	id := createIntentPost(t, repo)

	enqueued, err := repo.FinishSync(ctx, id, true, "")
	if err != nil {
		t.Fatalf("收尾失败: %v", err)
	}
	if !enqueued {
		t.Fatal("应自动入发布队列")
	}
	state, _, public, err := repo.PublishStatus(ctx, id)
	if err != nil {
		t.Fatalf("读取发布状态失败: %v", err)
	}
	if state != PublishPending || !public {
		t.Fatalf("状态 = %q / public=%v，期望 pending / true", state, public)
	}
	info, err := repo.PublishInfo(ctx, id)
	if err != nil {
		t.Fatalf("读取发布信息失败: %v", err)
	}
	if info.PathMode != "slug" || info.Slug != "frozen0001" {
		t.Fatalf("冻结值被改写为 %q/%q", info.PathMode, info.Slug)
	}
}

// TestFinishSyncKeepsHiddenWhenNotReady 验证发布条件不齐时只标记完成，
// 公开标记保持关闭并把原因写出来，等管理员补齐配置后手动发布。
func TestFinishSyncKeepsHiddenWhenNotReady(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	id := createIntentPost(t, repo)

	enqueued, err := repo.FinishSync(ctx, id, false, "发布条件未就绪")
	if err != nil {
		t.Fatalf("收尾失败: %v", err)
	}
	if enqueued {
		t.Fatal("条件不齐时不应入发布队列")
	}
	state, _, public, err := repo.PublishStatus(ctx, id)
	if err != nil {
		t.Fatalf("读取发布状态失败: %v", err)
	}
	if state != PublishIdle || public {
		t.Fatalf("状态 = %q / public=%v，期望 idle / false", state, public)
	}
	var note, status string
	if err := repo.handle.QueryRowContext(ctx,
		`SELECT COALESCE(publish_error, ''), status FROM posts WHERE id = ?`, id).Scan(&note, &status); err != nil {
		t.Fatalf("读取发布提示失败: %v", err)
	}
	if note != "发布条件未就绪" {
		t.Errorf("发布提示 = %q，期望记录原因", note)
	}
	if status != StatusCompleted {
		t.Errorf("状态 = %q，期望 completed", status)
	}
}

// TestFinishSyncLeavesManualIntentPrivate 验证只存图的任务收尾不会公开。
func TestFinishSyncLeavesManualIntentPrivate(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	id, err := repo.Create(ctx, Post{Title: "仅存图", ImageCount: 1, Status: StatusUploading})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	enqueued, err := repo.FinishSync(ctx, id, true, "")
	if err != nil {
		t.Fatalf("收尾失败: %v", err)
	}
	if enqueued {
		t.Fatal("仅存图的任务不应入发布队列")
	}
	state, _, public, err := repo.PublishStatus(ctx, id)
	if err != nil {
		t.Fatalf("读取发布状态失败: %v", err)
	}
	if state != PublishIdle || public {
		t.Fatalf("状态 = %q / public=%v，期望 idle / false", state, public)
	}
}

// TestFinishSyncRejectsPaused 验证暂停中的任务不会被收尾标记完成。
func TestFinishSyncRejectsPaused(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	id := createIntentPost(t, repo)
	if err := repo.UpdateStatus(ctx, id, StatusPublishing); err != nil {
		t.Fatalf("更新状态失败: %v", err)
	}
	if err := repo.Pause(ctx, id); err != nil {
		t.Fatalf("暂停失败: %v", err)
	}
	if _, err := repo.FinishSync(ctx, id, true, ""); !errors.Is(err, ErrControlInvalid) {
		t.Fatalf("暂停中收尾应返回 ErrControlInvalid，实际 %v", err)
	}
	state, _, public, err := repo.PublishStatus(ctx, id)
	if err != nil {
		t.Fatalf("读取发布状态失败: %v", err)
	}
	if state != PublishIdle || public {
		t.Fatalf("状态 = %q / public=%v，期望保持 idle / false", state, public)
	}
}

// TestRecoverAutoPublishCompletesInterruptedFinish 验证旧版本留下的
// 「已同步、未发布」记录在启动时补入发布队列。
func TestRecoverAutoPublishCompletesInterruptedFinish(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	id := createIntentPost(t, repo)
	// 模拟旧版本先落完成、后写发布状态却中断的结果。
	if err := repo.MarkPostFinished(ctx, id, StatusCompleted, ""); err != nil {
		t.Fatalf("标记完成失败: %v", err)
	}

	count, err := repo.RecoverAutoPublish(ctx, true, "发布条件未就绪")
	if err != nil {
		t.Fatalf("恢复失败: %v", err)
	}
	if count != 1 {
		t.Fatalf("补入队列 %d 条，期望 1", count)
	}
	state, _, public, err := repo.PublishStatus(ctx, id)
	if err != nil {
		t.Fatalf("读取发布状态失败: %v", err)
	}
	if state != PublishPending || !public {
		t.Fatalf("状态 = %q / public=%v，期望 pending / true", state, public)
	}
}

// TestRecoverAutoPublishKeepsHiddenWhenNotReady 验证条件不齐时启动恢复不会公开。
func TestRecoverAutoPublishKeepsHiddenWhenNotReady(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	id := createIntentPost(t, repo)
	if err := repo.MarkPostFinished(ctx, id, StatusCompleted, ""); err != nil {
		t.Fatalf("标记完成失败: %v", err)
	}

	count, err := repo.RecoverAutoPublish(ctx, false, "发布条件未就绪")
	if err != nil {
		t.Fatalf("恢复失败: %v", err)
	}
	if count != 0 {
		t.Fatalf("条件不齐时不应入队，实际 %d", count)
	}
	state, _, public, err := repo.PublishStatus(ctx, id)
	if err != nil {
		t.Fatalf("读取发布状态失败: %v", err)
	}
	if state != PublishIdle || public {
		t.Fatalf("状态 = %q / public=%v，期望 idle / false", state, public)
	}
}

// TestRecoverAutoPublishLeavesPublished 验证已发布的任务不会被启动恢复重复入队。
func TestRecoverAutoPublishLeavesPublished(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	id := createIntentPost(t, repo)
	if err := repo.MarkPostFinished(ctx, id, StatusCompleted, ""); err != nil {
		t.Fatalf("标记完成失败: %v", err)
	}
	if err := repo.MarkPublished(ctx, id, []string{"page01"}, []string{"https://telegra.ph/page01"}); err != nil {
		t.Fatalf("标记发布失败: %v", err)
	}
	if count, err := repo.RecoverAutoPublish(ctx, true, ""); err != nil || count != 0 {
		t.Fatalf("已发布任务不应重复入队：count=%d err=%v", count, err)
	}
	state, paths, public, err := repo.PublishStatus(ctx, id)
	if err != nil {
		t.Fatalf("读取发布状态失败: %v", err)
	}
	if !public || len(paths) != 1 {
		t.Fatalf("已发布结果被改写：public=%v paths=%v", public, paths)
	}
	if state == PublishPending || state == PublishPublishing {
		t.Fatalf("已发布任务被重新排队，状态 = %q", state)
	}
}
