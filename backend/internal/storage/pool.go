package storage

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
)

// PoolEntry 是池内的一个提供方：编号与频道供逐图来源记录，Provider 负责实际发送。
type PoolEntry struct {
	BotID    int64
	ChatID   string
	Alias    string
	Provider Provider
}

// Pool 按池顺序使用提供方；某个 Bot 被上游判为账号受限时换到下一个可用条目。
// 池不改变发送节奏：每次 Upload 仍由调用方串行择时。
type Pool struct {
	mu      sync.Mutex
	entries []PoolEntry
	active  int
	blocked map[int64]bool

	uploads int64
}

// NewPool 用给定条目创建池，至少需要一个提供方。
func NewPool(entries []PoolEntry) (*Pool, error) {
	if len(entries) == 0 {
		return nil, errors.New("Bot 池为空")
	}
	for _, entry := range entries {
		if entry.Provider == nil {
			return nil, errors.New("Bot 池条目缺少提供方")
		}
	}
	return &Pool{entries: entries, blocked: make(map[int64]bool)}, nil
}

// Name 报告提供方类型，池内只放同一类图床。
func (p *Pool) Name() string { return "telegram" }

// Ready 报告池内是否还有可写入的 Bot。
func (p *Pool) Ready() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pickLocked(-1) >= 0
}

// Upload 用当前生效的 Bot 发送；被上游拒绝后由调用方换 Bot 重试本张。
func (p *Pool) Upload(ctx context.Context, req UploadRequest) (UploadResult, error) {
	result, _, err := p.UploadWithOrigin(ctx, req)
	return result, err
}

// UploadWithOrigin 在选中条目的同一把锁内固定身份并发送，返回本次实际使用的 Bot 与凭据指纹。
func (p *Pool) UploadWithOrigin(ctx context.Context, req UploadRequest) (UploadResult, SendOrigin, error) {
	p.mu.Lock()
	if p.active < 0 || p.active >= len(p.entries) || p.blocked[p.entries[p.active].BotID] {
		next := p.pickLocked(-1)
		if next < 0 {
			p.mu.Unlock()
			return UploadResult{}, SendOrigin{}, &UploadError{Stage: "存图", Kind: KindAccount, Note: "池内没有可写入的 Bot"}
		}
		p.active = next
	}
	entry := p.entries[p.active]
	atomic.AddInt64(&p.uploads, 1)
	p.mu.Unlock()

	origin := SendOrigin{BotID: entry.BotID, ChatID: entry.ChatID}
	if versioned, ok := entry.Provider.(interface{ CredentialVersion() string }); ok {
		origin.CredentialVersion = versioned.CredentialVersion()
	}
	result, err := entry.Provider.Upload(ctx, req)
	return result, origin, err
}

// CredentialVersion 返回当前生效条目的凭据指纹，供发送结果核对。
func (p *Pool) CredentialVersion() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.active < 0 || p.active >= len(p.entries) {
		return ""
	}
	if versioned, ok := p.entries[p.active].Provider.(interface{ CredentialVersion() string }); ok {
		return versioned.CredentialVersion()
	}
	return ""
}

// Origin 返回当前生效条目的编号与原频道，供逐图来源记录。
func (p *Pool) Origin() (int64, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.active < 0 || p.active >= len(p.entries) {
		return 0, ""
	}
	entry := p.entries[p.active]
	return entry.BotID, entry.ChatID
}

// Failover 把当前 Bot 标为写入不可用并换到下一个；返回失败条目编号与新的提供方。
// 池内已无可写入条目时返回 false，调用方据此给出明确原因。
func (p *Pool) Failover(reason string) (failedBotID int64, next Provider, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.active >= 0 && p.active < len(p.entries) {
		failedBotID = p.entries[p.active].BotID
		if failedBotID != 0 {
			p.blocked[failedBotID] = true
		}
	}
	nextIndex := p.pickLocked(p.active)
	if nextIndex < 0 {
		return failedBotID, nil, false
	}
	p.active = nextIndex
	return failedBotID, p.entries[nextIndex].Provider, true
}

// Block 把指定编号的 Bot 标为写入不可用并返回下一个可用提供方。
// 与 Failover 不同，它不依赖“当前活动条目”，避免热切换或调序后封错对象。
func (p *Pool) Block(botID int64) (Provider, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if botID <= 0 {
		return nil, false
	}
	p.blocked[botID] = true
	nextIndex := p.pickLocked(-1)
	if nextIndex < 0 {
		return nil, false
	}
	p.active = nextIndex
	return p.entries[nextIndex].Provider, true
}

// pickLocked 从 start 之后找一个未停用的条目，-1 表示从头找。
func (p *Pool) pickLocked(start int) int {
	for offset := 1; offset <= len(p.entries); offset++ {
		index := (start + offset) % len(p.entries)
		if start < 0 {
			index = offset - 1
			if index >= len(p.entries) {
				break
			}
		}
		entry := p.entries[index]
		if entry.Provider == nil || p.blocked[entry.BotID] {
			continue
		}
		return index
	}
	return -1
}

// Unblock 让某个 Bot 重新参与挑选，供人工恢复后使用。
func (p *Pool) Unblock(botID int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.blocked, botID)
}

// Uploads 汇报池累计发送次数，供测试与诊断使用。
func (p *Pool) Uploads() int64 { return atomic.LoadInt64(&p.uploads) }
