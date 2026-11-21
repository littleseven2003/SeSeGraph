package api

import (
	"context"
	"net/http"
	"strconv"
	"testing"

	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
)

func TestProgressRequiresSession(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	rec := env.do(t, http.MethodGet, "/api/posts/1/progress", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("状态码 = %d，期望 401", rec.Code)
	}
}

func TestProgressMissingPost(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)
	rec := env.do(t, http.MethodGet, "/api/posts/999/progress", "", cookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("状态码 = %d，期望 404", rec.Code)
	}
}

func TestProgressReportsCounts(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)
	postID := env.createPost(t, cookie, 3, "photo")

	for _, sort := range []int{1, 2, 3} {
		path := "/api/posts/" + strconv.Itoa(int(postID)) + "/images?sort=" + strconv.Itoa(sort)
		if rec := env.doRaw(t, http.MethodPost, path, "image/jpeg", jpegHeader, cookie); rec.Code != http.StatusOK {
			t.Fatalf("上传 sort=%d 状态码 = %d", sort, rec.Code)
		}
	}
	images, err := env.posts.ListImages(context.Background(), postID)
	if err != nil || len(images) != 3 {
		t.Fatalf("读取图片失败: %v", err)
	}
	if err := env.posts.MarkImageStored(context.Background(), images[0].ID, "remote-1", 101, "telegram"); err != nil {
		t.Fatalf("标记已存失败: %v", err)
	}
	if err := env.posts.MarkImageFailed(context.Background(), images[1].ID, "上传失败"); err != nil {
		t.Fatalf("标记失败失败: %v", err)
	}

	rec := env.do(t, http.MethodGet, "/api/posts/"+strconv.Itoa(int(postID))+"/progress", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", rec.Code)
	}

	var payload struct {
		ID          int64  `json:"id"`
		Status      string `json:"status"`
		ImageCount  int    `json:"image_count"`
		StoredCount int    `json:"stored_count"`
		FailedCount int    `json:"failed_count"`
		Images      []struct {
			SortOrder int    `json:"sort_order"`
			Status    string `json:"status"`
			Error     string `json:"error"`
		} `json:"images"`
	}
	if err := decodeJSON(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if payload.ID != postID || payload.ImageCount != 3 || payload.StoredCount != 1 || payload.FailedCount != 1 {
		t.Errorf("统计不符: %+v", payload)
	}
	if len(payload.Images) != 3 || payload.Images[1].Error != "上传失败" {
		t.Errorf("图片明细不符: %+v", payload.Images)
	}
	if payload.Status != posts.StatusUploading {
		t.Errorf("状态 = %q，期望 uploading", payload.Status)
	}
}

func TestRetryRequiresSession(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	rec := env.do(t, http.MethodPost, "/api/posts/1/retry", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("状态码 = %d，期望 401", rec.Code)
	}
}

func TestRetryRequeuesFailedImages(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)
	postID := env.createPost(t, cookie, 2, "photo")

	uploadPath := func(sort int) string {
		return "/api/posts/" + strconv.Itoa(int(postID)) + "/images?sort=" + strconv.Itoa(sort)
	}
	for _, sort := range []int{1, 2} {
		if rec := env.doRaw(t, http.MethodPost, uploadPath(sort), "image/jpeg", jpegHeader, cookie); rec.Code != http.StatusOK {
			t.Fatalf("上传状态码 = %d", rec.Code)
		}
	}
	images, _ := env.posts.ListImages(context.Background(), postID)
	if err := env.posts.MarkImageFailed(context.Background(), images[1].ID, "上传失败"); err != nil {
		t.Fatalf("标记失败: %v", err)
	}
	if err := env.posts.MarkPostFinished(context.Background(), postID, posts.StatusFailed, "上传图片失败"); err != nil {
		t.Fatalf("更新帖子状态失败: %v", err)
	}

	rec := env.do(t, http.MethodPost, "/api/posts/"+strconv.Itoa(int(postID))+"/retry", "", cookie)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("状态码 = %d，期望 202，响应 %s", rec.Code, rec.Body.String())
	}

	stored, err := env.posts.Get(context.Background(), postID)
	if err != nil {
		t.Fatalf("读取帖子失败: %v", err)
	}
	if stored.Status != posts.StatusQueued {
		t.Errorf("状态 = %q，期望 queued", stored.Status)
	}

	images, _ = env.posts.ListImages(context.Background(), postID)
	if images[1].Status != posts.ImageStatusUploaded {
		t.Errorf("失败图片应恢复为待上传，实际 %q", images[1].Status)
	}
}

func TestRetryWithoutFailedImagesConflicts(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)
	postID := env.createPost(t, cookie, 1, "photo")

	rec := env.do(t, http.MethodPost, "/api/posts/"+strconv.Itoa(int(postID))+"/retry", "", cookie)
	if rec.Code != http.StatusConflict {
		t.Fatalf("状态码 = %d，期望 409", rec.Code)
	}
}
