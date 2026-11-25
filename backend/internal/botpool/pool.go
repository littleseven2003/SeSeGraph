package botpool

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
)

// KeyBots 是 Bot 池在配置表里的键，值是 JSON 数组；令牌只留在服务端，不向界面回显。
const KeyBots = "telegram.bots"

// 状态取值：正常可写入、已被上游标为冻结、人工停用。
const (
	StatusOK     = "ok"
	StatusFrozen = "frozen"
	StatusPaused = "paused"
)

// MaxBots 限制池内 Bot 数量，避免一次上传在过多令牌间轮换。
const MaxBots = 10

// Store 是配置表的读写接口，与设置存储一致，便于测试替换。
type Store interface {
	Get(ctx context.Context, key string) (string, bool, error)
	Set(ctx context.Context, key, value string) error
}

// Bot 是池内的一个 Bot：编号来自令牌自检，别名供界面展示，令牌不参与序列化输出以外的用途。
type Bot struct {
	BotID    int64  `json:"bot_id"`
	Alias    string `json:"alias"`
	Token    string `json:"token"`
	Priority int    `json:"priority"`
	// Status 是健康状态，空值按正常处理；冻结后由接管流程或人工恢复。
	Status string `json:"status,omitempty"`
	// StatusNote 是冻结原因的脱敏说明，例如「上游拒绝，账号受限」。
	StatusNote string `json:"status_note,omitempty"`
	UpdatedAt  string `json:"updated_at,omitempty"`
}

// Pool 读写池配置，并在冻结时更新健康状态。
type Pool struct {
	store Store
	now   func() time.Time
}

// New 基于配置存储创建池。
func New(store Store) *Pool {
	return &Pool{store: store, now: time.Now}
}

// List 返回池内全部 Bot，按优先级、编号升序，便于界面稳定展示。
func (p *Pool) List(ctx context.Context) ([]Bot, error) {
	raw, ok, err := p.store.Get(ctx, KeyBots)
	if err != nil {
		return nil, errors.New("读取 Bot 池失败")
	}
	if !ok || strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var bots []Bot
	if err := json.Unmarshal([]byte(raw), &bots); err != nil {
		return nil, errors.New("读取 Bot 池失败")
	}
	sortBots(bots)
	return bots, nil
}

// Replace 用新列表覆盖池配置；未提交令牌的条目沿用旧令牌，健康状态按编号延续。
// 调用方传入的 BotID 为 0 时视为尚未自检，仍可保存，待探测后回填。
func (p *Pool) Replace(ctx context.Context, bots []Bot) error {
	current, err := p.List(ctx)
	if err != nil {
		return err
	}
	byID := make(map[int64]Bot, len(current))
	byToken := make(map[string]Bot, len(current))
	for _, bot := range current {
		if bot.BotID > 0 {
			byID[bot.BotID] = bot
		}
		if bot.Token != "" {
			byToken[bot.Token] = bot
		}
	}

	if len(bots) > MaxBots {
		return errors.New("Bot 数量超出上限")
	}
	seenID := make(map[int64]bool, len(bots))
	seenToken := make(map[string]bool, len(bots))
	normalized := make([]Bot, 0, len(bots))
	for index, bot := range bots {
		bot.Alias = strings.TrimSpace(bot.Alias)
		bot.Token = strings.TrimSpace(bot.Token)
		if bot.Token == "" && bot.BotID > 0 {
			if old, ok := byID[bot.BotID]; ok {
				bot.Token = old.Token
			}
		}
		if !validToken(bot.Token) {
			return errors.New("Bot 令牌格式不正确")
		}
		if seenToken[bot.Token] {
			return errors.New("Bot 令牌重复")
		}
		seenToken[bot.Token] = true
		if bot.BotID > 0 {
			if seenID[bot.BotID] {
				return errors.New("Bot 编号重复")
			}
			seenID[bot.BotID] = true
		}
		bot.Priority = index
		// 同一令牌重新提交时沿用原健康状态，避免保存配置把冻结标记清掉。
		if old, ok := byToken[bot.Token]; ok {
			if bot.Status == "" {
				bot.Status = old.Status
			}
			if bot.StatusNote == "" {
				bot.StatusNote = old.StatusNote
			}
			if bot.UpdatedAt == "" {
				bot.UpdatedAt = old.UpdatedAt
			}
		}
		if bot.Status == "" {
			bot.Status = StatusOK
		}
		normalized = append(normalized, bot)
	}

	encoded, err := json.Marshal(normalized)
	if err != nil {
		return errors.New("保存 Bot 池失败")
	}
	if err := p.store.Set(ctx, KeyBots, string(encoded)); err != nil {
		return errors.New("保存 Bot 池失败")
	}
	return nil
}

// MarkFrozen 把某个 Bot 标为冻结并记录脱敏原因，之后的挑选会跳过它。
func (p *Pool) MarkFrozen(ctx context.Context, botID int64, note string) error {
	return p.setStatus(ctx, botID, StatusFrozen, note)
}

// MarkPaused 人工停用某个 Bot。
func (p *Pool) MarkPaused(ctx context.Context, botID int64, note string) error {
	return p.setStatus(ctx, botID, StatusPaused, note)
}

// MarkHealthy 清除冻结标记，让 Bot 重新参与挑选。
func (p *Pool) MarkHealthy(ctx context.Context, botID int64) error {
	return p.setStatus(ctx, botID, StatusOK, "")
}

func (p *Pool) setStatus(ctx context.Context, botID int64, status, note string) error {
	if botID <= 0 {
		return errors.New("Bot 编号无效")
	}
	bots, err := p.List(ctx)
	if err != nil {
		return err
	}
	found := false
	for index := range bots {
		if bots[index].BotID != botID {
			continue
		}
		bots[index].Status = status
		bots[index].StatusNote = note
		bots[index].UpdatedAt = p.now().UTC().Format(time.RFC3339)
		found = true
		break
	}
	if !found {
		return errors.New("Bot 不在池内")
	}
	encoded, err := json.Marshal(bots)
	if err != nil {
		return errors.New("保存 Bot 池失败")
	}
	if err := p.store.Set(ctx, KeyBots, string(encoded)); err != nil {
		return errors.New("保存 Bot 池失败")
	}
	return nil
}

// Usable 返回当前可写入的 Bot，按优先级排序，冻结与停用的排在最后且不参与挑选。
func Usable(bots []Bot) []Bot {
	usable := make([]Bot, 0, len(bots))
	for _, bot := range bots {
		if bot.Status == "" || bot.Status == StatusOK {
			usable = append(usable, bot)
		}
	}
	return usable
}

func sortBots(bots []Bot) {
	sort.SliceStable(bots, func(i, j int) bool {
		if bots[i].Priority != bots[j].Priority {
			return bots[i].Priority < bots[j].Priority
		}
		return bots[i].BotID < bots[j].BotID
	})
}

// validToken 只做形状校验：数字编号加冒号，再加非空令牌体；真实有效性由探测确认。
func validToken(token string) bool {
	parts := strings.SplitN(token, ":", 2)
	if len(parts) != 2 || parts[1] == "" || len(parts[0]) < 3 {
		return false
	}
	for _, char := range parts[0] {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}
