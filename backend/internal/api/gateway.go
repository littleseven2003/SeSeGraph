package api

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
	"github.com/littleseven2003/SeSeGraph/backend/internal/storage"
	"github.com/littleseven2003/SeSeGraph/backend/internal/traffic"
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

// imageType 返回受支持的图片类型（与上传白名单一致），其余返回空串。
func imageType(candidate string) string {
	value := strings.ToLower(strings.TrimSpace(candidate))
	if index := strings.IndexByte(value, ';'); index >= 0 {
		value = strings.TrimSpace(value[:index])
	}
	if allowedMediaTypes[value] {
		return value
	}
	return ""
}

// resolveContentType 选择响应类型：优先上游给出的图片类型；
// 上游只给空值或通用二进制流时才回落到入库时探测的结果；
// 上游明确给出非图片类型时按二进制流处理，不冒用入库类型。
func resolveContentType(primary, fallback string) string {
	value := strings.ToLower(strings.TrimSpace(primary))
	if index := strings.IndexByte(value, ';'); index >= 0 {
		value = strings.TrimSpace(value[:index])
	}
	if allowedMediaTypes[value] {
		return value
	}
	if value != "" && value != octetStream {
		return octetStream
	}
	if known := imageType(fallback); known != "" {
		return known
	}
	return octetStream
}

// rangeVerdict 是单区间解析结果。
type rangeVerdict int

const (
	rangeIgnored rangeVerdict = iota
	rangeUsable
	rangeUnsatisfiable
)

// parseByteRange 解析单区间 Range 头：只支持 bytes=start-end、bytes=start-、bytes=-N。
// 多区间与无法识别的语法按普通请求处理，起点超出总长时返回不可满足。
func parseByteRange(header string, size int64) (storage.ByteRange, rangeVerdict) {
	value := strings.TrimSpace(header)
	if size <= 0 || !strings.HasPrefix(strings.ToLower(value), "bytes=") {
		return storage.ByteRange{}, rangeIgnored
	}
	spec := strings.TrimSpace(value[len("bytes="):])
	if spec == "" || strings.Contains(spec, ",") {
		return storage.ByteRange{}, rangeIgnored
	}
	startText, endText, found := strings.Cut(spec, "-")
	if !found {
		return storage.ByteRange{}, rangeIgnored
	}
	startText = strings.TrimSpace(startText)
	endText = strings.TrimSpace(endText)

	if startText == "" {
		// bytes=-N：末尾 N 字节。
		suffix, err := strconv.ParseInt(endText, 10, 64)
		if err != nil || suffix <= 0 {
			return storage.ByteRange{}, rangeIgnored
		}
		if suffix > size {
			suffix = size
		}
		return storage.ByteRange{Start: size - suffix, Length: suffix}, rangeUsable
	}

	start, err := strconv.ParseInt(startText, 10, 64)
	if err != nil || start < 0 {
		return storage.ByteRange{}, rangeIgnored
	}
	if start >= size {
		return storage.ByteRange{}, rangeUnsatisfiable
	}
	if endText == "" {
		return storage.ByteRange{Start: start, Length: size - start}, rangeUsable
	}
	end, err := strconv.ParseInt(endText, 10, 64)
	if err != nil || end < start {
		return storage.ByteRange{}, rangeIgnored
	}
	if end >= size {
		end = size - 1
	}
	return storage.ByteRange{Start: start, Length: end - start + 1}, rangeUsable
}

// imageGateway 按短码把公开图片流式转发给访客。
type imageGateway struct {
	repo        *posts.Repository
	streams     storage.StreamProvider
	slots       chan struct{}
	concurrency int
	logger      *log.Logger
	// meter 统计出站流量并判断月度硬上限，可为空。
	meter *traffic.Recorder
}

// newImageGateway 创建网关，并发上限非正时使用默认值。
func newImageGateway(repo *posts.Repository, streams storage.StreamProvider, concurrency int, logger *log.Logger, meter *traffic.Recorder) *imageGateway {
	if concurrency <= 0 {
		concurrency = DefaultImageConcurrency
	}
	return &imageGateway{
		repo:        repo,
		streams:     streams,
		slots:       make(chan struct{}, concurrency),
		concurrency: concurrency,
		logger:      logger,
		meter:       meter,
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
	notFoundWithStatus(c, http.StatusNotFound, "图片不存在")
}

// notFoundWithStatus 返回指定的失败状态，用于流量熔断这类需要区分原因的拒绝。
func notFoundWithStatus(c *gin.Context, status int, message string) {
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.JSON(status, gin.H{"error": message})
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

	// 本月出站流量超过硬上限时直接拒绝，不再向 Telegram 取流。
	if g.meter.OverLimit() {
		notFoundWithStatus(c, http.StatusServiceUnavailable, "本月流量已超限，图片暂不可用")
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

	// 登记一次请求；并发传输中已越过上限时在这里再拦一次，确保不再向上游取流。
	if !g.meter.Begin(c.Request.Context()) {
		notFoundWithStatus(c, http.StatusServiceUnavailable, "本月流量已超限，图片暂不可用")
		return
	}

	ctx := c.Request.Context()
	var requested storage.ByteRange
	ranged := false
	if header := strings.TrimSpace(c.Request.Header.Get("Range")); header != "" {
		if ranger, ok := g.streams.(storage.RangeStreamer); ok {
			if size, sizeErr := ranger.Size(ctx, image.RemoteFileID); sizeErr == nil {
				parsed, verdict := parseByteRange(header, size)
				switch verdict {
				case rangeUnsatisfiable:
					c.Header("Accept-Ranges", "bytes")
					c.Header("Content-Range", "bytes */"+strconv.FormatInt(size, 10))
					c.Header("Cache-Control", "no-store")
					c.Status(http.StatusRequestedRangeNotSatisfiable)
					return
				case rangeUsable:
					requested, ranged = parsed, true
				}
			}
		}
	}

	stream, partial, err := g.openStream(ctx, image.RemoteFileID, requested, ranged)
	switch {
	case errors.Is(err, storage.ErrFileUnavailable):
		notFound(c)
		return
	case errors.Is(err, storage.ErrNotConfigured):
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "图片暂不可用"})
		return
	case err != nil:
		if ctx.Err() != nil {
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

	// 原图模式使用入库时嗅探的 MIME；photo 模式以 Telegram 回传格式为准，
	// 上游没给出可识别的图片类型时回落到入库结果，避免图片被当成二进制流下载。
	contentType := strings.TrimSpace(image.MIMEType)
	if image.UploadMode != storage.ModeDocument {
		contentType = resolveContentType(stream.ContentType, image.MIMEType)
	} else {
		contentType = resolveContentType(image.MIMEType, stream.ContentType)
	}
	c.Header("Content-Type", contentType)
	c.Header("Cache-Control", imageCacheControl)
	c.Header("Content-Disposition", "inline")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Accept-Ranges", "bytes")
	if partial {
		length := requested.Length
		if stream.Size > 0 && requested.Start+length > stream.Size {
			length = stream.Size - requested.Start
		}
		end := requested.Start + length - 1
		c.Header("Content-Range", "bytes "+strconv.FormatInt(requested.Start, 10)+"-"+
			strconv.FormatInt(end, 10)+"/"+strconv.FormatInt(stream.Size, 10))
		c.Header("Content-Length", strconv.FormatInt(length, 10))
		c.Status(http.StatusPartialContent)
	} else if stream.Size > 0 {
		c.Header("Content-Length", strconv.FormatInt(stream.Size, 10))
		c.Status(http.StatusOK)
	} else {
		c.Status(http.StatusOK)
	}

	// HEAD 只回头部，供播放器探测长度与是否支持区间。
	if c.Request.Method == http.MethodHead {
		return
	}

	// 边转发边计费：并发传输期间新请求就能看到最新用量，越过上限后不再取流。
	writer := flushWriter{writer: c.Writer, meter: g.meter, ctx: context.WithoutCancel(ctx)}
	_, copyErr := io.Copy(writer, stream.Body)
	if copyErr != nil {
		// 客户端断开或上游中断：连接已不可用，这里只记录一次固定说明。
		if c.Request.Context().Err() == nil && g.logger != nil {
			g.logger.Print("图片转发中断")
		}
	}
}

// openStream 按是否请求区间打开上游流；上游不支持区间时退回整段响应。
func (g *imageGateway) openStream(ctx context.Context, fileID string, r storage.ByteRange, ranged bool) (storage.Stream, bool, error) {
	if !ranged {
		stream, err := g.streams.OpenStream(ctx, fileID)
		return stream, false, err
	}
	ranger := g.streams.(storage.RangeStreamer)
	stream, err := ranger.OpenStreamRange(ctx, fileID, r)
	if errors.Is(err, storage.ErrRangeUnsupported) {
		stream, err = g.streams.OpenStream(ctx, fileID)
		return stream, false, err
	}
	return stream, true, err
}

// flushWriter 每次写入后立即刷新，保证慢流时首段能及时到达客户端。
// 只暴露 Write 方法，让 io.Copy 使用固定缓冲区而不是走整块快路径。
type flushWriter struct {
	writer gin.ResponseWriter
	meter  *traffic.Recorder
	ctx    context.Context
}

func (w flushWriter) Write(p []byte) (int, error) {
	written, err := w.writer.Write(p)
	if written > 0 {
		w.meter.AddBytes(w.ctx, int64(written))
		w.writer.Flush()
	}
	return written, err
}
