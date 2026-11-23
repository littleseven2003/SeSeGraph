package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/littleseven2003/SeSeGraph/backend/internal/db"
	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
	"github.com/littleseven2003/SeSeGraph/backend/internal/storage"
	"github.com/littleseven2003/SeSeGraph/backend/internal/traffic"
)

// fakeStreamProvider 记录调用并允许测试控制打开行为。
type fakeStreamProvider struct {
	mu    sync.Mutex
	calls int
	open  func(ctx context.Context, fileID string) (storage.Stream, error)
}

func (f *fakeStreamProvider) Name() string { return "fake" }

func (f *fakeStreamProvider) Upload(context.Context, storage.UploadRequest) (storage.UploadResult, error) {
	return storage.UploadResult{}, nil
}

func (f *fakeStreamProvider) OpenStream(ctx context.Context, fileID string) (storage.Stream, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	if f.open == nil {
		return storage.Stream{Body: io.NopCloser(strings.NewReader("image-bytes")), ContentType: "image/jpeg", Size: 11}, nil
	}
	return f.open(ctx, fileID)
}

func (f *fakeStreamProvider) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// ctxReader 模拟与请求上下文绑定的上游流：数据读完后阻塞到上下文结束。
type ctxReader struct {
	ctx      context.Context
	chunk    []byte
	consumed bool
	closed   chan struct{}
	once     sync.Once
}

func (r *ctxReader) Read(p []byte) (int, error) {
	if !r.consumed {
		r.consumed = true
		return copy(p, r.chunk), nil
	}
	<-r.ctx.Done()
	return 0, r.ctx.Err()
}

func (r *ctxReader) Close() error {
	r.once.Do(func() { close(r.closed) })
	return nil
}

// gatewayEnv 提供带假图床的网关测试环境。
type gatewayEnv struct {
	server *httptest.Server
	repo   *posts.Repository
	handle *sql.DB
}

func newGatewayEnv(t *testing.T, concurrency int, streams storage.StreamProvider) *gatewayEnv {
	t.Helper()
	return newGatewayEnvOptions(t, concurrency, 0, 0, streams)
}

func newGatewayEnvOptions(t *testing.T, concurrency, queueDepth int, wait time.Duration, streams storage.StreamProvider) *gatewayEnv {
	t.Helper()
	handle, err := db.Open(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	if err := db.Migrate(context.Background(), handle); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}

	repository := posts.NewRepository(handle)
	router := NewRouter(Deps{
		Version:          "v0.3.0-test",
		Commit:           "abc1234",
		Posts:            repository,
		Streams:          streams,
		ImageConcurrency: concurrency,
		ImageQueueDepth:  queueDepth,
		ImageQueueWait:   wait,
		Logger:           log.New(io.Discard, "", 0),
	})
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return &gatewayEnv{server: server, repo: repository, handle: handle}
}

// publishPost 建一个已完成且图片已存图的帖子，返回短码。
// public 为真时开启公开标记，disabled 为真时同时禁用帖子。
func (e *gatewayEnv) publishPost(t *testing.T, shortCode, mime, mode string, public, disabled bool) string {
	t.Helper()
	ctx := context.Background()
	postID, err := e.repo.Create(ctx, posts.Post{Title: "网关测试", ImageCount: 1, UploadMode: mode})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	if _, err := e.repo.AttachImage(ctx, posts.Image{
		PostID: postID, SortOrder: 1, Status: posts.ImageStatusUploaded,
		MimeType: mime, FileSize: 11, TempFilePath: "tmp/x.part",
	}); err != nil {
		t.Fatalf("保存图片失败: %v", err)
	}
	images, err := e.repo.ListImages(ctx, postID)
	if err != nil || len(images) != 1 {
		t.Fatalf("读取图片失败: %v", err)
	}
	if err := e.repo.MarkImageStored(ctx, images[0].ID, "remote-"+shortCode, 1, "telegram"); err != nil {
		t.Fatalf("标记存图失败: %v", err)
	}
	code := images[0].ShortCode
	if shortCode != "" {
		if _, err := e.handle.ExecContext(ctx, `UPDATE post_images SET short_code = ? WHERE id = ?`, shortCode, images[0].ID); err != nil {
			t.Fatalf("写入短码失败: %v", err)
		}
		code = shortCode
	}
	if err := e.repo.MarkPostFinished(ctx, postID, posts.StatusCompleted, ""); err != nil {
		t.Fatalf("标记完成失败: %v", err)
	}
	if public {
		if err := e.repo.SetPublic(ctx, postID, true); err != nil {
			t.Fatalf("设置公开失败: %v", err)
		}
	}
	if disabled {
		if _, err := e.handle.ExecContext(ctx, `UPDATE posts SET is_disabled = 1 WHERE id = ?`, postID); err != nil {
			t.Fatalf("禁用帖子失败: %v", err)
		}
	}
	return code
}

func (e *gatewayEnv) pendingPost(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	postID, err := e.repo.Create(ctx, posts.Post{Title: "未完成", ImageCount: 1})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	if _, err := e.repo.AttachImage(ctx, posts.Image{
		PostID: postID, SortOrder: 1, Status: posts.StatusUploading, MimeType: "image/jpeg",
	}); err != nil {
		t.Fatalf("保存图片失败: %v", err)
	}
	images, err := e.repo.ListImages(ctx, postID)
	if err != nil || len(images) != 1 {
		t.Fatalf("读取图片失败: %v", err)
	}
	return images[0].ShortCode
}

// publicize 按短码把对应帖子标记为公开，模拟管理员确认发布。
func (e *gatewayEnv) publicize(t *testing.T, code string) {
	t.Helper()
	if _, err := e.handle.ExecContext(context.Background(),
		`UPDATE posts SET is_public = 1 WHERE id = (SELECT post_id FROM post_images WHERE short_code = ?)`, code); err != nil {
		t.Fatalf("设置公开失败: %v", err)
	}
}

func getImage(t *testing.T, url string) (*http.Response, []byte) {
	t.Helper()
	response, err := http.Get(url)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}
	return response, body
}

func TestGatewayRejectsBeforeUpstream(t *testing.T) {
	provider := &fakeStreamProvider{}
	env := newGatewayEnv(t, 4, provider)

	disabledCode := env.publishPost(t, "", "image/jpeg", "photo", true, true)
	pendingCode := env.pendingPost(t)

	cases := []struct {
		name string
		code string
	}{
		{"未知短码", "abcdefghijkl"},
		{"格式不合法", "short"},
		{"未完成帖子", pendingCode},
		{"禁用帖子", disabledCode},
	}

	for _, testCase := range cases {
		response, _ := getImage(t, env.server.URL+"/i/"+testCase.code)
		if response.StatusCode != http.StatusNotFound {
			t.Errorf("%s 状态码 = %d，期望 404", testCase.name, response.StatusCode)
		}
		if cache := response.Header.Get("Cache-Control"); cache != "no-store" {
			t.Errorf("%s 404 应禁止缓存，实际 %q", testCase.name, cache)
		}
	}
	if calls := provider.callCount(); calls != 0 {
		t.Errorf("以上请求不应访问上游，实际调用 %d 次", calls)
	}
}

func TestGatewayRejectsLegacyCompletedPostUntilPublic(t *testing.T) {
	provider := &fakeStreamProvider{}
	env := newGatewayEnv(t, 4, provider)
	// 旧库升级场景：已存图、帖已完成，但公开标记默认关闭。
	code := env.publishPost(t, "legacy1234", "image/jpeg", "photo", false, false)

	response, body := getImage(t, env.server.URL+"/i/"+code)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("未公开的旧任务状态码 = %d，期望 404，响应: %s", response.StatusCode, body)
	}
	if calls := provider.callCount(); calls != 0 {
		t.Errorf("未公开的旧任务不应访问上游，实际调用 %d 次", calls)
	}

	// 管理员确认后开启公开标记即可访问。
	env.publicize(t, code)
	response, _ = getImage(t, env.server.URL+"/i/"+code)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("开启公开后状态码 = %d，期望 200", response.StatusCode)
	}
}

func TestGatewayStreamsWithCacheHeaders(t *testing.T) {
	provider := &fakeStreamProvider{}
	env := newGatewayEnv(t, 4, provider)
	code := env.publishPost(t, "abc123XYZ0", "image/jpeg", "document", true, false)

	response, body := getImage(t, env.server.URL+"/i/"+code)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", response.StatusCode)
	}
	if string(body) != "image-bytes" {
		t.Errorf("响应体 = %q", body)
	}
	if cache := response.Header.Get("Cache-Control"); cache != imageCacheControl {
		t.Errorf("Cache-Control = %q", cache)
	}
	if disposition := response.Header.Get("Content-Disposition"); disposition != "inline" {
		t.Errorf("Content-Disposition = %q", disposition)
	}
}

func TestGatewayUsesModeSpecificMIME(t *testing.T) {
	provider := &fakeStreamProvider{}
	env := newGatewayEnv(t, 4, provider)
	documentCode := env.publishPost(t, "doc12345", "image/png", "document", true, false)
	photoCode := env.publishPost(t, "photo1234", "image/jpeg", "photo", true, false)

	documentResponse, _ := getImage(t, env.server.URL+"/i/"+documentCode)
	if got := documentResponse.Header.Get("Content-Type"); got != "image/png" {
		t.Errorf("document 模式 Content-Type = %q，期望入库嗅探值 image/png", got)
	}

	photoResponse, _ := getImage(t, env.server.URL+"/i/"+photoCode)
	if got := photoResponse.Header.Get("Content-Type"); got != "image/jpeg" {
		t.Errorf("photo 模式 Content-Type = %q，期望上游值 image/jpeg", got)
	}
}

func TestGatewayRejectsNonImageMIME(t *testing.T) {
	provider := &fakeStreamProvider{}
	provider.open = func(context.Context, string) (storage.Stream, error) {
		return storage.Stream{
			Body:        io.NopCloser(strings.NewReader("<html>not an image</html>")),
			ContentType: "text/html; charset=utf-8",
			Size:        0,
		}, nil
	}
	env := newGatewayEnv(t, 4, provider)
	photoCode := env.publishPost(t, "html12345", "image/jpeg", "photo", true, false)
	documentCode := env.publishPost(t, "docmime12", "text/html", "document", true, false)

	for _, testCase := range []struct {
		name string
		code string
	}{
		{"photo 模式上游返回 HTML", photoCode},
		{"document 模式入库类型为 HTML", documentCode},
	} {
		response, body := getImage(t, env.server.URL+"/i/"+testCase.code)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s 状态码 = %d，期望 200", testCase.name, response.StatusCode)
		}
		if got := response.Header.Get("Content-Type"); got != octetStream {
			t.Errorf("%s Content-Type = %q，期望 %q", testCase.name, got, octetStream)
		}
		if got := response.Header.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s 缺少 nosniff，实际 %q", testCase.name, got)
		}
		if string(body) != "<html>not an image</html>" {
			t.Errorf("%s 响应体被篡改: %q", testCase.name, body)
		}
	}
}

func TestGatewayStreamsFirstChunkImmediately(t *testing.T) {
	pipeReader, pipeWriter := io.Pipe()
	provider := &fakeStreamProvider{}
	provider.open = func(context.Context, string) (storage.Stream, error) {
		return storage.Stream{Body: struct {
			io.Reader
			io.Closer
		}{Reader: pipeReader, Closer: pipeReader}, ContentType: "image/webp", Size: 0}, nil
	}
	env := newGatewayEnv(t, 4, provider)
	code := env.publishPost(t, "slow12345", "image/webp", "photo", true, false)

	// 上游先停顿再吐出首段，验证网关不会攒满缓冲区才回传。
	go func() {
		time.Sleep(150 * time.Millisecond)
		_, _ = pipeWriter.Write([]byte("first-chunk"))
		time.Sleep(150 * time.Millisecond)
		_ = pipeWriter.Close()
	}()

	response, err := http.Get(env.server.URL + "/i/" + code)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer func() { _ = response.Body.Close() }()

	buffer := make([]byte, len("first-chunk"))
	done := make(chan error, 1)
	go func() {
		_, err := io.ReadFull(response.Body, buffer)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("首段读取失败: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("首段数据未及时到达")
	}
	if string(buffer) != "first-chunk" {
		t.Errorf("首段内容 = %q", buffer)
	}
}

func TestGatewayLimitsConcurrency(t *testing.T) {
	var mu sync.Mutex
	active := 0
	maxActive := 0
	started := make(chan struct{}, 4)
	release := make(chan struct{})
	provider := &fakeStreamProvider{}
	provider.open = func(ctx context.Context, _ string) (storage.Stream, error) {
		mu.Lock()
		active++
		if active > maxActive {
			maxActive = active
		}
		mu.Unlock()
		started <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
		}
		mu.Lock()
		active--
		mu.Unlock()
		return storage.Stream{Body: io.NopCloser(strings.NewReader("ok")), ContentType: "image/jpeg", Size: 2}, nil
	}
	env := newGatewayEnv(t, 2, provider)
	code := env.publishPost(t, "conc12345", "image/jpeg", "photo", true, false)

	results := make(chan int, 2)
	for index := 0; index < 2; index++ {
		go func() {
			response, err := http.Get(env.server.URL + "/i/" + code)
			if err != nil {
				results <- -1
				return
			}
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			results <- response.StatusCode
		}()
	}
	for index := 0; index < 2; index++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("并发请求未及时进入上游")
		}
	}

	// 名额已满：第三个请求进入排队，不会立刻 503，也不会提前占用上游。
	queued := make(chan int, 1)
	go func() {
		response, err := http.Get(env.server.URL + "/i/" + code)
		if err != nil {
			queued <- -1
			return
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		queued <- response.StatusCode
	}()
	select {
	case <-started:
		t.Fatal("排队中的请求不应提前进入上游")
	case <-time.After(150 * time.Millisecond):
	}

	close(release)
	for index := 0; index < 2; index++ {
		select {
		case status := <-results:
			if status != http.StatusOK {
				t.Errorf("状态码 = %d，期望 200", status)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("请求未完成")
		}
	}
	select {
	case status := <-queued:
		if status != http.StatusOK {
			t.Errorf("排队请求状态码 = %d，期望 200", status)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("排队请求未完成")
	}
	mu.Lock()
	observed := maxActive
	mu.Unlock()
	if observed > 2 {
		t.Errorf("上游最大并发 = %d，超过上限 2", observed)
	}

	// 名额释放后新请求可以正常通过。
	after, _ := getImage(t, env.server.URL+"/i/"+code)
	if after.StatusCode != http.StatusOK {
		t.Errorf("释放后状态码 = %d，期望 200", after.StatusCode)
	}
}

func TestGatewayClosesUpstreamOnDisconnect(t *testing.T) {
	closed := make(chan struct{})
	var opened int
	var openMu sync.Mutex
	provider := &fakeStreamProvider{}
	provider.open = func(ctx context.Context, _ string) (storage.Stream, error) {
		openMu.Lock()
		opened++
		first := opened == 1
		openMu.Unlock()
		if !first {
			// 断连后的复查请求直接返回完整响应，避免再次阻塞。
			return storage.Stream{Body: io.NopCloser(strings.NewReader("again")), ContentType: "image/jpeg", Size: 5}, nil
		}
		return storage.Stream{Body: &ctxReader{ctx: ctx, chunk: []byte("first"), closed: closed}, ContentType: "image/jpeg", Size: 0}, nil
	}
	env := newGatewayEnv(t, 2, provider)
	code := env.publishPost(t, "drop12345", "image/jpeg", "photo", true, false)

	ctx, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, env.server.URL+"/i/"+code, nil)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer func() { _ = response.Body.Close() }()

	buffer := make([]byte, 5)
	if _, err := io.ReadFull(response.Body, buffer); err != nil {
		t.Fatalf("读取首段失败: %v", err)
	}
	cancel()

	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("客户端断开后上游流未关闭")
	}

	// 断连后名额应被释放：后续请求能立刻拿到 200。
	deadline := time.Now().Add(2 * time.Second)
	for {
		recheck, body := getImage(t, env.server.URL+"/i/"+code)
		if recheck.StatusCode == http.StatusOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("断连后名额未释放，状态码 %d，响应: %s", recheck.StatusCode, body)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestGatewayUpstreamErrorsAreDesensitized(t *testing.T) {
	provider := &fakeStreamProvider{}
	provider.open = func(context.Context, string) (storage.Stream, error) {
		return storage.Stream{}, storage.ErrUpstream
	}
	env := newGatewayEnv(t, 2, provider)
	code := env.publishPost(t, "err123456", "image/jpeg", "photo", true, false)

	response, body := getImage(t, env.server.URL+"/i/"+code)
	if response.StatusCode != http.StatusBadGateway {
		t.Fatalf("状态码 = %d，期望 502", response.StatusCode)
	}
	for _, secret := range []string{"remote-err123456", "telegram", "api.telegram.org", "token"} {
		if strings.Contains(string(body), secret) {
			t.Errorf("错误响应泄露了 %q: %s", secret, body)
		}
	}
}

func TestGatewayReportsUnavailableWhenProviderMissing(t *testing.T) {
	env := newGatewayEnv(t, 4, nil)
	code := env.publishPost(t, "none12345", "image/jpeg", "photo", true, false)

	response, _ := getImage(t, env.server.URL+"/i/"+code)
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("状态码 = %d，期望 503", response.StatusCode)
	}
}

// 保证 gin 在测试里使用测试模式，避免输出噪音。
func init() {
	gin.SetMode(gin.TestMode)
}

// TestGatewayUnavailableWhileProviderNotReady 验证运行期图床切走后网关返回 503，
// 与未配置状态对外表现一致，且不缓存失败结果。
func TestGatewayUnavailableWhileProviderNotReady(t *testing.T) {
	env := newGatewayEnv(t, 4, storage.NewSwitcher())
	code := env.publishPost(t, "notready01", "image/jpeg", "document", true, false)

	response, _ := getImage(t, env.server.URL+"/i/"+code)
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("状态码 = %d，期望 503", response.StatusCode)
	}
	if cache := response.Header.Get("Cache-Control"); cache != "no-store" {
		t.Errorf("Cache-Control = %q，期望 no-store", cache)
	}
}

// TestGatewayCountsTraffic 验证成功转发后按实际字节数计入流量统计。
func TestGatewayCountsTraffic(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)
	postID := publishedPost(t, env)
	code := shortCodeOf(t, env, postID)

	meter := traffic.New(nil, traffic.Options{})
	env.router = NewRouter(Deps{
		Version: "test", Commit: "abc", Auth: env.auth, Sessions: env.sessions,
		SetupToken: env.setupToken, Logger: env.logger, DataDir: env.dataDir, Posts: env.posts,
		Streams: &fakeStreamProvider{}, Traffic: meter,
	})

	if rec := env.do(t, http.MethodGet, "/i/"+code, "", cookie); rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", rec.Code)
	}
	stats, err := meter.Stats(context.Background())
	if err != nil {
		t.Fatalf("读取统计失败: %v", err)
	}
	if stats.BytesOut != int64(len("image-bytes")) || stats.RequestCount != 1 {
		t.Errorf("统计 = %+v，期望按实际写出字节计数", stats)
	}
}

// TestGatewayStopsAtMonthlyLimit 验证月度硬上限被触发后不再向 Telegram 取流。
func TestGatewayStopsAtMonthlyLimit(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)
	postID := publishedPost(t, env)
	code := shortCodeOf(t, env, postID)

	streams := &fakeStreamProvider{}
	meter := traffic.New(nil, traffic.Options{LimitBytes: 1})
	meter.Add(context.Background(), 2) // 已超过 1 字节上限
	env.router = NewRouter(Deps{
		Version: "test", Commit: "abc", Auth: env.auth, Sessions: env.sessions,
		SetupToken: env.setupToken, Logger: env.logger, DataDir: env.dataDir, Posts: env.posts,
		Streams: streams, Traffic: meter,
	})

	rec := env.do(t, http.MethodGet, "/i/"+code, "", cookie)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("状态码 = %d，期望 503", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "本月流量已超限") {
		t.Errorf("响应 = %s，期望说明流量超限", rec.Body.String())
	}
	if streams.callCount() != 0 {
		t.Errorf("超限时不应向上游取流，调用 = %d", streams.callCount())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("熔断响应不应被缓存: %s", rec.Header().Get("Cache-Control"))
	}
}

// gatedReader 先返回第一块数据，随后阻塞到测试放行，便于在传输中途观察熔断。
type gatedReader struct {
	chunks [][]byte
	gate   chan struct{}
	index  int
}

func (r *gatedReader) Read(p []byte) (int, error) {
	if r.index == 1 {
		<-r.gate
	}
	if r.index >= len(r.chunks) {
		return 0, io.EOF
	}
	written := copy(p, r.chunks[r.index])
	r.index++
	return written, nil
}

func (r *gatedReader) Close() error { return nil }

// waitUntil 轮询等待条件成立，超时判定失败。
func waitUntil(t *testing.T, cond func() bool, message string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("等待超时：%s", message)
}

// TestGatewayRejectsNewRequestsWhileStreamingAtLimit 验证传输中已越上限时，
// 新请求被拒绝且不再向上游取流，正在传输的连接照常完成。
func TestGatewayRejectsNewRequestsWhileStreamingAtLimit(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	postID := publishedPost(t, env)
	code := shortCodeOf(t, env, postID)

	gate := make(chan struct{})
	streams := &fakeStreamProvider{}
	streams.open = func(context.Context, string) (storage.Stream, error) {
		body := &gatedReader{chunks: [][]byte{[]byte(strings.Repeat("a", 1200)), []byte("tail")}, gate: gate}
		return storage.Stream{Body: body, ContentType: "image/jpeg"}, nil
	}
	meter := traffic.New(nil, traffic.Options{LimitBytes: 1000})
	env.router = NewRouter(Deps{
		Version: "test", Commit: "abc", Auth: env.auth, Sessions: env.sessions,
		SetupToken: env.setupToken, Logger: env.logger, DataDir: env.dataDir, Posts: env.posts,
		Streams: streams, Traffic: meter,
	})

	server := httptest.NewServer(env.router)
	defer server.Close()

	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		response, err := http.Get(server.URL + "/i/" + code)
		if err != nil {
			return
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
	}()

	waitUntil(t, meter.OverLimit, "第一块数据写出后应计入流量并越过上限")

	second, err := http.Get(server.URL + "/i/" + code)
	if err != nil {
		t.Fatalf("第二个请求失败: %v", err)
	}
	defer func() { _ = second.Body.Close() }()
	if second.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("状态码 = %d，期望 503", second.StatusCode)
	}
	if streams.callCount() != 1 {
		t.Errorf("越过上限后不应再取流，上游调用 = %d", streams.callCount())
	}

	close(gate)
	<-firstDone
}

// TestGatewayFallsBackToStoredMIME 验证上游只给通用二进制流时，回落到入库探测的图片类型。
func TestGatewayFallsBackToStoredMIME(t *testing.T) {
	provider := &fakeStreamProvider{}
	provider.open = func(context.Context, string) (storage.Stream, error) {
		return storage.Stream{
			Body:        io.NopCloser(strings.NewReader("image-bytes")),
			ContentType: "application/octet-stream",
			Size:        11,
		}, nil
	}
	env := newGatewayEnv(t, 4, provider)
	code := env.publishPost(t, "octphoto01", "image/png", "photo", true, false)

	response, _ := getImage(t, env.server.URL+"/i/"+code)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", response.StatusCode)
	}
	if got := response.Header.Get("Content-Type"); got != "image/png" {
		t.Errorf("Content-Type = %q，期望回落到入库类型 image/png", got)
	}
}

// rangeProvider 在假图床上补出区间能力，供网关 Range 测试使用。
type rangeProvider struct {
	*fakeStreamProvider
	size int64
	body []byte
}

func (p *rangeProvider) Size(context.Context, string) (int64, error) { return p.size, nil }

func (p *rangeProvider) OpenStream(context.Context, string) (storage.Stream, error) {
	return storage.Stream{
		Body:        io.NopCloser(bytes.NewReader(p.body)),
		ContentType: "video/mp4",
		Size:        int64(len(p.body)),
	}, nil
}

func (p *rangeProvider) OpenStreamRange(_ context.Context, _ string, r storage.ByteRange) (storage.Stream, error) {
	start := r.Start
	end := int64(len(p.body))
	if r.Length >= 0 && start+r.Length < end {
		end = start + r.Length
	}
	if start > end || start > int64(len(p.body)) {
		return storage.Stream{}, storage.ErrFileUnavailable
	}
	return storage.Stream{
		Body:        io.NopCloser(bytes.NewReader(p.body[start:end])),
		ContentType: "video/mp4",
		Size:        int64(len(p.body)),
		Start:       start,
		End:         end - 1,
	}, nil
}

// openRange 发一个带 Range 头的请求并读取响应。
func openRange(t *testing.T, method, url, header string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	if header != "" {
		req.Header.Set("Range", header)
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}
	return response, body
}

// newSwitcherProvider 用真实 Switcher 包一个指向测试文件服务的提供方，
// 验证生产入口的固定快照与区间能力，而不是靠测试替身直接满足接口。
func newSwitcherProvider(t *testing.T, body []byte, ignoreRange bool) storage.StreamProvider {
	return newSwitcherProviderMode(t, body, ignoreRange, false)
}

func newSwitcherProviderMode(t *testing.T, body []byte, ignoreRange, shortEnd bool) storage.StreamProvider {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/getFile") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":     true,
				"result": map[string]any{"file_path": "videos/clip.mp4", "file_size": len(body)},
			})
			return
		}
		header := r.Header.Get("Range")
		if strings.HasPrefix(header, "bytes=") && !ignoreRange {
			spec := strings.TrimPrefix(header, "bytes=")
			startText, endText, _ := strings.Cut(spec, "-")
			start, _ := strconv.Atoi(startText)
			end := len(body) - 1
			if endText != "" {
				if parsed, err := strconv.Atoi(endText); err == nil && parsed < end {
					end = parsed
				}
			}
			if shortEnd {
				end -= 1
			}
			w.Header().Set("Content-Type", "video/mp4")
			w.Header().Set("Content-Range", "bytes "+strconv.Itoa(start)+"-"+strconv.Itoa(end)+"/"+strconv.Itoa(len(body)))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(body[start : end+1])
			return
		}
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)
	provider, err := storage.NewTelegram(storage.TelegramConfig{
		Token: "123:synthetic", ChatID: "-100123", APIBase: server.URL,
	})
	if err != nil {
		t.Fatalf("创建提供方失败: %v", err)
	}
	switcher := storage.NewSwitcher()
	switcher.Swap(provider)
	return switcher
}

func TestGatewayRangeThroughSwitcher(t *testing.T) {
	body := []byte("0123456789")
	env := newGatewayEnv(t, 4, newSwitcherProvider(t, body, false))
	code := env.publishPost(t, "", "video/mp4", "photo", true, false)

	response, got := openRange(t, http.MethodGet, env.server.URL+"/i/"+code, "bytes=1-3")
	if response.StatusCode != http.StatusPartialContent {
		t.Fatalf("状态码 = %d，期望 206", response.StatusCode)
	}
	if response.Header.Get("Content-Range") != "bytes 1-3/10" || string(got) != "123" {
		t.Errorf("区间响应 = %q / %q", response.Header.Get("Content-Range"), got)
	}
}

func TestGatewayRangeThroughSwitcherIgnoresUpstream(t *testing.T) {
	body := []byte("0123456789")
	env := newGatewayEnv(t, 4, newSwitcherProvider(t, body, true))
	code := env.publishPost(t, "", "video/mp4", "photo", true, false)

	response, got := openRange(t, http.MethodGet, env.server.URL+"/i/"+code, "bytes=3-6")
	if response.StatusCode != http.StatusPartialContent {
		t.Fatalf("状态码 = %d，期望 206", response.StatusCode)
	}
	if response.Header.Get("Content-Range") != "bytes 3-6/10" {
		t.Errorf("Content-Range = %q", response.Header.Get("Content-Range"))
	}
	if response.Header.Get("Content-Length") != "4" || string(got) != "3456" {
		t.Errorf("长度/正文不符: %q / %q", response.Header.Get("Content-Length"), got)
	}
}

func TestGatewayRejectsMismatchedUpstreamRange(t *testing.T) {
	body := []byte("0123456789")
	env := newGatewayEnv(t, 4, newSwitcherProviderMode(t, body, false, true))
	code := env.publishPost(t, "", "video/mp4", "photo", true, false)

	response, _ := openRange(t, http.MethodGet, env.server.URL+"/i/"+code, "bytes=3-6")
	if response.StatusCode != http.StatusBadGateway {
		t.Errorf("上游少回一字节时状态码 = %d，期望 502", response.StatusCode)
	}
}

func TestGatewayIgnoresRangeForImages(t *testing.T) {
	body := []byte("0123456789")
	provider := &rangeProvider{fakeStreamProvider: &fakeStreamProvider{}, size: int64(len(body)), body: body}
	env := newGatewayEnv(t, 4, provider)
	code := env.publishPost(t, "", "image/jpeg", "photo", true, false)

	response, got := openRange(t, http.MethodGet, env.server.URL+"/i/"+code, "bytes=0-3")
	if response.StatusCode != http.StatusOK || string(got) != "0123456789" {
		t.Errorf("图片不应走区间: 状态 %d 正文 %q", response.StatusCode, got)
	}
	if response.Header.Get("Content-Range") != "" {
		t.Error("图片响应不应带 Content-Range")
	}
}

func TestGatewayServesByteRanges(t *testing.T) {
	body := []byte("0123456789")
	provider := &rangeProvider{fakeStreamProvider: &fakeStreamProvider{}, size: int64(len(body)), body: body}
	env := newGatewayEnv(t, 4, provider)
	code := env.publishPost(t, "", "video/mp4", "photo", true, false)
	url := env.server.URL + "/i/" + code

	cases := []struct {
		name       string
		method     string
		header     string
		wantStatus int
		wantRange  string
		wantBody   string
	}{
		{"闭区间", http.MethodGet, "bytes=0-3", http.StatusPartialContent, "bytes 0-3/10", "0123"},
		{"开放区间", http.MethodGet, "bytes=4-", http.StatusPartialContent, "bytes 4-9/10", "456789"},
		{"末尾区间", http.MethodGet, "bytes=-3", http.StatusPartialContent, "bytes 7-9/10", "789"},
		{"越界区间", http.MethodGet, "bytes=20-", http.StatusRequestedRangeNotSatisfiable, "", ""},
		{"多区间按整段", http.MethodGet, "bytes=0-0,2-3", http.StatusOK, "", "0123456789"},
		{"HEAD 探测", http.MethodHead, "bytes=0-3", http.StatusPartialContent, "bytes 0-3/10", ""},
	}
	for _, testCase := range cases {
		response, got := openRange(t, testCase.method, url, testCase.header)
		if response.StatusCode != testCase.wantStatus {
			t.Errorf("%s 状态码 = %d，期望 %d", testCase.name, response.StatusCode, testCase.wantStatus)
			continue
		}
		if testCase.wantRange == "" && testCase.wantStatus == http.StatusRequestedRangeNotSatisfiable {
			if got := response.Header.Get("Content-Range"); got != "bytes */10" {
				t.Errorf("%s Content-Range = %q，期望 bytes */10", testCase.name, got)
			}
			continue
		}
		if got := response.Header.Get("Content-Range"); got != testCase.wantRange {
			t.Errorf("%s Content-Range = %q，期望 %q", testCase.name, got, testCase.wantRange)
		}
		if response.Header.Get("Accept-Ranges") != "bytes" {
			t.Errorf("%s 缺少 Accept-Ranges", testCase.name)
		}
		if string(got) != testCase.wantBody {
			t.Errorf("%s 正文 = %q，期望 %q", testCase.name, got, testCase.wantBody)
		}
	}
}

func TestGatewayFallsBackWhenUpstreamIgnoresRange(t *testing.T) {
	// 普通假图床没有区间能力，带 Range 的请求按整段 200 返回。
	provider := &fakeStreamProvider{}
	env := newGatewayEnv(t, 4, provider)
	code := env.publishPost(t, "", "video/mp4", "photo", true, false)

	response, body := openRange(t, http.MethodGet, env.server.URL+"/i/"+code, "bytes=0-3")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", response.StatusCode)
	}
	if response.Header.Get("Content-Range") != "" {
		t.Errorf("整段响应不应带 Content-Range")
	}
	if string(body) != "image-bytes" {
		t.Errorf("正文 = %q", body)
	}
}

func TestGatewayRangeStillHidesWithdrawnMedia(t *testing.T) {
	body := []byte("0123456789")
	provider := &rangeProvider{fakeStreamProvider: &fakeStreamProvider{}, size: int64(len(body)), body: body}
	env := newGatewayEnv(t, 4, provider)
	code := env.publishPost(t, "", "video/mp4", "photo", true, true)

	response, _ := openRange(t, http.MethodGet, env.server.URL+"/i/"+code, "bytes=0-3")
	if response.StatusCode != http.StatusNotFound {
		t.Errorf("撤回后区间请求状态码 = %d，期望 404", response.StatusCode)
	}
}

// blockingProvider 让上游取流一直挂起，直到 release 关闭，用于观察排队行为。
type blockingProvider struct {
	*fakeStreamProvider
	opened  chan struct{}
	release chan struct{}
	count   int32
}

func (b *blockingProvider) OpenStream(ctx context.Context, _ string) (storage.Stream, error) {
	atomic.AddInt32(&b.count, 1)
	select {
	case b.opened <- struct{}{}:
	default:
	}
	select {
	case <-b.release:
	case <-ctx.Done():
	}
	return storage.Stream{Body: io.NopCloser(strings.NewReader("ok")), ContentType: "image/jpeg", Size: 2}, nil
}

func waitOpened(t *testing.T, opened chan struct{}, want int) {
	t.Helper()
	for index := 0; index < want; index++ {
		select {
		case <-opened:
		case <-time.After(2 * time.Second):
			t.Fatalf("第 %d 个请求未进入上游", index+1)
		}
	}
}

// TestGatewayQueueWaitTimeoutRejects 验证排队超过上限时间才返回 503。
func TestGatewayQueueWaitTimeoutRejects(t *testing.T) {
	provider := &blockingProvider{fakeStreamProvider: &fakeStreamProvider{}, opened: make(chan struct{}, 4), release: make(chan struct{})}
	env := newGatewayEnvOptions(t, 1, 4, 120*time.Millisecond, provider)
	code := env.publishPost(t, "queue123456", "image/jpeg", "photo", true, false)

	first := make(chan int, 1)
	go func() {
		response, err := http.Get(env.server.URL + "/i/" + code)
		if err != nil {
			first <- -1
			return
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		first <- response.StatusCode
	}()
	waitOpened(t, provider.opened, 1)

	started := time.Now()
	response, body := getImage(t, env.server.URL+"/i/"+code)
	elapsed := time.Since(started)
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("状态码 = %d，期望 503，响应: %s", response.StatusCode, body)
	}
	if response.Header.Get("Retry-After") != "1" {
		t.Errorf("Retry-After = %q，期望 1", response.Header.Get("Retry-After"))
	}
	if elapsed < 100*time.Millisecond {
		t.Errorf("排队耗时 %v，应至少等待设定的 120ms", elapsed)
	}
	if got := atomic.LoadInt32(&provider.count); got != 1 {
		t.Errorf("上游取流 %d 次，超时请求不应进入上游", got)
	}

	close(provider.release)
	select {
	case status := <-first:
		if status != http.StatusOK {
			t.Errorf("首请求状态码 = %d，期望 200", status)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("首请求未完成")
	}
}

// TestGatewayQueueFullRejectsImmediately 验证排队人数满额时立即拒绝，不等待。
func TestGatewayQueueFullRejectsImmediately(t *testing.T) {
	provider := &blockingProvider{fakeStreamProvider: &fakeStreamProvider{}, opened: make(chan struct{}, 4), release: make(chan struct{})}
	env := newGatewayEnvOptions(t, 1, 1, 2*time.Second, provider)
	code := env.publishPost(t, "queuefull123", "image/jpeg", "photo", true, false)

	first := make(chan int, 1)
	go func() {
		response, err := http.Get(env.server.URL + "/i/" + code)
		if err != nil {
			first <- -1
			return
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		first <- response.StatusCode
	}()
	waitOpened(t, provider.opened, 1)

	// 第二个请求占满唯一的排队名额。
	second := make(chan int, 1)
	go func() {
		response, err := http.Get(env.server.URL + "/i/" + code)
		if err != nil {
			second <- -1
			return
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		second <- response.StatusCode
	}()
	time.Sleep(150 * time.Millisecond)

	started := time.Now()
	response, body := getImage(t, env.server.URL+"/i/"+code)
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("状态码 = %d，期望 503，响应: %s", response.StatusCode, body)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Errorf("满队拒绝耗时 %v，应立即返回", elapsed)
	}
	if got := atomic.LoadInt32(&provider.count); got != 1 {
		t.Errorf("上游取流 %d 次，满队请求不应进入上游", got)
	}

	close(provider.release)
	for index, channel := range []chan int{first, second} {
		select {
		case status := <-channel:
			if status != http.StatusOK {
				t.Errorf("第 %d 个请求状态码 = %d，期望 200", index+1, status)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("第 %d 个请求未完成", index+1)
		}
	}
}

// TestGatewayCancelWhileWaitingLeavesQueue 验证排队中的客户端断开后立即退出，不占名额。
func TestGatewayCancelWhileWaitingLeavesQueue(t *testing.T) {
	provider := &blockingProvider{fakeStreamProvider: &fakeStreamProvider{}, opened: make(chan struct{}, 4), release: make(chan struct{})}
	env := newGatewayEnvOptions(t, 1, 4, 2*time.Second, provider)
	code := env.publishPost(t, "queuecancel1", "image/jpeg", "photo", true, false)

	first := make(chan int, 1)
	go func() {
		response, err := http.Get(env.server.URL + "/i/" + code)
		if err != nil {
			first <- -1
			return
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		first <- response.StatusCode
	}()
	waitOpened(t, provider.opened, 1)

	ctx, cancel := context.WithCancel(context.Background())
	waiter, err := http.NewRequestWithContext(ctx, http.MethodGet, env.server.URL+"/i/"+code, nil)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		response, err := http.DefaultClient.Do(waiter)
		if err == nil {
			_ = response.Body.Close()
		}
		done <- err
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Error("取消后请求应返回错误")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("取消后排队请求未退出")
	}
	if got := atomic.LoadInt32(&provider.count); got != 1 {
		t.Errorf("上游取流 %d 次，取消的排队请求不应进入上游", got)
	}

	close(provider.release)
	select {
	case status := <-first:
		if status != http.StatusOK {
			t.Errorf("首请求状态码 = %d，期望 200", status)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("首请求未完成")
	}

	// 名额与排队位都已释放，新请求应直接成功。
	after, body := getImage(t, env.server.URL+"/i/"+code)
	if after.StatusCode != http.StatusOK {
		t.Errorf("释放后状态码 = %d，响应: %s", after.StatusCode, body)
	}
}
