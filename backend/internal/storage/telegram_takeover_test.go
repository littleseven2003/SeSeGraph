package storage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// TestTelegramTakeoverForwardsThenDeletes 验证接管路径：先转发原消息取得自己的文件编号，
// 再删除这条临时消息；缺少原消息位置时不发请求。
func TestTelegramTakeoverForwardsThenDeletes(t *testing.T) {
	var mu sync.Mutex
	var methods []string
	var fromChat, fromMessage, deleteChat, deleteMessage string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		mu.Lock()
		methods = append(methods, method)
		switch method {
		case "forwardMessage":
			fromChat = r.PostForm.Get("from_chat_id")
			fromMessage = r.PostForm.Get("message_id")
		case "deleteMessage":
			deleteChat = r.PostForm.Get("chat_id")
			deleteMessage = r.PostForm.Get("message_id")
		}
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if method == "forwardMessage" {
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":77,"document":{"file_id":"own-file","file_size":10}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	t.Cleanup(server.Close)

	provider, err := NewTelegram(TelegramConfig{Token: "111:token", ChatID: "-100777", APIBase: server.URL})
	if err != nil {
		t.Fatalf("创建提供方失败: %v", err)
	}
	fileID, err := provider.TakeoverFile(context.Background(), "-100777", 55)
	if err != nil {
		t.Fatalf("接管失败: %v", err)
	}
	if fileID != "own-file" {
		t.Fatalf("新文件编号 = %q，期望 own-file", fileID)
	}

	mu.Lock()
	got := strings.Join(methods, ",")
	mu.Unlock()
	if got != "forwardMessage,deleteMessage" {
		t.Fatalf("调用顺序 = %q，期望转发后删除临时消息", got)
	}
	if fromChat != "-100777" || fromMessage != "55" {
		t.Errorf("转发参数 = %q/%q，期望原频道与原消息", fromChat, fromMessage)
	}
	if deleteChat != "-100777" || deleteMessage != "77" {
		t.Errorf("删除参数 = %q/%q，期望删除转发出来的 77 号消息", deleteChat, deleteMessage)
	}

	if _, err := provider.TakeoverFile(context.Background(), "", 55); err == nil {
		t.Fatal("缺少原频道时应直接报错")
	}
	if _, err := (*Telegram)(nil).TakeoverFile(context.Background(), "-100777", 55); err == nil {
		t.Fatal("未配置图床时应返回错误")
	}
}

// TestTelegramTakeoverRejectsBadPayload 验证上游返回不带媒体时按上游错误处理。
func TestTelegramTakeoverRejectsBadPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":9}}`))
	}))
	t.Cleanup(server.Close)

	provider, err := NewTelegram(TelegramConfig{Token: "111:token", ChatID: "-100777", APIBase: server.URL})
	if err != nil {
		t.Fatalf("创建提供方失败: %v", err)
	}
	if _, err := provider.TakeoverFile(context.Background(), "-100777", 55); err == nil {
		t.Fatal("转发结果没有文件编号时应报错")
	}
}
