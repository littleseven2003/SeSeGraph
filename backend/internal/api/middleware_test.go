package api

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRecoveryHidesPanicDetails(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := log.New(buf, "", 0)

	router := gin.New()
	router.Use(Recovery(logger))
	router.GET("/boom", func(*gin.Context) {
		panic("内部细节不应外泄")
	})

	req := httptest.NewRequest(http.MethodGet, "/boom?password=query-secret", nil)
	req.AddCookie(&http.Cookie{Name: "sesegraph_session", Value: "cookie-secret"})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("状态码 = %d，期望 500", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "服务器内部错误") {
		t.Errorf("响应应返回统一错误说明: %s", body)
	}
	for _, secret := range []string{"内部细节不应外泄", "query-secret", "cookie-secret"} {
		if strings.Contains(body, secret) {
			t.Errorf("响应不应包含 %q: %s", secret, body)
		}
	}

	logged := buf.String()
	for _, secret := range []string{"query-secret", "cookie-secret", "内部细节不应外泄"} {
		if strings.Contains(logged, secret) {
			t.Errorf("异常日志不应包含 %q，实际: %s", secret, logged)
		}
	}
	if !strings.Contains(logged, "/boom") {
		t.Errorf("异常日志应记录路由模板: %s", logged)
	}
}

func TestAccessLogIgnoresRequestContent(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := log.New(buf, "", 0)

	router := gin.New()
	router.Use(AccessLog(logger))
	router.POST("/api/auth/login", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	body := `{"username":"admin","password":"body-secret"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login?token=query-secret", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer header-secret")
	req.AddCookie(&http.Cookie{Name: "sesegraph_session", Value: "cookie-secret"})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	logged := buf.String()
	for _, secret := range []string{"body-secret", "query-secret", "header-secret", "cookie-secret"} {
		if strings.Contains(logged, secret) {
			t.Errorf("访问日志不应包含 %q，实际: %s", secret, logged)
		}
	}
	if !strings.Contains(logged, "POST /api/auth/login") {
		t.Errorf("访问日志应包含方法与路由模板: %s", logged)
	}
}

func TestRecoveryAndAccessLogUseDefaultLogger(t *testing.T) {
	// 传入空日志器时不应 panic，应回落到默认日志器。
	router := gin.New()
	router.Use(Recovery(nil), AccessLog(nil))
	router.GET("/ping", func(c *gin.Context) { c.Status(http.StatusOK) })

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ping", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", rec.Code)
	}
}
