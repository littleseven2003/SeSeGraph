package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
	"github.com/littleseven2003/SeSeGraph/backend/internal/publishgate"
	"github.com/littleseven2003/SeSeGraph/backend/internal/settings"
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

		// 与同步收尾的自动入队共用同一套发布前置检查。
		switch err := publishgate.Check(c.Request.Context(), deps.Telegraph, deps.PublicURL); {
		case errors.Is(err, publishgate.ErrTelegraph):
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Telegraph 未配置"})
			return
		case errors.Is(err, publishgate.ErrPublicURL):
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

		// 首次发布时原子冻结链接生成方式与随机短码；重试沿用已冻结值。
		// 旧版本已建过页面的任务（有 telegraph_paths）重试保持按标题口径，与后续设置隔离。
		mode := settings.PathModeTitle
		if info, infoErr := deps.Posts.PublishInfo(c.Request.Context(), postID); infoErr == nil && len(info.Paths) == 0 {
			if deps.Settings != nil {
				if effective, err := deps.Settings.Effective(c.Request.Context()); err == nil {
					mode = effective.TelegraphPathMode.Value
				}
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
		if err := deps.Posts.RequestPublishWithPath(c.Request.Context(), postID, mode, slug); err != nil {
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
