package api

import (
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
	"github.com/littleseven2003/SeSeGraph/backend/internal/storage"
)

const (
	// DefaultImageConcurrency 是图片网关的默认并发上限，避免把 1 核小机器拖垮。
	DefaultImageConcurrency = 25
	// imageCacheControl 是成功响应的强缓存头，图片内容按短码不可变。
	imageCacheControl = "public, max-age=31536000, immutable"
	// shortCodeMinLen 与 shortCodeMaxLen 限定合法短码长度。
	shortCodeMinLen = 8
	shortCodeMaxLen = 12
	// octetStream 是未识别类型时的兜底响应类型，避免把上游任意内容当同源资源执行。
	octetStream = "application/octet-stream"
)

// safeContentType 只放行受支持的图片类型（与上传白名单一致），其余统一回落为二进制流。
func safeContentType(candidate string) string {
	value := strings.ToLower(strings.TrimSpace(candidate))
	if index := strings.IndexByte(value, ';'); index >= 0 {
		value = strings.TrimSpace(value[:index])
	}
	if allowedImageTypes[value] {
		return value
	}
	return octetStream
}

// imageGateway 按短码把公开图片流式转发给访客。
type imageGateway struct {
	repo        *posts.Repository
	streams     storage.StreamProvider
	slots       chan struct{}
	concurrency int
	logger      *log.Logger
}

// newImageGateway 创建网关，并发上限非正时使用默认值。
func newImageGateway(repo *posts.Repository, streams storage.StreamProvider, concurrency int, logger *log.Logger) *imageGateway {
	if concurrency <= 0 {
		concurrency = DefaultImageConcurrency
	}
	return &imageGateway{
		repo:        repo,
		streams:     streams,
		slots:       make(chan struct{}, concurrency),
		concurrency: concurrency,
		logger:      logger,
	}
}

// validShortCode 判断短码是否为 8–12 位 Base62 字符。
func validShortCode(code string) bool {
	if len(code) < shortCodeMinLen || len(code) > shortCodeMaxLen {
		return false
	}
	for _, ch := range code {
		switch {
		case ch >= '0' && ch <= '9':
		case ch >= 'a' && ch <= 'z':
		case ch >= 'A' && ch <= 'Z':
		default:
			return false
		}
	}
	return true
}

// notFound 统一返回 404，并禁止上游与中间层缓存这个结果。
func notFound(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.JSON(http.StatusNotFound, gin.H{"error": "图片不存在"})
}

// handle 处理 GET /i/:short_code。
func (g *imageGateway) handle(c *gin.Context) {
	shortCode := strings.TrimSpace(c.Param("short_code"))
	if !validShortCode(shortCode) {
		notFound(c)
		return
	}

	image, err := g.repo.PublicImage(c.Request.Context(), shortCode)
	switch {
	case errors.Is(err, posts.ErrNotFound):
		notFound(c)
		return
	case err != nil:
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取图片失败"})
		return
	}

	if g.streams == nil {
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "图片暂不可用"})
		return
	}

	// 并发信号量满额时快速拒绝，避免低配实例堆积等待中的请求。
	select {
	case g.slots <- struct{}{}:
	default:
		c.Header("Cache-Control", "no-store")
		c.Header("Retry-After", "1")
		c.Header("X-Content-Type-Options", "nosniff")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "图片请求过多，请稍后重试"})
		return
	}
	defer func() { <-g.slots }()

	stream, err := g.streams.OpenStream(c.Request.Context(), image.RemoteFileID)
	switch {
	case errors.Is(err, storage.ErrFileUnavailable):
		notFound(c)
		return
	case errors.Is(err, storage.ErrNotConfigured):
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "图片暂不可用"})
		return
	case err != nil:
		if c.Request.Context().Err() != nil {
			return
		}
		if g.logger != nil {
			g.logger.Print("图片转发失败：上游不可用")
		}
		c.Header("Cache-Control", "no-store")
		c.Header("X-Content-Type-Options", "nosniff")
		c.JSON(http.StatusBadGateway, gin.H{"error": "图片暂不可用"})
		return
	}
	defer func() { _ = stream.Body.Close() }()

	// 原图模式使用入库时嗅探的 MIME；photo 模式以 Telegram 回传格式为准。
	contentType := strings.TrimSpace(image.MIMEType)
	if image.UploadMode != storage.ModeDocument {
		if upstream := strings.TrimSpace(stream.ContentType); upstream != "" {
			contentType = upstream
		}
	}
	c.Header("Content-Type", safeContentType(contentType))
	c.Header("Cache-Control", imageCacheControl)
	c.Header("Content-Disposition", "inline")
	c.Header("X-Content-Type-Options", "nosniff")
	if stream.Size > 0 {
		c.Header("Content-Length", strconv.FormatInt(stream.Size, 10))
	}
	c.Status(http.StatusOK)

	if _, err := io.Copy(flushWriter{writer: c.Writer}, stream.Body); err != nil {
		// 客户端断开或上游中断：连接已不可用，这里只记录一次固定说明。
		if c.Request.Context().Err() == nil && g.logger != nil {
			g.logger.Print("图片转发中断")
		}
	}
}

// flushWriter 每次写入后立即刷新，保证慢流时首段能及时到达客户端。
// 只暴露 Write 方法，让 io.Copy 使用固定缓冲区而不是走整块快路径。
type flushWriter struct {
	writer gin.ResponseWriter
}

func (w flushWriter) Write(p []byte) (int, error) {
	written, err := w.writer.Write(p)
	if written > 0 {
		w.writer.Flush()
	}
	return written, err
}
