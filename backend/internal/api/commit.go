package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
)

// commitPostHandler 校验图片是否齐全，齐全后把帖子置为待上传。
func commitPostHandler(deps Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		postID, parseErr := strconv.ParseInt(c.Param("id"), 10, 64)
		if parseErr != nil || postID <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "帖子编号不合法"})
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

		// 已入队的帖子重复提交视为成功，避免网络重试造成误报。
		if post.Status == posts.StatusQueued {
			c.JSON(http.StatusOK, gin.H{
				"id":          post.ID,
				"status":      posts.StatusQueued,
				"image_count": post.ImageCount,
			})
			return
		}
		if post.Status != posts.StatusUploading {
			c.JSON(http.StatusConflict, gin.H{"error": "当前状态不允许提交"})
			return
		}

		missing, err := deps.Posts.MissingOrders(c.Request.Context(), postID, post.ImageCount)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "校验图片完整性失败"})
			return
		}
		if len(missing) > 0 {
			c.JSON(http.StatusConflict, gin.H{
				"error":   "图片未上传完整",
				"missing": missing,
			})
			return
		}

		if err := deps.Posts.UpdateStatus(c.Request.Context(), postID, posts.StatusQueued); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "提交任务失败"})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"id":          post.ID,
			"status":      posts.StatusQueued,
			"image_count": post.ImageCount,
		})
	}
}
