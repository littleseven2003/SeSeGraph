package telegraph

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestClientKeepsTimeoutForInjectedClient 验证注入传输层时不会丢掉请求超时：
// 未指定超时沿用默认值，显式指定时尊重调用方取值。
func TestClientKeepsTimeoutForInjectedClient(t *testing.T) {
	client := New(Config{AccessToken: "test-token", HTTPClient: &http.Client{Transport: &http.Transport{}}})
	if client.client.Timeout != defaultTimeout {
		t.Errorf("超时 = %v，期望沿用默认值 %v", client.client.Timeout, defaultTimeout)
	}

	custom := 5 * time.Second
	client = New(Config{AccessToken: "test-token", HTTPClient: &http.Client{Timeout: custom}})
	if client.client.Timeout != custom {
		t.Errorf("超时 = %v，期望保留调用方取值 %v", client.client.Timeout, custom)
	}
}

// TestClientTimesOutOnHangingUpstream 验证上游不响应时请求会超时返回，
// 不会让发布队列或保存流程无限等待。
func TestClientTimesOutOnHangingUpstream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
	}))
	t.Cleanup(server.Close)

	client := New(Config{
		AccessToken: "test-token",
		APIBase:     server.URL,
		HTTPClient:  &http.Client{Timeout: 150 * time.Millisecond},
	})

	started := time.Now()
	_, err := client.CreatePage(context.Background(), PageRequest{
		Title:      "标题",
		AuthorName: "SeSe",
		Content:    []Node{Paragraph("正文")},
	})
	if err == nil {
		t.Fatal("上游不响应时应返回错误")
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Errorf("耗时 = %v，期望在超时后尽快返回", elapsed)
	}
}
