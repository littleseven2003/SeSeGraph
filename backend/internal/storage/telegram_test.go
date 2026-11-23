package storage

import (
	"bytes"
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

		if strings.Contains(api.method, "sendVideo") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"result": map[string]any{
					"message_id": 33,
					"video": map[string]any{
						"file_id":   "video-file-id",
						"file_size": len(api.content),
						"mime_type": "video/mp4",
					},
				},
			})
			return
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

// TestTelegramKeepsTimeoutForInjectedClient 验证注入传输层时不会丢掉请求超时：
// 未指定超时沿用默认值，显式指定时尊重调用方取值。
func TestTelegramKeepsTimeoutForInjectedClient(t *testing.T) {
	provider, err := NewTelegram(TelegramConfig{
		Token:      testToken,
		ChatID:     testChatID,
		HTTPClient: &http.Client{Transport: &http.Transport{}},
	})
	if err != nil {
		t.Fatalf("创建提供方失败: %v", err)
	}
	if provider.client.Timeout != defaultTimeout {
		t.Errorf("超时 = %v，期望沿用默认值 %v", provider.client.Timeout, defaultTimeout)
	}

	custom := 5 * time.Second
	provider, err = NewTelegram(TelegramConfig{
		Token:      testToken,
		ChatID:     testChatID,
		HTTPClient: &http.Client{Timeout: custom},
	})
	if err != nil {
		t.Fatalf("创建提供方失败: %v", err)
	}
	if provider.client.Timeout != custom {
		t.Errorf("超时 = %v，期望保留调用方取值 %v", provider.client.Timeout, custom)
	}
}

// TestTelegramUploadTimesOutOnHangingUpstream 验证上游不响应时请求会超时返回，
// 不会让上传队列或保存流程无限等待。
func TestTelegramUploadTimesOutOnHangingUpstream(t *testing.T) {
	// 模拟上游读完请求后长时间不返回响应。
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		time.Sleep(2 * time.Second)
	}))
	t.Cleanup(server.Close)

	provider, err := NewTelegram(TelegramConfig{
		Token:      testToken,
		ChatID:     testChatID,
		APIBase:    server.URL,
		HTTPClient: &http.Client{Timeout: 150 * time.Millisecond},
	})
	if err != nil {
		t.Fatalf("创建提供方失败: %v", err)
	}

	started := time.Now()
	_, err = provider.Upload(context.Background(), UploadRequest{
		FileName: "photo.jpg",
		MIMEType: "image/jpeg",
		Size:     7,
		Mode:     ModePhoto,
		Reader:   bytes.NewReader([]byte("payload")),
	})
	if err == nil {
		t.Fatal("上游不响应时应返回错误")
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Errorf("耗时 = %v，期望在超时后尽快返回", elapsed)
	}
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

func TestTelegramDoesNotFollowRedirects(t *testing.T) {
	var followed atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		followed.Add(1)
		_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":1}}`)
	}))
	defer target.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/stolen", http.StatusFound)
	}))
	defer redirector.Close()

	provider, err := NewTelegram(TelegramConfig{Token: testToken, ChatID: testChatID, APIBase: redirector.URL})
	if err != nil {
		t.Fatalf("创建提供方失败: %v", err)
	}

	_, err = provider.Upload(context.Background(), UploadRequest{Mode: ModePhoto, Reader: strings.NewReader("x")})
	if err == nil {
		t.Fatal("重定向响应不应被视为成功")
	}
	if followed.Load() != 0 {
		t.Errorf("不应跟随重定向，目标被访问 %d 次", followed.Load())
	}
	if strings.Contains(err.Error(), testToken) {
		t.Errorf("错误信息泄露令牌: %s", err.Error())
	}
}

func TestTelegramTransportErrorIsSanitized(t *testing.T) {
	provider, err := NewTelegram(TelegramConfig{
		Token:   testToken,
		ChatID:  testChatID,
		APIBase: "http://127.0.0.1:1",
	})
	if err != nil {
		t.Fatalf("创建提供方失败: %v", err)
	}

	_, err = provider.Upload(context.Background(), UploadRequest{Mode: ModePhoto, Reader: strings.NewReader("x")})
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("错误 = %v，期望 ErrUpstream", err)
	}
	message := err.Error()
	for _, secret := range []string{testToken, "127.0.0.1", "http://", "https://", testChatID} {
		if strings.Contains(message, secret) {
			t.Errorf("错误信息泄露 %q: %s", secret, message)
		}
	}
}

func TestTelegramEarlyRateLimitIsPreserved(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 不读取请求体，直接返回限流响应，模拟上游提前拒绝。
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"ok":false,"error_code":429,"parameters":{"retry_after":37}}`)
	}))
	defer server.Close()

	provider, err := NewTelegram(TelegramConfig{Token: testToken, ChatID: testChatID, APIBase: server.URL})
	if err != nil {
		t.Fatalf("创建提供方失败: %v", err)
	}

	payload := strings.Repeat("x", 512<<10)
	_, err = provider.Upload(context.Background(), UploadRequest{Mode: ModeDocument, Reader: strings.NewReader(payload)})
	var limited *RateLimitError
	if !errors.As(err, &limited) {
		t.Fatalf("错误 = %v，期望保留 RateLimitError", err)
	}
	if limited.RetryAfter != 37*time.Second {
		t.Errorf("RetryAfter = %s，期望 37s", limited.RetryAfter)
	}
}

type shortReader struct {
	data []byte
	sent bool
}

func (s *shortReader) Read(p []byte) (int, error) {
	if s.sent {
		return 0, errors.New("读取源中断")
	}
	s.sent = true
	n := copy(p, s.data)
	return n, nil
}

func TestTelegramWaitsForPipeCompletion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 只读少量请求体就返回成功，模拟上游提前确认。
		buf := make([]byte, 8)
		_, _ = r.Body.Read(buf)
		_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":1,"photo":[{"file_id":"id-1"}]}}`)
	}))
	defer server.Close()

	provider, err := NewTelegram(TelegramConfig{Token: testToken, ChatID: testChatID, APIBase: server.URL})
	if err != nil {
		t.Fatalf("创建提供方失败: %v", err)
	}

	_, err = provider.Upload(context.Background(), UploadRequest{
		Mode:   ModePhoto,
		Reader: &shortReader{data: []byte("0123456789")},
	})
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("错误 = %v，期望 ErrUpstream（管道未完整写出）", err)
	}
}

// TestVerifyFileReportsOwnership 验证旧图归属校验：同一个 Bot 可读，
// 其他 Bot 的标识按文件不可用处理，且不下载文件内容。
func TestVerifyFileReportsOwnership(t *testing.T) {
	downloads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fileID := r.URL.Query().Get("file_id")
		if fileID == "other-bot-file" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error_code": 400, "description": "invalid file_id"})
			return
		}
		if strings.HasPrefix(r.URL.Path, "/file/") {
			downloads++
			_, _ = w.Write([]byte("payload"))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":     true,
			"result": map[string]any{"file_path": "photos/legacy.png", "file_size": 7},
		})
	}))
	t.Cleanup(server.Close)

	provider, err := NewTelegram(TelegramConfig{Token: testToken, ChatID: testChatID, APIBase: server.URL})
	if err != nil {
		t.Fatalf("创建提供方失败: %v", err)
	}
	if err := provider.VerifyFile(context.Background(), "own-file"); err != nil {
		t.Fatalf("自己的文件应可读: %v", err)
	}
	if err := provider.VerifyFile(context.Background(), "other-bot-file"); !errors.Is(err, ErrFileUnavailable) {
		t.Fatalf("错误 = %v，期望 ErrFileUnavailable", err)
	}
	if err := provider.VerifyFile(context.Background(), ""); !errors.Is(err, ErrFileUnavailable) {
		t.Fatalf("空标识错误 = %v，期望 ErrFileUnavailable", err)
	}
	if downloads != 0 {
		t.Errorf("校验不应下载文件内容，下载次数 = %d", downloads)
	}
}

// TestTelegramSendMessage 验证纯文本通知的请求形态与错误映射。
func TestTelegramSendMessage(t *testing.T) {
	type received struct {
		chat string
		text string
	}
	calls := make([]received, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/sendMessage") {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("解析表单失败: %v", err)
		}
		calls = append(calls, received{chat: r.PostForm.Get("chat_id"), text: r.PostForm.Get("text")})
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.PostForm.Get("text"), "失败样本") {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error_code": 403})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{"message_id": 9}})
	}))
	t.Cleanup(server.Close)

	provider, err := NewTelegram(TelegramConfig{Token: testToken, ChatID: testChatID, APIBase: server.URL})
	if err != nil {
		t.Fatalf("创建提供方失败: %v", err)
	}
	if err := provider.SendMessage(context.Background(), "流量提醒"); err != nil {
		t.Fatalf("发送失败: %v", err)
	}
	if len(calls) != 1 || calls[0].chat != testChatID || calls[0].text != "流量提醒" {
		t.Fatalf("请求内容 = %+v，期望带上频道与文本", calls)
	}

	if err := provider.SendMessage(context.Background(), "失败样本"); !errors.Is(err, ErrUpstream) {
		t.Fatalf("错误 = %v，期望 ErrUpstream", err)
	}
	if err := provider.SendMessage(context.Background(), "  "); err == nil {
		t.Error("空内容应报错")
	}
}

// TestTelegramChatInfoPrivacy 验证 getChat 结果能区分公开与私有会话。
func TestTelegramChatInfoPrivacy(t *testing.T) {
	samples := []struct {
		name    string
		result  map[string]any
		want    string
		private bool
	}{
		{"私有频道", map[string]any{"id": -1001, "type": "channel", "title": "备份"}, "channel", true},
		{"公开频道", map[string]any{"id": -1002, "type": "channel", "username": "sese_backup"}, "channel", false},
		{"公开别名", map[string]any{"id": -1005, "type": "channel", "active_usernames": []string{"sese_alias"}}, "channel", false},
		{"无用户名的群", map[string]any{"id": -1006, "type": "group", "title": "运维"}, "group", true},
		{"私有群", map[string]any{"id": -1003, "type": "supergroup", "title": "运维"}, "supergroup", true},
		{"公开群", map[string]any{"id": -1004, "type": "supergroup", "username": "sese_ops"}, "supergroup", false},
		{"私聊", map[string]any{"id": 77, "type": "private"}, "private", true},
	}
	for _, sample := range samples {
		t.Run(sample.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.URL.Path, "/getChat") {
					http.NotFound(w, r)
					return
				}
				if err := r.ParseForm(); err != nil {
					t.Errorf("解析表单失败: %v", err)
				}
				if got := r.PostForm.Get("chat_id"); got != testChatID {
					t.Errorf("chat_id = %q，期望 %q", got, testChatID)
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": sample.result})
			}))
			t.Cleanup(server.Close)

			provider, err := NewTelegram(TelegramConfig{Token: testToken, ChatID: testChatID, APIBase: server.URL})
			if err != nil {
				t.Fatalf("创建提供方失败: %v", err)
			}
			info, err := provider.ChatInfo(context.Background())
			if err != nil {
				t.Fatalf("读取会话属性失败: %v", err)
			}
			if info.Type != sample.want {
				t.Errorf("类型 = %q，期望 %q", info.Type, sample.want)
			}
			if info.Private() != sample.private {
				t.Errorf("Private() = %v，期望 %v", info.Private(), sample.private)
			}
		})
	}
}

// TestTelegramChatInfoFailures 验证上游失败与空类型都按错误处理，不会误判为私有。
func TestTelegramChatInfoFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/getChat") && r.URL.Query().Get("empty") == "" {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{}})
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error_code": 403})
	}))
	t.Cleanup(server.Close)

	provider, err := NewTelegram(TelegramConfig{Token: testToken, ChatID: testChatID, APIBase: server.URL})
	if err != nil {
		t.Fatalf("创建提供方失败: %v", err)
	}
	if _, err := provider.ChatInfo(context.Background()); !errors.Is(err, ErrUpstream) {
		t.Errorf("空类型错误 = %v，期望 ErrUpstream", err)
	}

	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error_code": 403})
	}))
	t.Cleanup(failing.Close)
	denied, err := NewTelegram(TelegramConfig{Token: testToken, ChatID: testChatID, APIBase: failing.URL})
	if err != nil {
		t.Fatalf("创建提供方失败: %v", err)
	}
	if _, err := denied.ChatInfo(context.Background()); !errors.Is(err, ErrUpstream) {
		t.Errorf("上游拒绝错误 = %v，期望 ErrUpstream", err)
	}
}

func TestTelegramSendVideo(t *testing.T) {
	api := newFakeAPI(t)
	provider := newTestProvider(t, api, TelegramConfig{})

	content := []byte("fake-mp4-payload")
	result, err := provider.Upload(context.Background(), UploadRequest{
		FileName: "clip.mp4",
		MIMEType: "video/mp4",
		Mode:     ModeVideo,
		Size:     int64(len(content)),
		Reader:   strings.NewReader(string(content)),
	})
	if err != nil {
		t.Fatalf("上传失败: %v", err)
	}
	if api.method != "sendVideo" {
		t.Errorf("请求方法 = %q，期望 sendVideo", api.method)
	}
	if api.fileName != "clip.mp4" || string(api.content) != string(content) {
		t.Errorf("文件字段不符: name=%q size=%d", api.fileName, len(api.content))
	}
	if result.FileID != "video-file-id" || result.MIMEType != "video/mp4" || result.MessageID != 33 {
		t.Errorf("返回结果不符: %+v", result)
	}
}

func TestTelegramRejectsVideoOverTwentyMegabytes(t *testing.T) {
	api := newFakeAPI(t)
	provider := newTestProvider(t, api, TelegramConfig{})

	_, err := provider.Upload(context.Background(), UploadRequest{
		FileName: "big.mp4",
		MIMEType: "video/mp4",
		Mode:     ModeVideo,
		Size:     MaxVideoBytes + 1,
		Reader:   strings.NewReader("payload"),
	})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("错误 = %v，期望 ErrTooLarge", err)
	}
	if api.requests.Load() != 0 {
		t.Errorf("超限时不应发起请求，实际 %d 次", api.requests.Load())
	}
}
