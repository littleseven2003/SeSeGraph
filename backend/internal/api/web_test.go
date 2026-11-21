package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"
)

const (
	testIndexHTML = "<!doctype html><html><head><title>SeSeGraph</title></head><body>entry</body></html>"
	testAppJS     = "console.log('shell')"
)

func newWebEnv(t *testing.T) *gin.Engine {
	t.Helper()
	webFS := fstest.MapFS{
		"index.html":    {Data: []byte(testIndexHTML)},
		"static/app.js": {Data: []byte(testAppJS)},
	}
	return NewRouter(Deps{
		Version: "v0.1.0-test",
		Commit:  "abc1234",
		Auth:    nil,
		WebFS:   webFS,
	})
}

func TestServesIndexAtRoot(t *testing.T) {
	router := newWebEnv(t)

	for _, path := range []string{"/", "/login", "/setup"} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s 状态码 = %d，期望 200", path, rec.Code)
			continue
		}
		if !strings.Contains(rec.Body.String(), "entry") {
			t.Errorf("%s 应返回前端入口页，实际: %s", path, rec.Body.String())
		}
		if cache := rec.Header().Get("Cache-Control"); cache != "no-cache" {
			t.Errorf("%s Cache-Control = %q，期望 no-cache", path, cache)
		}
	}
}

func TestServesHashedAssetWithLongCache(t *testing.T) {
	router := newWebEnv(t)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/app.js", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", rec.Code)
	}
	if rec.Body.String() != testAppJS {
		t.Errorf("资源内容不符: %s", rec.Body.String())
	}
	if cache := rec.Header().Get("Cache-Control"); !strings.Contains(cache, "immutable") {
		t.Errorf("Cache-Control = %q，期望 immutable 长缓存", cache)
	}
}

func TestAPIPathsDoNotFallBackToIndex(t *testing.T) {
	router := newWebEnv(t)

	for _, path := range []string{"/api/unknown", "/i/abc123"} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s 状态码 = %d，期望 404", path, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "entry") {
			t.Errorf("%s 不应回退到入口页", path)
		}
	}
}

func TestMissingAssetReturns404(t *testing.T) {
	router := newWebEnv(t)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/missing.js", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("状态码 = %d，期望 404", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "entry") {
		t.Errorf("缺失静态资源不应回退到入口页")
	}
}

func TestNonGetRequestsAreNotServedPages(t *testing.T) {
	router := newWebEnv(t)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/unknown", strings.NewReader("")))
	if rec.Code != http.StatusNotFound {
		t.Errorf("状态码 = %d，期望 404", rec.Code)
	}
}

func TestMissingIndexFallsBackToHint(t *testing.T) {
	webFS := fstest.MapFS{
		".keep": {Data: []byte("placeholder")},
	}
	router := NewRouter(Deps{Version: "dev", Commit: "unknown", WebFS: webFS})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("状态码 = %d，期望 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "前端页面尚未构建") {
		t.Errorf("应提示前端尚未构建，实际: %s", rec.Body.String())
	}
}

func TestAPIUnknownPathsReturnJSON404(t *testing.T) {
	router := newWebEnv(t)
	paths := []string{
		"/api/unknown",
		"/api/thumbs/../../etc/passwd",
		"/i/../../etc/passwd",
	}
	for _, target := range paths {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s 状态码 = %d，期望 404", target, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "entry") {
			t.Errorf("%s 不应回退到入口页", target)
		}
	}
}
