package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/littleseven2003/SeSeGraph/backend/internal/settings"
)

// maxSettingsBodyBytes 是设置更新请求体的上限，只承载几项短文本配置。
const maxSettingsBodyBytes = 4096

// connectivityTimeout 覆盖两项并行自检的耗时上限。
const connectivityTimeout = 12 * time.Second

// settingsView 是设置读取响应。令牌只报告是否已设置与来源，绝不回显取值。
type settingsView struct {
	PublicURL string `json:"public_url"`
	Telegram  struct {
		TokenSet      bool   `json:"token_set"`
		TokenSource   string `json:"token_source"`
		ChannelID     string `json:"channel_id"`
		ChannelSource string `json:"channel_source"`
		APIBase       string `json:"api_base"`
		APIBaseSource string `json:"api_base_source"`
	} `json:"telegram"`
	Telegraph struct {
		TokenSet      bool   `json:"token_set"`
		TokenSource   string `json:"token_source"`
		AuthorName    string `json:"author_name"`
		AuthorSource  string `json:"author_source"`
		APIBase       string `json:"api_base"`
		APIBaseSource string `json:"api_base_source"`
	} `json:"telegraph"`
	// EnvManaged 列出由环境变量提供、界面不可覆盖的字段。
	EnvManaged []string `json:"env_managed"`
}

// buildSettingsView 汇总生效配置。敏感项只输出布尔标记。
func buildSettingsView(effective settings.Effective, publicURL string) settingsView {
	view := settingsView{PublicURL: publicURL, EnvManaged: []string{}}
	view.Telegram.TokenSet = effective.TelegramToken.Set()
	view.Telegram.TokenSource = effective.TelegramToken.Source
	view.Telegram.ChannelID = effective.TelegramChannel.Value
	view.Telegram.ChannelSource = effective.TelegramChannel.Source
	view.Telegram.APIBase = effective.TelegramAPIBase.Value
	view.Telegram.APIBaseSource = effective.TelegramAPIBase.Source

	view.Telegraph.TokenSet = effective.TelegraphToken.Set()
	view.Telegraph.TokenSource = effective.TelegraphToken.Source
	view.Telegraph.AuthorName = effective.TelegraphAuthor.Value
	view.Telegraph.AuthorSource = effective.TelegraphAuthor.Source
	view.Telegraph.APIBase = effective.TelegraphAPIBase.Value
	view.Telegraph.APIBaseSource = effective.TelegraphAPIBase.Source

	fields := []struct {
		name  string
		value settings.Value
	}{
		{"telegram.token", effective.TelegramToken},
		{"telegram.channel_id", effective.TelegramChannel},
		{"telegram.api_base", effective.TelegramAPIBase},
		{"telegraph.token", effective.TelegraphToken},
		{"telegraph.author_name", effective.TelegraphAuthor},
		{"telegraph.api_base", effective.TelegraphAPIBase},
	}
	for _, field := range fields {
		if field.value.Source == settings.SourceEnv {
			view.EnvManaged = append(view.EnvManaged, field.name)
		}
	}
	return view
}

// settingsHandler 返回当前生效配置与各项来源。
func settingsHandler(deps Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if deps.Settings == nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "服务器内部错误"})
			return
		}
		effective, err := deps.Settings.Effective(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "读取配置失败"})
			return
		}
		c.JSON(http.StatusOK, buildSettingsView(effective, deps.PublicURL))
	}
}

// settingsPatchRequest 是设置更新请求体，未出现的字段保持不变，空串表示清除数据库值。
type settingsPatchRequest struct {
	Telegram *struct {
		Token     *string `json:"token"`
		ChannelID *string `json:"channel_id"`
		APIBase   *string `json:"api_base"`
	} `json:"telegram"`
	Telegraph *struct {
		AuthorName *string `json:"author_name"`
		APIBase    *string `json:"api_base"`
	} `json:"telegraph"`
}

// toPatch 把请求体转换成配置补丁。
func (r settingsPatchRequest) toPatch() settings.Patch {
	var patch settings.Patch
	if r.Telegram != nil {
		patch.TelegramToken = r.Telegram.Token
		patch.TelegramChannel = r.Telegram.ChannelID
		patch.TelegramAPIBase = r.Telegram.APIBase
	}
	if r.Telegraph != nil {
		patch.TelegraphAuthor = r.Telegraph.AuthorName
		patch.TelegraphAPIBase = r.Telegraph.APIBase
	}
	return patch
}

// updateSettingsHandler 校验并保存设置，成功后返回最新视图。
func updateSettingsHandler(deps Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if deps.Settings == nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "服务器内部错误"})
			return
		}
		var req settingsPatchRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "请求体过大"})
				return
			}
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式不正确"})
			return
		}
		patch := req.toPatch()
		if patch.Empty() {
			c.JSON(http.StatusBadRequest, gin.H{"error": "没有需要保存的配置"})
			return
		}

		err := deps.Settings.Update(c.Request.Context(), patch)
		var problem *settings.FieldProblem
		var conflict *settings.FieldConflict
		switch {
		case err == nil:
		case errors.As(err, &problem):
			c.JSON(http.StatusBadRequest, gin.H{"error": "配置取值不合法", "fields": problem.Fields})
			return
		case errors.As(err, &conflict):
			c.JSON(http.StatusConflict, gin.H{
				"error":  "以下配置由环境变量提供，无法在界面修改",
				"fields": conflict.Fields,
			})
			return
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存配置失败"})
			return
		}

		effective, err := deps.Settings.Effective(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "读取配置失败"})
			return
		}
		c.JSON(http.StatusOK, buildSettingsView(effective, deps.PublicURL))
	}
}

// connectivityHandler 并行检测 Telegram 与 Telegraph，返回状态与耗时。
// 只做只读请求，不向频道发送内容，也不回显任何凭据。
func connectivityHandler(deps Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if deps.Settings == nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "服务器内部错误"})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), connectivityTimeout)
		defer cancel()

		effective, err := deps.Settings.Effective(ctx)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "读取配置失败"})
			return
		}
		result := settings.Probe(ctx, settings.ProbeOptions{
			TelegramToken:    effective.TelegramToken.Value,
			TelegramChannel:  effective.TelegramChannel.Value,
			TelegramAPIBase:  effective.TelegramAPIBase.Value,
			TelegraphToken:   effective.TelegraphToken.Value,
			TelegraphAPIBase: effective.TelegraphAPIBase.Value,
			Timeout:          settings.DefaultProbeTimeout,
		})
		c.JSON(http.StatusOK, result)
	}
}
