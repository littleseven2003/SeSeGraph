package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/littleseven2003/SeSeGraph/backend/internal/netclient"
	"github.com/littleseven2003/SeSeGraph/backend/internal/provider"
	"github.com/littleseven2003/SeSeGraph/backend/internal/settings"
)

// providerChanged 判断 Telegram 图床的传输层是否需要重建：
// 令牌、频道或影响 Telegram 的代理配置变化都要重新校验并切换提供方。
func providerChanged(current, prepared settings.Effective) bool {
	if current.TelegramToken.Value != prepared.TelegramToken.Value ||
		current.TelegramChannel.Value != prepared.TelegramChannel.Value {
		return true
	}
	return telegramProxyChanged(current.ProxyConfig(), prepared.ProxyConfig())
}

// telegramProxyChanged 只关心对 Telegram 生效的代理项，Telegraph 的开关不影响图床。
func telegramProxyChanged(before, after netclient.Config) bool {
	if before.Telegram != after.Telegram {
		return true
	}
	if !before.Telegram {
		return false
	}
	return before.Mode != after.Mode || before.URL != after.URL
}

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
	// Proxy 汇报代理配置的来源与开关。自定义地址可能带账密，只报是否已设置。
	Proxy struct {
		Mode             string `json:"mode"`
		ModeSource       string `json:"mode_source"`
		URLSet           bool   `json:"url_set"`
		URLSource        string `json:"url_source"`
		TelegramEnabled  bool   `json:"telegram_enabled"`
		TelegraphEnabled bool   `json:"telegraph_enabled"`
	} `json:"proxy"`
	// EnvManaged 列出由环境变量提供、界面不可覆盖的字段。
	EnvManaged []string `json:"env_managed"`
}

// endpointValue 返回可对外的上游地址：官方默认值直接展示，自定义地址只报来源。
// 自定义地址可能内嵌反代认证信息，因此不回显，服务端仍按生效值发起请求。
func endpointValue(value settings.Value) string {
	if value.Source == settings.SourceDefault {
		return value.Value
	}
	return ""
}

// buildSettingsView 汇总生效配置。敏感项只输出布尔标记。
func buildSettingsView(effective settings.Effective, publicURL string) settingsView {
	view := settingsView{PublicURL: publicURL, EnvManaged: []string{}}
	view.Telegram.TokenSet = effective.TelegramToken.Set()
	view.Telegram.TokenSource = effective.TelegramToken.Source
	view.Telegram.ChannelID = effective.TelegramChannel.Value
	view.Telegram.ChannelSource = effective.TelegramChannel.Source
	view.Telegram.APIBase = endpointValue(effective.TelegramAPIBase)
	view.Telegram.APIBaseSource = effective.TelegramAPIBase.Source

	view.Telegraph.TokenSet = effective.TelegraphToken.Set()
	view.Telegraph.TokenSource = effective.TelegraphToken.Source
	view.Telegraph.AuthorName = effective.TelegraphAuthor.Value
	view.Telegraph.AuthorSource = effective.TelegraphAuthor.Source
	view.Telegraph.APIBase = endpointValue(effective.TelegraphAPIBase)
	view.Telegraph.APIBaseSource = effective.TelegraphAPIBase.Source

	view.Proxy.Mode = effective.ProxyMode.Value
	view.Proxy.ModeSource = effective.ProxyMode.Source
	view.Proxy.URLSet = effective.ProxyURL.Set()
	view.Proxy.URLSource = effective.ProxyURL.Source
	view.Proxy.TelegramEnabled = effective.ProxyEnabled(netclient.ServiceTelegram)
	view.Proxy.TelegraphEnabled = effective.ProxyEnabled(netclient.ServiceTelegraph)

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
		{"proxy.mode", effective.ProxyMode},
		{"proxy.url", effective.ProxyURL},
		{"proxy.telegram", effective.ProxyTelegram},
		{"proxy.telegraph", effective.ProxyTelegraph},
	}
	for _, field := range fields {
		if field.value.Source == settings.SourceEnv {
			view.EnvManaged = append(view.EnvManaged, field.name)
		}
	}
	return view
}

// proxyClient 按生效的代理配置为一项服务构建检测客户端，构建失败时退回默认客户端。
func proxyClient(effective settings.Effective, service netclient.Service) *http.Client {
	client, err := netclient.NewClient(effective.ProxyConfig(), service)
	if err != nil {
		return nil
	}
	return client
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
// 上游地址（api_base）不在请求体内：它决定令牌发往何处，只能由环境变量或官方默认值提供。
type settingsPatchRequest struct {
	Telegram *struct {
		Token     *string `json:"token"`
		ChannelID *string `json:"channel_id"`
	} `json:"telegram"`
	Telegraph *struct {
		AuthorName *string `json:"author_name"`
	} `json:"telegraph"`
	Proxy *struct {
		Mode             *string `json:"mode"`
		URL              *string `json:"url"`
		TelegramEnabled  *bool   `json:"telegram_enabled"`
		TelegraphEnabled *bool   `json:"telegraph_enabled"`
	} `json:"proxy"`
}

// toPatch 把请求体转换成配置补丁。
func (r settingsPatchRequest) toPatch() settings.Patch {
	var patch settings.Patch
	if r.Telegram != nil {
		patch.TelegramToken = r.Telegram.Token
		patch.TelegramChannel = r.Telegram.ChannelID
	}
	if r.Telegraph != nil {
		patch.TelegraphAuthor = r.Telegraph.AuthorName
	}
	if r.Proxy != nil {
		patch.ProxyMode = r.Proxy.Mode
		patch.ProxyURL = r.Proxy.URL
		patch.ProxyTelegram = r.Proxy.TelegramEnabled
		patch.ProxyTelegraph = r.Proxy.TelegraphEnabled
	}
	return patch
}

// updateSettingsHandler 校验并保存设置，成功后返回最新视图。
// 保存全程持有设置锁：先做字段与图床凭据校验，再落库并热切换，避免并发保存交错生效。
func updateSettingsHandler(deps Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if deps.Settings == nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "服务器内部错误"})
			return
		}
		if deps.SettingsLock != nil {
			deps.SettingsLock.Lock()
			defer deps.SettingsLock.Unlock()
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

		// 先在内存中解析补丁：校验失败不会写库。
		prepared, err := deps.Settings.Prepare(c.Request.Context(), patch)
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

		// 图床凭据变化时先验证候选配置：拿到候选 Bot 编号，必要时独占上传队列。
		current, err := deps.Settings.Effective(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "读取配置失败"})
			return
		}
		var pending *provider.Candidate
		if deps.PrepareProvider != nil && providerChanged(current, prepared) {
			pending, err = deps.PrepareProvider(c.Request.Context(), prepared)
			if err != nil {
				switch {
				case errors.Is(err, provider.ErrBotMismatch):
					c.JSON(http.StatusConflict, gin.H{"error": "图库已有其他 Bot 上传的图片，切换后这些图片将无法读取"})
				case errors.Is(err, provider.ErrUploadBusy):
					c.JSON(http.StatusConflict, gin.H{"error": "有图片正在上传，请稍后再切换图床"})
				case errors.Is(err, provider.ErrVerify):
					c.JSON(http.StatusConflict, gin.H{"error": "无法验证新的 Bot 凭据，请检查令牌与网络"})
				default:
					c.JSON(http.StatusInternalServerError, gin.H{"error": "校验图床配置失败，请稍后重试"})
				}
				return
			}
			defer pending.Abort()
		}

		// 配置与 Bot 归属在同一个事务内落库：写失败时两者都不写入，运行时也不会切换。
		err = deps.Settings.UpdateWithBot(c.Request.Context(), patch, pending.BotID())
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

		// 落库成功后才让已验证的候选生效，复用 Prepare 的校验结果，不再访问上游。
		if pending != nil && deps.CommitProvider != nil {
			deps.CommitProvider(c.Request.Context(), pending)
		}

		if deps.Reload != nil {
			// 收尾失败时配置与提供方已是新值，界面提示重试与检查。
			if err := deps.Reload(c.Request.Context()); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "配置已保存，但生效失败，请检查后重试"})
				return
			}
		}

		effective, err := deps.Settings.Effective(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "读取配置失败"})
			return
		}
		c.JSON(http.StatusOK, buildSettingsView(effective, deps.PublicURL))
	}
}

// testConnectionHandler 并行检测 Telegram 与 Telegraph，返回状态与耗时。
// 只做只读请求，不向频道发送内容，也不回显任何凭据。
func testConnectionHandler(deps Deps) gin.HandlerFunc {
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
		// 自检走与生产客户端相同的代理配置，保证结果反映实际链路。
		result := settings.Probe(ctx, settings.ProbeOptions{
			TelegramToken:    effective.TelegramToken.Value,
			TelegramChannel:  effective.TelegramChannel.Value,
			TelegramAPIBase:  effective.TelegramAPIBase.Value,
			TelegraphToken:   effective.TelegraphToken.Value,
			TelegraphAPIBase: effective.TelegraphAPIBase.Value,
			TelegramClient:   proxyClient(effective, netclient.ServiceTelegram),
			TelegraphClient:  proxyClient(effective, netclient.ServiceTelegraph),
			Timeout:          settings.DefaultProbeTimeout,
		})
		c.JSON(http.StatusOK, result)
	}
}
