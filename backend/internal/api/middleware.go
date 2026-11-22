package api

import (
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/littleseven2003/SeSeGraph/backend/internal/auth"
)

// routeLabelKey 是处理函数主动标注日志路由的上下文键。
// 未注册路由的请求（如页面回退）没有路由模板，用标注替代原始路径，
// 既不把猜测的路径写进日志，也不至于全部记成未匹配。
const routeLabelKey = "sesegraph.route_label"

// 页面回退与静态资源的路由标注。
const (
	routeLabelSPA   = "spa"
	routeLabelAsset = "asset"
)

// routeOf 返回日志用的路由描述：优先路由模板，其次处理函数的标注。
func routeOf(c *gin.Context) string {
	if route := c.FullPath(); route != "" {
		return route
	}
	if label, ok := c.Get(routeLabelKey); ok {
		if text, ok := label.(string); ok && text != "" {
			return text
		}
	}
	return "unmatched"
}

// AccessLog 记录每次请求的方法、路由模板、状态码与耗时。
// 只使用路由模板或固定标注，不记录查询串、请求头、Cookie 与请求体，避免泄露密码或会话。
func AccessLog(logger *log.Logger) gin.HandlerFunc {
	if logger == nil {
		logger = log.Default()
	}
	return func(c *gin.Context) {
		started := time.Now()
		c.Next()

		logger.Printf("请求：%s %s 状态 %d 耗时 %s",
			c.Request.Method, routeOf(c), c.Writer.Status(), time.Since(started).Round(time.Millisecond))
	}
}

// Recovery 捕获处理过程中的异常，返回统一错误响应。
// 日志只记录方法与路由模板，不输出请求内容或异常取值。
func Recovery(logger *log.Logger) gin.HandlerFunc {
	if logger == nil {
		logger = log.Default()
	}
	return func(c *gin.Context) {
		defer func() {
			if rec := recover(); rec != nil {
				logger.Printf("请求处理异常：%s %s", c.Request.Method, routeOf(c))
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "服务器内部错误"})
			}
		}()
		c.Next()
	}
}

// RequireAuth 校验会话 Cookie，未登录时返回 401。
func RequireAuth(sessions *auth.Sessions) gin.HandlerFunc {
	return func(c *gin.Context) {
		if sessions == nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "服务器内部错误"})
			return
		}
		if _, err := sessions.AuthenticateRequest(c.Request); err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "未登录"})
			return
		}
		c.Next()
	}
}
