package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q，期望 no-store", got)
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

// TestThumbNeedsSessionAfterLogout 验证封面响应不被缓存，且登出后的请求重新受鉴权约束。
func TestThumbNeedsSessionAfterLogout(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)
	dir := filepath.Join(env.dataDir, posts.CoverDir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("创建封面目录失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "5.webp"), []byte("webp-bytes"), 0o640); err != nil {
		t.Fatalf("写入封面失败: %v", err)
	}

	rec := env.do(t, http.MethodGet, "/api/thumbs/5.webp", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("登录状态码 = %d，期望 200", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("登录响应 Cache-Control = %q，期望 no-store", got)
	}

	logout := env.do(t, http.MethodPost, "/api/auth/logout", "", cookie)
	if logout.Code != http.StatusNoContent {
		t.Fatalf("登出状态码 = %d，期望 204", logout.Code)
	}
	if cleared := logout.Header().Get("Set-Cookie"); cleared == "" || !strings.Contains(cleared, "Max-Age=0") {
		t.Errorf("登出应清空会话 Cookie，实际 %q", cleared)
	}

	after := env.do(t, http.MethodGet, "/api/thumbs/5.webp", "", nil)
	if after.Code != http.StatusUnauthorized {
		t.Errorf("登出后状态码 = %d，期望 401", after.Code)
	}
}

// TestThumbServesVersionedCoverFile 验证换封面后写入的「编号-12位十六进制.webp」也能读取。
func TestThumbServesVersionedCoverFile(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)
	dir := filepath.Join(env.dataDir, posts.CoverDir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("创建封面目录失败: %v", err)
	}
	name := "9-0123456789ab.webp"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("webp-bytes"), 0o640); err != nil {
		t.Fatalf("写入封面失败: %v", err)
	}

	rec := env.do(t, http.MethodGet, "/api/thumbs/"+name, "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200，响应: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "webp-bytes" {
		t.Errorf("响应体 = %q", rec.Body.String())
	}

	// 越界或不合规的文件名仍要拒绝，避免把封面目录当成通用静态目录。
	for _, bad := range []string{
		"9-0123.webp",
		"9-0123456789AZ.webp",
		"9-.webp",
		"9-0123456789ab.webp.bak",
		"../9-0123456789ab.webp",
	} {
		if got := env.do(t, http.MethodGet, "/api/thumbs/"+bad, "", cookie); got.Code != http.StatusNotFound {
			t.Errorf("%s 状态码 = %d，期望 404", bad, got.Code)
		}
	}
}
