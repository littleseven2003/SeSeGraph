package api

import (
	"context"
	"net/http"
	"strconv"
	"testing"

	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
)

// idText 把帖子编号转成路径片段。
func idText(id int64) string {
	return strconv.FormatInt(id, 10)
}

func TestCommitRequiresSession(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	rec := env.do(t, http.MethodPost, "/api/posts/1/commit", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("状态码 = %d，期望 401", rec.Code)
	}
}

func TestCommitMissingImagesStaysUploading(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)
	postID := env.createPost(t, cookie, 2, "photo")

	path := "/api/posts/" + idText(postID) + "/images?sort=1"
	if rec := env.doRaw(t, http.MethodPost, path, "image/jpeg", jpegHeader, cookie); rec.Code != http.StatusOK {
		t.Fatalf("上传状态码 = %d", rec.Code)
	}

	rec := env.do(t, http.MethodPost, "/api/posts/"+idText(postID)+"/commit", "", cookie)
	if rec.Code != http.StatusConflict {
		t.Fatalf("状态码 = %d，期望 409，响应: %s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Missing []int `json:"missing"`
	}
	if err := decodeJSON(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if len(payload.Missing) != 1 || payload.Missing[0] != 2 {
		t.Errorf("缺图序号 = %v，期望 [2]", payload.Missing)
	}

	stored, err := env.posts.Get(context.Background(), postID)
	if err != nil {
		t.Fatalf("读取帖子失败: %v", err)
	}
	if stored.Status != posts.StatusUploading {
		t.Errorf("缺图时状态 = %q，期望 uploading（不应入队）", stored.Status)
	}
}

func TestCommitQueuesWhenComplete(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)
	postID := env.createPost(t, cookie, 2, "photo")

	for _, sort := range []int{1, 2} {
		path := "/api/posts/" + idText(postID) + "/images?sort=" + strconv.Itoa(sort)
		if rec := env.doRaw(t, http.MethodPost, path, "image/jpeg", jpegHeader, cookie); rec.Code != http.StatusOK {
			t.Fatalf("上传 sort=%d 状态码 = %d", sort, rec.Code)
		}
	}

	rec := env.do(t, http.MethodPost, "/api/posts/"+idText(postID)+"/commit", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200，响应: %s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Status     string `json:"status"`
		ImageCount int    `json:"image_count"`
	}
	if err := decodeJSON(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if payload.Status != posts.StatusQueued || payload.ImageCount != 2 {
		t.Errorf("响应字段不符: %+v", payload)
	}

	stored, err := env.posts.Get(context.Background(), postID)
	if err != nil {
		t.Fatalf("读取帖子失败: %v", err)
	}
	if stored.Status != posts.StatusQueued {
		t.Errorf("入库状态 = %q，期望 queued", stored.Status)
	}
}

func TestCommitIsIdempotent(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)
	postID := env.createPost(t, cookie, 1, "photo")

	path := "/api/posts/" + idText(postID) + "/images?sort=1"
	if rec := env.doRaw(t, http.MethodPost, path, "image/jpeg", jpegHeader, cookie); rec.Code != http.StatusOK {
		t.Fatalf("上传状态码 = %d", rec.Code)
	}

	for attempt := 0; attempt < 2; attempt++ {
		rec := env.do(t, http.MethodPost, "/api/posts/"+idText(postID)+"/commit", "", cookie)
		if rec.Code != http.StatusOK {
			t.Fatalf("第 %d 次提交状态码 = %d，期望 200", attempt+1, rec.Code)
		}
	}
}

func TestUploadAfterCommitIsRejected(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)
	postID := env.createPost(t, cookie, 1, "photo")

	uploadPath := "/api/posts/" + idText(postID) + "/images?sort=1"
	if rec := env.doRaw(t, http.MethodPost, uploadPath, "image/jpeg", jpegHeader, cookie); rec.Code != http.StatusOK {
		t.Fatalf("上传状态码 = %d", rec.Code)
	}
	if rec := env.do(t, http.MethodPost, "/api/posts/"+idText(postID)+"/commit", "", cookie); rec.Code != http.StatusOK {
		t.Fatalf("提交状态码 = %d", rec.Code)
	}

	rec := env.doRaw(t, http.MethodPost, uploadPath, "image/jpeg", jpegHeader, cookie)
	if rec.Code != http.StatusConflict {
		t.Fatalf("入队后上传状态码 = %d，期望 409", rec.Code)
	}
}

func TestCommitMissingPost(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)

	rec := env.do(t, http.MethodPost, "/api/posts/999/commit", "", cookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("状态码 = %d，期望 404", rec.Code)
	}
}
