package api

import (
	"errors"
	"log"
	"net"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/littleseven2003/SeSeGraph/backend/internal/auth"
)

// Deps 汇总构建路由所需的依赖。
type Deps struct {
	Version          string
	Commit           string
	Auth             *auth.Service
	Sessions         *auth.Sessions
	AllowRemoteSetup bool
	Logger           *log.Logger
}

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
	router.POST("/api/auth/setup", setupHandler(deps))
	router.POST("/api/auth/login", loginHandler(deps))
	router.POST("/api/auth/logout", logoutHandler(deps))

	admin := router.Group("/api", RequireAuth(deps.Sessions))
	admin.GET("/posts", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"items": []any{}})
	})

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
		if !deps.AllowRemoteSetup && !isLoopbackRequest(c.Request.RemoteAddr) {
			c.JSON(http.StatusForbidden, gin.H{"error": "首次初始化仅允许从本机访问"})
			return
		}

		var req credentialsRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式不正确"})
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
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式不正确"})
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

// isLoopbackRequest 判断请求来源是否为本机。只取连接地址，不信任代理头。
func isLoopbackRequest(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
