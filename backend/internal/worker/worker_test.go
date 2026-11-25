package worker

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/littleseven2003/SeSeGraph/backend/internal/db"
	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
	"github.com/littleseven2003/SeSeGraph/backend/internal/storage"
)

type fakeProvider struct {
	mu       sync.Mutex
	calls    int
	failures []error
}

func (f *fakeProvider) Name() string { return "fake" }

func (f *fakeProvider) Upload(_ context.Context, req storage.UploadRequest) (storage.UploadResult, error) {
	f.mu.Lock()
	f.calls++
	call := f.calls
	var failure error
	if len(f.failures) > 0 {
		failure = f.failures[0]
		f.failures = f.failures[1:]
	}
	f.mu.Unlock()

	if _, err := io.Copy(io.Discard, req.Reader); err != nil {
		return storage.UploadResult{}, storage.ErrUpstream
	}
	if failure != nil {
		return storage.UploadResult{}, failure
	}
	return storage.UploadResult{Provider: "fake", FileID: fmt.Sprintf("file-%d", call), MessageID: int64(call)}, nil
}

func (f *fakeProvider) OpenStream(context.Context, string) (storage.Stream, error) {
	return storage.Stream{Body: io.NopCloser(strings.NewReader(""))}, nil
}

func (f *fakeProvider) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// testClock 提供可推进的时钟，让间隔与退避断言稳定可复现。
// snapshotFake 在首次上传时触发回调，用于模拟上传途中发生的配置切换。
type snapshotFake struct {
	inner    *fakeProvider
	onUpload func()
	once     sync.Once
}

func (s *snapshotFake) Name() string { return s.inner.Name() }

func (s *snapshotFake) Upload(ctx context.Context, req storage.UploadRequest) (storage.UploadResult, error) {
	if s.onUpload != nil {
		s.once.Do(s.onUpload)
	}
	return s.inner.Upload(ctx, req)
}

func (s *snapshotFake) Snapshot() (storage.StreamProvider, bool) { return s.inner, true }

type testClock struct {
	mu        sync.Mutex
	now       time.Time
	durations []time.Duration
}

func newTestClock() *testClock {
	return &testClock{now: time.Unix(1700000000, 0)}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) sleep(_ context.Context, d time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	c.durations = append(c.durations, d)
	return nil
}

func (c *testClock) recorded() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.durations...)
}

type env struct {
	handle   *sql.DB
	repo     *posts.Repository
	dataDir  string
	provider *fakeProvider
	sleeps   *testClock
	gate     *storage.UploadGate
	worker   *Worker
	// publishReadyErr 模拟发布前置条件：nil 表示已配置，非 nil 表示不可发布。
	publishReadyErr error
}

func newEnv(t *testing.T) *env {
	t.Helper()
	handle, err := db.Open(filepath.Join(t.TempDir(), "sesegraph.db"))
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	if err := db.Migrate(context.Background(), handle); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}

	e := &env{
		handle:   handle,
		repo:     posts.NewRepository(handle),
		dataDir:  t.TempDir(),
		provider: &fakeProvider{},
		sleeps:   newTestClock(),
		gate:     storage.NewUploadGate(),
	}
	worker, err := New(Options{
		Repository: e.repo,
		Provider:   e.provider,
		DataDir:    e.dataDir,
		Interval:   time.Millisecond,
		Logger:     log.New(io.Discard, "", 0),
		Sleep:      e.sleeps.sleep,
		Now:        e.sleeps.Now,
		Gate:       e.gate,
		PublishReady: func(context.Context) error {
			return e.publishReadyErr
		},
	})
	if err != nil {
		t.Fatalf("创建队列失败: %v", err)
	}
	e.worker = worker
	return e
}

func (e *env) queuedPost(t *testing.T, imageCount int, mode string) int64 {
	t.Helper()
	return e.queuedPostWithIntent(t, imageCount, mode, "")
}

// queuedPostWithIntent 用指定的发布意图建任务，其余步骤与普通任务一致。
func (e *env) queuedPostWithIntent(t *testing.T, imageCount int, mode, intent string) int64 {
	t.Helper()
	ctx := context.Background()
	post := posts.Post{Title: "上传任务", ImageCount: imageCount, UploadMode: mode, PublishIntent: intent}
	id, err := e.repo.Create(ctx, post)
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}

	dir := filepath.Join(e.dataDir, posts.TempDir, strconv.FormatInt(id, 10))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	for sort := 1; sort <= imageCount; sort++ {
		name := fmt.Sprintf("%d.part", sort)
		content := []byte(fmt.Sprintf("image-%d", sort))
		if err := os.WriteFile(filepath.Join(dir, name), content, 0o600); err != nil {
			t.Fatalf("写入临时文件失败: %v", err)
		}
		if _, err := e.repo.AttachImage(ctx, posts.Image{
			PostID:       id,
			SortOrder:    sort,
			MimeType:     "image/jpeg",
			FileSize:     int64(len(content)),
			TempFilePath: "tmp/" + strconv.FormatInt(id, 10) + "/" + name,
			Status:       posts.ImageStatusUploaded,
		}); err != nil {
			t.Fatalf("保存图片记录失败: %v", err)
		}
	}
	if missing, err := e.repo.Enqueue(ctx, id); err != nil || len(missing) > 0 {
		t.Fatalf("入队失败: missing=%v err=%v", missing, err)
	}
	return id
}

func TestProcessUploadsAllImages(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id := e.queuedPost(t, 2, "photo")

	if _, ok, err := e.repo.ClaimNext(ctx); err != nil || !ok {
		t.Fatalf("领取失败: ok=%v err=%v", ok, err)
	}
	if err := e.worker.Process(ctx, id); err != nil {
		t.Fatalf("处理失败: %v", err)
	}

	stored, err := e.repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("读取帖子失败: %v", err)
	}
	if stored.Status != posts.StatusCompleted {
		t.Errorf("状态 = %q，期望 completed", stored.Status)
	}
	if e.provider.callCount() != 2 {
		t.Errorf("上传次数 = %d，期望 2", e.provider.callCount())
	}
	if count, _ := e.repo.CountStored(ctx, id); count != 2 {
		t.Errorf("已存图片 = %d，期望 2", count)
	}
	if entries, err := os.ReadDir(filepath.Join(e.dataDir, posts.TempDir, strconv.FormatInt(id, 10))); err == nil && len(entries) != 0 {
		t.Errorf("成功后应删除临时文件，仍剩 %d 个", len(entries))
	}
}

func TestProcessSkipsAlreadyStoredImages(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id := e.queuedPost(t, 2, "photo")

	if _, _, err := e.repo.ClaimNext(ctx); err != nil {
		t.Fatalf("领取失败: %v", err)
	}
	if err := e.worker.Process(ctx, id); err != nil {
		t.Fatalf("首次处理失败: %v", err)
	}

	// 模拟任务被重新排队后再次处理。
	if err := e.repo.UpdateStatus(ctx, id, posts.StatusQueued); err != nil {
		t.Fatalf("更新状态失败: %v", err)
	}
	if _, ok, err := e.repo.ClaimNext(ctx); err != nil || !ok {
		t.Fatalf("重新领取失败: ok=%v err=%v", ok, err)
	}
	if err := e.worker.Process(ctx, id); err != nil {
		t.Fatalf("再次处理失败: %v", err)
	}

	if e.provider.callCount() != 2 {
		t.Errorf("上传次数 = %d，期望仍为 2（已存图不重复上传）", e.provider.callCount())
	}
	stored, _ := e.repo.Get(ctx, id)
	if stored.Status != posts.StatusCompleted {
		t.Errorf("状态 = %q，期望 completed", stored.Status)
	}
}

func TestRateLimitRetriesWithRetryAfter(t *testing.T) {
	e := newEnv(t)
	e.provider.failures = []error{&storage.RateLimitError{RetryAfter: 2 * time.Second}}
	ctx := context.Background()
	id := e.queuedPost(t, 1, "photo")

	if _, _, err := e.repo.ClaimNext(ctx); err != nil {
		t.Fatalf("领取失败: %v", err)
	}
	if err := e.worker.Process(ctx, id); err != nil {
		t.Fatalf("处理失败: %v", err)
	}
	if e.provider.callCount() != 2 {
		t.Errorf("上传次数 = %d，期望 2（限流后重试一次）", e.provider.callCount())
	}
	durations := e.sleeps.recorded()
	if len(durations) != 1 || durations[0] != 2*time.Second {
		t.Errorf("退避时长 = %v，期望 [2s]", durations)
	}
	stored, _ := e.repo.Get(ctx, id)
	if stored.Status != posts.StatusCompleted {
		t.Errorf("状态 = %q，期望 completed", stored.Status)
	}
}

func TestRateLimitExhaustedMarksFailed(t *testing.T) {
	e := newEnv(t)
	e.provider.failures = []error{
		&storage.RateLimitError{RetryAfter: time.Second},
		&storage.RateLimitError{RetryAfter: time.Second},
		&storage.RateLimitError{RetryAfter: time.Second},
	}
	ctx := context.Background()
	id := e.queuedPost(t, 1, "photo")

	if _, _, err := e.repo.ClaimNext(ctx); err != nil {
		t.Fatalf("领取失败: %v", err)
	}
	if err := e.worker.Process(ctx, id); err == nil {
		t.Fatal("重试耗尽后应返回错误")
	}

	stored, _ := e.repo.Get(ctx, id)
	if stored.Status != posts.StatusFailed {
		t.Errorf("状态 = %q，期望 failed", stored.Status)
	}
	if e.provider.callCount() != 3 {
		t.Errorf("上传次数 = %d，期望 3 次尝试", e.provider.callCount())
	}
}

func TestMissingTempFileMarksFailed(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id := e.queuedPost(t, 1, "photo")
	_ = os.RemoveAll(filepath.Join(e.dataDir, posts.TempDir, strconv.FormatInt(id, 10)))

	if _, _, err := e.repo.ClaimNext(ctx); err != nil {
		t.Fatalf("领取失败: %v", err)
	}
	if err := e.worker.Process(ctx, id); err == nil {
		t.Fatal("缺少临时文件应返回错误")
	}
	stored, _ := e.repo.Get(ctx, id)
	if stored.Status != posts.StatusFailed {
		t.Errorf("状态 = %q，期望 failed", stored.Status)
	}
}

func TestRecoverRequeuesInterrupted(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id := e.queuedPost(t, 1, "photo")

	if _, _, err := e.repo.ClaimNext(ctx); err != nil {
		t.Fatalf("领取失败: %v", err)
	}

	recovery, err := e.worker.Recover(ctx)
	if err != nil {
		t.Fatalf("恢复失败: %v", err)
	}
	if recovery.Requeued != 1 {
		t.Errorf("恢复条数 = %d，期望 1", recovery.Requeued)
	}
	stored, _ := e.repo.Get(ctx, id)
	if stored.Status != posts.StatusQueued {
		t.Errorf("状态 = %q，期望 queued", stored.Status)
	}
}

func TestRunDrainsQueueAndStops(t *testing.T) {
	e := newEnv(t)
	id := e.queuedPost(t, 2, "document")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		e.worker.Run(ctx)
		close(done)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		stored, err := e.repo.Get(context.Background(), id)
		if err == nil && stored.Status == posts.StatusCompleted {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("取消后循环应退出")
	}

	stored, _ := e.repo.Get(context.Background(), id)
	if stored.Status != posts.StatusCompleted {
		t.Errorf("状态 = %q，期望 completed", stored.Status)
	}
}

func TestProcessStopsOnContextCancel(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	id := e.queuedPost(t, 2, "photo")
	if _, _, err := e.repo.ClaimNext(ctx); err != nil {
		t.Fatalf("领取失败: %v", err)
	}
	cancel()

	err := e.worker.Process(ctx, id)
	if err == nil {
		t.Fatal("取消后应返回错误")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("错误 = %v，期望上下文取消", err)
	}
}

func TestIntervalSharedAcrossSingleImagePosts(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.worker.interval = time.Second

	first := e.queuedPost(t, 1, "photo")
	second := e.queuedPost(t, 1, "photo")

	for i := 0; i < 2; i++ {
		if _, err := e.worker.ProcessOnce(ctx); err != nil {
			t.Fatalf("第 %d 个任务处理失败: %v", i+1, err)
		}
	}

	if e.provider.callCount() != 2 {
		t.Fatalf("上传次数 = %d，期望 2", e.provider.callCount())
	}
	durations := e.sleeps.recorded()
	if len(durations) != 1 || durations[0] != time.Second {
		t.Errorf("跨任务间隔 = %v，期望恰好一次 1s", durations)
	}

	for _, id := range []int64{first, second} {
		stored, err := e.repo.Get(ctx, id)
		if err != nil || stored.Status != posts.StatusCompleted {
			t.Errorf("帖子 %d 状态 = %v err=%v", id, stored, err)
		}
	}
}

func TestExhaustedRetryAfterDelaysNextPost(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.worker.interval = time.Second
	e.provider.failures = []error{
		&storage.RateLimitError{RetryAfter: 5 * time.Second},
		&storage.RateLimitError{RetryAfter: 5 * time.Second},
		&storage.RateLimitError{RetryAfter: 5 * time.Second},
	}

	first := e.queuedPost(t, 1, "photo")
	second := e.queuedPost(t, 1, "photo")

	if _, err := e.worker.ProcessOnce(ctx); err == nil {
		t.Fatal("首个任务重试耗尽应返回错误")
	}
	failed, _ := e.repo.Get(ctx, first)
	if failed.Status != posts.StatusFailed {
		t.Errorf("首个任务状态 = %q，期望 failed", failed.Status)
	}

	if _, err := e.worker.ProcessOnce(ctx); err != nil {
		t.Fatalf("第二个任务处理失败: %v", err)
	}
	completed, _ := e.repo.Get(ctx, second)
	if completed.Status != posts.StatusCompleted {
		t.Errorf("第二个任务状态 = %q，期望 completed", completed.Status)
	}

	durations := e.sleeps.recorded()
	if len(durations) != 3 {
		t.Fatalf("等待次数 = %d，期望 3（两次重试等待 + 一次跨任务门控）: %v", len(durations), durations)
	}
	for index, wait := range durations {
		if wait != 5*time.Second {
			t.Errorf("第 %d 次等待 = %s，期望 5s", index+1, wait)
		}
	}
	if e.provider.callCount() != 4 {
		t.Errorf("上传调用 = %d，期望 4（首个 3 次 + 次个 1 次）", e.provider.callCount())
	}
}

// TestProcessOnceWaitsWhileProviderNotReady 验证图床未配置时队列不领取任务，
// 排队中的图片不会被判为失败。
func TestProcessOnceWaitsWhileProviderNotReady(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id := e.queuedPost(t, 1, "photo")

	switcher := storage.NewSwitcher()
	queue, err := New(Options{
		Repository: e.repo,
		Provider:   switcher,
		DataDir:    e.dataDir,
		Interval:   time.Millisecond,
	})
	if err != nil {
		t.Fatalf("创建队列失败: %v", err)
	}

	processed, err := queue.ProcessOnce(ctx)
	if err != nil {
		t.Fatalf("未配置时不应报错: %v", err)
	}
	if processed {
		t.Error("未配置时不应领取任务")
	}
	stored, err := e.repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("读取帖子失败: %v", err)
	}
	if stored.Status != posts.StatusQueued {
		t.Errorf("状态 = %q，期望保持 queued", stored.Status)
	}
	if e.provider.callCount() != 0 {
		t.Errorf("上传次数 = %d，期望 0", e.provider.callCount())
	}
}

// TestProcessUsesSingleProviderForWholePost 验证一帖在途上传固定使用同一份配置：
// 上传途中切换提供方，后续图片仍走开始时的那一份。
func TestProcessUsesSingleProviderForWholePost(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.queuedPost(t, 2, "photo")

	original := e.provider
	other := &fakeProvider{}
	wrapper := &snapshotFake{inner: original, onUpload: func() {
		e.worker.provider = other
	}}
	e.worker.provider = wrapper

	if _, err := e.worker.ProcessOnce(ctx); err != nil {
		t.Fatalf("处理失败: %v", err)
	}
	if original.callCount() != 2 {
		t.Errorf("原提供方上传次数 = %d，期望 2", original.callCount())
	}
	if other.callCount() != 0 {
		t.Errorf("切换后的提供方上传次数 = %d，期望 0", other.callCount())
	}
}

// blockingFake 在上传时阻塞到放行，用于观察任务全程占用协调点。
type blockingFake struct {
	inner   *fakeProvider
	once    sync.Once
	started chan struct{}
	release chan struct{}
}

func (b *blockingFake) Name() string { return b.inner.Name() }

func (b *blockingFake) Upload(ctx context.Context, req storage.UploadRequest) (storage.UploadResult, error) {
	b.once.Do(func() { close(b.started) })
	<-b.release
	return b.inner.Upload(ctx, req)
}

// TestProcessOnceWaitsWhileSwitchExclusive 验证切换独占期间队列不领取任务：
// 任务保持排队、上游零请求，独占释放后正常完成。
func TestProcessOnceWaitsWhileSwitchExclusive(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id := e.queuedPost(t, 1, "photo")

	releaseSwitch, ok := e.gate.EnterSwitch(ctx, 0)
	if !ok {
		t.Fatal("独占协调点应成功")
	}
	processed, err := e.worker.ProcessOnce(ctx)
	if err != nil {
		t.Fatalf("处理失败: %v", err)
	}
	if processed {
		t.Error("切换独占期间不应领取任务")
	}
	stored, err := e.repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("读取帖子失败: %v", err)
	}
	if stored.Status != posts.StatusQueued {
		t.Errorf("状态 = %q，期望保持 queued", stored.Status)
	}
	if e.provider.callCount() != 0 {
		t.Errorf("上传次数 = %d，期望 0", e.provider.callCount())
	}

	releaseSwitch()
	processed, err = e.worker.ProcessOnce(ctx)
	if err != nil || !processed {
		t.Fatalf("释放后处理失败: processed=%v err=%v", processed, err)
	}
	if e.provider.callCount() != 1 {
		t.Errorf("上传次数 = %d，期望 1", e.provider.callCount())
	}
}

// TestProcessHoldsGateForWholePost 验证上传全程占用协调点：
// 首图落库前切换无法独占，任务结束后才放行。
func TestProcessHoldsGateForWholePost(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.queuedPost(t, 1, "photo")

	blocking := &blockingFake{
		inner:   e.provider,
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	e.worker.provider = blocking

	done := make(chan error, 1)
	go func() {
		_, err := e.worker.ProcessOnce(ctx)
		done <- err
	}()
	<-blocking.started

	if _, ok := e.gate.EnterSwitch(ctx, 0); ok {
		t.Error("上传在途时不应允许独占切换")
	}
	close(blocking.release)
	if err := <-done; err != nil {
		t.Fatalf("处理失败: %v", err)
	}

	releaseSwitch, ok := e.gate.EnterSwitch(ctx, 0)
	if !ok {
		t.Fatal("任务结束后应允许独占切换")
	}
	releaseSwitch()
}

// TestProcessHonorsPauseAndStop 验证暂停让出任务、恢复后继续，停止后不再上传。
func TestProcessHonorsPauseAndStop(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id := e.queuedPost(t, 2, "photo")

	if _, ok, err := e.repo.ClaimNext(ctx); err != nil || !ok {
		t.Fatalf("领取失败: ok=%v err=%v", ok, err)
	}
	if err := e.repo.Pause(ctx, id); err != nil {
		t.Fatalf("暂停失败: %v", err)
	}
	if err := e.worker.Process(ctx, id); err != nil {
		t.Fatalf("暂停后处理应正常返回: %v", err)
	}
	if e.provider.calls != 0 {
		t.Errorf("暂停期间上传 %d 次，期望 0", e.provider.calls)
	}
	post, err := e.repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("读取帖子失败: %v", err)
	}
	if post.Status != posts.StatusQueued {
		t.Errorf("暂停后状态 = %q，期望回到队列", post.Status)
	}

	if err := e.repo.Resume(ctx, id); err != nil {
		t.Fatalf("恢复失败: %v", err)
	}
	if _, ok, err := e.repo.ClaimNext(ctx); err != nil || !ok {
		t.Fatalf("恢复后领取失败: ok=%v err=%v", ok, err)
	}
	if err := e.worker.Process(ctx, id); err != nil {
		t.Fatalf("恢复后处理失败: %v", err)
	}
	if e.provider.calls != 2 {
		t.Errorf("恢复后上传 %d 次，期望 2", e.provider.calls)
	}

	second := e.queuedPost(t, 1, "photo")
	if _, ok, err := e.repo.ClaimNext(ctx); err != nil || !ok {
		t.Fatalf("领取第二帖失败: ok=%v err=%v", ok, err)
	}
	if err := e.repo.Stop(ctx, second); err != nil {
		t.Fatalf("停止失败: %v", err)
	}
	before := e.provider.calls
	if err := e.worker.Process(ctx, second); err != nil {
		t.Fatalf("停止后处理应正常返回: %v", err)
	}
	if e.provider.calls != before {
		t.Errorf("停止后仍上传了 %d 次", e.provider.calls-before)
	}
}

// TestProcessEnqueuesAutoPublish 验证提交时选了自动发布的任务在同步完成后直接入队，
// 不需要管理员再点一次；只存图的任务保持未发布。
func TestProcessEnqueuesAutoPublish(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	autoID := e.queuedPostWithIntent(t, 2, "photo", posts.PublishIntentAuto)
	manualID := e.queuedPost(t, 2, "photo")

	for _, id := range []int64{autoID, manualID} {
		if _, ok, err := e.repo.ClaimNext(ctx); err != nil || !ok {
			t.Fatalf("领取失败: ok=%v err=%v", ok, err)
		}
		if err := e.worker.Process(ctx, id); err != nil {
			t.Fatalf("处理失败: %v", err)
		}
	}

	state, _, public, err := e.repo.PublishStatus(ctx, autoID)
	if err != nil {
		t.Fatalf("读取发布状态失败: %v", err)
	}
	if state != posts.PublishPending || !public {
		t.Errorf("自动发布任务状态 = %q / public=%v，期望 pending / true", state, public)
	}

	state, _, public, err = e.repo.PublishStatus(ctx, manualID)
	if err != nil {
		t.Fatalf("读取发布状态失败: %v", err)
	}
	if state != posts.PublishIdle || public {
		t.Errorf("仅存图任务状态 = %q / public=%v，期望 idle / false", state, public)
	}
}

// TestProcessDoesNotAutoPublishStopped 验证停止后收尾不会把任务送进发布队列。
func TestProcessDoesNotAutoPublishStopped(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id := e.queuedPostWithIntent(t, 1, "photo", posts.PublishIntentAuto)
	if _, ok, err := e.repo.ClaimNext(ctx); err != nil || !ok {
		t.Fatalf("领取失败: ok=%v err=%v", ok, err)
	}
	if err := e.repo.Stop(ctx, id); err != nil {
		t.Fatalf("停止失败: %v", err)
	}
	if err := e.worker.Process(ctx, id); err != nil {
		t.Fatalf("处理失败: %v", err)
	}
	state, _, _, err := e.repo.PublishStatus(ctx, id)
	if err != nil {
		t.Fatalf("读取发布状态失败: %v", err)
	}
	if state == posts.PublishPending || state == posts.PublishPublishing {
		t.Errorf("停止的任务不应入发布队列，状态 = %q", state)
	}
}

// TestProcessKeepsAutoPublishHiddenWhenNotReady 验证发布条件不齐时收尾不会公开，
// 任务停在已存图未公开，等管理员补齐 Telegraph 与 HTTPS 后手动发布。
func TestProcessKeepsAutoPublishHiddenWhenNotReady(t *testing.T) {
	e := newEnv(t)
	e.publishReadyErr = errors.New("发布条件未就绪")
	ctx := context.Background()
	id := e.queuedPostWithIntent(t, 1, "photo", posts.PublishIntentAuto)
	if _, ok, err := e.repo.ClaimNext(ctx); err != nil || !ok {
		t.Fatalf("领取失败: ok=%v err=%v", ok, err)
	}
	if err := e.worker.Process(ctx, id); err != nil {
		t.Fatalf("处理失败: %v", err)
	}

	state, _, public, err := e.repo.PublishStatus(ctx, id)
	if err != nil {
		t.Fatalf("读取发布状态失败: %v", err)
	}
	if state != posts.PublishIdle || public {
		t.Fatalf("状态 = %q / public=%v，期望 idle / false", state, public)
	}
	stored, err := e.repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("读取帖子失败: %v", err)
	}
	if stored.Status != posts.StatusCompleted {
		t.Errorf("状态 = %q，期望 completed（图片应已存好）", stored.Status)
	}
}

// TestProcessAutoPublishesAfterPauseAndResume 验证同步途中暂停会回队列，
// 恢复后重新收尾才入发布队列，暂停期间不会抢先公开。
func TestProcessAutoPublishesAfterPauseAndResume(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id := e.queuedPostWithIntent(t, 1, "photo", posts.PublishIntentAuto)
	if _, ok, err := e.repo.ClaimNext(ctx); err != nil || !ok {
		t.Fatalf("领取失败: ok=%v err=%v", ok, err)
	}
	if err := e.repo.Pause(ctx, id); err != nil {
		t.Fatalf("暂停失败: %v", err)
	}
	if err := e.worker.Process(ctx, id); err != nil {
		t.Fatalf("暂停中处理应正常返回: %v", err)
	}
	state, _, public, err := e.repo.PublishStatus(ctx, id)
	if err != nil {
		t.Fatalf("读取发布状态失败: %v", err)
	}
	if state != posts.PublishIdle || public {
		t.Fatalf("暂停中状态 = %q / public=%v，期望 idle / false", state, public)
	}

	if err := e.repo.Resume(ctx, id); err != nil {
		t.Fatalf("恢复失败: %v", err)
	}
	if _, ok, err := e.repo.ClaimNext(ctx); err != nil || !ok {
		t.Fatalf("恢复后领取失败: ok=%v err=%v", ok, err)
	}
	if err := e.worker.Process(ctx, id); err != nil {
		t.Fatalf("恢复后处理失败: %v", err)
	}
	state, _, public, err = e.repo.PublishStatus(ctx, id)
	if err != nil {
		t.Fatalf("读取发布状态失败: %v", err)
	}
	if state != posts.PublishPending || !public {
		t.Fatalf("恢复后状态 = %q / public=%v，期望 pending / true", state, public)
	}
	if e.provider.callCount() != 1 {
		t.Errorf("上传次数 = %d，已存图不应重复上传", e.provider.callCount())
	}
}

// TestRecoverEnqueuesInterruptedAutoPublish 验证同步收尾后立刻重启，
// 下次启动会把「已同步、未发布」的任务补进发布队列。
func TestRecoverEnqueuesInterruptedAutoPublish(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id := e.queuedPostWithIntent(t, 1, "photo", posts.PublishIntentAuto)
	if _, ok, err := e.repo.ClaimNext(ctx); err != nil || !ok {
		t.Fatalf("领取失败: ok=%v err=%v", ok, err)
	}
	// 模拟旧版本：完成落库后、发布状态写入前进程退出。
	if _, err := e.repo.FinishSync(ctx, id, false, "发布条件未就绪"); err != nil {
		t.Fatalf("收尾失败: %v", err)
	}
	e.publishReadyErr = nil
	recovery, err := e.worker.Recover(ctx)
	if err != nil {
		t.Fatalf("启动恢复失败: %v", err)
	}
	if recovery.AutoEnqueued != 1 {
		t.Errorf("补入发布队列 %d 条，期望 1", recovery.AutoEnqueued)
	}
	state, _, public, err := e.repo.PublishStatus(ctx, id)
	if err != nil {
		t.Fatalf("读取发布状态失败: %v", err)
	}
	if state != posts.PublishPending || !public {
		t.Fatalf("启动恢复后状态 = %q / public=%v，期望 pending / true", state, public)
	}
}

// TestRecoverKeepsHiddenWithoutPublishConfig 验证启动恢复同样受发布条件约束。
func TestRecoverKeepsHiddenWithoutPublishConfig(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id := e.queuedPostWithIntent(t, 1, "photo", posts.PublishIntentAuto)
	if _, ok, err := e.repo.ClaimNext(ctx); err != nil || !ok {
		t.Fatalf("领取失败: ok=%v err=%v", ok, err)
	}
	if _, err := e.repo.FinishSync(ctx, id, false, "发布条件未就绪"); err != nil {
		t.Fatalf("收尾失败: %v", err)
	}
	e.publishReadyErr = errors.New("Telegraph 未配置")
	if _, err := e.worker.Recover(ctx); err != nil {
		t.Fatalf("启动恢复失败: %v", err)
	}
	state, _, public, err := e.repo.PublishStatus(ctx, id)
	if err != nil {
		t.Fatalf("读取发布状态失败: %v", err)
	}
	if state != posts.PublishIdle || public {
		t.Fatalf("状态 = %q / public=%v，期望 idle / false", state, public)
	}
}

// TestFinishOnlyRetrySendsNothing 确认收尾失败重跑只补收尾，图床调用次数不增加。
func TestFinishOnlyRetrySendsNothing(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id := e.queuedPost(t, 2, "photo")
	if _, err := e.worker.ProcessOnce(ctx); err != nil {
		t.Fatalf("首次处理失败: %v", err)
	}
	if e.provider.callCount() != 2 {
		t.Fatalf("首次上传调用 = %d，期望 2", e.provider.callCount())
	}

	// 模拟「图都存好、仅收尾写库失败」的终态。
	if _, err := e.handle.ExecContext(ctx,
		`UPDATE posts SET status = ?, error_message = ? WHERE id = ?`,
		posts.StatusFailed, "同步收尾写库失败，请到任务页重试", id); err != nil {
		t.Fatalf("写入失败态出错: %v", err)
	}
	ok, err := e.repo.RequeueFinish(ctx, id)
	if err != nil || !ok {
		t.Fatalf("收尾重排失败：ok=%v err=%v", ok, err)
	}
	before := e.provider.callCount()
	if _, err := e.worker.ProcessOnce(ctx); err != nil {
		t.Fatalf("重跑收尾出错: %v", err)
	}
	if e.provider.callCount() != before {
		t.Fatalf("重跑收尾又调用了图床：%d → %d", before, e.provider.callCount())
	}
	post, err := e.repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("读取帖子失败: %v", err)
	}
	if post.Status != posts.StatusCompleted {
		t.Errorf("重跑收尾后状态 = %s，期望已完成", post.Status)
	}
}

