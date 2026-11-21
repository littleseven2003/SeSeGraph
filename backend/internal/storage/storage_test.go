package storage

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeProvider struct{}

func (fakeProvider) Name() string { return "fake" }

func (fakeProvider) Upload(context.Context, UploadRequest) (UploadResult, error) {
	return UploadResult{Provider: "fake", FileID: "file-1"}, nil
}

func TestProviderContract(t *testing.T) {
	var provider Provider = fakeProvider{}
	result, err := provider.Upload(context.Background(), UploadRequest{Reader: strings.NewReader("x")})
	if err != nil {
		t.Fatalf("上传失败: %v", err)
	}
	if result.FileID != "file-1" || result.Provider != "fake" {
		t.Errorf("返回值不符: %+v", result)
	}
}

func TestRateLimitErrorUnwraps(t *testing.T) {
	err := &RateLimitError{RetryAfter: 37 * time.Second}
	if !errors.Is(err, ErrRateLimited) {
		t.Error("应可用 errors.Is 判断为限流")
	}
	message := err.Error()
	if !strings.Contains(message, "37s") {
		t.Errorf("错误信息应包含等待时长: %s", message)
	}
}

func TestModeConstants(t *testing.T) {
	if ModePhoto != "photo" || ModeDocument != "document" {
		t.Error("模式常量取值被改动")
	}
}
