package api

import (
	"bufio"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
)

// 允许上传的图片类型，与前端过滤规则一致。
var allowedImageTypes = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/webp": true,
	"image/gif":  true,
}

// uploadImageHandler 把单张图片流式写入临时目录，并记录上传状态。
func uploadImageHandler(deps Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		postID, parseErr := strconv.ParseInt(c.Param("id"), 10, 64)
		if parseErr != nil || postID <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "帖子编号不合法"})
			return
		}

		sortOrder, parseErr := strconv.Atoi(c.Query("sort"))
		if parseErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "序号不合法"})
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
		if post.Status != posts.StatusUploading {
			c.JSON(http.StatusConflict, gin.H{"error": "当前状态不允许继续上传"})
			return
		}
		if sortOrder < 1 || sortOrder > post.ImageCount {
			c.JSON(http.StatusBadRequest, gin.H{"error": "序号超出声明的图片数量"})
			return
		}

		reader := bufio.NewReader(c.Request.Body)
		head, peekErr := reader.Peek(512)
		if peekErr != nil && !errors.Is(peekErr, io.EOF) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "读取图片内容失败"})
			return
		}
		mimeType := http.DetectContentType(head)
		if index := strings.IndexByte(mimeType, ';'); index > 0 {
			mimeType = strings.TrimSpace(mimeType[:index])
		}
		if !allowedImageTypes[mimeType] {
			c.JSON(http.StatusUnsupportedMediaType, gin.H{"error": "不支持的图片类型"})
			return
		}

		saved, saveErr := posts.SaveImageStream(deps.DataDir, postID, sortOrder, reader, posts.LimitForMode(post.UploadMode))
		switch {
		case errors.Is(saveErr, posts.ErrImageTooLarge):
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "图片超过大小限制"})
			return
		case saveErr != nil:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存图片失败"})
			return
		}

		if err := deps.Posts.SaveImage(c.Request.Context(), posts.Image{
			PostID:       postID,
			SortOrder:    sortOrder,
			MimeType:     mimeType,
			FileSize:     saved.FileSize,
			TempFilePath: saved.Path,
			Status:       posts.ImageStatusUploaded,
		}); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存图片记录失败"})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"post_id":    postID,
			"sort_order": sortOrder,
			"file_size":  saved.FileSize,
			"mime_type":  mimeType,
			"status":     posts.ImageStatusUploaded,
		})
	}
}
