package views

import (
	"context"
	"errors"
	"io"
	"log"
	"sync"
	"testing"
	"time"

	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
)

// fakeStore 是内存版仓储，用来观察同步是否落库。
type fakeStore struct {
	mu         sync.Mutex
	targets    []posts.ViewTarget
	ineligible map[int64]bool
	snapshots  map[int64]posts.ViewSnapshot
	saveCalls  int
	lastSaved  int
	skipSave   bool
}

func newFakeStore(targets ...posts.ViewTarget) *fakeStore {
	return &fakeStore{
		targets:    targets,
		ineligible: make(map[int64]bool),
		snapshots:  make(map[int64]posts.ViewSnapshot),
	}
}

func (s *fakeStore) ListViewTargets(_ context.Context, afterID int64, limit int) ([]posts.ViewTarget, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]posts.ViewTarget, 0, limit)
	scanned := afterID
	for _, target := range s.targets {
		if target.PostID <= afterID {
			continue
		}
		if len(result) >= limit {
			break
		}
		result = append(result, target)
		scanned = target.PostID
	}
	return result, scanned, nil
}

func (s *fakeStore) ViewTargetByID(_ context.Context, postID int64) (posts.ViewTarget, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, target := range s.targets {
		if target.PostID == postID {
			return target, !s.ineligible[postID], nil
		}
	}
	return posts.ViewTarget{}, false, posts.ErrNotFound
}

func (s *fakeStore) ReadViews(_ context.Context, postID int64) (posts.ViewSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshots[postID], nil
}

func (s *fakeStore) SaveViews(_ context.Context, target posts.ViewTarget, count int, at time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.skipSave {
		return false, nil
	}
	s.saveCalls++
	s.lastSaved = count
	s.snapshots[target.PostID] = posts.ViewSnapshot{Count: count, UpdatedAt: at.UTC().Format("2006-01-02T15:04:05Z")}
	return true, nil
}

// fakeFetcher 记录调用次数，可通过 gate 阻塞以观察并发去重。
type fakeFetcher struct {
	mu     sync.Mutex
	calls  int
	counts map[string]int
	err    error
	gate   chan struct{}
}

func (f *fakeFetcher) GetViews(ctx context.Context, path string) (int, error) {
	f.mu.Lock()
	f.calls++
	gate := f.gate
	f.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	if f.err != nil {
		return 0, f.err
	}
	return f.counts[path], nil
}

func (f *fakeFetcher) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func newTestService(t *testing.T, store Store, fetcher Fetcher) *Service {
	t.Helper()
	service, err := New(Options{
		Store:  store,
		Client: func(context.Context) (Fetcher, error) { return fetcher, nil },
		Logger: log.New(io.Discard, "", 0),
		Now:    func() time.Time { return time.Date(2026, 11, 22, 8, 0, 0, 0, time.UTC) },
		Sleep:  func(context.Context, time.Duration) error { return nil },
		Spacing: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("创建服务失败: %v", err)
	}
	return service
}

func TestSyncOnceWritesConfirmedZero(t *testing.T) {
	store := newFakeStore(posts.ViewTarget{PostID: 1, Path: "Page-11-22"})
	fetcher := &fakeFetcher{counts: map[string]int{"Page-11-22": 0}}
	service := newTestService(t, store, fetcher)

	synced, failed := service.SyncOnce(context.Background())
	if synced != 1 || failed != 0 {
		t.Fatalf("同步结果 = %d/%d，期望 1/0", synced, failed)
	}
	snapshot, _ := store.ReadViews(context.Background(), 1)
	if snapshot.Count != 0 || snapshot.UpdatedAt != "2026-11-22T08:00:00Z" {
		t.Fatalf("首次零浏览未落库：%+v", snapshot)
	}
}

func TestSyncOnceKeepsGoingAfterFailure(t *testing.T) {
	store := newFakeStore(
		posts.ViewTarget{PostID: 1, Path: "Bad-11-22"},
		posts.ViewTarget{PostID: 2, Path: "Good-11-22"},
	)
	fetcher := &failingFetcher{failPath: "Bad-11-22", counts: map[string]int{"Good-11-22": 4}}
	service := newTestService(t, store, fetcher)

	synced, failed := service.SyncOnce(context.Background())
	if synced != 1 || failed != 1 {
		t.Fatalf("同步结果 = %d/%d，期望 1/1", synced, failed)
	}
	snapshot, _ := store.ReadViews(context.Background(), 2)
	if snapshot.Count != 4 {
		t.Fatalf("后续条目未落库：%+v", snapshot)
	}
}

// failingFetcher 只让指定路径失败，其余正常返回。
type failingFetcher struct {
	failPath string
	counts   map[string]int
}

func (f *failingFetcher) GetViews(_ context.Context, path string) (int, error) {
	if path == f.failPath {
		return 0, errors.New("上游失败")
	}
	return f.counts[path], nil
}

func TestRefreshDeduplicatesConcurrentCalls(t *testing.T) {
	store := newFakeStore(posts.ViewTarget{PostID: 7, Path: "Page-11-22"})
	gate := make(chan struct{})
	fetcher := &fakeFetcher{counts: map[string]int{"Page-11-22": 9}, gate: gate}
	service := newTestService(t, store, fetcher)

	first := make(chan Result, 1)
	second := make(chan Result, 1)
	go func() {
		result, err := service.Refresh(context.Background(), 7)
		if err != nil {
			t.Errorf("第一次刷新失败: %v", err)
		}
		first <- result
	}()
	waitForCalls(t, fetcher, 1)
	go func() {
		result, err := service.Refresh(context.Background(), 7)
		if err != nil {
			t.Errorf("并发刷新失败: %v", err)
		}
		second <- result
	}()
	time.Sleep(20 * time.Millisecond)
	close(gate)

	a, b := <-first, <-second
	if a.Count != 9 || b.Count != 9 {
		t.Fatalf("并发结果 = %d/%d，期望 9", a.Count, b.Count)
	}
	if calls := fetcher.callCount(); calls != 1 {
		t.Fatalf("上游调用 %d 次，期望只打一次", calls)
	}
}

func waitForCalls(t *testing.T, fetcher *fakeFetcher, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if fetcher.callCount() >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("上游调用次数未达到 %d", want)
}

func TestRefreshCooldownReturnsCached(t *testing.T) {
	store := newFakeStore(posts.ViewTarget{PostID: 3, Path: "Page-11-22"})
	fetcher := &fakeFetcher{counts: map[string]int{"Page-11-22": 5}}
	service := newTestService(t, store, fetcher)

	first, err := service.Refresh(context.Background(), 3)
	if err != nil || first.Count != 5 || first.Cached {
		t.Fatalf("首次刷新 = %+v err=%v", first, err)
	}
	second, err := service.Refresh(context.Background(), 3)
	if err != nil {
		t.Fatalf("冷却期刷新失败: %v", err)
	}
	if !second.Cached || second.Count != 5 {
		t.Fatalf("冷却期结果 = %+v，期望命中缓存", second)
	}
	if calls := fetcher.callCount(); calls != 1 {
		t.Fatalf("冷却期仍打了上游：%d 次", calls)
	}
}

func TestRefreshKeepsCacheOnFetchFailure(t *testing.T) {
	store := newFakeStore(posts.ViewTarget{PostID: 4, Path: "Page-11-22"})
	store.snapshots[4] = posts.ViewSnapshot{Count: 12, UpdatedAt: "2026-11-21T08:00:00Z"}
	fetcher := &fakeFetcher{err: errors.New("上游失败")}
	service := newTestService(t, store, fetcher)

	if _, err := service.Refresh(context.Background(), 4); !errors.Is(err, ErrFetch) {
		t.Fatalf("错误 = %v，期望 ErrFetch", err)
	}
	snapshot, _ := store.ReadViews(context.Background(), 4)
	if snapshot.Count != 12 || snapshot.UpdatedAt != "2026-11-21T08:00:00Z" {
		t.Fatalf("失败后缓存被改写：%+v", snapshot)
	}

	// 失败同样进入冷却，避免连点持续打上游。
	result, err := service.Refresh(context.Background(), 4)
	if err != nil {
		t.Fatalf("冷却期刷新失败: %v", err)
	}
	if !result.Cached || result.Count != 12 {
		t.Fatalf("冷却期结果 = %+v", result)
	}
	if calls := fetcher.callCount(); calls != 1 {
		t.Fatalf("失败后未限频：%d 次", calls)
	}
}

func TestRefreshRejectsIneligiblePost(t *testing.T) {
	store := newFakeStore(posts.ViewTarget{PostID: 5, Path: "Page-11-22"})
	store.ineligible[5] = true
	service := newTestService(t, store, &fakeFetcher{counts: map[string]int{"Page-11-22": 1}})

	if _, err := service.Refresh(context.Background(), 5); !errors.Is(err, ErrNotApplicable) {
		t.Fatalf("错误 = %v，期望 ErrNotApplicable", err)
	}
	if _, err := service.Refresh(context.Background(), 99); !errors.Is(err, posts.ErrNotFound) {
		t.Fatalf("不存在帖子的错误 = %v，期望 ErrNotFound", err)
	}
}

// fakeClock 让间隔与冷却按注入的时间推进，避免用例真的睡觉。
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	sleeps []time.Duration
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 11, 22, 8, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Sleep(_ context.Context, d time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sleeps = append(c.sleeps, d)
	c.now = c.now.Add(d)
	return nil
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func (c *fakeClock) recorded() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.sleeps...)
}

func newClockedService(t *testing.T, store Store, fetcher Fetcher, clock *fakeClock, spacing time.Duration) *Service {
	t.Helper()
	service, err := New(Options{
		Store:   store,
		Client:  func(context.Context) (Fetcher, error) { return fetcher, nil },
		Logger:  log.New(io.Discard, "", 0),
		Now:     clock.Now,
		Sleep:   clock.Sleep,
		Spacing: spacing,
	})
	if err != nil {
		t.Fatalf("创建服务失败: %v", err)
	}
	return service
}

func TestSyncOnceSpacesUpstreamCallsByDefault(t *testing.T) {
	store := newFakeStore(
		posts.ViewTarget{PostID: 1, Path: "A-1"},
		posts.ViewTarget{PostID: 2, Path: "A-2"},
		posts.ViewTarget{PostID: 3, Path: "A-3"},
	)
	fetcher := &fakeFetcher{counts: map[string]int{"A-1": 1, "A-2": 2, "A-3": 3}}
	clock := newFakeClock()
	// 不传 Spacing：默认 1 秒必须生效，避免后台连打上游。
	service := newClockedService(t, store, fetcher, clock, 0)

	if synced, failed := service.SyncOnce(context.Background()); synced != 3 || failed != 0 {
		t.Fatalf("同步结果 = %d/%d", synced, failed)
	}
	sleeps := clock.recorded()
	if len(sleeps) != 2 {
		t.Fatalf("间隔次数 = %d，期望 2", len(sleeps))
	}
	for _, d := range sleeps {
		if d < DefaultSpacing {
			t.Fatalf("间隔 = %v，期望不少于 %v", d, DefaultSpacing)
		}
	}
}

func TestManualRefreshSharesUpstreamGate(t *testing.T) {
	store := newFakeStore(
		posts.ViewTarget{PostID: 1, Path: "A-1"},
		posts.ViewTarget{PostID: 2, Path: "A-2"},
	)
	fetcher := &fakeFetcher{counts: map[string]int{"A-1": 1, "A-2": 2}}
	clock := newFakeClock()
	service := newClockedService(t, store, fetcher, clock, 0)

	if _, err := service.Refresh(context.Background(), 1); err != nil {
		t.Fatalf("首次刷新失败: %v", err)
	}
	if len(clock.recorded()) != 0 {
		t.Fatalf("首个请求不应等待：%v", clock.recorded())
	}
	if _, err := service.Refresh(context.Background(), 2); err != nil {
		t.Fatalf("第二次刷新失败: %v", err)
	}
	sleeps := clock.recorded()
	if len(sleeps) != 1 || sleeps[0] < DefaultSpacing {
		t.Fatalf("手刷未与后台共用间隔入口：%v", sleeps)
	}
}

func TestCooldownRecordsPrunedAfterWindow(t *testing.T) {
	store := newFakeStore(posts.ViewTarget{PostID: 1, Path: "A-1"})
	fetcher := &fakeFetcher{counts: map[string]int{"A-1": 7}}
	clock := newFakeClock()
	service := newClockedService(t, store, fetcher, clock, time.Millisecond)

	if _, err := service.Refresh(context.Background(), 1); err != nil {
		t.Fatalf("首次刷新失败: %v", err)
	}
	clock.Advance(DefaultCooldown + time.Second)
	service.SyncOnce(context.Background())
	if calls := fetcher.callCount(); calls != 2 {
		t.Fatalf("冷却过期后应重新同步，实际调用 %d 次", calls)
	}
	service.mu.Lock()
	remaining := len(service.last)
	service.mu.Unlock()
	if remaining != 1 {
		t.Fatalf("回收后冷却记录 = %d，期望只保留进行中的那一条", remaining)
	}
}

func TestRefreshReportsNotApplicableWhenWriteSkipped(t *testing.T) {
	store := newFakeStore(posts.ViewTarget{PostID: 1, Path: "A-1"})
	store.skipSave = true
	service := newTestService(t, store, &fakeFetcher{counts: map[string]int{"A-1": 5}})

	if _, err := service.Refresh(context.Background(), 1); !errors.Is(err, ErrNotApplicable) {
		t.Fatalf("错误 = %v，期望 ErrNotApplicable", err)
	}
}

func TestCachedAfterFailureReportsFailed(t *testing.T) {
	store := newFakeStore(posts.ViewTarget{PostID: 1, Path: "A-1"})
	store.snapshots[1] = posts.ViewSnapshot{Count: 42, UpdatedAt: "2026-11-21T08:00:00Z"}
	service := newTestService(t, store, &fakeFetcher{err: errors.New("上游失败")})

	if _, err := service.Refresh(context.Background(), 1); !errors.Is(err, ErrFetch) {
		t.Fatalf("首刷错误 = %v", err)
	}
	result, err := service.Refresh(context.Background(), 1)
	if err != nil {
		t.Fatalf("冷却期刷新失败: %v", err)
	}
	if !result.Cached || !result.Failed || result.Count != 42 {
		t.Fatalf("冷却期结果 = %+v，期望标明失败并显示缓存", result)
	}
}
