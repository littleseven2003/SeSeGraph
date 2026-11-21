package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
)

// togglePostHandler 切换单帖熔断状态。
// 禁用后该帖短码在源站立即 404，恢复时沿用原有公开授权。
func togglePostHandler(deps Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		postID, parseErr := strconv.ParseInt(c.Param("id"), 10, 64)
		if parseErr != nil || postID <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "帖子编号不合法"})
			return
		}

		disabled, public, err := deps.Posts.ToggleDisabled(c.Request.Context(), postID)
		if errors.Is(err, posts.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "帖子不存在"})
			return
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "更新熔断状态失败"})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"id":       postID,
			"disabled": disabled,
			"public":   public,
		})
	}
}
