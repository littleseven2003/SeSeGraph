package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
)

// setDisabledRequest 是撤回/恢复请求体：显式给出目标状态。
// 用指针区分「未提供」与 false，避免缺字段被当作恢复处理。
type setDisabledRequest struct {
	Disabled *bool `json:"disabled"`
}

// togglePostHandler 把单帖熔断状态写为请求给定的目标值。
// 禁用后该帖短码在源站立即 404，恢复时沿用原有公开授权；
// 重复提交同一目标值保持原状态，客户端超时重试不会反向公开图片。
func togglePostHandler(deps Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		postID, parseErr := strconv.ParseInt(c.Param("id"), 10, 64)
		if parseErr != nil || postID <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "帖子编号不合法"})
			return
		}

		var req setDisabledRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "请求体过大"})
				return
			}
			c.JSON(http.StatusBadRequest, gin.H{"error": "需要明确指定 disabled 目标状态"})
			return
		}
		if req.Disabled == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "需要明确指定 disabled 目标状态"})
			return
		}

		disabled, public, err := deps.Posts.SetDisabled(c.Request.Context(), postID, *req.Disabled)
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
