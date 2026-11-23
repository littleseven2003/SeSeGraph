package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
)

// progressImage 是进度接口中的单张图片信息，不包含远端标识。
type progressImage struct {
	SortOrder int    `json:"sort_order"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
}

// progressHandler 返回帖子的上传进度。
func progressHandler(deps Deps) gin.HandlerFunc {
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

		images, err := deps.Posts.ListImages(c.Request.Context(), postID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "读取图片进度失败"})
			return
		}

		items := make([]progressImage, 0, len(images))
		stored, failed := 0, 0
		for _, image := range images {
			items = append(items, progressImage{
				SortOrder: image.SortOrder,
				Status:    image.Status,
				Error:     image.ErrorMessage,
			})
			switch image.Status {
			case posts.ImageStatusStored:
				stored++
			case posts.ImageStatusFailed:
				failed++
			}
		}

		tags, err := deps.Posts.ListTags(c.Request.Context(), postID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "读取标签失败"})
			return
		}

		publishState, publishURLs, public, err := deps.Posts.PublishStatus(c.Request.Context(), postID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "读取发布状态失败"})
			return
		}

		coverSort, err := deps.Posts.CoverSort(c.Request.Context(), postID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "读取封面序号失败"})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"id":               post.ID,
			"status":           post.Status,
			"tags":             tags,
			"image_count":      post.ImageCount,
			"control_state":    post.ControlState,
			"upload_round":     post.UploadRound,
			"cover_sort_order": coverSort,
			"upload_mode":      post.UploadMode,
			"stored_count":     stored,
			"failed_count":     failed,
			"images":           items,
			"publish_state":    publishState,
			"public":           public,
			"telegraph_urls":   publishURLs,
		})
	}
}

// retryHandler 把失败的图片重新排入队列。
func retryHandler(deps Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		postID, parseErr := strconv.ParseInt(c.Param("id"), 10, 64)
		if parseErr != nil || postID <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "帖子编号不合法"})
			return
		}

		if rejectPendingDelete(c, deps, postID) {
			return
		}

		retried, err := deps.Posts.ResetFailedImages(c.Request.Context(), postID)
		switch {
		case errors.Is(err, posts.ErrPendingDelete):
			c.JSON(http.StatusConflict, gin.H{"error": "帖子正在删除，无法继续操作"})
			return
		case errors.Is(err, posts.ErrNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "帖子不存在"})
			return
		case err != nil:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "重新排队失败"})
			return
		case retried == 0:
			c.JSON(http.StatusConflict, gin.H{"error": "没有需要重试的图片"})
			return
		}

		c.JSON(http.StatusAccepted, gin.H{
			"id":      postID,
			"status":  posts.StatusQueued,
			"retried": retried,
		})
	}
}

// trafficHandler 返回当月出站流量与阈值，供后台查看。
func trafficHandler(deps Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if deps.Traffic == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "流量统计不可用"})
			return
		}
		stats, err := deps.Traffic.Stats(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "读取流量统计失败"})
			return
		}
		c.JSON(http.StatusOK, stats)
	}
}
