package api

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
	"github.com/littleseven2003/SeSeGraph/backend/internal/publisher"
	"github.com/littleseven2003/SeSeGraph/backend/internal/storage"
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
		if deps.Editor == nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "服务器内部错误"})
			return
		}

		// 新封面先写成独立文件：远端或本地提交失败时删掉它，旧封面仍可读。
		thumbPath := post.CoverThumbPath
		newFile := ""
		if len(cover) > 0 {
			newFile, err = posts.SaveCoverVersioned(deps.DataDir, postID, cover)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "保存封面失败"})
				return
			}
			thumbPath = newFile
		}

		changeErr := deps.Editor.ChangeCover(ctx, postID, req.SortOrder, thumbPath)
		if changeErr != nil {
			if newFile != "" {
				posts.RemoveCoverFile(deps.DataDir, newFile)
			}
			switch {
			case errors.Is(changeErr, publisher.ErrPublishInProgress),
				errors.Is(changeErr, publisher.ErrPageCountChanged),
				errors.Is(changeErr, publisher.ErrPublicURLInvalid),
				errors.Is(changeErr, posts.ErrPendingDelete),
				errors.Is(changeErr, posts.ErrCoverConflict):
				c.JSON(http.StatusConflict, gin.H{"error": "当前状态不允许更换封面"})
			case errors.Is(changeErr, posts.ErrNotFound):
				c.JSON(http.StatusNotFound, gin.H{"error": "帖子不存在"})
			default:
				c.JSON(http.StatusBadGateway, gin.H{"error": "更新远端页面失败，请稍后重试"})
			}
			return
		}
		if newFile != "" && post.CoverThumbPath != "" && post.CoverThumbPath != newFile {
			posts.RemoveCoverFile(deps.DataDir, post.CoverThumbPath)
		}

		c.JSON(http.StatusOK, gin.H{
			"id":               postID,
			"sort_order":       req.SortOrder,
			"cover_thumb_path": thumbPath,
		})
	}
}

// adminMediaHandler 在管理员会话下转发原图或视频，供换封面时生成缩略图。
// 不经过公开图片网关，未公开或已撤回的帖子也能在后台读取自己的媒体。
func adminMediaHandler(deps Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		postID, parseErr := strconv.ParseInt(c.Param("id"), 10, 64)
		if parseErr != nil || postID <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "帖子编号不合法"})
			return
		}
		sortOrder, parseErr := strconv.Atoi(c.Param("sort"))
		if parseErr != nil || sortOrder <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "序号不合法"})
			return
		}
		if _, err := deps.Posts.Get(c.Request.Context(), postID); err != nil {
			if errors.Is(err, posts.ErrNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "帖子不存在"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "读取帖子失败"})
			return
		}

		image, err := deps.Posts.ImageByOrder(c.Request.Context(), postID, sortOrder)
		if errors.Is(err, posts.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "图片不存在"})
			return
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "读取图片失败"})
			return
		}
		if image.Status != posts.ImageStatusStored || image.RemoteFileID == "" {
			c.JSON(http.StatusConflict, gin.H{"error": "媒体尚未存入频道"})
			return
		}
		if deps.Streams == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "图床暂不可用"})
			return
		}

		stream, err := deps.Streams.OpenStream(c.Request.Context(), image.RemoteFileID)
		switch {
		case errors.Is(err, storage.ErrFileUnavailable):
			c.JSON(http.StatusNotFound, gin.H{"error": "媒体不存在"})
			return
		case err != nil:
			c.JSON(http.StatusBadGateway, gin.H{"error": "读取媒体失败"})
			return
		}
		defer func() { _ = stream.Body.Close() }()

		c.Header("Content-Type", image.MimeType)
		c.Header("Cache-Control", "no-store")
		c.Header("X-Content-Type-Options", "nosniff")
		if stream.Size > 0 {
			c.Header("Content-Length", strconv.FormatInt(stream.Size, 10))
		}
		c.Status(http.StatusOK)
		if c.Request.Method == http.MethodHead {
			return
		}
		if _, err := io.Copy(c.Writer, stream.Body); err != nil {
			return
		}
	}
}
