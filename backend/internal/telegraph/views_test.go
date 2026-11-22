package telegraph

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestGetViewsReadsInteger 验证按契约读取单个整数，且请求不带令牌。
func TestGetViewsReadsInteger(t *testing.T) {
	var gotPath, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		_, _ = w.Write([]byte(`{"ok":true,"result":{"views":1234}}`))
	}))
	defer server.Close()

	client := New(Config{APIBase: server.URL, AccessToken: "secret-token"})
	views, err := client.GetViews(context.Background(), "Page-11-22")
	if err != nil {
		t.Fatalf("读取浏览量失败: %v", err)
	}
	if views != 1234 {
		t.Fatalf("浏览量 = %d，期望 1234", views)
	}
	if gotPath != "/getViews/Page-11-22" {
		t.Errorf("请求路径 = %q", gotPath)
	}
	if strings.Contains(gotBody, "secret-token") || strings.Contains(gotBody, "access_token") {
		t.Errorf("浏览量请求不应携带令牌：%q", gotBody)
	}
}

// TestGetViewsRequiresInteger 验证缺失、null、负数与非整数都算上游失败。
func TestGetViewsRequiresInteger(t *testing.T) {
	bodies := []string{
		`{"ok":true,"result":{}}`,
		`{"ok":true,"result":{"views":null}}`,
		`{"ok":true,"result":{"views":-1}}`,
		`{"ok":true,"result":{"views":"12"}}`,
		`{"ok":true,"result":{"views":{"2026":1}}}`,
	}
	for _, body := range bodies {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(body))
		}))
		client := New(Config{APIBase: server.URL})
		if _, err := client.GetViews(context.Background(), "Page-11-22"); !errors.Is(err, ErrUpstream) {
			t.Errorf("响应 %s 的错误 = %v，期望 ErrUpstream", body, err)
		}
		server.Close()
	}
}

// TestGetViewsRejectsBadPath 验证空路径与含空白、斜杠的路径在本地拦下。
func TestGetViewsRejectsBadPath(t *testing.T) {
	client := New(Config{APIBase: "http://127.0.0.1:1"})
	for _, path := range []string{"", "   ", "a/b", "a b"} {
		if _, err := client.GetViews(context.Background(), path); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("路径 %q 的错误 = %v，期望 ErrInvalidInput", path, err)
		}
	}
}

// TestGetViewsUpstreamFailure 验证上游返回失败时只给统一错误，不回显细节。
func TestGetViewsUpstreamFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":false,"error":"PAGE_ACCESS_DENIED"}`))
	}))
	defer server.Close()

	client := New(Config{APIBase: server.URL})
	_, err := client.GetViews(context.Background(), "Page-11-22")
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("错误 = %v，期望 ErrUpstream", err)
	}
	if strings.Contains(err.Error(), "PAGE_ACCESS_DENIED") {
		t.Errorf("错误信息泄露了上游细节：%v", err)
	}
}
