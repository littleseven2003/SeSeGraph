package api

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/littleseven2003/SeSeGraph/backend/internal/telegraph"
)

const (
	// maxTelegraphBodyBytes 是账户配置请求体的上限。
	maxTelegraphBodyBytes = 4096
	maxTelegraphShortName = 32
)

// telegraphAccountRequest 是申请账户的请求体。
type telegraphAccountRequest struct {
	ShortName  string `json:"short_name"`
	AuthorName string `json:"author_name"`
}

// telegraphStatusHandler 返回账户配置状态，不包含访问令牌。
func telegraphStatusHandler(deps Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if deps.Telegraph == nil {
			c.JSON(http.StatusOK, telegraph.Status{Configured: false, Source: "none"})
			return
		}
		status, err := deps.Telegraph.Status(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "读取账户状态失败"})
			return
		}
		c.JSON(http.StatusOK, status)
	}
}

// telegraphAccountHandler 申请 Telegraph 账户并持久化访问令牌。
func telegraphAccountHandler(deps Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if deps.Telegraph == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Telegraph 未配置"})
			return
		}

		var req telegraphAccountRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "请求体过大"})
				return
			}
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式不正确"})
			return
		}
		shortName := strings.TrimSpace(req.ShortName)
		authorName := strings.TrimSpace(req.AuthorName)
		if len(shortName) > maxTelegraphShortName {
			c.JSON(http.StatusBadRequest, gin.H{"error": "账户短名不超过 32 字符"})
			return
		}
		if utf8.RuneCountInString(authorName) > maxAuthorRunes {
			c.JSON(http.StatusBadRequest, gin.H{"error": "作者名不超过 100 字"})
			return
		}

		status, err := deps.Telegraph.CreateAccount(c.Request.Context(), shortName, authorName)
		switch {
		case errors.Is(err, telegraph.ErrInvalidInput):
			c.JSON(http.StatusBadRequest, gin.H{"error": "账户参数不合法"})
			return
		case errors.Is(err, telegraph.ErrNotConfigured):
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Telegraph 未配置"})
			return
		case errors.Is(err, telegraph.ErrUpstream):
			c.JSON(http.StatusBadGateway, gin.H{"error": "Telegraph 请求失败"})
			return
		case err != nil:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存账户失败"})
			return
		}
		c.JSON(http.StatusCreated, status)
	}
}
