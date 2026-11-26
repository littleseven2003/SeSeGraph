package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/littleseven2003/SeSeGraph/backend/internal/botpool"
	"github.com/littleseven2003/SeSeGraph/backend/internal/netclient"
	"github.com/littleseven2003/SeSeGraph/backend/internal/settings"
	"github.com/littleseven2003/SeSeGraph/backend/internal/storage"
)

// botsBody 是多 Bot 池的读取响应；令牌只以掩码出现。
type botsBody struct {
	Bots        []botEntry `json:"bots"`
	PoolEnabled bool       `json:"pool_enabled"`
}

// botEntry 是界面展示用的 Bot 条目。
type botEntry struct {
	BotID       int64  `json:"bot_id"`
	Alias       string `json:"alias"`
	Priority    int    `json:"priority"`
	Status      string `json:"status"`
	StatusNote  string `json:"status_note"`
	MaskedToken string `json:"masked_token"`
	Usable      bool   `json:"usable"`
}

// botsRequest 是保存 Bot 池的请求体；令牌留空表示沿用该编号已有的令牌。
type botsRequest struct {
	Bots []struct {
		BotID    int64  `json:"bot_id"`
		Alias    string `json:"alias"`
		Token    string `json:"token"`
		Priority int    `json:"priority"`
		Status   string `json:"status"`
	} `json:"bots"`
}

// botSaveTimeout 是保存 Bot 池的超时，与设置保存一致，避免请求挂住。
const botSaveTimeout = 15 * time.Second

// botProbeTimeout 是单个候选令牌探测的上限，避免保存请求被上游拖住。
const botProbeTimeout = 10 * time.Second

// probeBots 默认调用真实探测；测试替换它即可覆盖失败分支，不必接触上游。
var probeBots = settings.Probe

// verifyBotTokens 用候选令牌依次执行 getMe、getChat 与 getChatMember；
// 只对本次提交了新令牌的条目探测，失败时返回带别名的可读原因。
func verifyBotTokens(ctx context.Context, deps Deps, bots []botpool.Bot) ([]int64, error) {
	ids := make([]int64, len(bots))
	if deps.Settings == nil {
		return ids, nil
	}
	effective, err := deps.Settings.Effective(ctx)
	if err != nil {
		return nil, errors.New("读取图床配置失败")
	}
	return verifyBotTokensWith(ctx, effective, bots)
}

// verifyBotTokensWith 用给定的候选生效配置探测，供「频道与列表同次提交」复用。
func verifyBotTokensWith(ctx context.Context, effective settings.Effective, bots []botpool.Bot) ([]int64, error) {
	needed := make([]bool, len(bots))
	for index := range needed {
		needed[index] = true
	}
	return verifySelectedBotTokens(ctx, effective, bots, needed)
}

// verifySelectedBotTokens 只探测本次真的换了令牌、新加的、或从停用恢复到可写的条目。
// 保留原样（含冻结或人工停用）的条目不再要求频道发布权限，否则冻结 A 时连加个备用 B 都做不到。
func verifySelectedBotTokens(ctx context.Context, effective settings.Effective, bots []botpool.Bot, needed []bool) ([]int64, error) {
	ids := make([]int64, len(bots))
	for index, bot := range bots {
		if index < len(needed) && !needed[index] {
			continue
		}
		token := strings.TrimSpace(bot.Token)
		if token == "" {
			continue
		}
		name := strings.TrimSpace(bot.Alias)
		if name == "" {
			name = "未命名 Bot"
		}
		client, clientErr := netclient.NewClient(effective.ProxyConfig(), netclient.ServiceTelegram)
		if clientErr != nil {
			return nil, errors.New(name + "：网络客户端创建失败")
		}
		result := probeBots(ctx, settings.ProbeOptions{
			TelegramToken:   token,
			TelegramChannel: effective.TelegramChannel.Value,
			TelegramAPIBase: effective.TelegramAPIBase.Value,
			TelegramClient:  client,
			Timeout:         botProbeTimeout,
		})
		if result.Telegram.Status != settings.ProbeOK {
			detail := result.Telegram.Detail
			if detail == "" {
				detail = "连通性校验未通过"
			}
			return nil, errors.New(name + "：" + detail)
		}
		// 探测通过后解析真实编号，供列表去重与逐图来源使用。
		provider, buildErr := storage.NewTelegram(storage.TelegramConfig{
			Token:      token,
			ChatID:     effective.TelegramChannel.Value,
			APIBase:    effective.TelegramAPIBase.Value,
			HTTPClient: client,
		})
		if buildErr != nil {
			return nil, errors.New(name + "：无法读取 Bot 身份")
		}
		botID, idErr := provider.BotID(ctx)
		if idErr != nil {
			return nil, errors.New(name + "：无法读取 Bot 身份")
		}
		ids[index] = botID
	}
	return ids, nil
}

// maskToken 只保留令牌尾部四位，其余用圆点代替，避免明文回显。
func maskToken(token string) string {
	trimmed := strings.TrimSpace(token)
	if trimmed == "" {
		return ""
	}
	if len(trimmed) <= 4 {
		return "••••"
	}
	return "••••" + trimmed[len(trimmed)-4:]
}

// botsHandler 读取 Bot 池，含别名、优先级与健康状态。
func botsHandler(deps Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if deps.Bots == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "多 Bot 池未启用"})
			return
		}
		bots, err := deps.Bots.List(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "读取 Bot 池失败"})
			return
		}
		entries := make([]botEntry, 0, len(bots))
		usable := 0
		for _, bot := range bots {
			entries = append(entries, botEntry{
				BotID:       bot.BotID,
				Alias:       bot.Alias,
				Priority:    bot.Priority,
				Status:      bot.Status,
				StatusNote:  bot.StatusNote,
				MaskedToken: maskToken(bot.Token),
				Usable:      bot.Status == "" || bot.Status == botpool.StatusOK,
			})
			if bot.Status == "" || bot.Status == botpool.StatusOK {
				usable++
			}
		}
		c.JSON(http.StatusOK, botsBody{Bots: entries, PoolEnabled: usable >= 2})
	}
}

// telegramRequest 是一次提交的 Telegram 配置：频道与 Bot 列表一起校验、一起落库。
type telegramRequest struct {
	ChannelID string           `json:"channel_id"`
	Bots      []botsRequestBot `json:"bots"`
}

// botsRequestBot 是单条 Bot 的提交结构。
type botsRequestBot struct {
	BotID    int64  `json:"bot_id"`
	Alias    string `json:"alias"`
	Token    string `json:"token"`
	Priority int    `json:"priority"`
	Status   string `json:"status"`
}

// telegramSettingsHandler 用同一份候选配置校验频道与 Bot 列表：
// 候选频道参与令牌探测，全部通过后先写频道再写列表并热替换运行配置。
func telegramSettingsHandler(deps Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if deps.Bots == nil || deps.Settings == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "图床配置未启用"})
			return
		}
		var request telegramRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式不正确"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), botSaveTimeout)
		defer cancel()

		effective, err := deps.Settings.Effective(ctx)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "读取图床配置失败"})
			return
		}
		channel := strings.TrimSpace(request.ChannelID)
		if channel == "" {
			channel = effective.TelegramChannel.Value
		}
		// 候选频道参与探测：换频道与换令牌一次完成，不会用旧频道验证新令牌。
		candidate := effective
		candidate.TelegramChannel.Value = channel

		current, err := deps.Bots.List(ctx)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "读取 Bot 列表失败"})
			return
		}
		candidates := make([]botpool.Bot, 0, len(request.Bots))
		for _, item := range request.Bots {
			candidates = append(candidates, botpool.Bot{
				BotID: item.BotID, Alias: item.Alias, Token: item.Token,
				Priority: item.Priority, Status: item.Status,
			})
		}
		// 先规范化再探测：未提交令牌的条目沿用原值，轮换时探测的是新令牌。
		bots, err := deps.Bots.Normalize(ctx, candidates)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		needed := botProbeNeeded(current, bots)
		ids, err := verifySelectedBotTokens(ctx, candidate, bots, needed)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		probed := false
		for index := range bots {
			if !needed[index] {
				// 保留原状的条目（含冻结或人工停用）不再探测，编号与令牌照旧。
				continue
			}
			probed = true
			if bots[index].BotID == 0 {
				bots[index].BotID = ids[index]
				continue
			}
			// 轮换令牌时探测到的编号必须与原行一致，否则会把媒体归属写到另一个 Bot 名下。
			if ids[index] != 0 && ids[index] != bots[index].BotID {
				c.JSON(http.StatusBadRequest, gin.H{"error": "第 " + strconv.Itoa(index+1) +
					" 个 Bot 的令牌对应编号 " + strconv.FormatInt(ids[index], 10) +
					"，与原行不一致，请删除后重新添加"})
				return
			}
		}
		// 改了频道但没有任何条目需要探测时，单独确认候选频道可读，避免写入一个连不上的频道。
		if strings.TrimSpace(channel) != strings.TrimSpace(effective.TelegramChannel.Value) && !probed {
			if err := verifyChannelReadable(ctx, candidate, bots); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
		}
		seen := make(map[int64]string, len(bots))
		for _, bot := range bots {
			if bot.BotID == 0 {
				continue
			}
			if previous, ok := seen[bot.BotID]; ok {
				c.JSON(http.StatusBadRequest, gin.H{"error": "同一个 Bot 不能重复添加（" + previous + "）"})
				return
			}
			seen[bot.BotID] = bot.Alias
		}
		if err := ensureRemovable(ctx, deps, bots); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		// 频道与列表在同一个事务里落库：任何一项失败都不会留下半套配置。
		_, encoded, err := deps.Bots.Encode(ctx, bots)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		channelValue := channel
		if err := deps.Settings.UpdateAtomic(ctx, settings.Patch{TelegramChannel: &channelValue},
			map[string]string{botpool.KeyBots: encoded}); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "频道或 Bot 列表校验未通过"})
			return
		}
		if deps.ReloadBots != nil {
			if err := deps.ReloadBots(ctx); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "已保存，但运行配置切换失败，请重新保存"})
				return
			}
		}
		botsHandler(deps)(c)
	}
}

// botProbeNeeded 判断候选条目是否需要重新探测：新加的、换了令牌的、
// 以及从停用或冻结恢复到可写的条目才需要，保留原状的条目不再要求频道发布权限。
func botProbeNeeded(current, bots []botpool.Bot) []bool {
	byID := make(map[int64]botpool.Bot, len(current))
	for _, bot := range current {
		if bot.BotID > 0 {
			byID[bot.BotID] = bot
		}
	}
	needed := make([]bool, len(bots))
	for index, bot := range bots {
		old, ok := byID[bot.BotID]
		switch {
		case bot.BotID == 0 || !ok:
			needed[index] = true
		case old.Token != bot.Token:
			needed[index] = true
		case bot.Status == botpool.StatusOK && old.Status != "" && old.Status != botpool.StatusOK:
			needed[index] = true
		}
	}
	return needed
}

// verifyChannelReadable 用任一候选 Bot 读一次会话信息，确认候选频道存在且可访问。
func verifyChannelReadable(ctx context.Context, effective settings.Effective, bots []botpool.Bot) error {
	for _, bot := range bots {
		token := strings.TrimSpace(bot.Token)
		if token == "" {
			continue
		}
		client, clientErr := netclient.NewClient(effective.ProxyConfig(), netclient.ServiceTelegram)
		if clientErr != nil {
			continue
		}
		provider, buildErr := storage.NewTelegram(storage.TelegramConfig{
			Token:      token,
			ChatID:     effective.TelegramChannel.Value,
			APIBase:    effective.TelegramAPIBase.Value,
			HTTPClient: client,
		})
		if buildErr != nil || provider == nil {
			continue
		}
		if _, err := provider.ChatInfo(ctx); err == nil {
			return nil
		}
	}
	return errors.New("频道校验未通过")
}

// ensureRemovable 按整份保存后的保留列表检查：仍有媒体只能靠被移除的 Bot 读取时拒绝删除。
func ensureRemovable(ctx context.Context, deps Deps, bots []botpool.Bot) error {
	if deps.Posts == nil || deps.Bots == nil {
		return nil
	}
	existing, err := deps.Bots.List(ctx)
	if err != nil {
		return nil
	}
	// 只有真的从提交里消失的条目才算移除；保留但冻结或停用的 A 不算移除，
	// 否则「保留冻结 A、加入 B」会被误判成删掉 A 而无法保存。
	present := make(map[int64]bool, len(bots))
	for _, bot := range bots {
		if bot.BotID > 0 {
			present[bot.BotID] = true
		}
	}
	// 停用或冻结的条目不会参与读图，不能当作「已有其他 Bot 可读」的依据。
	usable := botpool.Usable(bots)
	keepIDs := make([]int64, 0, len(usable))
	for _, bot := range usable {
		if bot.BotID > 0 {
			keepIDs = append(keepIDs, bot.BotID)
		}
	}
	for _, old := range existing {
		if old.BotID == 0 || present[old.BotID] {
			continue
		}
		count, countErr := deps.Posts.CountImagesExclusiveToRemoval(ctx, old.BotID, keepIDs)
		if countErr != nil {
			return errors.New("检查媒体归属失败")
		}
		if count > 0 {
			name := strings.TrimSpace(old.Alias)
			if name == "" {
				name = "该 Bot"
			}
			return errors.New(name + "：仍有 " + strconv.FormatInt(count, 10) + " 张图只能由它读取，请先完成接管再移除")
		}
	}
	return nil
}

// updateBotsHandler 保存 Bot 池；未提交令牌的条目按编号沿用原值。
func updateBotsHandler(deps Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if deps.Bots == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "多 Bot 池未启用"})
			return
		}
		var request botsRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式不正确"})
			return
		}
		bots := make([]botpool.Bot, 0, len(request.Bots))
		for _, item := range request.Bots {
			bots = append(bots, botpool.Bot{
				BotID: item.BotID, Alias: item.Alias, Token: item.Token,
				Priority: item.Priority, Status: item.Status,
			})
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), botSaveTimeout)
		defer cancel()
		// 新提交的令牌保存前先探测身份与频道发布权限，失败时保留原有配置。
		ids, err := verifyBotTokens(ctx, deps, bots)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		for index := range bots {
			if bots[index].BotID == 0 {
				bots[index].BotID = ids[index]
			}
		}
		// 解析后的真实编号必须唯一，避免同一个 Bot 在列表里出现两次。
		seen := make(map[int64]string, len(bots))
		for _, bot := range bots {
			if bot.BotID == 0 {
				continue
			}
			if previous, ok := seen[bot.BotID]; ok {
				c.JSON(http.StatusBadRequest, gin.H{"error": "同一个 Bot 不能重复添加（" + previous + "）"})
				return
			}
			seen[bot.BotID] = bot.Alias
		}
		if err := ensureRemovable(ctx, deps, bots); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if err := deps.Bots.Replace(ctx, bots); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		// 保存成功后立刻替换运行配置：下一个任务就按新列表分配。
		if deps.ReloadBots != nil {
			if err := deps.ReloadBots(ctx); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "已保存，但运行配置切换失败，请重新保存"})
				return
			}
		}
		botsHandler(deps)(c)
	}
}
