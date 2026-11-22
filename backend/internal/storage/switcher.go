package storage

import (
	"context"
	"sync"
)

// Switcher 在运行期承载当前图床提供方，保存设置后原子切换。
// 上传队列与图片网关共用同一个实例，切换后两侧立即读取同一份配置。
type Switcher struct {
	mu       sync.RWMutex
	provider *Telegram
}

// NewSwitcher 创建可热切换的提供方容器，初始状态为未配置。
func NewSwitcher() *Switcher {
	return &Switcher{}
}

// Swap 替换当前提供方，传 nil 表示回到未配置状态。
func (s *Switcher) Swap(provider *Telegram) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.provider = provider
}

// Ready 报告是否已有可用提供方，队列据此决定是否领取任务。
func (s *Switcher) Ready() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.provider != nil
}

// Name 返回当前提供方标识，未配置时返回 unconfigured。
func (s *Switcher) Name() string {
	if s == nil {
		return "unconfigured"
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.provider == nil {
		return "unconfigured"
	}
	return s.provider.Name()
}

// current 返回当前提供方，调用方不得在锁外持有其内部状态。
func (s *Switcher) current() (*Telegram, error) {
	if s == nil {
		return nil, ErrNotConfigured
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.provider == nil {
		return nil, ErrNotConfigured
	}
	return s.provider, nil
}

// Upload 按当前提供方上传单张图片。
func (s *Switcher) Upload(ctx context.Context, req UploadRequest) (UploadResult, error) {
	provider, err := s.current()
	if err != nil {
		return UploadResult{}, err
	}
	return provider.Upload(ctx, req)
}

// OpenStream 按当前提供方开启流式读取。
func (s *Switcher) OpenStream(ctx context.Context, fileID string) (Stream, error) {
	provider, err := s.current()
	if err != nil {
		return Stream{}, err
	}
	return provider.OpenStream(ctx, fileID)
}
