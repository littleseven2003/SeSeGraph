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

// TestGetViewsSumsNestedCounts 验证上游按年月日时嵌套返回的计数被累加成一个总数。
func TestGetViewsSumsNestedCounts(t *testing.T) {
	var gotPath, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		_, _ = w.Write([]byte(`{"ok":true,"result":{"views":{"2026":{"11":{"22":{"10":3,"11":2},"23":{"9":1}}},"2027":{"1":{"1":{"8":5}}}}}}`))
	}))
	defer server.Close()

	client := New(Config{APIBase: server.URL, AccessToken: "secret-token"})
	views, err := client.GetViews(context.Background(), "Page-11-22")
	if err != nil {
		t.Fatalf("读取浏览量失败: %v", err)
	}
	if views != 11 {
		t.Fatalf("浏览量 = %d，期望 11", views)
	}
	if gotPath != "/getViews/Page-11-22" {
		t.Errorf("请求路径 = %q", gotPath)
	}
	if strings.Contains(gotBody, "secret-token") || strings.Contains(gotBody, "access_token") {
		t.Errorf("浏览量请求不应携带令牌：%q", gotBody)
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
