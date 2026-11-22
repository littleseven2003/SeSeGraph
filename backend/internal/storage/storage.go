package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

// 上传模式，与发帖时的画质选择一致。
const (
	ModePhoto    = "photo"
	ModeDocument = "document"
)

// 图床操作对外返回的错误类别。
var (
	ErrTooLarge        = errors.New("图片超过图床大小限制")
	ErrRateLimited     = errors.New("图床限流")
	ErrUpstream        = errors.New("图床上传失败")
	ErrNotConfigured   = errors.New("图床未配置")
	ErrUnsupportedMode = errors.New("不支持的画质模式")
	// ErrFileUnavailable 表示远端文件已不可取，调用方应返回 404 而不是 5xx。
	ErrFileUnavailable = errors.New("远端文件不可用")
)

// RateLimitError 表示上游要求等待后重试，具体排期由调用方决定。
type RateLimitError struct {
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("%s：建议 %s 后重试", ErrRateLimited.Error(), e.RetryAfter.Round(time.Second))
}

// Unwrap 让调用方可以用 errors.Is(err, ErrRateLimited) 判断类别。
func (e *RateLimitError) Unwrap() error {
	return ErrRateLimited
}

// UploadRequest 描述一次上传。Reader 由调用方提供，按流读取，不整体载入内存。
type UploadRequest struct {
	FileName string
	MIMEType string
	Size     int64
	Mode     string
	Caption  string
	Reader   io.Reader
}

// UploadResult 是上传成功后的远端标识。
type UploadResult struct {
	Provider  string
	FileID    string
	MessageID int64
	MIMEType  string
	FileSize  int64
}

// Provider 是可插拔图床的统一接口，v1.0 由 Telegram 实现。
type Provider interface {
	Name() string
	Upload(ctx context.Context, req UploadRequest) (UploadResult, error)
}

// Stream 是一次流式下载的句柄。Body 由调用方负责关闭，读取不整块载入内存。
type Stream struct {
	Body        io.ReadCloser
	ContentType string
	Size        int64
}

// Snapshotter 是提供方的可选接口：返回一份固定视图，
// 让一次任务或一次请求在配置热切换期间仍使用同一份图床配置。
type Snapshotter interface {
	Snapshot() (StreamProvider, bool)
}

// StreamProvider 是在上传之外提供流式读取的图床接口，供图片网关按远端标识转发。
type StreamProvider interface {
	Provider
	OpenStream(ctx context.Context, fileID string) (Stream, error)
}
