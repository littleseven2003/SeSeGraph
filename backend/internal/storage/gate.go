package storage

import (
	"context"
	"sync"
	"time"
)

// switchPoll 是切换等待在途上传结束时的轮询间隔。
const switchPoll = 20 * time.Millisecond

// UploadGate 是上传任务与图床切换共用的协调点。
// 上传任务领取后持有共享名额，跨 Bot 切换在确认没有在途任务后独占，
// 避免切换期间落库的图片被记到另一个 Bot 名下。
type UploadGate struct {
	rw sync.RWMutex
}

// NewUploadGate 创建协调点。
func NewUploadGate() *UploadGate {
	return &UploadGate{}
}

// EnterUpload 为即将领取的上传任务占用共享名额。
// 切换正在独占时返回 false，队列下一轮重新尝试，不会阻塞队列。
func (g *UploadGate) EnterUpload() (func(), bool) {
	if g == nil {
		return func() {}, true
	}
	if !g.rw.TryRLock() {
		return nil, false
	}
	return g.rw.RUnlock, true
}

// EnterSwitch 独占协调点，期间不领取新的上传任务。
// 已有任务在处理时最多等待 wait，超时返回 false；wait 非正时立即返回。
func (g *UploadGate) EnterSwitch(ctx context.Context, wait time.Duration) (func(), bool) {
	if g == nil {
		return func() {}, true
	}
	if g.rw.TryLock() {
		return g.rw.Unlock, true
	}
	if wait <= 0 {
		return nil, false
	}

	deadline := time.Now().Add(wait)
	timer := time.NewTimer(switchPoll)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, false
		case <-timer.C:
		}
		if g.rw.TryLock() {
			return g.rw.Unlock, true
		}
		if !time.Now().Before(deadline) {
			return nil, false
		}
		timer.Reset(switchPoll)
	}
}
