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
// 旧提供方不再被引用，顺带释放它的空闲连接。
func (s *Switcher) Swap(provider *Telegram) {
	s.mu.Lock()
	previous := s.provider
	s.provider = provider
	s.mu.Unlock()
	if previous != nil && previous != provider {
		previous.CloseIdleConnections()
	}
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

// Current 返回当前提供方实例，供调用方在同一份配置上完成校验与发送。
// 即使随后保存设置切换了频道，已取得的实例仍指向原来的令牌与目标。
func (s *Switcher) Current() (*Telegram, error) {
	return s.current()
}

// Snapshot 返回当前提供方的固定视图，调用方可在一段流程内复用同一份配置。
func (s *Switcher) Snapshot() (StreamProvider, bool) {
	if s == nil {
		return nil, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.provider == nil {
		return nil, false
	}
	return s.provider, true
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

// SendMessage 按当前提供方发送一条纯文本消息，未配置时返回 ErrNotConfigured。
func (s *Switcher) SendMessage(ctx context.Context, text string) error {
	provider, err := s.current()
	if err != nil {
		return err
	}
	return provider.SendMessage(ctx, text)
}

// ChatInfo 按当前提供方读取目标会话属性，用于备份发送前确认目标是否私有。
func (s *Switcher) ChatInfo(ctx context.Context) (ChatInfo, error) {
	provider, err := s.current()
	if err != nil {
		return ChatInfo{}, err
	}
	return provider.ChatInfo(ctx)
}

// SendDocument 按当前提供方把本地文件作为文档发送，用于自动备份推送。
// 需要与校验绑定同一份配置时，改用 Current 取实例再调用其 SendDocument。
func (s *Switcher) SendDocument(ctx context.Context, path, caption string) error {
	provider, err := s.current()
	if err != nil {
		return err
	}
	return provider.SendDocument(ctx, path, caption)
}
