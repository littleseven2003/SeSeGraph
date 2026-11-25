package posts

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/littleseven2003/SeSeGraph/backend/internal/db"
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

// TestControlStateSurvivesRestart 验证暂停与停止状态写入数据库，重开连接后仍生效。
func TestControlStateSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "restart.db")
	handle, err := db.Open(path)
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	if err := db.Migrate(ctx, handle); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	repo := NewRepository(handle)
	paused := controlPost(t, repo)
	if _, err := repo.Enqueue(ctx, paused); err != nil && !errors.Is(err, ErrAlreadyQueued) {
		t.Fatalf("入队失败: %v", err)
	}
	if err := repo.Pause(ctx, paused); err != nil {
		t.Fatalf("暂停失败: %v", err)
	}
	stopped := controlPost(t, repo)
	if err := repo.Stop(ctx, stopped); err != nil {
		t.Fatalf("停止失败: %v", err)
	}
	_ = handle.Close()

	reopened, err := db.Open(path)
	if err != nil {
		t.Fatalf("重新打开数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	repo = NewRepository(reopened)

	if state, _, err := repo.Control(ctx, paused); err != nil || state != ControlPaused {
		t.Errorf("重启后暂停状态 = %q / %v，期望 paused", state, err)
	}
	if state, _, err := repo.Control(ctx, stopped); err != nil || state != ControlStopped {
		t.Errorf("重启后停止状态 = %q / %v，期望 stopped", state, err)
	}
	if _, ok, err := repo.ClaimNext(ctx); err != nil || ok {
		t.Errorf("重启后仍领取了暂停或停止的任务: ok=%v err=%v", ok, err)
	}
}

// TestMarkTaskFailedWhilePaused 确认暂停中在途失败也能落库，不会停在处理中。
func TestMarkTaskFailedWhilePaused(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	id, err := repo.Create(ctx, Post{Title: "暂停中失败", ImageCount: 2, Status: StatusPublishing})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	if err := repo.Pause(ctx, id); err != nil {
		t.Fatalf("暂停失败: %v", err)
	}
	if err := repo.MarkTaskFailed(ctx, id, "存图失败：账号受限"); err != nil {
		t.Fatalf("标记失败出错: %v", err)
	}
	post, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("读取帖子失败: %v", err)
	}
	var message string
	if err := repo.handle.QueryRowContext(ctx, `SELECT COALESCE(error_message, '') FROM posts WHERE id = ?`, id).Scan(&message); err != nil {
		t.Fatalf("读取错误原因失败: %v", err)
	}
	if post.Status != StatusFailed || message != "存图失败：账号受限" {
		t.Errorf("暂停中失败未落库：状态 %s，原因 %q", post.Status, message)
	}
}

// TestResumeFromPublishingReturnsToQueue 确认恢复同步中的任务回队列而不是退回上传。
func TestResumeFromPublishingReturnsToQueue(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	id, err := repo.Create(ctx, Post{Title: "同步暂停", ImageCount: 2, Status: StatusPublishing})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	if _, err := repo.handle.ExecContext(ctx, `UPDATE posts SET error_message = '旧原因' WHERE id = ?`, id); err != nil {
		t.Fatalf("写入旧原因失败: %v", err)
	}
	if err := repo.Pause(ctx, id); err != nil {
		t.Fatalf("暂停失败: %v", err)
	}
	if err := repo.Resume(ctx, id); err != nil {
		t.Fatalf("恢复失败: %v", err)
	}
	post, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("读取帖子失败: %v", err)
	}
	if post.Status != StatusQueued {
		t.Errorf("恢复后状态 = %s，期望回队列 %s", post.Status, StatusQueued)
	}
	var message string
	if err := repo.handle.QueryRowContext(ctx, `SELECT COALESCE(error_message, '') FROM posts WHERE id = ?`, id).Scan(&message); err != nil {
		t.Fatalf("读取错误原因失败: %v", err)
	}
	if message != "" {
		t.Errorf("恢复后仍留着旧原因：%q", message)
	}
}

// TestConcurrentClaimKeepsPostsIsolated 确认两套图集同时排队时各领各的，
// 已存图与失败原因不会串到另一条任务上。
func TestConcurrentClaimKeepsPostsIsolated(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	first, err := repo.Create(ctx, Post{Title: "图集甲", ImageCount: 2, Status: StatusQueued})
	if err != nil {
		t.Fatalf("创建甲失败: %v", err)
	}
	second, err := repo.Create(ctx, Post{Title: "图集乙", ImageCount: 2, Status: StatusQueued})
	if err != nil {
		t.Fatalf("创建乙失败: %v", err)
	}
	claimed := map[int64]bool{}
	for i := 0; i < 2; i++ {
		id, ok, err := repo.ClaimNext(ctx)
		if err != nil || !ok {
			t.Fatalf("第 %d 次领取失败：ok=%v err=%v", i+1, ok, err)
		}
		if claimed[id] {
			t.Fatalf("同一条任务被领了两次：%d", id)
		}
		claimed[id] = true
	}
	if !claimed[first] || !claimed[second] {
		t.Fatalf("两条任务应各领一次：%v", claimed)
	}

	// 甲存一张、失败一张，乙的数字不受影响。
	imagesA, err := repo.PendingImages(ctx, first)
	if err != nil || len(imagesA) != 0 {
		t.Fatalf("无图片时待上传应为空：%v %d", err, len(imagesA))
	}
	if err := repo.MarkTaskFailed(ctx, first, "存图失败：账号受限"); err != nil {
		t.Fatalf("标记甲失败出错: %v", err)
	}
	postA, _ := repo.Get(ctx, first)
	postB, _ := repo.Get(ctx, second)
	if postA.Status != StatusFailed || postB.Status != StatusPublishing {
		t.Errorf("失败状态串了：甲 %s，乙 %s", postA.Status, postB.Status)
	}
	var messageA, messageB string
	if err := repo.handle.QueryRowContext(ctx, `SELECT COALESCE(error_message, '') FROM posts WHERE id = ?`, first).Scan(&messageA); err != nil {
		t.Fatalf("读取甲原因失败: %v", err)
	}
	if err := repo.handle.QueryRowContext(ctx, `SELECT COALESCE(error_message, '') FROM posts WHERE id = ?`, second).Scan(&messageB); err != nil {
		t.Fatalf("读取乙原因失败: %v", err)
	}
	if messageA == "" || messageB != "" {
		t.Errorf("失败原因串了：甲 %q，乙 %q", messageA, messageB)
	}
}

// TestRequeueFinishOnlyWhenAllStored 确认只有「图都存好、只差收尾」才允许重跑收尾。
func TestRequeueFinishOnlyWhenAllStored(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	id, err := repo.Create(ctx, Post{Title: "收尾失败", ImageCount: 2, Status: StatusUploading})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	for order := 1; order <= 2; order++ {
		if _, err := repo.AttachImage(ctx, Image{PostID: id, SortOrder: order, MimeType: "image/jpeg", Status: ImageStatusUploaded}); err != nil {
			t.Fatalf("挂图失败: %v", err)
		}
		var imageID int64
		if err := repo.handle.QueryRowContext(ctx, `SELECT id FROM post_images WHERE post_id = ? AND sort_order = ?`, id, order).Scan(&imageID); err != nil {
			t.Fatalf("读取图片编号失败: %v", err)
		}
		if order == 1 {
			// 只存了一张时不允许重跑收尾。
			if ok, err := repo.RequeueFinish(ctx, id); err != nil || ok {
				t.Fatalf("图未存齐不应放行：ok=%v err=%v", ok, err)
			}
		}
		if err := repo.MarkImageStored(ctx, imageID, "file-"+strconv.Itoa(order), int64(order), "telegram"); err != nil {
			t.Fatalf("标记已存失败: %v", err)
		}
	}
	// 图都存好后再模拟「已同步、收尾写库失败」的终态。
	if _, err := repo.handle.ExecContext(ctx, `UPDATE posts SET error_message = '同步收尾写库失败，请到任务页重试', status = ? WHERE id = ?`, StatusFailed, id); err != nil {
		t.Fatalf("写入原因失败: %v", err)
	}
	ok, err := repo.RequeueFinish(ctx, id)
	if err != nil || !ok {
		t.Fatalf("图存齐后应可重跑收尾：ok=%v err=%v", ok, err)
	}
	post, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("读取帖子失败: %v", err)
	}
	if post.Status != StatusQueued {
		t.Errorf("状态 = %s，期望回队列", post.Status)
	}
	var message string
	if err := repo.handle.QueryRowContext(ctx, `SELECT COALESCE(error_message, '') FROM posts WHERE id = ?`, id).Scan(&message); err != nil {
		t.Fatalf("读取原因失败: %v", err)
	}
	if message != "" {
		t.Errorf("重跑收尾时应清掉旧原因：%q", message)
	}
}

// TestResumeKeepsReasonWhenPausedTaskFailed 确认「暂停→在途失败→继续」后原因仍可见。
func TestResumeKeepsReasonWhenPausedTaskFailed(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	id, err := repo.Create(ctx, Post{Title: "暂停中失败后恢复", ImageCount: 1, Status: StatusUploading})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	if err := repo.Pause(ctx, id); err != nil {
		t.Fatalf("暂停失败: %v", err)
	}
	if err := repo.MarkTaskFailed(ctx, id, "存图失败：账号受限（…），请解冻或更换 Bot，上游状态 400"); err != nil {
		t.Fatalf("标记失败出错: %v", err)
	}
	if err := repo.Resume(ctx, id); err != nil {
		t.Fatalf("恢复失败: %v", err)
	}
	post, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("读取帖子失败: %v", err)
	}
	if post.Status != StatusFailed {
		t.Errorf("失败任务恢复后不应回到处理中：%s", post.Status)
	}
	var message string
	if err := repo.handle.QueryRowContext(ctx, `SELECT COALESCE(error_message, '') FROM posts WHERE id = ?`, id).Scan(&message); err != nil {
		t.Fatalf("读取原因失败: %v", err)
	}
	if message == "" {
		t.Error("继续后应保留失败原因，方便用户知道为什么失败")
	}
}

// TestRetryRejectsPausedTask 确认暂停任务的失败图片不会被重排成永远不跑的队列项。
func TestRetryRejectsPausedTask(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	id, err := repo.Create(ctx, Post{Title: "暂停任务重试", ImageCount: 1, Status: StatusUploading})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	if _, err := repo.AttachImage(ctx, Image{PostID: id, SortOrder: 1, MimeType: "image/jpeg", Status: ImageStatusFailed}); err != nil {
		t.Fatalf("挂图失败: %v", err)
	}
	if err := repo.Pause(ctx, id); err != nil {
		t.Fatalf("暂停失败: %v", err)
	}
	retried, err := repo.ResetFailedImages(ctx, id)
	if err != nil {
		t.Fatalf("重排出错: %v", err)
	}
	if retried != 0 {
		t.Errorf("暂停任务不应被重排：%d", retried)
	}
	post, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("读取帖子失败: %v", err)
	}
	if post.Status != StatusUploading {
		t.Errorf("暂停任务状态被改动：%s", post.Status)
	}
}

