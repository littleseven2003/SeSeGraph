package storage

import (
	"context"
	"sync"
)

// Dynamic 承载运行期的提供方与池：配置保存后整份替换，读取侧始终拿到一致的实例。
// 未配置可写入的 Bot 时 Poll 返回错误，由上传队列保留任务等待，不回退到旧令牌。
type Dynamic struct {
	mu       sync.RWMutex
	provider Provider
	pool     *Pool
}

// NewDynamic 用初始提供方创建运行配置；provider 可为 nil。
func NewDynamic(provider Provider) *Dynamic {
	return &Dynamic{provider: provider}
}

// Set 原子替换提供方与池；pool 为空表示当前不是多 Bot 模式。
func (d *Dynamic) Set(provider Provider, pool *Pool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.provider = provider
	d.pool = pool
}

// Current 返回当前提供方，可能为 nil。
// 多 Bot 模式下始终返回池本身：切换只改池内的活动条目，读侧拿到的仍是同一份运行配置。
func (d *Dynamic) Current() Provider {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.pool != nil {
		return d.pool
	}
	return d.provider
}

// Name 报告提供方类型。
func (d *Dynamic) Name() string {
	current := d.Current()
	if current == nil {
		return "telegram"
	}
	return current.Name()
}

// Ready 报告当前是否有可写入的提供方。
func (d *Dynamic) Ready() bool {
	current := d.Current()
	if current == nil {
		return false
	}
	if probe, ok := current.(interface{ Ready() bool }); ok {
		return probe.Ready()
	}
	return true
}

// Upload 用当前提供方发送；没有可用配置时返回权限类别错误，任务保持等待语义由调用方判断。
func (d *Dynamic) Upload(ctx context.Context, req UploadRequest) (UploadResult, error) {
	current := d.Current()
	if current == nil {
		return UploadResult{}, &UploadError{Stage: "存图", Kind: KindAuth, Note: "尚未配置可写入的 Bot"}
	}
	return current.Upload(ctx, req)
}

// UploadWithOrigin 把本次发送连同实际使用的 Bot、频道与凭据指纹一起返回：
// 多 Bot 模式下由池在选中条目的同一把锁内固定，单 Bot 模式由提供方自身给出。
func (d *Dynamic) UploadWithOrigin(ctx context.Context, req UploadRequest) (UploadResult, SendOrigin, error) {
	current := d.Current()
	if current == nil {
		return UploadResult{}, SendOrigin{}, &UploadError{Stage: "存图", Kind: KindAuth, Note: "尚未配置可写入的 Bot"}
	}
	if originProvider, ok := current.(OriginProvider); ok {
		return originProvider.UploadWithOrigin(ctx, req)
	}
	origin := SendOrigin{}
	if source, ok := current.(Origin); ok {
		origin.BotID, origin.ChatID = source.Origin()
	}
	if versioned, ok := current.(interface{ CredentialVersion() string }); ok {
		origin.CredentialVersion = versioned.CredentialVersion()
	}
	result, err := current.Upload(ctx, req)
	return result, origin, err
}

// Origin 返回当前提供方的编号与频道，供逐图来源记录。
func (d *Dynamic) Origin() (int64, string) {
	current := d.Current()
	if origin, ok := current.(Origin); ok {
		return origin.Origin()
	}
	return 0, ""
}

// CredentialVersion 返回当前提供方的凭据指纹，供发送结果核对。
func (d *Dynamic) CredentialVersion() string {
	if versioned, ok := d.Current().(interface{ CredentialVersion() string }); ok {
		return versioned.CredentialVersion()
	}
	return ""
}

// FailoverBot 只把本次调用实际使用的 Bot 标为写入不可用并换到下一个可用条目。
// 即使运行配置在发送途中被替换，也不会误伤新配置里的其他 Bot。
func (d *Dynamic) FailoverBot(botID int64) (Provider, bool) {
	d.mu.RLock()
	pool := d.pool
	d.mu.RUnlock()
	if pool == nil || botID <= 0 {
		return nil, false
	}
	next, ok := pool.Block(botID)
	if !ok {
		return nil, false
	}
	return next, true
}

func (d *Dynamic) Failover(reason string) (int64, Provider, bool) {
	d.mu.RLock()
	pool := d.pool
	d.mu.RUnlock()
	if pool == nil {
		return 0, nil, false
	}
	failedID, next, ok := pool.Failover(reason)
	if !ok {
		return failedID, nil, false
	}
	d.mu.Lock()
	d.provider = next
	d.mu.Unlock()
	return failedID, next, true
}
