package storage

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

// TestKindForTelegram 覆盖已知上游原因与未知响应的归类。
func TestKindForTelegram(t *testing.T) {
	cases := []struct {
		status int
		desc   string
		want   string
	}{
		{429, "Too Many Requests", KindRateLimit},
		{400, "Bad Request: FROZEN_METHOD_INVALID", KindAccount},
		{400, "Bad Request: chat not found", KindAuth},
		{403, "Forbidden: bot is not a member of the channel chat", KindAuth},
		{400, "Bad Request: file is too big", KindTooLarge},
		{400, "Bad Request: PHOTO_INVALID_DIMENSIONS", KindTooLarge},
		{502, "Bad Gateway", KindUpstream},
		{400, "Bad Request: something new", KindUnknown},
	}
	for _, item := range cases {
		if got := KindForTelegram(item.status, item.desc); got != item.want {
			t.Errorf("KindForTelegram(%d, %q) = %q，期望 %q", item.status, item.desc, got, item.want)
		}
	}
}

// TestDescribeUploadKeepsReadableReason 确认账号受限能看懂，且不回显原始描述。
func TestDescribeUploadKeepsReadableReason(t *testing.T) {
	frozen := &UploadError{Stage: "存图", Kind: KindAccount, Status: 400}
	text := DescribeUpload(frozen)
	if !strings.Contains(text, "账号受限") || !strings.Contains(text, "解冻") {
		t.Errorf("账号受限说明 = %q，期望带可操作提示", text)
	}
	if strings.Contains(text, "FROZEN") || strings.Contains(text, "bot") {
		t.Errorf("说明不应带原始上游描述：%q", text)
	}
	if !strings.Contains(text, "上游状态 400") {
		t.Errorf("说明应保留可用状态码：%q", text)
	}

	tooLarge := DescribeUpload(ErrTooLarge)
	if !strings.Contains(tooLarge, "10MB") {
		t.Errorf("超限说明 = %q，期望带上限", tooLarge)
	}

	timeout := DescribeUpload(context.DeadlineExceeded)
	if !strings.Contains(timeout, "超时") {
		t.Errorf("超时说明 = %q", timeout)
	}

	var netErr net.Error = timeoutError{}
	if got := DescribeUpload(netErr); !strings.Contains(got, "超时") {
		t.Errorf("网络超时说明 = %q", got)
	}
	if got := DescribeUpload(errors.New("dial tcp 10.0.0.1:443: connect: connection refused")); got == "" || strings.Contains(got, "10.0.0.1") {
		t.Errorf("未知错误不应泄露地址：%q", got)
	}
}

// timeoutError 只实现 net.Error 的超时判定。
type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

// TestRateLimitErrorStillDescribes 确认限流仍走原有类别。
func TestRateLimitErrorStillDescribes(t *testing.T) {
	err := &RateLimitError{RetryAfter: 3 * time.Second}
	if got := DescribeUpload(err); !strings.Contains(got, "限流") {
		t.Errorf("限流说明 = %q", got)
	}
}
