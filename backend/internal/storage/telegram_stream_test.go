package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeFileAPI 模拟 Telegram 的 getFile 与文件下载接口。
type fakeFileAPI struct {
	server *httptest.Server

	mu            sync.Mutex
	getFileCalls  int
	downloadCalls int
	path          string
	failDownload  bool
	failPath      string
	getFileStatus int
	getFileBody   string
	body          []byte
	contentType   string
	// rangeMode 控制下载接口对 Range 的处理：empty 忽略、honor 返回 206、bad 返回错位区间。
	rangeMode string
	lastRange string
}

func newFakeFileAPI(t *testing.T) *fakeFileAPI {
	t.Helper()
	api := &fakeFileAPI{
		path:        "photos/one.jpg",
		body:        []byte("image-bytes"),
		contentType: "image/jpeg",
	}
	api.server = httptest.NewServer(http.HandlerFunc(api.handle))
	t.Cleanup(api.server.Close)
	return api
}

func (f *fakeFileAPI) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if strings.Contains(r.URL.Path, "/getFile") {
		f.getFileCalls++
		if f.getFileStatus != 0 {
			w.WriteHeader(f.getFileStatus)
			_, _ = w.Write([]byte(f.getFileBody))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"result": map[string]any{
				"file_path": f.path,
				"file_size": len(f.body),
			},
		})
		return
	}

	f.downloadCalls++
	if f.failDownload || (f.failPath != "" && strings.HasSuffix(r.URL.Path, f.failPath)) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	f.lastRange = r.Header.Get("Range")
	if f.rangeMode == "honor" {
		start, end, ok := parseRangeForTest(r.Header.Get("Range"), int64(len(f.body)))
		if !ok {
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		w.Header().Set("Content-Type", f.contentType)
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(f.body)))
		w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(f.body[start : end+1])
		return
	}
	if f.rangeMode == "bad" {
		w.Header().Set("Content-Type", f.contentType)
		w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", len(f.body)-1, len(f.body)+5))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(f.body)
		return
	}
	w.Header().Set("Content-Type", f.contentType)
	_, _ = w.Write(f.body)
}

func (f *fakeFileAPI) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.getFileCalls, f.downloadCalls
}

func (f *fakeFileAPI) setPath(path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.path = path
}

func (f *fakeFileAPI) setFailDownload(fail bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failDownload = fail
}

func (f *fakeFileAPI) setFailPath(path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failPath = path
}

func (f *fakeFileAPI) setContentType(contentType string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.contentType = contentType
}

func (f *fakeFileAPI) setGetFileStatus(status int, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getFileStatus = status
	f.getFileBody = body
}

func newDownloadProvider(t *testing.T, api *fakeFileAPI, clock func() time.Time) *Telegram {
	t.Helper()
	provider, err := NewTelegram(TelegramConfig{
		Token:         "123:synthetic-token",
		ChatID:        "-100123",
		APIBase:       api.server.URL,
		PathTTL:       45 * time.Minute,
		PathCacheSize: 4,
		Now:           clock,
	})
	if err != nil {
		t.Fatalf("创建提供方失败: %v", err)
	}
	return provider
}

func TestOpenStreamCachesFilePath(t *testing.T) {
	api := newFakeFileAPI(t)
	provider := newDownloadProvider(t, api, time.Now)
	ctx := context.Background()

	for round := 0; round < 3; round++ {
		stream, err := provider.OpenStream(ctx, "file-1")
		if err != nil {
			t.Fatalf("第 %d 次打开流失败: %v", round+1, err)
		}
		data, err := io.ReadAll(stream.Body)
		if err != nil {
			t.Fatalf("读取失败: %v", err)
		}
		if err := stream.Body.Close(); err != nil {
			t.Fatalf("关闭失败: %v", err)
		}
		if string(data) != "image-bytes" {
			t.Fatalf("内容 = %q", data)
		}
		if stream.ContentType != "image/jpeg" {
			t.Errorf("Content-Type = %q", stream.ContentType)
		}
	}

	getFileCalls, downloadCalls := api.counts()
	if getFileCalls != 1 {
		t.Errorf("getFile 调用 %d 次，期望命中缓存后仍为 1 次", getFileCalls)
	}
	if downloadCalls != 3 {
		t.Errorf("下载调用 %d 次，期望 3 次", downloadCalls)
	}
}

func TestOpenStreamRefreshesExpiredPathOnce(t *testing.T) {
	api := newFakeFileAPI(t)
	api.setPath("photos/old.jpg")
	current := time.Date(2026, 11, 22, 3, 0, 0, 0, time.UTC)
	provider := newDownloadProvider(t, api, func() time.Time { return current })

	first, err := provider.OpenStream(context.Background(), "file-2")
	if err != nil {
		t.Fatalf("首次打开失败: %v", err)
	}
	_ = first.Body.Close()

	// 旧路径失效：下载返回 404，getFile 给出新路径后重试成功。
	api.setPath("photos/new.jpg")
	api.setFailPath("/photos/old.jpg")

	second, err := provider.OpenStream(context.Background(), "file-2")
	if err != nil {
		t.Fatalf("失效后重试失败: %v", err)
	}
	_ = second.Body.Close()

	getFileCalls, downloadCalls := api.counts()
	if getFileCalls != 2 {
		t.Errorf("getFile 调用 %d 次，期望 2 次（首次 + 刷新一次）", getFileCalls)
	}
	if downloadCalls != 3 {
		t.Errorf("下载调用 %d 次，期望 3 次（首次 + 失败 + 重试）", downloadCalls)
	}

	// 刷新后缓存的是新路径，再取一次不应再查 getFile。
	api.setFailPath("")
	third, err := provider.OpenStream(context.Background(), "file-2")
	if err != nil {
		t.Fatalf("刷新后打开失败: %v", err)
	}
	_ = third.Body.Close()
	if calls, _ := api.counts(); calls != 2 {
		t.Errorf("刷新后 getFile 调用 %d 次，期望仍为 2 次", calls)
	}
}

func TestOpenStreamStopsAfterSecondNotFound(t *testing.T) {
	api := newFakeFileAPI(t)
	api.setFailDownload(true)
	provider := newDownloadProvider(t, api, time.Now)

	stream, err := provider.OpenStream(context.Background(), "file-3")
	if !errors.Is(err, ErrFileUnavailable) {
		t.Fatalf("错误 = %v，期望 ErrFileUnavailable", err)
	}
	if stream.Body != nil {
		t.Error("失败时不应返回响应体")
	}

	getFileCalls, downloadCalls := api.counts()
	if getFileCalls != 2 {
		t.Errorf("getFile 调用 %d 次，期望只刷新一次共 2 次", getFileCalls)
	}
	if downloadCalls != 2 {
		t.Errorf("下载调用 %d 次，期望 2 次", downloadCalls)
	}
}

func TestOpenStreamHonoursCancelledContext(t *testing.T) {
	api := newFakeFileAPI(t)
	provider := newDownloadProvider(t, api, time.Now)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := provider.OpenStream(ctx, "file-4"); err == nil {
		t.Fatal("取消的上下文应返回错误")
	}
	if getFileCalls, downloadCalls := api.counts(); getFileCalls != 0 || downloadCalls != 0 {
		t.Errorf("取消后不应发起请求，getFile=%d download=%d", getFileCalls, downloadCalls)
	}
}

func TestOpenStreamCancelDuringReadClosesBody(t *testing.T) {
	started := make(chan struct{})
	serverClosed := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/getFile") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":     true,
				"result": map[string]any{"file_path": "photos/slow.jpg", "file_size": 10},
			})
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("x"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		close(started)
		<-r.Context().Done()
		serverClosed <- struct{}{}
	}))
	defer server.Close()

	provider, err := NewTelegram(TelegramConfig{Token: "123:fake", ChatID: "-100", APIBase: server.URL})
	if err != nil {
		t.Fatalf("创建提供方失败: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	stream, err := provider.OpenStream(ctx, "file-5")
	if err != nil {
		t.Fatalf("打开流失败: %v", err)
	}
	<-started

	buffer := make([]byte, 4)
	if _, err := stream.Body.Read(buffer); err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("首字节读取失败: %v", err)
	}
	cancel()

	if _, err := io.ReadAll(stream.Body); err == nil {
		t.Error("取消后读取应返回错误")
	}
	_ = stream.Body.Close()

	select {
	case <-serverClosed:
	case <-time.After(2 * time.Second):
		t.Fatal("关闭响应体后上游连接未结束")
	}
}

func TestOpenStreamCloseReleasesUpstream(t *testing.T) {
	done := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/getFile") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":     true,
				"result": map[string]any{"file_path": "photos/hold.jpg", "file_size": 100},
			})
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("partial"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
		done <- struct{}{}
	}))
	defer server.Close()

	provider, err := NewTelegram(TelegramConfig{Token: "123:fake", ChatID: "-100", APIBase: server.URL})
	if err != nil {
		t.Fatalf("创建提供方失败: %v", err)
	}

	stream, err := provider.OpenStream(context.Background(), "file-6")
	if err != nil {
		t.Fatalf("打开流失败: %v", err)
	}
	if err := stream.Body.Close(); err != nil {
		t.Fatalf("关闭响应体失败: %v", err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("关闭响应体后上游连接未释放")
	}
}

func TestOpenStreamErrorsAreDesensitized(t *testing.T) {
	api := newFakeFileAPI(t)
	api.setGetFileStatus(http.StatusInternalServerError, `{"ok":false,"description":"token 123:synthetic-token photos/secret.jpg"}`)
	provider := newDownloadProvider(t, api, time.Now)

	_, err := provider.OpenStream(context.Background(), "file-7")
	if err == nil {
		t.Fatal("上游失败应返回错误")
	}
	for _, secret := range []string{"123:synthetic-token", "photos/secret.jpg", api.server.URL, "secret"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("错误信息泄露了 %q: %v", secret, err)
		}
	}
	if !errors.Is(err, ErrUpstream) {
		t.Errorf("错误类别 = %v，期望 ErrUpstream", err)
	}
}

func TestOpenStreamDoesNotFollowRedirect(t *testing.T) {
	leaked := 0
	var leakMu sync.Mutex
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leakMu.Lock()
		leaked++
		leakMu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/getFile") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":     true,
				"result": map[string]any{"file_path": "photos/moved.jpg", "file_size": 10},
			})
			return
		}
		http.Redirect(w, r, target.URL+"/stolen", http.StatusFound)
	}))
	defer redirector.Close()

	provider, err := NewTelegram(TelegramConfig{Token: "123:fake", ChatID: "-100", APIBase: redirector.URL})
	if err != nil {
		t.Fatalf("创建提供方失败: %v", err)
	}

	if _, err := provider.OpenStream(context.Background(), "file-8"); err == nil {
		t.Fatal("重定向应被拒绝")
	}
	leakMu.Lock()
	defer leakMu.Unlock()
	if leaked != 0 {
		t.Errorf("重定向目标被访问 %d 次，期望 0 次", leaked)
	}
}

func TestPathCacheExpiresAndEvicts(t *testing.T) {
	current := time.Date(2026, 11, 22, 3, 0, 0, 0, time.UTC)
	cache := newPathCache(45*time.Minute, 2)
	cache.set("a", pathEntry{path: "a.jpg"}, current)
	cache.set("b", pathEntry{path: "b.jpg"}, current)

	if _, ok := cache.get("a", current.Add(44*time.Minute)); !ok {
		t.Error("未过期时应命中")
	}
	if _, ok := cache.get("a", current.Add(46*time.Minute)); ok {
		t.Error("过期后不应命中")
	}

	cache.set("c", pathEntry{path: "c.jpg"}, current)
	if _, ok := cache.get("a", current); ok {
		t.Error("容量超限时应淘汰最早过期的条目")
	}
	for _, key := range []string{"b", "c"} {
		if _, ok := cache.get(key, current); !ok {
			t.Errorf("条目 %q 不应被淘汰", key)
		}
	}
}

// TestOpenStreamInfersImageTypeFromPath 验证下载头是通用二进制流时按扩展名补出图片类型。
func TestOpenStreamInfersImageTypeFromPath(t *testing.T) {
	ctx := context.Background()
	api := newFakeFileAPI(t)
	provider := newDownloadProvider(t, api, time.Now)

	api.setContentType("application/octet-stream")
	stream, err := provider.OpenStream(ctx, "file-jpg")
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	_ = stream.Body.Close()
	if stream.ContentType != "image/jpeg" {
		t.Errorf("Content-Type = %q，期望按路径补出 image/jpeg", stream.ContentType)
	}

	// 上游明确给出非图片类型时保持原值，由网关决定如何回落。
	api.setContentType("text/html")
	html, err := provider.OpenStream(ctx, "file-html")
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	_ = html.Body.Close()
	if html.ContentType != "text/html" {
		t.Errorf("Content-Type = %q，期望保留上游的 text/html", html.ContentType)
	}
}

// parseRangeForTest 解析测试用到的单区间请求。
func parseRangeForTest(header string, size int64) (int64, int64, bool) {
	if !strings.HasPrefix(header, "bytes=") {
		return 0, 0, false
	}
	spec := strings.TrimPrefix(header, "bytes=")
	startText, endText, found := strings.Cut(spec, "-")
	if !found {
		return 0, 0, false
	}
	start, err := strconv.ParseInt(startText, 10, 64)
	if err != nil || start < 0 || start >= size {
		return 0, 0, false
	}
	end := size - 1
	if endText != "" {
		parsed, parseErr := strconv.ParseInt(endText, 10, 64)
		if parseErr != nil || parsed < start {
			return 0, 0, false
		}
		if parsed < end {
			end = parsed
		}
	}
	return start, end, true
}

func TestOpenStreamRangeHonorsUpstream206(t *testing.T) {
	api := newFakeFileAPI(t)
	api.body = []byte("0123456789")
	api.rangeMode = "honor"
	provider := newDownloadProvider(t, api, nil)

	stream, err := provider.OpenStreamRange(context.Background(), "file-1", ByteRange{Start: 2, Length: 4})
	if err != nil {
		t.Fatalf("区间读取失败: %v", err)
	}
	defer func() { _ = stream.Body.Close() }()
	got, err := io.ReadAll(stream.Body)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if string(got) != "2345" || stream.Start != 2 || stream.End != 5 || stream.Size != 10 {
		t.Errorf("区间结果不符: body=%q start=%d end=%d size=%d", got, stream.Start, stream.End, stream.Size)
	}
}

func TestOpenStreamRangeRejectsMismatched206(t *testing.T) {
	api := newFakeFileAPI(t)
	api.body = []byte("0123456789")
	api.rangeMode = "bad"
	provider := newDownloadProvider(t, api, nil)

	if _, err := provider.OpenStreamRange(context.Background(), "file-1", ByteRange{Start: 0, Length: 2}); !errors.Is(err, ErrUpstream) {
		t.Fatalf("错位区间错误 = %v，期望 ErrUpstream", err)
	}
}

func TestOpenStreamRangeFallsBackWhenUpstreamIgnoresRange(t *testing.T) {
	api := newFakeFileAPI(t)
	api.body = []byte("0123456789")
	provider := newDownloadProvider(t, api, nil)

	stream, err := provider.OpenStreamRange(context.Background(), "file-1", ByteRange{Start: 3, Length: 4})
	if err != nil {
		t.Fatalf("回退读取失败: %v", err)
	}
	defer func() { _ = stream.Body.Close() }()
	got, _ := io.ReadAll(stream.Body)
	if string(got) != "3456" {
		t.Errorf("回退正文 = %q，期望跳过前缀并限长", got)
	}
	if stream.Start != 3 || stream.End != 6 || stream.Size != 10 {
		t.Errorf("回退响应区间不符: start=%d end=%d size=%d", stream.Start, stream.End, stream.Size)
	}
}

func TestOpenStreamRangeRefreshesPathOn404(t *testing.T) {
	api := newFakeFileAPI(t)
	api.body = []byte("0123456789")
	api.rangeMode = "honor"
	api.failPath = "missing.jpg"
	provider := newDownloadProvider(t, api, nil)
	provider.cache.set("file-1", pathEntry{path: "stale/missing.jpg", size: int64(len(api.body)), expires: time.Now().Add(time.Hour)}, time.Now())

	stream, err := provider.OpenStreamRange(context.Background(), "file-1", ByteRange{Start: 1, Length: 2})
	if err != nil {
		t.Fatalf("刷新路径后读取失败: %v", err)
	}
	defer func() { _ = stream.Body.Close() }()
	got, _ := io.ReadAll(stream.Body)
	if string(got) != "12" {
		t.Errorf("刷新后正文 = %q", got)
	}
	if getCalls, downloadCalls := api.counts(); getCalls != 1 || downloadCalls != 2 {
		t.Errorf("调用次数 = getFile %d / download %d，期望 1 / 2", getCalls, downloadCalls)
	}
}
