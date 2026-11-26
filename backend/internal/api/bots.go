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
	for _, bot := range bots {
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
		ids[indexForBot(bots, bot)] = botID
	}
	return ids, nil
}

// indexForBot 按令牌找到候选在列表中的位置，用于回填解析出的编号。
func indexForBot(bots []botpool.Bot, target botpool.Bot) int {
	for index, bot := range bots {
		if bot.Token == target.Token {
			return index
		}
	}
	return 0
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
		// 删除保护：仍有媒体只能靠被移除的 Bot 读取时，先接管再删。
		if deps.Posts != nil {
			existing, _ := deps.Bots.List(ctx)
			keep := make(map[int64]bool, len(bots))
			for _, bot := range bots {
				keep[bot.BotID] = true
			}
			keepIDs := make([]int64, 0, len(bots))
			for _, bot := range bots {
				if bot.BotID > 0 {
					keepIDs = append(keepIDs, bot.BotID)
				}
			}
			for _, old := range existing {
				if old.BotID == 0 || keep[old.BotID] {
					continue
				}
				count, countErr := deps.Posts.CountImagesExclusiveToRemoval(ctx, old.BotID, keepIDs)
				if countErr != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": "检查媒体归属失败"})
					return
				}
				if count > 0 {
					name := strings.TrimSpace(old.Alias)
					if name == "" {
						name = "该 Bot"
					}
					c.JSON(http.StatusBadRequest, gin.H{"error": name + "：仍有 " + strconv.FormatInt(count, 10) + " 张图只能由它读取，请先完成接管再移除"})
					return
				}
			}
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
