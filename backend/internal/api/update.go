package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
	"github.com/littleseven2003/SeSeGraph/backend/internal/publisher"
	"github.com/littleseven2003/SeSeGraph/backend/internal/telegraph"
)

// updatePostRequest 是在线编辑请求体，字段与建帖保持一致。
type updatePostRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	AuthorName  string `json:"author_name"`
	// Tags 省略时保留原标签，空数组表示清空。
	Tags *[]string `json:"tags"`
}

// updatePostHandler 更新帖子元数据：已发布帖先改远端页面，全部成功后才落本地记录。
func updatePostHandler(deps Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		postID, parseErr := strconv.ParseInt(c.Param("id"), 10, 64)
		if parseErr != nil || postID <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "帖子编号不合法"})
			return
		}

		if rejectPendingDelete(c, deps, postID) {
			return
		}

		var req updatePostRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式不正确"})
			return
		}
		title := strings.TrimSpace(req.Title)
		switch {
		case title == "" || utf8.RuneCountInString(title) > maxTitleRunes:
			c.JSON(http.StatusBadRequest, gin.H{"error": "标题不能为空且不超过 200 字"})
			return
		case utf8.RuneCountInString(req.Description) > maxDescriptionRunes:
			c.JSON(http.StatusBadRequest, gin.H{"error": "说明不超过 5000 字"})
			return
		case utf8.RuneCountInString(req.AuthorName) > maxAuthorRunes:
			c.JSON(http.StatusBadRequest, gin.H{"error": "作者名不超过 100 字"})
			return
		}
		if deps.Editor == nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "服务器内部错误"})
			return
		}

		var tags *[]string
		if req.Tags != nil {
			normalized, problem := normalizeTags(*req.Tags)
			if problem != "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": problem})
				return
			}
			tags = &normalized
		}

		err := deps.Editor.Update(c.Request.Context(), postID, publisher.Metadata{
			Title:       title,
			Description: req.Description,
			AuthorName:  strings.TrimSpace(req.AuthorName),
			Tags:        tags,
		})
		switch {
		case err == nil:
		case errors.Is(err, posts.ErrNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "帖子不存在"})
			return
		case errors.Is(err, posts.ErrPendingDelete):
			c.JSON(http.StatusConflict, gin.H{"error": "帖子正在删除，无法继续操作"})
			return
		case errors.Is(err, publisher.ErrPageCountChanged):
			c.JSON(http.StatusConflict, gin.H{"error": "修改后分页数量会变化，请调整说明长度"})
			return
		case errors.Is(err, publisher.ErrPublishInProgress), errors.Is(err, posts.ErrPublishBusy):
			c.JSON(http.StatusConflict, gin.H{"error": "帖子正在发布流程中，请稍后再编辑"})
			return
		case errors.Is(err, publisher.ErrPublicURLInvalid):
			c.JSON(http.StatusConflict, gin.H{"error": "需要先配置公开 HTTPS 地址"})
			return
		case errors.Is(err, telegraph.ErrNotConfigured):
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Telegraph 未配置"})
			return
		case errors.Is(err, telegraph.ErrContentTooLarge):
			c.JSON(http.StatusConflict, gin.H{"error": "说明内容超出单页上限"})
			return
		case errors.Is(err, telegraph.ErrInvalidInput):
			c.JSON(http.StatusBadRequest, gin.H{"error": "标题或说明不合法"})
			return
		default:
			c.JSON(http.StatusBadGateway, gin.H{"error": "更新 Telegraph 页面失败，本地记录未改动"})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"id":          postID,
			"title":       title,
			"description": req.Description,
			"author_name": strings.TrimSpace(req.AuthorName),
		})
	}
}
