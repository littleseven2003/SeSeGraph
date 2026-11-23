package api

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
)

// 允许上传的媒体类型，与前端过滤规则一致。
var allowedMediaTypes = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/webp": true,
	"image/gif":  true,
	"video/mp4":  true,
}

// DefaultUploadConcurrency 是单实例同时接收上传的默认上限。
// 前端每轮只开两路，这里留出多标签页与重试的余量，同时守住临时目录与内存。
const DefaultUploadConcurrency = 3

// uploadLimiter 限制同时接收的上传请求数，满额后短暂排队，超时才拒绝。
type uploadLimiter struct {
	slots chan struct{}
	wait  time.Duration
}

// newUploadLimiter 创建上传限流器，参数非正时使用默认值。
func newUploadLimiter(concurrency int, wait time.Duration) *uploadLimiter {
	if concurrency <= 0 {
		concurrency = DefaultUploadConcurrency
	}
	if wait <= 0 {
		wait = 15 * time.Second
	}
	return &uploadLimiter{slots: make(chan struct{}, concurrency), wait: wait}
}

// acquire 在等待上限内获取一个上传名额；客户端断开或超时返回 false。
// 调用方必须在读请求体之前获取名额，避免超大请求在排队时占用连接缓冲区。
func (l *uploadLimiter) acquire(ctx context.Context) bool {
	select {
	case l.slots <- struct{}{}:
		return true
	default:
	}
	timer := time.NewTimer(l.wait)
	defer timer.Stop()
	select {
	case l.slots <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	case <-timer.C:
		return false
	}
}

// release 归还上传名额。
func (l *uploadLimiter) release() { <-l.slots }

// uploadImageHandler 把单张图片流式写入临时目录，并记录上传状态。
func uploadImageHandler(deps Deps, limiter *uploadLimiter) gin.HandlerFunc {
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
		if post.ControlState != "" && post.ControlState != posts.ControlRunning {
			c.JSON(http.StatusConflict, gin.H{"error": "任务已暂停或停止，不能继续上传"})
			return
		}
		round := -1
		if raw := strings.TrimSpace(c.Query("round")); raw != "" {
			parsed, parseErr := strconv.Atoi(raw)
			if parseErr != nil || parsed < 0 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "上传轮次不合法"})
				return
			}
			if parsed != post.UploadRound {
				c.JSON(http.StatusConflict, gin.H{"error": "任务已恢复，请刷新后继续上传"})
				return
			}
			round = parsed
		}
		if sortOrder < 1 || sortOrder > post.ImageCount {
			c.JSON(http.StatusBadRequest, gin.H{"error": "序号超出声明的图片数量"})
			return
		}

		// 名额满时在这里短暂排队；真正读请求体之前必须拿到名额。
		if limiter != nil {
			if !limiter.acquire(c.Request.Context()) {
				if c.Request.Context().Err() != nil {
					return
				}
				c.Header("Retry-After", "1")
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": "上传请求过多，请稍后重试"})
				return
			}
			defer limiter.release()
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
		if !allowedMediaTypes[mimeType] {
			c.JSON(http.StatusUnsupportedMediaType, gin.H{"error": "不支持的媒体类型"})
			return
		}

		saved, saveErr := posts.SaveImageStream(deps.DataDir, postID, sortOrder, reader,
			posts.LimitForMedia(post.UploadMode, mimeType))
		switch {
		case errors.Is(saveErr, posts.ErrImageTooLarge):
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "媒体超过大小限制"})
			return
		case saveErr != nil:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存图片失败"})
			return
		}

		previous, attachErr := deps.Posts.AttachImageWithRound(c.Request.Context(), posts.Image{
			PostID:       postID,
			SortOrder:    sortOrder,
			MimeType:     mimeType,
			FileSize:     saved.FileSize,
			TempFilePath: saved.Path,
			Status:       posts.ImageStatusUploaded,
		}, round)
		switch {
		case attachErr == nil:
			// 覆盖旧序号后清理被替换的文件。
			posts.RemoveTempFile(deps.DataDir, previous)
		case errors.Is(attachErr, posts.ErrNotFound):
			posts.RemoveTempFile(deps.DataDir, saved.Path)
			c.JSON(http.StatusNotFound, gin.H{"error": "帖子不存在"})
			return
		case errors.Is(attachErr, posts.ErrNotUploading):
			posts.RemoveTempFile(deps.DataDir, saved.Path)
			c.JSON(http.StatusConflict, gin.H{"error": "当前状态不允许继续上传"})
			return
		case errors.Is(attachErr, posts.ErrControlInvalid):
			posts.RemoveTempFile(deps.DataDir, saved.Path)
			c.JSON(http.StatusConflict, gin.H{"error": "任务已暂停或恢复，请刷新后继续上传"})
			return
		default:
			posts.RemoveTempFile(deps.DataDir, saved.Path)
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
