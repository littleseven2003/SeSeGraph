package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/littleseven2003/SeSeGraph/backend/internal/config"
	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
)

// publishPostHandler 由管理员确认发布：校验发布条件后开启图片访问并进入发布队列。
// 旧版已存图任务必须显式调用本接口才会公开。
func publishPostHandler(deps Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		postID, parseErr := strconv.ParseInt(c.Param("id"), 10, 64)
		if parseErr != nil || postID <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "帖子编号不合法"})
			return
		}

		if rejectPendingDelete(c, deps, postID) {
			return
		}

		if deps.Telegraph == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Telegraph 未配置"})
			return
		}
		if _, err := deps.Telegraph.Client(c.Request.Context()); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Telegraph 未配置"})
			return
		}
		// 公开地址会被直接拼上图片路径，必须是结构完整的 HTTPS 地址。
		if !config.PublicURLIsHTTPS(deps.PublicURL) {
			c.JSON(http.StatusConflict, gin.H{"error": "需要先配置公开 HTTPS 地址"})
			return
		}

		post, err := deps.Posts.Get(c.Request.Context(), postID)
		if errors.Is(err, posts.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "帖子不存在"})
			return
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "读取帖子失败"})
			return
		}
		if post.Status != posts.StatusCompleted {
			c.JSON(http.StatusConflict, gin.H{"error": "图片尚未存图完成"})
			return
		}

		if err := deps.Posts.RequestPublish(c.Request.Context(), postID); err != nil {
			if errors.Is(err, posts.ErrPendingDelete) {
				c.JSON(http.StatusConflict, gin.H{"error": "帖子正在删除，无法继续操作"})
				return
			}
			if errors.Is(err, posts.ErrNotFound) {
				c.JSON(http.StatusConflict, gin.H{"error": "当前状态不可发布"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "发起发布失败"})
			return
		}

		c.JSON(http.StatusAccepted, gin.H{
			"id":            postID,
			"publish_state": posts.PublishPending,
			"public":        true,
		})
	}
}
