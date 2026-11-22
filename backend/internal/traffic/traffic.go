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
	Alert func(month string, bytesOut, alertBytes int64)
	// Now 便于测试替换时钟。
	Now func() time.Time
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
	lastFlush time.Time
}

// ErrUnavailable 表示统计存储不可用。
var ErrUnavailable = errors.New("流量统计不可用")

// New 创建统计器。handle 为空时只做内存计数，不刷盘。
func New(handle *sql.DB, opts Options) *Recorder {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Recorder{handle: handle, opts: opts, lastFlush: now()}
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
		return errors.New("读取流量统计失败")
	}
	r.alertSent = alertSent != 0
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

	now := r.opts.Now
	if now == nil {
		now = time.Now
	}
	clock := now()
	if err := r.loadLocked(ctx, clock); err != nil {
		// 统计失败不影响图片转发，随后继续按内存计数。
		r.bytesOut += bytes
		r.requests++
		return r.overLimitLocked()
	}
	if bytes > 0 {
		r.bytesOut += bytes
	}
	r.requests++
	r.pending += bytes
	r.updatedAt = clock

	needFlush := clock.Sub(r.lastFlush) >= FlushInterval || r.pending >= FlushBytes
	if needFlush {
		_ = r.flushLocked(ctx)
	}

	r.maybeAlertLocked(clock)
	return r.overLimitLocked()
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

// maybeAlertLocked 在首次越过预警阈值时回调一次，同一个月只提醒一次。
func (r *Recorder) maybeAlertLocked(now time.Time) {
	if r.opts.AlertBytes <= 0 || r.alertSent || r.bytesOut < r.opts.AlertBytes {
		return
	}
	r.alertSent = true
	r.updatedAt = now
	_ = r.flushLocked(context.Background())
	if r.opts.Alert != nil {
		month, bytes, threshold := r.month, r.bytesOut, r.opts.AlertBytes
		alert := r.opts.Alert
		go alert(month, bytes, threshold)
	}
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
