package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func doGet(t *testing.T, router http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestHealthReturnsVersionInfo(t *testing.T) {
	router := NewRouter("v0.1.0-test", "abc1234")
	rec := doGet(t, router, "/api/health")

	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if len(body) != 3 {
		t.Errorf("响应字段数 = %d，期望仅 3 个: %v", len(body), body)
	}
	if body["status"] != "ok" {
		t.Errorf("status = %v", body["status"])
	}
	if body["version"] != "v0.1.0-test" {
		t.Errorf("version = %v", body["version"])
	}
	if body["commit"] != "abc1234" {
		t.Errorf("commit = %v", body["commit"])
	}
}

func TestHealthDefaultsInDevelopment(t *testing.T) {
	router := NewRouter("dev", "unknown")
	rec := doGet(t, router, "/api/health")

	var body HealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if body.Version != "dev" || body.Commit != "unknown" {
		t.Errorf("开发默认值不符: %+v", body)
	}
}

func TestOnlyHealthRouteIsPublic(t *testing.T) {
	router := NewRouter("dev", "unknown")
	paths := []string{"/", "/api/settings", "/api/posts", "/api/health/extra", "/i/abc123"}
	for _, path := range paths {
		rec := doGet(t, router, path)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s 状态码 = %d，期望 404", path, rec.Code)
		}
	}
}
