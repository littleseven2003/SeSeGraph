package api

import (
	"context"
	"database/sql"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/littleseven2003/SeSeGraph/backend/internal/db"
	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
	"github.com/littleseven2003/SeSeGraph/backend/internal/storage"
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
		Logger:           log.New(io.Discard, "", 0),
	})
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return &gatewayEnv{server: server, repo: repository, handle: handle}
}

// publishPost 建一个已完成且图片已存图的帖子，返回短码。
func (e *gatewayEnv) publishPost(t *testing.T, shortCode, mime, mode string, disabled bool) string {
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

	disabledCode := env.publishPost(t, "", "image/jpeg", "photo", true)
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

func TestGatewayStreamsWithCacheHeaders(t *testing.T) {
	provider := &fakeStreamProvider{}
	env := newGatewayEnv(t, 4, provider)
	code := env.publishPost(t, "abc123XYZ0", "image/jpeg", "document", false)

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
	documentCode := env.publishPost(t, "doc12345", "image/png", "document", false)
	photoCode := env.publishPost(t, "photo1234", "image/jpeg", "photo", false)

	documentResponse, _ := getImage(t, env.server.URL+"/i/"+documentCode)
	if got := documentResponse.Header.Get("Content-Type"); got != "image/png" {
		t.Errorf("document 模式 Content-Type = %q，期望入库嗅探值 image/png", got)
	}

	photoResponse, _ := getImage(t, env.server.URL+"/i/"+photoCode)
	if got := photoResponse.Header.Get("Content-Type"); got != "image/jpeg" {
		t.Errorf("photo 模式 Content-Type = %q，期望上游值 image/jpeg", got)
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
	code := env.publishPost(t, "slow12345", "image/webp", "photo", false)

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
	code := env.publishPost(t, "conc12345", "image/jpeg", "photo", false)

	results := make(chan int, 3)
	for index := 0; index < 3; index++ {
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

	// 先确认只有 2 个请求进入上游。
	for index := 0; index < 2; index++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("并发请求未及时进入上游")
		}
	}
	select {
	case <-started:
		t.Fatal("第三个请求应排队等待，不应进入上游")
	case <-time.After(200 * time.Millisecond):
	}

	close(release)
	for index := 0; index < 3; index++ {
		select {
		case status := <-results:
			if status != http.StatusOK {
				t.Errorf("状态码 = %d，期望 200", status)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("请求未完成")
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if maxActive > 2 {
		t.Errorf("上游最大并发 = %d，超过上限 2", maxActive)
	}
}

func TestGatewayClosesUpstreamOnDisconnect(t *testing.T) {
	readerCtx := make(chan context.Context, 1)
	closed := make(chan struct{})
	provider := &fakeStreamProvider{}
	provider.open = func(ctx context.Context, _ string) (storage.Stream, error) {
		reader := &ctxReader{ctx: ctx, chunk: []byte("first"), closed: closed}
		readerCtx <- ctx
		return storage.Stream{Body: reader, ContentType: "image/jpeg", Size: 0}, nil
	}
	env := newGatewayEnv(t, 2, provider)
	code := env.publishPost(t, "drop12345", "image/jpeg", "photo", false)

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
}

func TestGatewayUpstreamErrorsAreDesensitized(t *testing.T) {
	provider := &fakeStreamProvider{}
	provider.open = func(context.Context, string) (storage.Stream, error) {
		return storage.Stream{}, storage.ErrUpstream
	}
	env := newGatewayEnv(t, 2, provider)
	code := env.publishPost(t, "err123456", "image/jpeg", "photo", false)

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
	code := env.publishPost(t, "none12345", "image/jpeg", "photo", false)

	response, _ := getImage(t, env.server.URL+"/i/"+code)
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("状态码 = %d，期望 503", response.StatusCode)
	}
}

// 保证 gin 在测试里使用测试模式，避免输出噪音。
func init() {
	gin.SetMode(gin.TestMode)
}
