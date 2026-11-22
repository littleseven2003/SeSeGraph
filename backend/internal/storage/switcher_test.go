package storage

import (
	"context"
	"errors"
	"testing"
)

// TestSwitcherUnconfigured 验证初始状态与清空配置后的行为。
func TestSwitcherUnconfigured(t *testing.T) {
	ctx := context.Background()
	switcher := NewSwitcher()
	if switcher.Ready() {
		t.Error("初始状态应为未就绪")
	}
	if switcher.Name() != "unconfigured" {
		t.Errorf("初始标识 = %q，期望 unconfigured", switcher.Name())
	}
	if _, err := switcher.Upload(ctx, UploadRequest{}); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("未配置上传错误 = %v，期望 ErrNotConfigured", err)
	}
	if _, err := switcher.OpenStream(ctx, "file"); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("未配置读取错误 = %v，期望 ErrNotConfigured", err)
	}

	var nilSwitcher *Switcher
	if nilSwitcher.Ready() {
		t.Error("空指针不应报告就绪")
	}
	if _, err := nilSwitcher.Upload(ctx, UploadRequest{}); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("空指针上传错误 = %v，期望 ErrNotConfigured", err)
	}
}

// TestSwitcherSwap 验证替换后立即生效，清空后回到未配置。
func TestSwitcherSwap(t *testing.T) {
	switcher := NewSwitcher()
	provider, err := NewTelegram(TelegramConfig{Token: "123:token", ChatID: "-1001234567890"})
	if err != nil {
		t.Fatalf("创建提供方失败: %v", err)
	}

	switcher.Swap(provider)
	if !switcher.Ready() || switcher.Name() != "telegram" {
		t.Errorf("切换后 ready=%v name=%s，期望就绪且为 telegram", switcher.Ready(), switcher.Name())
	}

	switcher.Swap(nil)
	if switcher.Ready() {
		t.Error("清空后应回到未就绪")
	}
	if _, err := switcher.OpenStream(context.Background(), "file"); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("清空后读取错误 = %v，期望 ErrNotConfigured", err)
	}
}
