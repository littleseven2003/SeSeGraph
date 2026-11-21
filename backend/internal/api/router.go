package api

import (
	"crypto/subtle"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/littleseven2003/SeSeGraph/backend/internal/auth"
	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
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
}

// maxAuthBodyBytes 是登录与初始化请求体的上限，超出的请求不会被解码。
const maxAuthBodyBytes = 4096

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

	admin := router.Group("/api", RequireAuth(deps.Sessions))
	admin.GET("/posts", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"items": []any{}})
	})
	admin.POST("/posts", limitBody(maxCreatePostBodyBytes), createPostHandler(deps))
	admin.POST("/posts/:id/images", uploadImageHandler(deps))
	admin.POST("/posts/:id/commit", commitPostHandler(deps))
	admin.GET("/posts/:id/progress", progressHandler(deps))
	admin.POST("/posts/:id/retry", retryHandler(deps))

	if deps.WebFS != nil {
		router.NoRoute(spaHandler(deps.WebFS))
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
		if deps.Sessions != nil {
			if _, err := deps.Sessions.AuthenticateRequest(c.Request); err == nil {
				authenticated = true
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

		if err := issueSession(c, deps, req.Username); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "签发会话失败"})
			return
		}
		c.JSON(http.StatusCreated, gin.H{"username": strings.TrimSpace(req.Username)})
	}
}

// loginHandler 校验账号密码并签发会话 Cookie。
func loginHandler(deps Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req credentialsRequest
		if !bindCredentials(c, &req) {
			return
		}

		switch err := deps.Auth.Authenticate(c.Request.Context(), req.Username, req.Password); {
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

		if err := issueSession(c, deps, req.Username); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "签发会话失败"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"username": strings.TrimSpace(req.Username)})
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

func issueSession(c *gin.Context, deps Deps, username string) error {
	if deps.Sessions == nil {
		return auth.ErrInvalidSession
	}
	deps.Sessions.SetCookie(c.Writer, strings.TrimSpace(username))
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
