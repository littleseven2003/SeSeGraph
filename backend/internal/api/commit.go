package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
	"github.com/littleseven2003/SeSeGraph/backend/internal/settings"
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

		missing, enqueueErr := deps.Posts.Enqueue(c.Request.Context(), postID)
		switch {
		case enqueueErr == nil:
		case errors.Is(enqueueErr, posts.ErrAlreadyQueued):
			// 已入队的帖子重复提交视为成功，避免网络重试造成误报。
			c.JSON(http.StatusOK, gin.H{
				"id":          post.ID,
				"status":      posts.StatusQueued,
				"image_count": post.ImageCount,
			})
			return
		case errors.Is(enqueueErr, posts.ErrNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "帖子不存在"})
			return
		case errors.Is(enqueueErr, posts.ErrNotUploading):
			c.JSON(http.StatusConflict, gin.H{"error": "当前状态不允许提交"})
			return
		default:
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

		// 首次入队时冻结链接生成方式：之后改设置不影响这次发布。
		mode := settings.PathModeTitle
		if deps.Settings != nil {
			if effective, err := deps.Settings.Effective(c.Request.Context()); err == nil {
				mode = effective.TelegraphPathMode.Value
			}
		}
		slug := ""
		if mode == settings.PathModeSlug {
			generated, err := posts.NewPublishSlug()
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "生成链接短码失败"})
				return
			}
			slug = generated
		}
		if err := deps.Posts.FreezePublishPath(c.Request.Context(), post.ID, mode, slug); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存链接生成方式失败"})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"id":          post.ID,
			"status":      posts.StatusQueued,
			"image_count": post.ImageCount,
		})
	}
}
