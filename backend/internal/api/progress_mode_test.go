package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
)

// TestProgressReportsTaskUploadMode 验证进度接口带回任务画质，供前端续传时按原模式校验。
func TestProgressReportsTaskUploadMode(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)

	postID, err := env.posts.Create(context.Background(), posts.Post{
		Title: "原图任务", ImageCount: 1, UploadMode: "document", Status: posts.StatusUploading,
	})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}

	rec := env.do(t, http.MethodGet, "/api/posts/"+strconv.FormatInt(postID, 10)+"/progress", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("读取进度状态 = %d", rec.Code)
	}
	var payload struct {
		UploadMode string `json:"upload_mode"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if payload.UploadMode != "document" {
		t.Errorf("进度里的画质 = %q，期望 document", payload.UploadMode)
	}
}
