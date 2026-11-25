package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/littleseven2003/SeSeGraph/backend/internal/botpool"
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
	} `json:"bots"`
}

// botSaveTimeout 是保存 Bot 池的超时，与设置保存一致，避免请求挂住。
const botSaveTimeout = 15 * time.Second

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
				BotID: item.BotID, Alias: item.Alias, Token: item.Token, Priority: item.Priority,
			})
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), botSaveTimeout)
		defer cancel()
		if err := deps.Bots.Replace(ctx, bots); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		botsHandler(deps)(c)
	}
}
