package traffic

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/littleseven2003/SeSeGraph/backend/internal/db"
)

// testClock 提供可推进的时钟。
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock() *testClock {
	return &testClock{now: time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// newRecorder 基于测试库创建统计器。
func newRecorder(t *testing.T, clock *testClock, opts Options) *Recorder {
	t.Helper()
	handle, err := db.Open(filepath.Join(t.TempDir(), "traffic.db"))
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	if err := db.Migrate(context.Background(), handle); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	opts.Now = clock.Now
	return New(handle, opts)
}

func TestRecorderCountsAndPersists(t *testing.T) {
	ctx := context.Background()
	clock := newTestClock()
	recorder := newRecorder(t, clock, Options{})

	recorder.Add(ctx, 1024)
	recorder.Add(ctx, 2048)
	stats, err := recorder.Stats(ctx)
	if err != nil {
		t.Fatalf("读取统计失败: %v", err)
	}
	if stats.BytesOut != 3072 || stats.RequestCount != 2 {
		t.Fatalf("统计 = %+v，期望 3072 字节 / 2 次", stats)
	}
	if stats.Month != "2026-01" {
		t.Errorf("月份 = %q，期望 2026-01", stats.Month)
	}

	if err := recorder.Flush(ctx); err != nil {
		t.Fatalf("刷盘失败: %v", err)
	}

	// 新实例从数据库恢复当月基数。
	reloaded := New(recorder.handle, Options{Now: clock.Now})
	stats, err = reloaded.Stats(ctx)
	if err != nil {
		t.Fatalf("读取统计失败: %v", err)
	}
	if stats.BytesOut != 3072 || stats.RequestCount != 2 {
		t.Errorf("重启后统计 = %+v，期望沿用已落库数值", stats)
	}
}

func TestRecorderFlushesByInterval(t *testing.T) {
	ctx := context.Background()
	clock := newTestClock()
	recorder := newRecorder(t, clock, Options{})

	recorder.Add(ctx, 100)
	if _, err := recorder.Stats(ctx); err != nil {
		t.Fatalf("读取失败: %v", err)
	}

	// 未到刷盘间隔时数据库里还没有这一行。
	var count int
	if err := recorder.handle.QueryRowContext(ctx, `SELECT COUNT(*) FROM traffic_stats`).Scan(&count); err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if count != 0 {
		t.Fatalf("未到刷盘条件就写库: %d 行", count)
	}

	clock.Advance(FlushInterval)
	recorder.Add(ctx, 100)
	if err := recorder.handle.QueryRowContext(ctx, `SELECT COUNT(*) FROM traffic_stats`).Scan(&count); err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if count != 1 {
		t.Errorf("越过刷盘间隔后应落库，实际 %d 行", count)
	}
}

func TestRecorderFlushesByBytes(t *testing.T) {
	ctx := context.Background()
	clock := newTestClock()
	recorder := newRecorder(t, clock, Options{})

	// 单次即达到 5MB，应立刻落库，不必等 30 秒。
	recorder.Add(ctx, FlushBytes)

	var stored int64
	if err := recorder.handle.QueryRowContext(ctx,
		`SELECT bytes_out FROM traffic_stats WHERE month = '2026-01'`).Scan(&stored); err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if stored != FlushBytes {
		t.Errorf("已落库字节 = %d，期望 %d", stored, FlushBytes)
	}
}

func TestRecorderRollsOverByMonth(t *testing.T) {
	ctx := context.Background()
	clock := newTestClock()
	recorder := newRecorder(t, clock, Options{})

	recorder.Add(ctx, 500)
	clock.Advance(40 * 24 * time.Hour) // 跨月
	recorder.Add(ctx, 300)

	stats, err := recorder.Stats(ctx)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if stats.Month != "2026-02" || stats.BytesOut != 300 || stats.RequestCount != 1 {
		t.Errorf("二月统计 = %+v，期望 300 字节 / 1 次", stats)
	}

	var january int64
	if err := recorder.handle.QueryRowContext(ctx,
		`SELECT bytes_out FROM traffic_stats WHERE month = '2026-01'`).Scan(&january); err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if january != 500 {
		t.Errorf("一月字节 = %d，期望换月前落盘的 500", january)
	}
}

func TestRecorderAlertsOncePerMonth(t *testing.T) {
	ctx := context.Background()
	clock := newTestClock()
	alerts := make(chan Stats, 4)
	var mu sync.Mutex
	var called int
	recorder := newRecorder(t, clock, Options{
		AlertBytes: 1000,
		Alert: func(month string, bytesOut, alertBytes int64) error {
			mu.Lock()
			called++
			mu.Unlock()
			alerts <- Stats{Month: month, BytesOut: bytesOut, AlertBytes: alertBytes}
			return nil
		},
	})

	recorder.Add(ctx, 600)
	time.Sleep(20 * time.Millisecond)
	select {
	case got := <-alerts:
		t.Fatalf("未过阈值就已告警: %+v", got)
	default:
	}

	recorder.Add(ctx, 600)
	select {
	case got := <-alerts:
		if got.Month != "2026-01" || got.BytesOut != 1200 || got.AlertBytes != 1000 {
			t.Errorf("告警内容 = %+v，期望当月与阈值", got)
		}
	case <-time.After(time.Second):
		t.Fatal("越过阈值后应触发一次告警")
	}

	recorder.Add(ctx, 500)
	time.Sleep(20 * time.Millisecond)
	select {
	case got := <-alerts:
		t.Fatalf("同月不应重复告警: %+v", got)
	default:
	}

	// 送达后才登记已提醒标记，等待后台写入完成再回读。
	waitFor(t, func() bool {
		stats, err := recorder.Stats(ctx)
		return err == nil && stats.AlertSent
	})

	// 告警标记随统计落库，重启后不再重复提醒。
	reloaded := New(recorder.handle, Options{Now: clock.Now, AlertBytes: 1000, Alert: func(string, int64, int64) error {
		t.Error("重启后不应再次告警")
		return nil
	}})
	if _, err := reloaded.Stats(ctx); err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if !reloaded.alertSent {
		t.Error("重启后应沿用具已告警标记")
	}
}

func TestRecorderReportsOverLimit(t *testing.T) {
	ctx := context.Background()
	clock := newTestClock()
	recorder := newRecorder(t, clock, Options{LimitBytes: 1000})

	if recorder.OverLimit() {
		t.Fatal("初始不应超限")
	}
	if over := recorder.Add(ctx, 600); over {
		t.Error("600 字节不应超限")
	}
	if over := recorder.Add(ctx, 600); !over {
		t.Error("累计 1200 字节应超限")
	}
	if !recorder.OverLimit() {
		t.Error("超限后 OverLimit 应为真")
	}
	stats, _ := recorder.Stats(ctx)
	if stats.LimitBytes != 1000 {
		t.Errorf("快照里的上限 = %d，期望 1000", stats.LimitBytes)
	}
}

func TestRecorderWithoutHandle(t *testing.T) {
	ctx := context.Background()
	clock := newTestClock()
	recorder := New(nil, Options{Now: clock.Now})

	recorder.Add(ctx, 42)
	stats, err := recorder.Stats(ctx)
	if err != nil {
		t.Fatalf("无存储时不应报错: %v", err)
	}
	if stats.BytesOut != 42 {
		t.Errorf("字节 = %d，期望 42", stats.BytesOut)
	}
	if err := recorder.Flush(ctx); err != nil {
		t.Errorf("无存储时刷盘应静默成功: %v", err)
	}
}

// waitFor 轮询等待条件成立，超时则判定失败。
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("等待条件超时")
}

func TestRecorderFlushesWhenIdle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clock := newTestClock()
	recorder := newRecorder(t, clock, Options{FlushEvery: 10 * time.Millisecond})
	stop := recorder.Start(ctx)
	defer stop()

	// 时钟不推进，也不再有新的 Add：只能靠后台定时协程落盘。
	recorder.Add(ctx, 2048)
	waitFor(t, func() bool {
		var stored int64
		err := recorder.handle.QueryRowContext(ctx,
			`SELECT bytes_out FROM traffic_stats WHERE month = '2026-01'`).Scan(&stored)
		return err == nil && stored == 2048
	})
}

func TestRecorderRetriesAlertWithoutNewTraffic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clock := newTestClock()

	var mu sync.Mutex
	var attempts int
	recorder := newRecorder(t, clock, Options{
		AlertBytes: 1000,
		LimitBytes: 1500,
		FlushEvery: 10 * time.Millisecond,
		AlertRetry: time.Minute,
		Alert: func(string, int64, int64) error {
			mu.Lock()
			defer mu.Unlock()
			attempts++
			if attempts == 1 {
				return errors.New("通知发送失败")
			}
			return nil
		},
	})
	stop := recorder.Start(ctx)
	defer stop()

	// 一次越过预警与上限：此后网关不再接受新请求，也不会再计费。
	recorder.Add(ctx, 2000)
	if !recorder.OverLimit() {
		t.Fatal("应已超过硬上限")
	}
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return attempts == 1
	})
	waitUntilStored := func(want int64) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			var stored int64
			err := recorder.handle.QueryRowContext(ctx,
				`SELECT alert_sent FROM traffic_stats WHERE month = '2026-01'`).Scan(&stored)
			if err == nil && stored == want {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("数据库 alert_sent 未变为 %d", want)
	}
	waitUntilStored(0)

	// 没有任何新流量，只推进时钟，后台协程应补发并登记已提醒。
	clock.Advance(2 * time.Minute)
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return attempts == 2
	})
	waitUntilStored(1)
}

func TestRecorderBeginRejectsAtLimitDuringStream(t *testing.T) {
	ctx := context.Background()
	clock := newTestClock()
	recorder := newRecorder(t, clock, Options{LimitBytes: 1000})

	if !recorder.Begin(ctx) {
		t.Fatal("未超限时应允许开始转发")
	}
	// 同一连接继续累加，跨过上限后新请求应被拒绝。
	recorder.AddBytes(ctx, 1100)
	if recorder.Begin(ctx) {
		t.Error("并发传输中越过上限后，新请求应被拒绝")
	}
	if !recorder.OverLimit() {
		t.Error("越过上限后 OverLimit 应为真")
	}
}

func TestRecorderRetriesFailedAlert(t *testing.T) {
	ctx := context.Background()
	clock := newTestClock()

	var mu sync.Mutex
	var attempts int
	var failedOnce bool
	recorder := newRecorder(t, clock, Options{
		AlertBytes: 1000,
		AlertRetry: time.Minute,
		Alert: func(string, int64, int64) error {
			mu.Lock()
			defer mu.Unlock()
			attempts++
			if attempts == 1 {
				failedOnce = true
				return errors.New("通知发送失败")
			}
			return nil
		},
	})

	recorder.Add(ctx, 1200)
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return failedOnce
	})
	// 发送失败不算送达，标记应回滚。
	waitFor(t, func() bool {
		stats, err := recorder.Stats(ctx)
		return err == nil && !stats.AlertSent
	})

	// 重试间隔内不再重复发送。
	recorder.Add(ctx, 10)
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	got := attempts
	mu.Unlock()
	if got != 1 {
		t.Fatalf("重试间隔内尝试次数 = %d，期望 1", got)
	}

	clock.Advance(2 * time.Minute)
	recorder.Add(ctx, 10)
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return attempts == 2
	})
	waitFor(t, func() bool {
		stats, err := recorder.Stats(ctx)
		return err == nil && stats.AlertSent
	})

	// 失败期间不应把已提醒标记写进数据库。
	var stored int64
	if err := recorder.handle.QueryRowContext(ctx,
		`SELECT alert_sent FROM traffic_stats WHERE month = '2026-01'`).Scan(&stored); err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if stored != 1 {
		t.Errorf("送达后数据库 alert_sent = %d，期望 1", stored)
	}
}
