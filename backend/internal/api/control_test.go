package api

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
)

// TestTaskControlEndpoints 验证暂停、继续、停止的状态流转与进度字段。
func TestTaskControlEndpoints(t *testing.T) {
	env, _ := newEditEnv(t)
	cookie := env.setup(t)
	ctx := context.Background()
	postID, err := env.posts.Create(ctx, posts.Post{Title: "控制接口", ImageCount: 1, Status: posts.StatusUploading})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	if _, err := env.posts.AttachImage(ctx, posts.Image{
		PostID: postID, SortOrder: 1, Status: posts.ImageStatusUploaded, MimeType: "image/jpeg",
	}); err != nil {
		t.Fatalf("保存图片失败: %v", err)
	}

	rec := env.do(t, http.MethodPost, fmt.Sprintf("/api/posts/%d/pause", postID), "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("暂停状态码 = %d，响应: %s", rec.Code, rec.Body.String())
	}
	rec = env.do(t, http.MethodGet, fmt.Sprintf("/api/posts/%d/progress", postID), "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("进度状态码 = %d", rec.Code)
	}
	var progress map[string]any
	if err := decodeJSON(rec.Body.Bytes(), &progress); err != nil {
		t.Fatalf("解析进度失败: %v", err)
	}
	if progress["control_state"] != posts.ControlPaused {
		t.Errorf("进度控制状态 = %v，期望 paused", progress["control_state"])
	}
	if round, ok := progress["upload_round"].(float64); !ok || round != 0 {
		t.Errorf("上传轮次 = %v，期望 0", progress["upload_round"])
	}

	rec = env.do(t, http.MethodPost, fmt.Sprintf("/api/posts/%d/resume", postID), "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("继续状态码 = %d，响应: %s", rec.Code, rec.Body.String())
	}
	rec = env.do(t, http.MethodGet, fmt.Sprintf("/api/posts/%d/progress", postID), "", cookie)
	if err := decodeJSON(rec.Body.Bytes(), &progress); err != nil {
		t.Fatalf("解析进度失败: %v", err)
	}
	if progress["control_state"] != posts.ControlRunning {
		t.Errorf("继续后控制状态 = %v，期望 running", progress["control_state"])
	}
	if round, ok := progress["upload_round"].(float64); !ok || round != 1 {
		t.Errorf("继续后上传轮次 = %v，期望 1", progress["upload_round"])
	}

	rec = env.do(t, http.MethodPost, fmt.Sprintf("/api/posts/%d/stop", postID), "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("停止状态码 = %d，响应: %s", rec.Code, rec.Body.String())
	}
	if rec := env.do(t, http.MethodPost, fmt.Sprintf("/api/posts/%d/resume", postID), "", cookie); rec.Code != http.StatusConflict {
		t.Errorf("停止后继续状态码 = %d，期望 409", rec.Code)
	}
	if rec := env.do(t, http.MethodPost, fmt.Sprintf("/api/posts/%d/pause", postID), "", cookie); rec.Code != http.StatusConflict {
		t.Errorf("重复停止后暂停状态码 = %d，期望 409", rec.Code)
	}
}

// TestUploadRejectsPausedTask 验证暂停期间上传被拒绝，且不读请求体。
func TestUploadRejectsPausedTask(t *testing.T) {
	env, _ := newEditEnv(t)
	cookie := env.setup(t)
	ctx := context.Background()
	postID, err := env.posts.Create(ctx, posts.Post{Title: "暂停上传", ImageCount: 1, Status: posts.StatusUploading})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	if err := env.posts.Pause(ctx, postID); err != nil {
		t.Fatalf("暂停失败: %v", err)
	}
	rec := env.do(t, http.MethodPost, fmt.Sprintf("/api/posts/%d/images?sort=1", postID), "", cookie)
	if rec.Code != http.StatusConflict {
		t.Errorf("状态码 = %d，期望 409，响应: %s", rec.Code, rec.Body.String())
	}
}
