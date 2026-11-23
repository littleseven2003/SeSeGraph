// Package traffic 按自然月统计图片网关的出站流量。
// 计数在内存累加，按时间或字节数刷盘 SQLite；达到预警阈值时回调告警，超过上限时由网关熔断。
package traffic

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"
)

// 刷盘策略：距上次刷盘超过 30 秒，或累计未落库字节达到 5MB。
const (
	FlushInterval = 30 * time.Second
	FlushBytes    = 5 << 20
	// AlertRetryDelay 是预警发送失败后的重试间隔。
	// 失败时不算送达，隔一段时间再试，避免每张图片都触发一次失败的发送。
	AlertRetryDelay = 10 * time.Minute
	// GB 是阈值换算单位。
	GB = int64(1) << 30
)

// Stats 是某个月的流量快照。阈值只做展示，不参与统计。
type Stats struct {
	Month        string `json:"month"`
	BytesOut     int64  `json:"bytes_out"`
	RequestCount int64  `json:"request_count"`
	AlertSent    bool   `json:"alert_sent"`
	AlertBytes   int64  `json:"alert_bytes"`
	LimitBytes   int64  `json:"limit_bytes"`
	UpdatedAt    string `json:"updated_at"`
}

// Options 汇总阈值与告警回调。
type Options struct {
	// AlertBytes 是月度预警阈值，非正表示不预警。
	AlertBytes int64
	// LimitBytes 是月度硬上限，非正表示不熔断。
	LimitBytes int64
	// Alert 在当月首次越过预警阈值时调用，回调在独立协程里执行，不应阻塞请求。
	// 返回错误表示没有送达：此时不记为已提醒，隔 AlertRetry 后重试。
	Alert func(month string, bytesOut, alertBytes int64) error
	// Now 便于测试替换时钟。
	Now func() time.Time
	// FlushEvery 覆盖后台刷盘间隔，非正时使用 FlushInterval。
	FlushEvery time.Duration
	// AlertRetry 覆盖告警重试间隔，非正时使用 AlertRetryDelay。
	AlertRetry time.Duration
}

// Recorder 维护当月流量计数与刷盘节奏。单个实例服务整个进程。
type Recorder struct {
	handle *sql.DB
	opts   Options

	mu        sync.Mutex
	month     string
	bytesOut  int64
	requests  int64
	alertSent bool
	updatedAt time.Time
	pending   int64
	dirty     bool
	lastFlush time.Time

	// alerting 表示一次告警发送正在进行，避免并发重复发送。
	alerting bool
	// alertRetryAt 是下一次允许尝试告警的时刻；上次发送失败时推后。
	alertRetryAt time.Time

	// started 与 stop 控制后台定时落盘的协程。
	started bool
	stop    chan struct{}
}

// ErrUnavailable 表示统计存储不可用。
var ErrUnavailable = errors.New("流量统计不可用")

// New 创建统计器。handle 为空时只做内存计数，不刷盘。
func New(handle *sql.DB, opts Options) *Recorder {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Recorder{handle: handle, opts: opts, lastFlush: now(), stop: make(chan struct{})}
}

// interval 返回后台刷盘间隔。
func (r *Recorder) interval() time.Duration {
	if r.opts.FlushEvery > 0 {
		return r.opts.FlushEvery
	}
	return FlushInterval
}

// retryDelay 返回告警失败后的重试间隔。
func (r *Recorder) retryDelay() time.Duration {
	if r.opts.AlertRetry > 0 {
		return r.opts.AlertRetry
	}
	return AlertRetryDelay
}

// Start 启动后台定时落盘。低流量下没有新的 Add 也能把余量写进数据库。
// 返回的函数可停止协程并等待其退出，重复调用返回空函数。
func (r *Recorder) Start(ctx context.Context) func() {
	if r == nil {
		return func() {}
	}
	r.mu.Lock()
	if r.started {
		r.mu.Unlock()
		return func() {}
	}
	r.started = true
	r.mu.Unlock()

	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(r.interval())
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-r.stop:
				return
			case <-ticker.C:
				r.idleFlush(ctx)
			}
		}
	}()

	var once sync.Once
	return func() {
		once.Do(func() {
			close(r.stop)
			<-done
		})
	}
}

// idleFlush 在空闲时钟点把待落盘的计数写入数据库，并检查是否需要补发预警。
// 预警重试不依赖新的图片请求：并发熔断后即使没有流量，也能在重试间隔到达后补发。
func (r *Recorder) idleFlush(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	clock := r.clock()
	if err := r.loadLocked(ctx, clock); err != nil {
		return
	}
	if r.dirty {
		_ = r.flushLocked(ctx)
	}
	r.maybeAlertLocked(clock)
}

// currentMonth 返回统计使用的自然月标识。
func (r *Recorder) currentMonth(now time.Time) string {
	return now.Format("2006-01")
}

// loadLocked 在月份变化或首次访问时从数据库读取该月基数。
func (r *Recorder) loadLocked(ctx context.Context, now time.Time) error {
	month := r.currentMonth(now)
	if r.month == month {
		return nil
	}
	// 换月前把上一个月的余量落盘，避免跨月丢失。
	if r.month != "" && r.pending > 0 {
		if err := r.flushLocked(ctx); err != nil {
			return err
		}
	}
	r.month = month
	r.bytesOut = 0
	r.requests = 0
	r.alertSent = false
	r.pending = 0
	r.dirty = false
	r.updatedAt = time.Time{}

	if r.handle == nil {
		return nil
	}
	var alertSent int64
	var updated string
	err := r.handle.QueryRowContext(ctx,
		`SELECT bytes_out, request_count, alert_sent, updated_at FROM traffic_stats WHERE month = ?`,
		month).Scan(&r.bytesOut, &r.requests, &alertSent, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		r.bytesOut = 0
		r.requests = 0
		r.dirty = false
		return errors.New("读取流量统计失败")
	}
	r.alertSent = alertSent != 0
	r.dirty = false
	if parsed, parseErr := time.Parse("2006-01-02 15:04:05", updated); parseErr == nil {
		r.updatedAt = parsed
	}
	return nil
}

// Add 记录一次成功转发的出站字节数，并按需刷盘与触发预警。
// 返回是否需要熔断（当前月已超过硬上限）。
func (r *Recorder) Add(ctx context.Context, bytes int64) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.recordLocked(ctx, bytes, true)
}

// Begin 在开始转发前登记一次请求，并返回是否允许继续。
// 返回 false 表示当月已达到硬上限：调用方不应再向上游取流。
func (r *Recorder) Begin(ctx context.Context) bool {
	if r == nil {
		return true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return !r.recordLocked(ctx, 0, true)
}

// AddBytes 累加已经写给客户端的字节，不重复计请求。
// 网关按固定缓冲区逐块调用，使并发传输中的用量始终是最新的。
func (r *Recorder) AddBytes(ctx context.Context, bytes int64) {
	if r == nil || bytes <= 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recordLocked(ctx, bytes, false)
}

// recordLocked 在持锁状态下累加计数、按节奏刷盘并检查预警与上限。
// request 为真时请求数加一；返回是否需要熔断。
func (r *Recorder) recordLocked(ctx context.Context, bytes int64, request bool) bool {
	clock := r.clock()
	if err := r.loadLocked(ctx, clock); err != nil {
		// 统计失败不影响图片转发，随后继续按内存计数。
		if bytes > 0 {
			r.bytesOut += bytes
			r.pending += bytes
		}
		if request {
			r.requests++
		}
		r.dirty = true
		return r.overLimitLocked()
	}
	if bytes > 0 {
		r.bytesOut += bytes
		r.pending += bytes
	}
	if request {
		r.requests++
	}
	r.dirty = true
	r.updatedAt = clock

	needFlush := clock.Sub(r.lastFlush) >= r.interval() || r.pending >= FlushBytes
	if needFlush {
		_ = r.flushLocked(ctx)
	}

	r.maybeAlertLocked(clock)
	return r.overLimitLocked()
}

// SetLimits 热更新月度预警与硬上限，非正值表示关闭对应阈值。
// 阈值调高后允许按新值重新提醒一次，调低则立即生效。
func (r *Recorder) SetLimits(alertBytes, limitBytes int64) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.opts.AlertBytes = alertBytes
	r.opts.LimitBytes = limitBytes
	if alertBytes <= 0 || r.bytesOut < alertBytes {
		r.alertSent = false
	}
}

// OverLimit 报告当月是否已超过硬上限。
func (r *Recorder) OverLimit() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.loadLocked(context.Background(), r.clock()); err != nil {
		return r.overLimitLocked()
	}
	return r.overLimitLocked()
}

// overLimitLocked 判断是否超过硬上限。
func (r *Recorder) overLimitLocked() bool {
	return r.opts.LimitBytes > 0 && r.bytesOut >= r.opts.LimitBytes
}

// clock 返回当前时间。
func (r *Recorder) clock() time.Time {
	if r.opts.Now != nil {
		return r.opts.Now()
	}
	return time.Now()
}

// maybeAlertLocked 在越过预警阈值时回调一次，同一个月只提醒一次。
// 只有发送成功才持久化已提醒标记；失败后由后台协程在重试间隔到达时再次尝试，
// 因此熔断后没有新的图片请求也仍能补发。
func (r *Recorder) maybeAlertLocked(now time.Time) {
	if r.opts.AlertBytes <= 0 || r.alertSent || r.alerting || r.bytesOut < r.opts.AlertBytes {
		return
	}
	if !r.alertRetryAt.IsZero() && now.Before(r.alertRetryAt) {
		return
	}
	if r.opts.Alert == nil {
		r.alertSent = true
		r.dirty = true
		_ = r.flushLocked(context.Background())
		return
	}

	r.alerting = true
	r.updatedAt = now
	month, bytes, threshold := r.month, r.bytesOut, r.opts.AlertBytes
	alert := r.opts.Alert
	go func() {
		err := alert(month, bytes, threshold)
		r.mu.Lock()
		defer r.mu.Unlock()
		r.alerting = false
		if err != nil {
			r.alertRetryAt = r.clock().Add(r.retryDelay())
			return
		}
		// 只在仍是同一个月时记录，避免把新月份的标记写错。
		if r.month == month {
			r.alertSent = true
			r.dirty = true
			_ = r.flushLocked(context.Background())
		}
	}()
}

// flushLocked 把内存中的当月累计写入数据库。时间为零时跳过，避免写入无意义的行。
func (r *Recorder) flushLocked(ctx context.Context) error {
	if r.handle == nil || r.month == "" {
		return nil
	}
	alertSent := int64(0)
	if r.alertSent {
		alertSent = 1
	}
	if _, err := r.handle.ExecContext(ctx,
		`INSERT INTO traffic_stats (month, bytes_out, request_count, alert_sent, updated_at)
		 VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(month) DO UPDATE SET
		   bytes_out = excluded.bytes_out,
		   request_count = excluded.request_count,
		   alert_sent = excluded.alert_sent,
		   updated_at = CURRENT_TIMESTAMP`,
		r.month, r.bytesOut, r.requests, alertSent); err != nil {
		return errors.New("保存流量统计失败")
	}
	r.pending = 0
	r.dirty = false
	r.lastFlush = r.clock()
	return nil
}

// Flush 立即把内存计数写入数据库。
func (r *Recorder) Flush(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.loadLocked(ctx, r.clock()); err != nil {
		return err
	}
	return r.flushLocked(ctx)
}

// Close 在进程退出前把余量落盘。
func (r *Recorder) Close(ctx context.Context) error {
	return r.Flush(ctx)
}

// Stats 返回当月统计快照，并补齐对外展示的阈值。
func (r *Recorder) Stats(ctx context.Context) (Stats, error) {
	if r == nil {
		return Stats{Month: time.Now().Format("2006-01")}, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	clock := r.clock()
	if err := r.loadLocked(ctx, clock); err != nil {
		return Stats{}, err
	}
	updated := ""
	if !r.updatedAt.IsZero() {
		updated = r.updatedAt.UTC().Format(time.RFC3339)
	}
	return Stats{
		Month:        r.month,
		BytesOut:     r.bytesOut,
		RequestCount: r.requests,
		AlertSent:    r.alertSent,
		AlertBytes:   r.opts.AlertBytes,
		LimitBytes:   r.opts.LimitBytes,
		UpdatedAt:    updated,
	}, nil
}
