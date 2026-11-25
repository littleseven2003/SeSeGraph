package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

// poolFake 是池测试用的提供方替身，可注入一次失败。
type poolFake struct {
	botID    int64
	calls    int
	failWith error
}

func (f *poolFake) Name() string { return "fake" }

func (f *poolFake) Upload(_ context.Context, req UploadRequest) (UploadResult, error) {
	f.calls++
	if f.failWith != nil {
		fail := f.failWith
		f.failWith = nil
		if _, err := io.Copy(io.Discard, req.Reader); err != nil {
			return UploadResult{}, ErrUpstream
		}
		return UploadResult{}, fail
	}
	if _, err := io.Copy(io.Discard, req.Reader); err != nil {
		return UploadResult{}, ErrUpstream
	}
	return UploadResult{FileID: fmt.Sprintf("file-%d-%d", f.botID, f.calls)}, nil
}

// TestPoolFailoverMovesToNextBot 验证冻结的 Bot 被跳过，换到的 Bot 记录自己的编号，
// 且再次调用 Upload 只落在新的 Bot 上。
func TestPoolFailoverMovesToNextBot(t *testing.T) {
	first := &poolFake{botID: 111, failWith: &UploadError{Stage: "存图", Kind: KindAccount, Status: 400}}
	second := &poolFake{botID: 222}

	pool, err := NewPool([]PoolEntry{
		{BotID: 111, ChatID: "-100777", Alias: "A", Provider: first},
		{BotID: 222, ChatID: "-100777", Alias: "B", Provider: second},
	})
	if err != nil {
		t.Fatalf("创建池失败: %v", err)
	}

	if botID, _ := pool.Origin(); botID != 111 {
		t.Fatalf("初始 Bot = %d，期望 111", botID)
	}

	_, err = pool.Upload(context.Background(), UploadRequest{Reader: strings.NewReader("x")})
	if err == nil || !IsAccountLimited(err) {
		t.Fatalf("首个 Bot 应返回账号受限，实际 %v", err)
	}

	failedID, next, ok := pool.Failover("上游拒绝，账号受限")
	if !ok || next == nil {
		t.Fatal("池内还有健康 Bot，切换应成功")
	}
	if failedID != 111 {
		t.Fatalf("失败 Bot = %d，期望 111", failedID)
	}
	if botID, _ := pool.Origin(); botID != 222 {
		t.Fatalf("切换后 Bot = %d，期望 222", botID)
	}

	if _, err := pool.Upload(context.Background(), UploadRequest{Reader: strings.NewReader("y")}); err != nil {
		t.Fatalf("备用 Bot 发送失败: %v", err)
	}
	if second.calls != 1 || first.calls != 1 {
		t.Fatalf("调用次数 = %d/%d，期望各 1", first.calls, second.calls)
	}
	if !pool.Ready() {
		t.Fatal("池内仍有健康 Bot，应报告就绪")
	}
}

// TestPoolReportsEmptyAfterAllFrozen 验证池内全部冻结后不再谎称就绪。
func TestPoolReportsEmptyAfterAllFrozen(t *testing.T) {
	first := &poolFake{botID: 111}
	second := &poolFake{botID: 222}
	pool, err := NewPool([]PoolEntry{
		{BotID: 111, Provider: first},
		{BotID: 222, Provider: second},
	})
	if err != nil {
		t.Fatalf("创建池失败: %v", err)
	}
	if _, _, ok := pool.Failover("冻结"); !ok {
		t.Fatal("第一个切换应成功")
	}
	if _, _, ok := pool.Failover("冻结"); ok {
		t.Fatal("池内已无健康 Bot，切换应失败")
	}
	if pool.Ready() {
		t.Fatal("全部冻结后不应报告就绪")
	}

	_, err = pool.Upload(context.Background(), UploadRequest{Reader: strings.NewReader("x")})
	if !errors.Is(err, ErrUpstream) && !IsAccountLimited(err) {
		t.Fatalf("无可用 Bot 时应返回明确错误，实际 %v", err)
	}
}
