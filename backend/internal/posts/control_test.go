package posts

import (
	"context"
	"errors"
	"testing"
)

// controlPost 建一个正在上传、已有一张已上传图片的任务。
func controlPost(t *testing.T, repo *Repository) int64 {
	t.Helper()
	ctx := context.Background()
	id, err := repo.Create(ctx, Post{Title: "控制测试", ImageCount: 2, Status: StatusUploading})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	for order := 1; order <= 2; order++ {
		if _, err := repo.AttachImage(ctx, Image{PostID: id, SortOrder: order, Status: ImageStatusUploaded, MimeType: "image/jpeg"}); err != nil {
			t.Fatalf("保存图片失败: %v", err)
		}
	}
	return id
}

// TestPauseResumeRoundGatesStaleWrites 验证暂停进入排队、恢复递增轮次并拒绝迟到写入。
func TestPauseResumeRoundGatesStaleWrites(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	id := controlPost(t, repo)

	if _, err := repo.Enqueue(ctx, id); err != nil && !errors.Is(err, ErrAlreadyQueued) {
		t.Fatalf("入队失败: %v", err)
	}
	claimed, ok, err := repo.ClaimNext(ctx)
	if err != nil || !ok || claimed != id {
		t.Fatalf("领取结果 = %d/%v/%v，期望领到任务", claimed, ok, err)
	}

	if err := repo.Pause(ctx, id); err != nil {
		t.Fatalf("暂停失败: %v", err)
	}
	if err := repo.ReleaseToQueue(ctx, id); err != nil {
		t.Fatalf("放回队列失败: %v", err)
	}
	if _, ok, err := repo.ClaimNext(ctx); err != nil || ok {
		t.Fatalf("暂停任务被再次领取: %v / %v", ok, err)
	}

	if err := repo.Resume(ctx, id); err != nil {
		t.Fatalf("恢复失败: %v", err)
	}
	state, round, err := repo.Control(ctx, id)
	if err != nil || state != ControlRunning || round != 1 {
		t.Fatalf("恢复后状态 = %q/%d/%v，期望 running/1", state, round, err)
	}

	// 全部图片已上传，恢复后回到排队，上传队列可以重新领取。
	if _, ok, err := repo.ClaimNext(ctx); err != nil || !ok {
		t.Fatalf("恢复后领取失败: ok=%v err=%v", ok, err)
	}
}

// TestRoundGuardRejectsStaleUpload 验证写图记录前复核上传轮次与暂停状态。
func TestRoundGuardRejectsStaleUpload(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	id := controlPost(t, repo)

	if _, err := repo.handle.ExecContext(ctx,
		`UPDATE posts SET upload_round = 3 WHERE id = ?`, id); err != nil {
		t.Fatalf("写入轮次失败: %v", err)
	}
	_, err := repo.AttachImageWithRound(ctx, Image{
		PostID: id, SortOrder: 2, Status: ImageStatusUploaded, MimeType: "image/jpeg",
	}, 2)
	if !errors.Is(err, ErrControlInvalid) {
		t.Fatalf("旧轮次写入错误 = %v，期望 ErrControlInvalid", err)
	}
	if _, err := repo.AttachImageWithRound(ctx, Image{
		PostID: id, SortOrder: 2, Status: ImageStatusUploaded, MimeType: "image/jpeg",
	}, 3); err != nil {
		t.Fatalf("新轮次写入失败: %v", err)
	}

	if err := repo.Pause(ctx, id); err != nil {
		t.Fatalf("暂停失败: %v", err)
	}
	if _, err := repo.AttachImageWithRound(ctx, Image{
		PostID: id, SortOrder: 2, Status: ImageStatusUploaded, MimeType: "image/jpeg",
	}, 3); !errors.Is(err, ErrControlInvalid) {
		t.Fatalf("暂停期间写入错误 = %v，期望 ErrControlInvalid", err)
	}
}

// TestStopIsTerminalAndClosesPublic 验证停止是终态、短码关闭且不可恢复。
func TestStopIsTerminalAndClosesPublic(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	id := controlPost(t, repo)
	if err := repo.SetPublic(ctx, id, true); err != nil {
		t.Fatalf("设为公开失败: %v", err)
	}

	if err := repo.Stop(ctx, id); err != nil {
		t.Fatalf("停止失败: %v", err)
	}
	post, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("读取帖子失败: %v", err)
	}
	if post.Status != StatusStopped || post.ControlState != ControlStopped {
		t.Errorf("停止后状态 = %q/%q，期望 stopped/stopped", post.Status, post.ControlState)
	}
	var public int
	if err := repo.handle.QueryRowContext(ctx, `SELECT is_public FROM posts WHERE id = ?`, id).Scan(&public); err != nil {
		t.Fatalf("读取公开标记失败: %v", err)
	}
	if public != 0 {
		t.Error("停止后短码仍对外公开")
	}
	if err := repo.Resume(ctx, id); !errors.Is(err, ErrControlInvalid) {
		t.Errorf("停止后恢复错误 = %v，期望拒绝", err)
	}
	if err := repo.Stop(ctx, id); !errors.Is(err, ErrControlInvalid) {
		t.Errorf("重复停止错误 = %v，期望拒绝", err)
	}
}

// TestStopRejectsFinishedTask 验证已完成任务不允许暂停。
func TestPauseRejectsFinishedTask(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	id := controlPost(t, repo)
	if err := repo.MarkPostFinished(ctx, id, StatusCompleted, ""); err != nil {
		t.Fatalf("标记完成失败: %v", err)
	}
	if err := repo.Pause(ctx, id); !errors.Is(err, ErrControlInvalid) {
		t.Errorf("已完成任务暂停错误 = %v，期望拒绝", err)
	}
}

// TestStopKeepsPublishedShortCodes 验证已发布帖停止后短码仍可用。
func TestStopKeepsPublishedShortCodes(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	id, err := repo.Create(ctx, Post{Title: "已发布", ImageCount: 1, Status: StatusCompleted})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	if err := repo.SetPublic(ctx, id, true); err != nil {
		t.Fatalf("设为公开失败: %v", err)
	}
	if err := repo.RequestPublish(ctx, id); err != nil {
		t.Fatalf("请求发布失败: %v", err)
	}
	if err := repo.MarkPublished(ctx, id, []string{"Page-1"}, []string{"https://telegra.ph/Page-1"}); err != nil {
		t.Fatalf("标记发布失败: %v", err)
	}
	if err := repo.Stop(ctx, id); err != nil {
		t.Fatalf("停止失败: %v", err)
	}
	var public int
	if err := repo.handle.QueryRowContext(ctx, `SELECT is_public FROM posts WHERE id = ?`, id).Scan(&public); err != nil {
		t.Fatalf("读取公开标记失败: %v", err)
	}
	if public != 1 {
		t.Error("已发布帖停止后短码被关闭")
	}
}
