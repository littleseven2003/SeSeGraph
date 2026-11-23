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
			ID           int64  `json:"id"`
			Status       string `json:"status"`
			PublishState string `json:"publish_state"`
			ControlState string `json:"control_state"`
			ImageCount   int    `json:"image_count"`
			StoredCount  int    `json:"stored_count"`
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
