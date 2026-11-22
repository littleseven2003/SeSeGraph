package storage

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
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

// documentServer 统计收到的文档上传次数。
func documentServer(t *testing.T) (*httptest.Server, func() int) {
	t.Helper()
	var mu sync.Mutex
	count := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		mu.Lock()
		count++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"result": map[string]any{
				"message_id": 1,
				"document":   map[string]any{"file_id": "doc-1", "file_size": 10},
			},
		})
	}))
	t.Cleanup(server.Close)
	return server, func() int {
		mu.Lock()
		defer mu.Unlock()
		return count
	}
}

// TestSwitcherSendDocumentKeepsFixedProvider 验证校验后即使切换频道，发送仍走原提供方。
func TestSwitcherSendDocumentKeepsFixedProvider(t *testing.T) {
	oldServer, oldCount := documentServer(t)
	newServer, newCount := documentServer(t)

	path := filepath.Join(t.TempDir(), "sesegraph-20261122-030000.zip")
	if err := os.WriteFile(path, []byte("zip-bytes"), 0o600); err != nil {
		t.Fatalf("写入备份文件失败: %v", err)
	}

	switcher := NewSwitcher()
	oldProvider, err := NewTelegram(TelegramConfig{Token: "a-token", ChatID: "-100111", APIBase: oldServer.URL})
	if err != nil {
		t.Fatalf("创建旧提供方失败: %v", err)
	}
	switcher.Swap(oldProvider)

	// 备份发送先固定提供方，再校验并发送；期间保存设置切换频道不应影响这次发送。
	fixed, err := switcher.Current()
	if err != nil {
		t.Fatalf("读取当前提供方失败: %v", err)
	}
	newProvider, err := NewTelegram(TelegramConfig{Token: "b-token", ChatID: "-100222", APIBase: newServer.URL})
	if err != nil {
		t.Fatalf("创建新提供方失败: %v", err)
	}
	switcher.Swap(newProvider)

	if err := fixed.SendDocument(context.Background(), path, "备份"); err != nil {
		t.Fatalf("发送备份失败: %v", err)
	}
	if oldCount() != 1 {
		t.Errorf("原频道收到 %d 次文档，期望 1 次", oldCount())
	}
	if newCount() != 0 {
		t.Errorf("新频道收到 %d 次文档，期望 0 次", newCount())
	}

	// 切换后按当前提供方发送，应落到新频道。
	if err := switcher.SendDocument(context.Background(), path, "备份"); err != nil {
		t.Fatalf("切换后发送失败: %v", err)
	}
	if newCount() != 1 {
		t.Errorf("切换后新频道收到 %d 次文档，期望 1 次", newCount())
	}
}
