package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
	"github.com/littleseven2003/SeSeGraph/backend/internal/publisher"
)

// changeCoverRequest 是更换封面请求体：序号必填，缩略图可选。
// 只改序号时沿用旧缩略图，避免已经正确的封面被无谓覆盖。
type changeCoverRequest struct {
	SortOrder      int    `json:"sort_order"`
	CoverThumbnail string `json:"cover_thumbnail"`
}

// changeCoverHandler 更换帖子封面。
// 上传与待上传阶段只写本地；已发布帖先按新顺序改写远端页面，
// 全部成功后才切换本地序号与缩略图，失败时界面仍读到旧封面。
func changeCoverHandler(deps Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		postID, parseErr := strconv.ParseInt(c.Param("id"), 10, 64)
		if parseErr != nil || postID <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "帖子编号不合法"})
			return
		}

		var req changeCoverRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式不正确"})
			return
		}
		if req.SortOrder <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "封面序号不合法"})
			return
		}

		ctx := c.Request.Context()
		post, err := deps.Posts.Get(ctx, postID)
		switch {
		case errors.Is(err, posts.ErrNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "帖子不存在"})
			return
		case err != nil:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "读取帖子失败"})
			return
		}
		if req.SortOrder > post.ImageCount {
			c.JSON(http.StatusBadRequest, gin.H{"error": "封面序号超出图片数量"})
			return
		}

		pending, err := deps.Posts.PendingDelete(ctx, postID)
		if errors.Is(err, posts.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "帖子不存在"})
			return
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "读取删除状态失败"})
			return
		}
		if pending {
			c.JSON(http.StatusConflict, gin.H{"error": "帖子正在删除，不能更换封面"})
			return
		}

		info, err := deps.Posts.PublishInfo(ctx, postID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "读取发布信息失败"})
			return
		}
		if info.State == posts.PublishPublishing {
			c.JSON(http.StatusConflict, gin.H{"error": "正在进行发布，请稍后重试"})
			return
		}

		cover, decodeErr := decodeCover(req.CoverThumbnail)
		if decodeErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "封面缩略图不合法或过大"})
			return
		}

		// 序号变化才需要改写远端页面；只替换缩略图时远端没有封面字段可改。
		if info.CoverSortOrder != req.SortOrder && deps.Editor != nil {
			reorderErr := deps.Editor.ReorderCover(ctx, postID, req.SortOrder)
			switch {
			case reorderErr == nil:
			case errors.Is(reorderErr, publisher.ErrPublishInProgress),
				errors.Is(reorderErr, publisher.ErrPageCountChanged),
				errors.Is(reorderErr, publisher.ErrPublicURLInvalid),
				errors.Is(reorderErr, posts.ErrPendingDelete):
				c.JSON(http.StatusConflict, gin.H{"error": "当前状态不允许更换封面"})
				return
			default:
				c.JSON(http.StatusBadGateway, gin.H{"error": "更新远端页面失败，请稍后重试"})
				return
			}
		}

		thumbPath := ""
		if len(cover) > 0 {
			thumbPath, err = posts.SaveCover(deps.DataDir, postID, cover)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "保存封面失败"})
				return
			}
		} else {
			thumbPath = post.CoverThumbPath
		}

		if err := deps.Posts.SetCoverSelection(ctx, postID, req.SortOrder, thumbPath); err != nil {
			if errors.Is(err, posts.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "帖子不存在"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存封面失败"})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"id":               postID,
			"sort_order":       req.SortOrder,
			"cover_thumb_path": thumbPath,
		})
	}
}
