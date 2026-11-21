package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
)

const validPostBody = `{"title":"海边写真","description":"夏日海边的一组照片","author_name":"SeSe","upload_mode":"photo","image_count":3}`

func TestCreatePostRequiresSession(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	rec := env.do(t, http.MethodPost, "/api/posts", validPostBody, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("未登录创建帖子状态码 = %d，期望 401", rec.Code)
	}
}

func TestCreatePostStoresMetadata(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)

	rec := env.do(t, http.MethodPost, "/api/posts", validPostBody, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("状态码 = %d，期望 201，响应: %s", rec.Code, rec.Body.String())
	}

	var body struct {
		ID         int64  `json:"id"`
		Title      string `json:"title"`
		Status     string `json:"status"`
		ImageCount int    `json:"image_count"`
		UploadMode string `json:"upload_mode"`
	}
	if err := decodeJSON(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if body.Status != posts.StatusUploading {
		t.Errorf("状态 = %q，期望 uploading", body.Status)
	}
	if body.ImageCount != 3 || body.UploadMode != "photo" {
		t.Errorf("字段不符: %+v", body)
	}

	stored, err := env.posts.Get(context.Background(), body.ID)
	if err != nil {
		t.Fatalf("读取帖子失败: %v", err)
	}
	if stored.Title != "海边写真" || stored.Description != "夏日海边的一组照片" || stored.AuthorName != "SeSe" {
		t.Errorf("元数据未按预期保存: %+v", stored)
	}
}

func TestCreatePostRejectsInvalidInput(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)

	cases := map[string]string{
		"空标题":    `{"title":"   ","image_count":1}`,
		"图片数为零":  `{"title":"标题","image_count":0}`,
		"画质模式非法": `{"title":"标题","image_count":1,"upload_mode":"raw"}`,
		"说明过长":   `{"title":"标题","image_count":1,"description":"` + strings.Repeat("字", 5001) + `"}`,
	}
	for name, body := range cases {
		rec := env.do(t, http.MethodPost, "/api/posts", body, cookie)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s 状态码 = %d，期望 400", name, rec.Code)
		}
	}
}

func TestCreatePostSavesCover(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)

	pixels := []byte{0x52, 0x49, 0x46, 0x46, 0x00, 0x00, 0x00, 0x00}
	cover := "data:image/webp;base64," + base64.StdEncoding.EncodeToString(pixels)
	body := `{"title":"带封面","image_count":1,"cover_thumbnail":"` + cover + `"}`

	rec := env.do(t, http.MethodPost, "/api/posts", body, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("状态码 = %d，期望 201，响应: %s", rec.Code, rec.Body.String())
	}

	var payload struct {
		ID             int64  `json:"id"`
		CoverThumbPath string `json:"cover_thumb_path"`
	}
	if err := decodeJSON(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if payload.CoverThumbPath == "" {
		t.Fatal("应返回封面路径")
	}
	file := filepath.Join(env.dataDir, filepath.FromSlash(payload.CoverThumbPath))
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("封面文件应存在: %v", err)
	}
}

func TestCreatePostRejectsOversizedBody(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)

	huge := strings.Repeat("a", maxCreatePostBodyBytes*2)
	body := `{"title":"超大请求","image_count":1,"description":"` + huge + `"}`

	rec := env.do(t, http.MethodPost, "/api/posts", body, cookie)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("状态码 = %d，期望 413", rec.Code)
	}

	var total int
	if err := env.handle.QueryRow("SELECT COUNT(*) FROM posts").Scan(&total); err != nil {
		t.Fatalf("统计帖子失败: %v", err)
	}
	if total != 0 {
		t.Errorf("超限请求不应创建帖子，实际 %d 条", total)
	}
}

func TestCreatePostRejectsOversizedCover(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)

	huge := base64.StdEncoding.EncodeToString(make([]byte, maxCoverBytes+1))
	body := `{"title":"封面过大","image_count":1,"cover_thumbnail":"` + huge + `"}`
	rec := env.do(t, http.MethodPost, "/api/posts", body, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("状态码 = %d，期望 400", rec.Code)
	}
}

func decodeJSON(data []byte, target any) error {
	return json.Unmarshal(data, target)
}
