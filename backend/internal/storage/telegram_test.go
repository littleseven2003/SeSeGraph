package storage

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	testToken  = "123456:test-token-value"
	testChatID = "-1001234567890"
)

// chunkTracker 记录读取时使用的最大缓冲区，用来证明传输是流式的。
type chunkTracker struct {
	data     []byte
	offset   int
	maxChunk int
}

func (c *chunkTracker) Read(p []byte) (int, error) {
	if len(p) > c.maxChunk {
		c.maxChunk = len(p)
	}
	if c.offset >= len(c.data) {
		return 0, io.EOF
	}
	n := copy(p, c.data[c.offset:])
	c.offset += n
	return n, nil
}

type fakeAPI struct {
	server   *httptest.Server
	requests atomic.Int32
	method   string
	chatID   string
	fileName string
	content  []byte
	caption  string
}

func newFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()
	api := &fakeAPI{}
	api.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.requests.Add(1)
		api.method = r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]

		reader, err := r.MultipartReader()
		if err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		for {
			part, nextErr := reader.NextPart()
			if nextErr == io.EOF {
				break
			}
			if nextErr != nil {
				http.Error(w, "bad part", http.StatusBadRequest)
				return
			}
			body, _ := io.ReadAll(part)
			if part.FileName() != "" {
				api.fileName = part.FileName()
				api.content = body
			} else {
				switch part.FormName() {
				case "chat_id":
					api.chatID = string(body)
				case "caption":
					api.caption = string(body)
				}
			}
		}

		if strings.Contains(api.method, "sendDocument") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"result": map[string]any{
					"message_id": 22,
					"document": map[string]any{
						"file_id":   "doc-file-id",
						"file_size": len(api.content),
						"mime_type": "image/jpeg",
					},
				},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"result": map[string]any{
				"message_id": 11,
				"photo": []map[string]any{
					{"file_id": "small", "file_size": 100},
					{"file_id": "large-file-id", "file_size": len(api.content)},
				},
			},
		})
	}))
	t.Cleanup(api.server.Close)
	return api
}

func newTestProvider(t *testing.T, api *fakeAPI, cfg TelegramConfig) *Telegram {
	t.Helper()
	cfg.Token = testToken
	cfg.ChatID = testChatID
	cfg.APIBase = api.server.URL
	provider, err := NewTelegram(cfg)
	if err != nil {
		t.Fatalf("创建提供方失败: %v", err)
	}
	return provider
}

func TestTelegramSendPhoto(t *testing.T) {
	api := newFakeAPI(t)
	provider := newTestProvider(t, api, TelegramConfig{})

	content := []byte("fake-photo-payload")
	result, err := provider.Upload(context.Background(), UploadRequest{
		FileName: "img.jpg",
		Mode:     ModePhoto,
		Size:     int64(len(content)),
		Caption:  "第一张",
		Reader:   strings.NewReader(string(content)),
	})
	if err != nil {
		t.Fatalf("上传失败: %v", err)
	}

	if api.method != "sendPhoto" {
		t.Errorf("请求方法 = %q，期望 sendPhoto", api.method)
	}
	if api.chatID != testChatID {
		t.Errorf("chat_id = %q", api.chatID)
	}
	if api.caption != "第一张" {
		t.Errorf("caption = %q", api.caption)
	}
	if api.fileName != "img.jpg" || string(api.content) != string(content) {
		t.Errorf("文件字段不符: name=%q size=%d", api.fileName, len(api.content))
	}
	if result.FileID != "large-file-id" || result.MessageID != 11 || result.Provider != "telegram" {
		t.Errorf("返回结果不符: %+v", result)
	}
}

func TestTelegramSendDocument(t *testing.T) {
	api := newFakeAPI(t)
	provider := newTestProvider(t, api, TelegramConfig{})

	content := []byte("original-bytes")
	result, err := provider.Upload(context.Background(), UploadRequest{
		FileName: "raw.jpg",
		Mode:     ModeDocument,
		Reader:   strings.NewReader(string(content)),
	})
	if err != nil {
		t.Fatalf("上传失败: %v", err)
	}
	if api.method != "sendDocument" {
		t.Errorf("请求方法 = %q，期望 sendDocument", api.method)
	}
	if result.FileID != "doc-file-id" || result.MIMEType != "image/jpeg" {
		t.Errorf("返回结果不符: %+v", result)
	}
	if result.FileSize != int64(len(content)) {
		t.Errorf("文件大小 = %d，期望 %d", result.FileSize, len(content))
	}
}

func TestTelegramRejectsKnownOversizeBeforeRequest(t *testing.T) {
	api := newFakeAPI(t)
	provider := newTestProvider(t, api, TelegramConfig{PhotoMaxBytes: 1024})

	_, err := provider.Upload(context.Background(), UploadRequest{
		Mode:   ModePhoto,
		Size:   2048,
		Reader: strings.NewReader("x"),
	})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("错误 = %v，期望 ErrTooLarge", err)
	}
	if api.requests.Load() != 0 {
		t.Errorf("超限时不应发起请求，实际 %d 次", api.requests.Load())
	}
}

func TestTelegramRejectsStreamOversize(t *testing.T) {
	api := newFakeAPI(t)
	provider := newTestProvider(t, api, TelegramConfig{DocumentMaxBytes: 1024})

	_, err := provider.Upload(context.Background(), UploadRequest{
		Mode:   ModeDocument,
		Reader: strings.NewReader(strings.Repeat("x", 4096)),
	})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("错误 = %v，期望 ErrTooLarge", err)
	}
}

func TestTelegramCancellation(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	defer close(release)

	provider, err := NewTelegram(TelegramConfig{
		Token:   testToken,
		ChatID:  testChatID,
		APIBase: server.URL,
	})
	if err != nil {
		t.Fatalf("创建提供方失败: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()

	started := time.Now()
	_, err = provider.Upload(ctx, UploadRequest{
		Mode:   ModePhoto,
		Reader: strings.NewReader(strings.Repeat("x", 64)),
	})
	if err == nil {
		t.Fatal("取消后应返回错误")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Errorf("错误 = %v，期望上下文取消类错误", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Errorf("取消后应立即返回，实际耗时 %s", elapsed)
	}
}

func TestTelegramStreamsWithoutBuffering(t *testing.T) {
	api := newFakeAPI(t)
	provider := newTestProvider(t, api, TelegramConfig{DocumentMaxBytes: 8 << 20})

	tracker := &chunkTracker{data: make([]byte, 3<<20)}
	if _, err := provider.Upload(context.Background(), UploadRequest{
		Mode:   ModeDocument,
		Reader: tracker,
	}); err != nil {
		t.Fatalf("上传失败: %v", err)
	}
	if tracker.maxChunk > 1<<20 {
		t.Errorf("单次读取 = %d 字节，疑似整体缓冲", tracker.maxChunk)
	}
}

func TestTelegramConfigValidation(t *testing.T) {
	if _, err := NewTelegram(TelegramConfig{ChatID: testChatID}); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("缺少 Token 应返回 ErrNotConfigured，实际 %v", err)
	}
	if _, err := NewTelegram(TelegramConfig{Token: testToken}); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("缺少频道应返回 ErrNotConfigured，实际 %v", err)
	}

	provider := newTestProvider(t, newFakeAPI(t), TelegramConfig{})
	if _, err := provider.Upload(context.Background(), UploadRequest{
		Mode:   "raw",
		Reader: strings.NewReader("x"),
	}); !errors.Is(err, ErrUnsupportedMode) {
		t.Errorf("未知模式应返回 ErrUnsupportedMode，实际 %v", err)
	}
}

func newErrorServer(t *testing.T, status int, body string) *Telegram {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)

	provider, err := NewTelegram(TelegramConfig{Token: testToken, ChatID: testChatID, APIBase: server.URL})
	if err != nil {
		t.Fatalf("创建提供方失败: %v", err)
	}
	return provider
}

func TestTelegramRateLimitedParsesRetryAfter(t *testing.T) {
	provider := newErrorServer(t, http.StatusTooManyRequests,
		`{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 37","parameters":{"retry_after":37}}`)

	_, err := provider.Upload(context.Background(), UploadRequest{Mode: ModePhoto, Reader: strings.NewReader("x")})
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("错误 = %v，期望 ErrRateLimited", err)
	}

	var limited *RateLimitError
	if !errors.As(err, &limited) {
		t.Fatalf("错误应可提取为 RateLimitError，实际 %T", err)
	}
	if limited.RetryAfter != 37*time.Second {
		t.Errorf("RetryAfter = %s，期望 37s", limited.RetryAfter)
	}
}

func TestTelegramRateLimitedWithoutParameters(t *testing.T) {
	provider := newErrorServer(t, http.StatusTooManyRequests, `{"ok":false,"error_code":429}`)

	_, err := provider.Upload(context.Background(), UploadRequest{Mode: ModePhoto, Reader: strings.NewReader("x")})
	var limited *RateLimitError
	if !errors.As(err, &limited) {
		t.Fatalf("错误应可提取为 RateLimitError，实际 %v", err)
	}
	if limited.RetryAfter != 0 {
		t.Errorf("RetryAfter = %s，期望 0", limited.RetryAfter)
	}
}

func TestTelegramErrorsDoNotLeakCredentials(t *testing.T) {
	cases := map[string]string{
		"上游说明含令牌":   `{"ok":false,"error_code":400,"description":"bad request to /bot` + testToken + `/sendPhoto","parameters":{}}`,
		"上游说明含文件地址": `{"ok":false,"error_code":404,"description":"https://api.telegram.org/file/bot` + testToken + `/photos/1.jpg"}`,
		"非法 JSON":   `not-json`,
	}
	for name, body := range cases {
		provider := newErrorServer(t, http.StatusInternalServerError, body)
		_, err := provider.Upload(context.Background(), UploadRequest{Mode: ModePhoto, Reader: strings.NewReader("x")})
		if err == nil {
			t.Fatalf("%s：应返回错误", name)
		}
		message := err.Error()
		for _, secret := range []string{testToken, "api.telegram.org/file", testChatID} {
			if strings.Contains(message, secret) {
				t.Errorf("%s：错误信息泄露 %q：%s", name, secret, message)
			}
		}
	}
}

func TestTelegramSuccessResultHasNoURL(t *testing.T) {
	api := newFakeAPI(t)
	provider := newTestProvider(t, api, TelegramConfig{})

	result, err := provider.Upload(context.Background(), UploadRequest{Mode: ModePhoto, Reader: strings.NewReader("x")})
	if err != nil {
		t.Fatalf("上传失败: %v", err)
	}
	if strings.Contains(result.FileID, "http") || strings.Contains(result.FileID, testToken) {
		t.Errorf("远端标识不应包含地址或令牌: %q", result.FileID)
	}
}
