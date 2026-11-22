// Package provider 负责图床凭据的激活校验与切换。
// Telegram 的 file_id 不能跨 Bot 使用，因此换 Bot 前必须确认不会破坏已有图片。
package provider

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/littleseven2003/SeSeGraph/backend/internal/settings"
	"github.com/littleseven2003/SeSeGraph/backend/internal/storage"
)

// 激活失败类别，调用方据此映射到对外状态码。
var (
	// ErrVerify 表示候选凭据无法验证（令牌无效或上游不可达）。
	ErrVerify = errors.New("无法验证图床凭据")
	// ErrBotMismatch 表示图库已有其他 Bot 上传的图片，切换后这些图片将无法读取。
	ErrBotMismatch = errors.New("图库已有其他 Bot 上传的图片")
	// ErrUploadBusy 表示仍有上传任务在处理，此时不能换成另一个 Bot。
	ErrUploadBusy = errors.New("有上传任务正在处理")
)

// DefaultSwitchWait 是跨 Bot 切换等待在途上传结束的最长时间。
const DefaultSwitchWait = 3 * time.Second

// Guard 串行化提供方切换：先校验候选凭据，再按 Bot 编号与已存图片决定是否允许切换。
type Guard struct {
	mu         sync.Mutex
	switcher   *storage.Switcher
	store      *settings.Store
	count      func(context.Context) (int64, error)
	gate       *storage.UploadGate
	botID      int64
	switchWait time.Duration
}

// NewGuard 创建切换守卫。count 用于统计已存图数量，可为 nil（视为没有图片）；
// gate 与上传队列共用，跨 Bot 切换时独占它，确认没有在途任务后再统计图片。
func NewGuard(switcher *storage.Switcher, store *settings.Store, count func(context.Context) (int64, error), gate *storage.UploadGate) *Guard {
	return &Guard{switcher: switcher, store: store, count: count, gate: gate, switchWait: DefaultSwitchWait}
}

// SetSwitchWait 调整跨 Bot 切换等待在途上传结束的时长，非正值表示不等待。
func (g *Guard) SetSwitchWait(wait time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.switchWait = wait
}

// BotID 返回当前激活的 Bot 编号，未激活时为 0。
func (g *Guard) BotID() int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.botID
}

// Candidate 是一次已校验、尚未生效的图床切换。
// 校验结果被复用：Commit 不再访问上游，也不再写库，归属由调用方与配置在同一事务内持久化。
type Candidate struct {
	provider *storage.Telegram
	botID    int64
	release  func()
}

// BotID 返回候选 Bot 编号，nil 候选（清空配置）返回 0。
func (c *Candidate) BotID() int64 {
	if c == nil {
		return 0
	}
	return c.botID
}

// Abort 放弃这次切换：释放独占的上传队列，运行时保持原样。可重复调用。
func (c *Candidate) Abort() {
	if c == nil || c.release == nil {
		return
	}
	c.release()
	c.release = nil
}

// Prepare 校验候选凭据与 Bot 归属，返回待提交的切换。
// 换成另一个 Bot 时先独占上传队列：确认没有在途任务后再统计已存图片，
// 独占一直持有到 Commit 或 Abort，期间不会有新的图片落到另一个 Bot 名下。
// candidate 为 nil 表示清空配置，不需要校验上游。
func (g *Guard) Prepare(ctx context.Context, candidate *storage.Telegram) (*Candidate, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if candidate == nil {
		return &Candidate{release: func() {}}, nil
	}
	id, err := candidate.BotID(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, ErrVerify
	}

	if active := g.activeBotIDLocked(ctx); active == 0 || active == id {
		return &Candidate{provider: candidate, botID: id, release: func() {}}, nil
	}

	release, ok := g.gate.EnterSwitch(ctx, g.switchWait)
	if !ok {
		return nil, ErrUploadBusy
	}
	count := int64(0)
	if g.count != nil {
		if count, err = g.count(ctx); err != nil {
			release()
			return nil, err
		}
	}
	if count > 0 {
		release()
		return nil, ErrBotMismatch
	}
	return &Candidate{provider: candidate, botID: id, release: release}, nil
}

// Commit 让已验证的候选生效。归属记录由调用方与配置在同一事务内写入，这里不再落库。
func (g *Guard) Commit(ctx context.Context, candidate *Candidate) {
	if candidate == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	defer candidate.Abort()

	g.switcher.Swap(candidate.provider)
	if candidate.botID != 0 {
		g.botID = candidate.botID
	}
}

// Activate 校验并立即切换，同时把 Bot 归属写入配置表；服务启动时使用。
// 保存设置的路径改用 Prepare 与 Commit，以便归属与配置落在同一个事务里。
func (g *Guard) Activate(ctx context.Context, candidate *storage.Telegram) error {
	prepared, err := g.Prepare(ctx, candidate)
	if err != nil {
		return err
	}
	if prepared.botID != 0 && g.store != nil {
		if err := g.store.Set(ctx, settings.KeyTelegramBotID, strconv.FormatInt(prepared.botID, 10)); err != nil {
			prepared.Abort()
			return err
		}
	}
	g.Commit(ctx, prepared)
	return nil
}

// activeBotIDLocked 返回当前 Bot 归属：优先内存中的编号，重启后回落到持久化记录。
func (g *Guard) activeBotIDLocked(ctx context.Context) int64 {
	if g.botID != 0 {
		return g.botID
	}
	return g.persistedBotID(ctx)
}

// persistedBotID 读取持久化的 Bot 编号，用于服务重启后继续判断图片归属。
func (g *Guard) persistedBotID(ctx context.Context) int64 {
	if g.store == nil {
		return 0
	}
	raw, ok, err := g.store.Get(ctx, settings.KeyTelegramBotID)
	if err != nil || !ok {
		return 0
	}
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return 0
	}
	return value
}
