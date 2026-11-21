package api

import (
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/littleseven2003/SeSeGraph/backend/internal/auth"
)

// AccessLog 记录每次请求的方法、路由模板、状态码与耗时。
// 只使用路由模板，不记录查询串、请求头、Cookie 与请求体，避免泄露密码或会话。
func AccessLog(logger *log.Logger) gin.HandlerFunc {
	if logger == nil {
		logger = log.Default()
	}
	return func(c *gin.Context) {
		started := time.Now()
		c.Next()

		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		logger.Printf("请求：%s %s 状态 %d 耗时 %s",
			c.Request.Method, route, c.Writer.Status(), time.Since(started).Round(time.Millisecond))
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
				route := c.FullPath()
				if route == "" {
					route = "unmatched"
				}
				logger.Printf("请求处理异常：%s %s", c.Request.Method, route)
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
