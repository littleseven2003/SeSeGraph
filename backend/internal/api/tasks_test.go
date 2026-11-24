package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
)

// TestTasksListReturnsStagesAndControl 验证任务列表返回三阶段状态与控制状态。
func TestTasksListReturnsStagesAndControl(t *testing.T) {
	env, _ := newEditEnv(t)
	cookie := env.setup(t)
	ctx := context.Background()

	postID, err := env.posts.Create(ctx, posts.Post{Title: "任务列表", ImageCount: 2, Status: posts.StatusUploading})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	if _, err := env.posts.AttachImage(ctx, posts.Image{
		PostID: postID, SortOrder: 1, Status: posts.ImageStatusUploaded, MimeType: "image/jpeg",
	}); err != nil {
		t.Fatalf("保存图片失败: %v", err)
	}
	if err := env.posts.Pause(ctx, postID); err != nil {
		t.Fatalf("暂停失败: %v", err)
	}

	rec := env.do(t, http.MethodGet, "/api/tasks?page=1&page_size=10", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，响应: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []struct {
			ID            int64  `json:"id"`
			Status        string `json:"status"`
			PublishState  string `json:"publish_state"`
			ControlState  string `json:"control_state"`
			ImageCount    int    `json:"image_count"`
			StoredCount   int    `json:"stored_count"`
			UploadedCount int    `json:"uploaded_count"`
			ChannelCount  int    `json:"channel_count"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析任务失败: %v", err)
	}
	if body.Total != 1 || len(body.Items) != 1 {
		t.Fatalf("任务 = %d/%d，期望 1 条", body.Total, len(body.Items))
	}
	item := body.Items[0]
	if item.ID != postID || item.ControlState != posts.ControlPaused || item.ImageCount != 2 || item.StoredCount != 1 {
		t.Errorf("任务行 = %+v", item)
	}
	// 已接收 1 张但尚未存入频道：三段状态不能互相冒充。
	if item.UploadedCount != 1 || item.ChannelCount != 0 {
		t.Errorf("任务行 = %+v", item)
	}

	// 停止后不再出现在进行中列表里。
	if err := env.posts.Stop(ctx, postID); err != nil {
		t.Fatalf("停止失败: %v", err)
	}
	if err := env.posts.Delete(ctx, postID); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	rec = env.do(t, http.MethodGet, fmt.Sprintf("/api/tasks?page=1&page_size=10"), "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("第二次状态码 = %d", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析任务失败: %v", err)
	}
	if body.Total != 0 {
		t.Errorf("删除后任务数 = %d，期望 0", body.Total)
	}
}

// TestUploadRejectsStaleRound 验证上传轮次不匹配时拒绝写入。
func TestUploadRejectsStaleRound(t *testing.T) {
	env, _ := newEditEnv(t)
	cookie := env.setup(t)
	ctx := context.Background()
	postID, err := env.posts.Create(ctx, posts.Post{Title: "轮次", ImageCount: 1, Status: posts.StatusUploading})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	if _, err := env.handle.ExecContext(ctx, `UPDATE posts SET upload_round = 2 WHERE id = ?`, postID); err != nil {
		t.Fatalf("写入轮次失败: %v", err)
	}
	rec := env.do(t, http.MethodPost, fmt.Sprintf("/api/posts/%d/images?sort=1&round=1", postID), "", cookie)
	if rec.Code != http.StatusConflict {
		t.Errorf("旧轮次状态码 = %d，期望 409，响应: %s", rec.Code, rec.Body.String())
	}
}

// TestTasksListFiltersBeforePaging 验证筛选在分页前生效，且三段计数各自独立。
func TestTasksListFiltersBeforePaging(t *testing.T) {
	env, _ := newEditEnv(t)
	cookie := env.setup(t)
	ctx := context.Background()

	create := func(title string) int64 {
		id, err := env.posts.Create(ctx, posts.Post{Title: title, ImageCount: 2, Status: posts.StatusUploading})
		if err != nil {
			t.Fatalf("创建帖子失败: %v", err)
		}
		return id
	}
	active := create("进行中")
	paused := create("已暂停")
	stopped := create("已停止")
	finished := create("已完成")
	failed := create("失败")

	// 进行中的帖子：一张已接收未入频道，一张已入频道，三段口径不能互相冒充。
	for _, image := range []posts.Image{
		{PostID: active, SortOrder: 1, Status: posts.ImageStatusUploaded, MimeType: "image/jpeg"},
		{PostID: active, SortOrder: 2, Status: posts.ImageStatusStored, MimeType: "image/jpeg"},
	} {
		if _, err := env.posts.AttachImage(ctx, image); err != nil {
			t.Fatalf("保存图片失败: %v", err)
		}
	}
	for _, stmt := range []string{
		fmt.Sprintf(`UPDATE posts SET control_state = 'paused' WHERE id = %d`, paused),
		fmt.Sprintf(`UPDATE posts SET control_state = 'stopped' WHERE id = %d`, stopped),
		fmt.Sprintf(`UPDATE posts SET status = 'completed', publish_state = 'published' WHERE id = %d`, finished),
		fmt.Sprintf(`UPDATE posts SET status = 'failed', publish_state = 'failed' WHERE id = %d`, failed),
	} {
		if _, err := env.handle.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("准备任务状态失败: %v", err)
		}
	}

	type page struct {
		Items []struct {
			ID            int64 `json:"id"`
			StoredCount   int   `json:"stored_count"`
			UploadedCount int   `json:"uploaded_count"`
			ChannelCount  int   `json:"channel_count"`
		} `json:"items"`
		Total int `json:"total"`
	}
	read := func(status string) page {
		rec := env.do(t, http.MethodGet, "/api/tasks?page=1&page_size=2&status="+status, "", cookie)
		if rec.Code != http.StatusOK {
			t.Fatalf("状态 %q 状态码 = %d，响应: %s", status, rec.Code, rec.Body.String())
		}
		var body page
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("解析任务失败: %v", err)
		}
		return body
	}
	countOf := func(status string) int { return read(status).Total }

	if got := countOf(""); got != 5 {
		t.Errorf("全部任务 = %d，期望 5", got)
	}
	// 未知筛选回落为全部，旧调用不会因为参数拼错而看不到任务。
	if got := countOf("weird"); got != 5 {
		t.Errorf("未知筛选 = %d，期望回落全部 5", got)
	}
	if got := countOf(posts.TaskFilterActive); got != 1 {
		t.Errorf("进行中 = %d，期望 1", got)
	}
	if got := countOf(posts.TaskFilterPaused); got != 1 {
		t.Errorf("已暂停 = %d，期望 1", got)
	}
	if got := countOf(posts.TaskFilterStopped); got != 1 {
		t.Errorf("已停止 = %d，期望 1", got)
	}
	if got := countOf(posts.TaskFilterFinished); got != 1 {
		t.Errorf("已完成 = %d，期望 1", got)
	}

	// total 与分页同条件：page_size 小于命中数时仍返回筛选后的总数。
	many := read(posts.TaskFilterAll)
	if many.Total != 5 || len(many.Items) > 2 {
		t.Errorf("全部任务分页 = %d 条 / 共 %d，期望最多 2 条共 5 条", len(many.Items), many.Total)
	}
	activePage := read(posts.TaskFilterActive)
	if len(activePage.Items) != 1 || activePage.Items[0].ID != active {
		t.Fatalf("进行中任务 = %+v", activePage.Items)
	}
	row := activePage.Items[0]
	if row.StoredCount != 2 || row.UploadedCount != 1 || row.ChannelCount != 1 {
		t.Errorf("三段计数 = 已接收 %d / 已上传 %d / 已入频道 %d，期望 2 / 1 / 1",
			row.StoredCount, row.UploadedCount, row.ChannelCount)
	}
}
