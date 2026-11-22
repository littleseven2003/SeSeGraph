package api

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"
	"testing"

	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
)

// TestDeletePostRemovesShortCode 验证删除后详情与公开短码立即失效。
func TestDeletePostRemovesShortCode(t *testing.T) {
	env := newTestEnvWith(t, testSetupToken, func(deps *Deps, _ *sql.DB) {
		deps.Streams = &fakeStreamProvider{}
	})
	cookie := env.setup(t)
	postID := publishedPost(t, env)
	code := shortCodeOf(t, env, postID)

	if rec := env.do(t, http.MethodGet, "/i/"+code, "", cookie); rec.Code != http.StatusOK {
		t.Fatalf("删除前短码状态 = %d，期望 200", rec.Code)
	}

	if rec := env.do(t, http.MethodDelete, "/api/posts/"+strconv.FormatInt(postID, 10), "", cookie); rec.Code != http.StatusNoContent {
		t.Fatalf("删除状态 = %d，期望 204，响应: %s", rec.Code, rec.Body.String())
	}
	if rec := env.do(t, http.MethodGet, "/i/"+code, "", cookie); rec.Code != http.StatusNotFound {
		t.Errorf("删除后短码状态 = %d，期望 404", rec.Code)
	}
	if rec := env.do(t, http.MethodGet, "/api/posts/"+strconv.FormatInt(postID, 10), "", cookie); rec.Code != http.StatusNotFound {
		t.Errorf("删除后详情状态 = %d，期望 404", rec.Code)
	}
	if rec := env.do(t, http.MethodDelete, "/api/posts/"+strconv.FormatInt(postID, 10), "", cookie); rec.Code != http.StatusNotFound {
		t.Errorf("重复删除状态 = %d，期望 404", rec.Code)
	}
}

// TestDeletePostRejectsRunningTask 验证上传中的任务不被删除，避免与队列交错。
func TestDeletePostRejectsRunningTask(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)

	for _, status := range []string{posts.StatusUploading, posts.StatusQueued, posts.StatusPublishing} {
		postID, err := env.posts.Create(context.Background(), posts.Post{
			Title: "进行中的任务", ImageCount: 1, UploadMode: "photo", Status: status,
		})
		if err != nil {
			t.Fatalf("创建帖子失败: %v", err)
		}
		rec := env.do(t, http.MethodDelete, "/api/posts/"+strconv.FormatInt(postID, 10), "", cookie)
		if rec.Code != http.StatusConflict {
			t.Errorf("%s 状态删除 = %d，期望 409", status, rec.Code)
		}
		if _, err := env.posts.Get(context.Background(), postID); err != nil {
			t.Errorf("%s 状态删除后记录应保留: %v", status, err)
		}
	}

	// 存图完成但 Telegraph 待发布或发布中同样拒绝，避免发布队列拿着已删记录。
	for _, state := range []string{posts.PublishPending, posts.PublishPublishing} {
		postID, err := env.posts.Create(context.Background(), posts.Post{
			Title: "待发布任务", ImageCount: 1, UploadMode: "photo", Status: posts.StatusCompleted,
		})
		if err != nil {
			t.Fatalf("创建帖子失败: %v", err)
		}
		if _, err := env.handle.Exec(`UPDATE posts SET publish_state = ? WHERE id = ?`, state, postID); err != nil {
			t.Fatalf("更新发布状态失败: %v", err)
		}
		rec := env.do(t, http.MethodDelete, "/api/posts/"+strconv.FormatInt(postID, 10), "", cookie)
		if rec.Code != http.StatusConflict {
			t.Errorf("发布状态 %s 删除 = %d，期望 409", state, rec.Code)
		}
		if _, err := env.posts.Get(context.Background(), postID); err != nil {
			t.Errorf("发布状态 %s 删除后记录应保留: %v", state, err)
		}
	}

	// 任务失败后允许删除。
	postID, err := env.posts.Create(context.Background(), posts.Post{
		Title: "失败任务", ImageCount: 1, UploadMode: "photo", Status: posts.StatusFailed,
	})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	if rec := env.do(t, http.MethodDelete, "/api/posts/"+strconv.FormatInt(postID, 10), "", cookie); rec.Code != http.StatusNoContent {
		t.Errorf("失败任务删除状态 = %d，期望 204", rec.Code)
	}
}
