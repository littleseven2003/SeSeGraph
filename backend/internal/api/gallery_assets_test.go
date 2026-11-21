package api

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
)

func TestTagsEndpointReturnsCounts(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)
	galleryPost(t, env, "第一帖", "", "写真", "海边")
	galleryPost(t, env, "第二帖", "", "写真")

	rec := env.do(t, http.MethodGet, "/api/tags", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", rec.Code)
	}
	var body struct {
		Items []struct {
			Name  string `json:"name"`
			Count int    `json:"count"`
		} `json:"items"`
	}
	if err := decodeJSON(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if len(body.Items) != 2 || body.Items[0].Name != "写真" || body.Items[0].Count != 2 {
		t.Errorf("标签统计 = %+v", body.Items)
	}
}

func TestThumbServesCoverFile(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)
	dir := filepath.Join(env.dataDir, posts.CoverDir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("创建封面目录失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "7.webp"), []byte("webp-bytes"), 0o640); err != nil {
		t.Fatalf("写入封面失败: %v", err)
	}

	rec := env.do(t, http.MethodGet, "/api/thumbs/7.webp", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200，响应: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "image/webp" {
		t.Errorf("Content-Type = %q，期望 image/webp", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("缺少 nosniff 头，实际 %q", got)
	}
	if rec.Body.String() != "webp-bytes" {
		t.Errorf("响应体 = %q", rec.Body.String())
	}

	for _, name := range []string{"8.webp", "7.png", "cover.webp", "..%2F..%2Fetc%2Fpasswd"} {
		rec := env.do(t, http.MethodGet, "/api/thumbs/"+name, "", cookie)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s 状态码 = %d，期望 404", name, rec.Code)
		}
	}
}
