package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/littleseven2003/SeSeGraph/backend/internal/auth"
	"github.com/littleseven2003/SeSeGraph/backend/internal/backup"
	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
	"github.com/littleseven2003/SeSeGraph/backend/internal/provider"
	"github.com/littleseven2003/SeSeGraph/backend/internal/publisher"
	"github.com/littleseven2003/SeSeGraph/backend/internal/settings"
	"github.com/littleseven2003/SeSeGraph/backend/internal/storage"
	"github.com/littleseven2003/SeSeGraph/backend/internal/telegraph"
	"github.com/littleseven2003/SeSeGraph/backend/internal/traffic"
	"github.com/littleseven2003/SeSeGraph/backend/internal/views"
)

// Deps 汇总构建路由所需的依赖。
type Deps struct {
	Version    string
	Commit     string
	Auth       *auth.Service
	Sessions   *auth.Sessions
	SetupToken string
	Logger     *log.Logger
	WebFS      fs.FS
	DataDir    string
	Posts      *posts.Repository
	// Streams 提供按远端标识流式读取的能力，未配置时 /i/ 路由返回 503。
	Streams storage.StreamProvider
	// ImageConcurrency 是图片网关的并发上限，非正时使用默认值。
	ImageConcurrency int
	// ImageQueueDepth 是满额后的排队人数上限，非正时使用默认值。
	ImageQueueDepth int
	// ImageQueueWait 是单个请求排队等待的最长时间，非正时使用默认值。
	ImageQueueWait time.Duration
	// UploadConcurrency 是同时接收上传的上限，非正时使用默认值。
	UploadConcurrency int
	// UploadQueueWait 是上传满额后的最长排队时间，非正时使用默认值。
	UploadQueueWait time.Duration
	// Traffic 统计图片出站流量并按月度上限熔断，未装配时不计流量。
	Traffic *traffic.Recorder
	// Backup 生成并管理 SQLite 热备，未装配时备份接口不可用。
	Backup *backup.Manager
	// BackupSchedule 是定时备份的展示文案，为空表示未启用定时备份。
	BackupSchedule string
	// ApplyBackupSchedule 在设置保存后热更新自动备份计划。
	ApplyBackupSchedule func(settings.Effective) error
	// Telegraph 负责账户配置与建号，未配置时为 nil。
	Telegraph *telegraph.Manager
	// PublicURL 是对外访问地址，发布前要求为 HTTPS。
	PublicURL string
	// RSSEnabled 控制是否对外提供图集订阅源。
	RSSEnabled bool
	// Editor 负责把已发布帖的元数据同步到远端页面。
	Editor *publisher.Editor
	// Settings 解析运行期可修改的配置，未装配时设置接口不可用。
	Settings *settings.Resolver
	// Reload 在设置保存并切换图床后收尾，未装配时不做额外处理。
	Reload func(context.Context) error
	// PrepareProvider 在保存前校验候选图床配置，返回待提交的切换候选。
	// 候选携带候选 Bot 编号，需要与配置在同一事务内持久化。
	PrepareProvider func(context.Context, settings.Effective) (*provider.Candidate, error)
	// CommitProvider 在配置落库成功后让已验证的候选生效，未装配时跳过。
	CommitProvider func(context.Context, *provider.Candidate)
	// SettingsLock 串行化设置保存，保证落库顺序与生效顺序一致。
	SettingsLock *sync.Mutex
	// Views 提供浏览量刷新，未装配时刷新接口返回 503。
	Views *views.Service
}

// maxAuthBodyBytes 是登录与初始化请求体的上限，超出的请求不会被解码。
const maxAuthBodyBytes = 4096

// maxToggleBodyBytes 是撤回与恢复请求体的上限，只承载一个布尔目标状态。
const maxToggleBodyBytes = 256

// setupTokenHeader 是初始化凭证的请求头名称。
const setupTokenHeader = "X-Setup-Token"

// credentialsRequest 是初始化与登录接口的请求体。
type credentialsRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// NewRouter 构建对外路由。健康检查与鉴权入口公开，管理接口需要登录。
func NewRouter(deps Deps) *gin.Engine {
	router := gin.New()
	router.Use(Recovery(deps.Logger), AccessLog(deps.Logger))

	router.GET("/api/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":  "ok",
			"version": deps.Version,
			"commit":  deps.Commit,
		})
	})
	router.GET("/api/auth/status", statusHandler(deps))
	router.POST("/api/auth/setup", limitBody(maxAuthBodyBytes), setupHandler(deps))
	router.POST("/api/auth/login", limitBody(maxAuthBodyBytes), loginHandler(deps))
	router.POST("/api/auth/logout", logoutHandler(deps))

	// 公开图片网关：短码即凭证，不经过管理会话。
	gateway := newImageGateway(deps.Posts, deps.Streams, deps.ImageConcurrency,
		deps.ImageQueueDepth, deps.ImageQueueWait, deps.Logger, deps.Traffic)
	router.GET("/i/:short_code", gateway.handle)
	// HEAD 供浏览器与播放器探测长度和区间支持，不返回正文。
	router.HEAD("/i/:short_code", gateway.handle)

	// 公开订阅源：默认关闭，启用且配置有效 HTTPS 地址时才提供。
	router.GET("/api/site", siteHandler(deps))
	router.GET("/rss.xml", rssHandler(deps))
	router.HEAD("/rss.xml", rssHandler(deps))

	// 上传限流与图片网关相互独立：前者约束写请求，后者约束出站取流。
	uploads := newUploadLimiter(deps.UploadConcurrency, deps.UploadQueueWait)

	admin := router.Group("/api", RequireAuth(deps.Sessions, deps.Auth))
	admin.GET("/posts", listPostsHandler(deps))
	admin.GET("/tasks", tasksHandler(deps))
	admin.GET("/posts/:id", postDetailHandler(deps))
	admin.GET("/posts/:id/media/:sort", adminMediaHandler(deps))
	admin.GET("/posts/:id/media/:sort/thumb", mediaThumbHandler(deps))
	admin.PUT("/posts/:id/media/:sort/thumb", limitBody(maxThumbBodyBytes), mediaThumbUploadHandler(deps))
	admin.POST("/posts/:id/media-thumbs/reset", mediaThumbResetHandler(deps))
	admin.PUT("/posts/:id", limitBody(maxCreatePostBodyBytes), updatePostHandler(deps))
	admin.PUT("/posts/:id/cover", limitBody(maxCreatePostBodyBytes), changeCoverHandler(deps))
	admin.DELETE("/posts/:id", deletePostHandler(deps))
	admin.PATCH("/posts/:id/toggle", limitBody(maxToggleBodyBytes), togglePostHandler(deps))
	admin.GET("/stats/traffic", trafficHandler(deps))
	admin.GET("/tags", tagsHandler(deps))
	admin.GET("/thumbs/:filename", thumbHandler(deps))
	admin.POST("/posts", limitBody(maxCreatePostBodyBytes), createPostHandler(deps))
	admin.POST("/posts/:id/images", uploadImageHandler(deps, uploads))
	admin.POST("/posts/:id/commit", commitPostHandler(deps))
	admin.POST("/posts/:id/pause", pauseTaskHandler(deps))
	admin.POST("/posts/:id/resume", resumeTaskHandler(deps))
	admin.POST("/posts/:id/stop", stopTaskHandler(deps))
	admin.GET("/settings", settingsHandler(deps))
	admin.PUT("/settings", limitBody(maxSettingsBodyBytes), updateSettingsHandler(deps))
	admin.POST("/settings/test-connection", testConnectionHandler(deps))
	admin.GET("/telegraph/status", telegraphStatusHandler(deps))
	admin.POST("/settings/media-thumbs/clear", mediaThumbClearHandler(deps))
	admin.POST("/settings/telegraph/create-account", limitBody(maxTelegraphBodyBytes), telegraphAccountHandler(deps))
	admin.PUT("/settings/admin-credentials", limitBody(maxAccountBodyBytes), updateCredentialsHandler(deps))
	admin.POST("/backup/trigger", backupTriggerHandler(deps))
	admin.GET("/backup/download", backupDownloadHandler(deps))
	admin.GET("/backup/status", backupStatusHandler(deps))
	admin.GET("/posts/:id/progress", progressHandler(deps))
	admin.POST("/posts/:id/retry", retryHandler(deps))
	admin.POST("/posts/:id/publish", publishPostHandler(deps))
	admin.POST("/posts/:id/views/refresh", refreshViewsHandler(deps))

	if deps.WebFS != nil {
		router.NoRoute(spaHandler(deps.WebFS, subscriptionURL(deps)))
	}

	return router
}

// statusHandler 返回是否已初始化以及当前请求是否已登录。
func statusHandler(deps Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		initialized, err := deps.Auth.IsInitialized(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "读取管理员状态失败"})
			return
		}
		authenticated := false
		if deps.Sessions != nil && deps.Auth != nil {
			if _, version, err := deps.Sessions.AuthenticateRequest(c.Request); err == nil {
				if current, err := deps.Auth.SessionVersion(c.Request.Context()); err == nil && current == version {
					authenticated = true
				}
			}
		}
		c.JSON(http.StatusOK, gin.H{
			"initialized":   initialized,
			"authenticated": authenticated,
		})
	}
}

// setupHandler 完成首次管理员初始化，已初始化后不再允许调用。
func setupHandler(deps Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 初始化凭证必须显式配置，避免经由反代的可达入口被抢先注册。
		if deps.SetupToken == "" {
			c.JSON(http.StatusForbidden, gin.H{"error": "未配置初始化凭证，请通过环境变量完成首次初始化"})
			return
		}
		provided := c.GetHeader(setupTokenHeader)
		if subtle.ConstantTimeCompare([]byte(provided), []byte(deps.SetupToken)) != 1 {
			c.JSON(http.StatusForbidden, gin.H{"error": "初始化凭证不正确"})
			return
		}

		var req credentialsRequest
		if !bindCredentials(c, &req) {
			return
		}

		switch err := deps.Auth.Create(c.Request.Context(), req.Username, req.Password); {
		case err == nil:
		case errors.Is(err, auth.ErrAlreadyInitialized):
			c.JSON(http.StatusConflict, gin.H{"error": "管理员已初始化"})
			return
		case errors.Is(err, auth.ErrInvalidInput):
			c.JSON(http.StatusBadRequest, gin.H{"error": "账号或密码不符合要求"})
			return
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "初始化管理员失败"})
			return
		}

		creds, err := deps.Auth.Credentials(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "签发会话失败"})
			return
		}
		if err := issueSession(c, deps, creds); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "签发会话失败"})
			return
		}
		c.JSON(http.StatusCreated, gin.H{"username": creds.Username})
	}
}

// loginHandler 校验账号密码并签发会话 Cookie。
func loginHandler(deps Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req credentialsRequest
		if !bindCredentials(c, &req) {
			return
		}

		creds, err := deps.Auth.Authenticate(c.Request.Context(), req.Username, req.Password)
		switch {
		case err == nil:
		case errors.Is(err, auth.ErrNotInitialized):
			c.JSON(http.StatusConflict, gin.H{"error": "管理员尚未初始化"})
			return
		case errors.Is(err, auth.ErrInvalidCredentials):
			c.JSON(http.StatusUnauthorized, gin.H{"error": "账号或密码不正确"})
			return
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "登录失败"})
			return
		}

		if err := issueSession(c, deps, creds); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "签发会话失败"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"username": creds.Username})
	}
}

// logoutHandler 清除会话 Cookie。
func logoutHandler(deps Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if deps.Sessions != nil {
			deps.Sessions.ClearCookie(c.Writer)
		}
		c.Status(http.StatusNoContent)
	}
}

// issueSession 用校验时读到的会话版本签发 Cookie。
func issueSession(c *gin.Context, deps Deps, creds auth.Credentials) error {
	if deps.Sessions == nil {
		return auth.ErrInvalidSession
	}
	deps.Sessions.SetCookie(c.Writer, strings.TrimSpace(creds.Username), creds.Version)
	return nil
}

// limitBody 限制请求体大小，超限时读取会直接失败。
func limitBody(limit int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		}
		c.Next()
	}
}

// bindCredentials 解析请求体，超限时返回 413，格式错误时返回 400。
func bindCredentials(c *gin.Context, req *credentialsRequest) bool {
	if err := c.ShouldBindJSON(req); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "请求体过大"})
			return false
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式不正确"})
		return false
	}
	return true
}
