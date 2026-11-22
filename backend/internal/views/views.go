// Package views 负责把 Telegraph 浏览量低频同步到本地缓存，并为手动刷新提供冷却与去重。
package views

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
)

const (
	// DefaultInterval 是后台同步的默认轮询间隔。
	DefaultInterval = 30 * time.Minute
	// DefaultSpacing 是单条之间的默认间隔，避免连续打满上游。
	DefaultSpacing = time.Second
	// DefaultCooldown 是同一帖两次刷新的最短间隔。
	DefaultCooldown = 60 * time.Second
	// DefaultBatchSize 是每次从库里读取的目标条数。
	DefaultBatchSize = 50
)

var (
	// ErrNotApplicable 表示该帖不在统计范围（未发布、未公开或已撤回）。
	ErrNotApplicable = errors.New("图集状态不适用浏览量统计")
	// ErrFetch 表示上游读取失败，调用方只回显脱敏文案并保留旧缓存。
	ErrFetch = errors.New("读取浏览量失败")
)

// Store 是同步需要的本地读写能力。
type Store interface {
	ListViewTargets(ctx context.Context, afterID int64, limit int) ([]posts.ViewTarget, error)
	ViewTargetByID(ctx context.Context, postID int64) (posts.ViewTarget, bool, error)
	ReadViews(ctx context.Context, postID int64) (posts.ViewSnapshot, error)
	SaveViews(ctx context.Context, target posts.ViewTarget, count int, at time.Time) (bool, error)
}

// Fetcher 读取一页的累计浏览量。
type Fetcher interface {
	GetViews(ctx context.Context, path string) (int, error)
}

// Options 是同步服务的装配参数。
type Options struct {
	Store     Store
	Client    func(ctx context.Context) (Fetcher, error)
	Logger    *log.Logger
	Interval  time.Duration
	Spacing   time.Duration
	Cooldown  time.Duration
	BatchSize int
	Now       func() time.Time
	Sleep     func(ctx context.Context, d time.Duration) error
}

// Result 是一次刷新的结果，Cached 表示没有联网、直接返回了本地缓存。
type Result struct {
	PostID    int64
	Count     int
	UpdatedAt string
	Cached    bool
}

// call 是一次进行中的同步，等待者共享同一份结果。
type call struct {
	done   chan struct{}
	result Result
	err    error
}

// Service 串行执行后台同步，并给手动刷新提供冷却与同帖去重。
type Service struct {
	store     Store
	client    func(ctx context.Context) (Fetcher, error)
	logger    *log.Logger
	interval  time.Duration
	spacing   time.Duration
	cooldown  time.Duration
	batchSize int
	now       func() time.Time
	sleep     func(ctx context.Context, d time.Duration) error

	mu       sync.Mutex
	last     map[int64]time.Time
	inflight map[int64]*call
}

// New 创建同步服务，未填写的参数使用默认值。
func New(opts Options) (*Service, error) {
	if opts.Store == nil || opts.Client == nil {
		return nil, errors.New("浏览量同步缺少依赖")
	}
	service := &Service{
		store:     opts.Store,
		client:    opts.Client,
		logger:    opts.Logger,
		interval:  opts.Interval,
		spacing:   opts.Spacing,
		cooldown:  opts.Cooldown,
		batchSize: opts.BatchSize,
		now:       opts.Now,
		sleep:     opts.Sleep,
		last:      make(map[int64]time.Time),
		inflight:  make(map[int64]*call),
	}
	if service.interval <= 0 {
		service.interval = DefaultInterval
	}
	if service.spacing < 0 {
		service.spacing = DefaultSpacing
	}
	if service.cooldown <= 0 {
		service.cooldown = DefaultCooldown
	}
	if service.batchSize <= 0 {
		service.batchSize = DefaultBatchSize
	}
	if service.now == nil {
		service.now = time.Now
	}
	if service.sleep == nil {
		service.sleep = sleepContext
	}
	if service.logger == nil {
		service.logger = log.Default()
	}
	return service, nil
}

// Run 启动后台同步：先跑一轮，之后按间隔循环，直到 ctx 结束。
func (s *Service) Run(ctx context.Context) {
	s.SyncOnce(ctx)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.SyncOnce(ctx)
		}
	}
}

// SyncOnce 同步一轮：按 ID 小批读取目标，单路串行，单条失败只记日志。
func (s *Service) SyncOnce(ctx context.Context) (synced, failed int) {
	if _, err := s.client(ctx); err != nil {
		s.logger.Print("浏览量同步跳过：Telegraph 未配置")
		return 0, 0
	}

	var afterID int64
	for {
		targets, err := s.store.ListViewTargets(ctx, afterID, s.batchSize)
		if err != nil {
			s.logger.Print("浏览量同步读取目标失败")
			return synced, failed
		}
		if len(targets) == 0 {
			return synced, failed
		}
		for _, target := range targets {
			afterID = target.PostID
			if ctx.Err() != nil {
				return synced, failed
			}
			entry := s.begin(target.PostID)
			if entry == nil {
				continue
			}
			if _, err := s.execute(ctx, entry, target); err != nil {
				failed++
				s.logger.Print("浏览量同步失败：单帖上游请求未完成")
			} else {
				synced++
			}
			if err := s.sleep(ctx, s.spacing); err != nil {
				return synced, failed
			}
		}
	}
}

// Refresh 手动刷新单帖。冷却期内与并发请求都直接返回已有结果，不重复打上游。
func (s *Service) Refresh(ctx context.Context, postID int64) (Result, error) {
	target, eligible, err := s.store.ViewTargetByID(ctx, postID)
	if err != nil {
		return Result{}, err
	}
	if !eligible {
		return Result{}, ErrNotApplicable
	}

	s.mu.Lock()
	if entry, ok := s.inflight[postID]; ok {
		s.mu.Unlock()
		return s.wait(ctx, entry)
	}
	if last, ok := s.last[postID]; ok && s.now().Sub(last) < s.cooldown {
		s.mu.Unlock()
		return s.cached(ctx, postID)
	}
	entry := s.start(postID)
	s.mu.Unlock()
	return s.execute(ctx, entry, target)
}

// begin 为后台同步占用一帖，冷却期内或已在同步时返回 nil。
func (s *Service) begin(postID int64) *call {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.inflight[postID]; ok {
		return nil
	}
	if last, ok := s.last[postID]; ok && s.now().Sub(last) < s.cooldown {
		return nil
	}
	return s.start(postID)
}

// start 登记一次进行中的同步，调用方需要持有锁。
func (s *Service) start(postID int64) *call {
	entry := &call{done: make(chan struct{})}
	s.inflight[postID] = entry
	s.last[postID] = s.now()
	return entry
}

// execute 读取上游并写回缓存，结果与错误都留给等待者复用。
func (s *Service) execute(ctx context.Context, entry *call, target posts.ViewTarget) (Result, error) {
	defer func() {
		s.mu.Lock()
		delete(s.inflight, target.PostID)
		s.mu.Unlock()
		close(entry.done)
	}()

	fetcher, err := s.client(ctx)
	if err != nil {
		entry.err = ErrFetch
		return Result{}, ErrFetch
	}
	count, err := fetcher.GetViews(ctx, target.Path)
	if err != nil {
		entry.err = ErrFetch
		return Result{}, ErrFetch
	}
	if _, err := s.store.SaveViews(ctx, target, count, s.now()); err != nil {
		entry.err = err
		return Result{}, err
	}
	snapshot, err := s.store.ReadViews(ctx, target.PostID)
	if err != nil {
		entry.err = err
		return Result{}, err
	}
	entry.result = Result{PostID: target.PostID, Count: snapshot.Count, UpdatedAt: snapshot.UpdatedAt}
	return entry.result, nil
}

// wait 等待同帖的进行中请求，复用它已经拿到的结果。
func (s *Service) wait(ctx context.Context, entry *call) (Result, error) {
	select {
	case <-entry.done:
		return entry.result, entry.err
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
}

// cached 读取本地缓存，UpdatedAt 为空表示还没有同步过。
func (s *Service) cached(ctx context.Context, postID int64) (Result, error) {
	snapshot, err := s.store.ReadViews(ctx, postID)
	if err != nil {
		return Result{}, err
	}
	return Result{PostID: postID, Count: snapshot.Count, UpdatedAt: snapshot.UpdatedAt, Cached: true}, nil
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
