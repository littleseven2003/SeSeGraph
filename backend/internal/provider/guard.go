// Package provider 负责图床凭据的激活校验与切换。
// Telegram 的 file_id 不能跨 Bot 使用，因此换 Bot 前必须确认不会破坏已有图片。
package provider

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"

	"github.com/littleseven2003/SeSeGraph/backend/internal/settings"
	"github.com/littleseven2003/SeSeGraph/backend/internal/storage"
)

// 激活失败类别，调用方据此映射到对外状态码。
var (
	// ErrVerify 表示候选凭据无法验证（令牌无效或上游不可达）。
	ErrVerify = errors.New("无法验证图床凭据")
	// ErrBotMismatch 表示图库已有其他 Bot 上传的图片，切换后这些图片将无法读取。
	ErrBotMismatch = errors.New("图库已有其他 Bot 上传的图片")
)

// Guard 串行化提供方切换：先校验候选凭据，再按 Bot 编号与已存图片决定是否允许切换。
type Guard struct {
	mu       sync.Mutex
	switcher *storage.Switcher
	store    *settings.Store
	count    func(context.Context) (int64, error)
	botID    int64
}

// NewGuard 创建切换守卫。count 用于统计已存图数量，可为 nil（视为没有图片）。
func NewGuard(switcher *storage.Switcher, store *settings.Store, count func(context.Context) (int64, error)) *Guard {
	return &Guard{switcher: switcher, store: store, count: count}
}

// BotID 返回当前激活的 Bot 编号，未激活时为 0。
func (g *Guard) BotID() int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.botID
}

// ActiveBotID 读取持久化的 Bot 编号，用于服务重启后继续判断图片归属。
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

// Validate 校验候选凭据与 Bot 归属，不切换提供方。
// 返回候选 Bot 编号，供保存前确认切换不会破坏已有图片。
func (g *Guard) Validate(ctx context.Context, candidate *storage.Telegram) (int64, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.validateLocked(ctx, candidate)
}

// validateLocked 在锁内完成凭据校验与 Bot 归属判断。
func (g *Guard) validateLocked(ctx context.Context, candidate *storage.Telegram) (int64, error) {
	if candidate == nil {
		return 0, nil
	}
	id, err := candidate.BotID(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return 0, err
		}
		return 0, ErrVerify
	}

	active := g.botID
	if active == 0 {
		active = g.persistedBotID(ctx)
	}
	if active != 0 && active != id {
		count := int64(0)
		if g.count != nil {
			count, err = g.count(ctx)
			if err != nil {
				return 0, err
			}
		}
		if count > 0 {
			return 0, ErrBotMismatch
		}
	}
	return id, nil
}

// Activate 校验候选提供方并切换。candidate 为 nil 表示清空配置，同时保留 Bot 归属记录。
// 同一 Bot 换令牌允许切换；换成另一个 Bot 时，只有图库尚未存过图片才允许。
func (g *Guard) Activate(ctx context.Context, candidate *storage.Telegram) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if candidate == nil {
		g.switcher.Swap(nil)
		return nil
	}
	id, err := g.validateLocked(ctx, candidate)
	if err != nil {
		return err
	}
	g.switcher.Swap(candidate)
	g.botID = id
	if g.store != nil {
		_ = g.store.Set(ctx, settings.KeyTelegramBotID, strconv.FormatInt(id, 10))
	}
	return nil
}
