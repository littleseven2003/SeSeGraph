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
	// ModeVideo 走 sendVideo 通道，与画质模式无关，只由内容类型决定。
	ModeVideo = "video"
)

// 图床操作对外返回的错误类别。
var (
	ErrTooLarge        = errors.New("图片超过图床大小限制")
	ErrRateLimited     = errors.New("图床限流")
	ErrUpstream        = errors.New("图床上传失败")
	ErrNotConfigured   = errors.New("图床未配置")
	ErrUnsupportedMode = errors.New("不支持的画质模式")
	// ErrRangeUnsupported 表示上游不支持区间且无法安全跳过前缀。
	ErrRangeUnsupported = errors.New("上游不支持区间读取")
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

// Origin 由多 Bot 池的提供方实现，用于把每张图记到实际发送它的 Bot 名下。
// 返回的编号为 0 表示尚未确认身份，此时按旧口径只记录提供方名称。
type Origin interface {
	Origin() (botID int64, chatID string)
}

// SendOrigin 是一次发送实际使用的身份：编号、频道与凭据指纹。
// 三者一起固定，热替换期间迟到的结果才能按真实发送者核对与落库。
type SendOrigin struct {
	BotID             int64
	ChatID            string
	CredentialVersion string
}

// OriginProvider 在发送的同时返回本次实际使用的身份，避免先取身份再发送时被并发换池影响。
type OriginProvider interface {
	UploadWithOrigin(ctx context.Context, req UploadRequest) (UploadResult, SendOrigin, error)
}

// Identify 给提供方补上池里已验明的编号与频道：单 Bot 装配或切换后仍能记录逐图归属，
// 不必等第一次 getMe。botID 非正时原样返回，保持旧行为。
func Identify(provider Provider, botID int64, chatID string) Provider {
	if provider == nil || botID <= 0 {
		return provider
	}
	return &identifiedProvider{Provider: provider, botID: botID, chatID: chatID}
}

// identifiedProvider 在提供方之上固定身份，发送回执与逐图来源都取这里的值。
type identifiedProvider struct {
	Provider
	botID  int64
	chatID string
}

func (p *identifiedProvider) Origin() (int64, string) { return p.botID, p.chatID }

func (p *identifiedProvider) CredentialVersion() string {
	if versioned, ok := p.Provider.(interface{ CredentialVersion() string }); ok {
		return versioned.CredentialVersion()
	}
	return ""
}

func (p *identifiedProvider) UploadWithOrigin(ctx context.Context, req UploadRequest) (UploadResult, SendOrigin, error) {
	origin := SendOrigin{BotID: p.botID, ChatID: p.chatID, CredentialVersion: p.CredentialVersion()}
	if originProvider, ok := p.Provider.(OriginProvider); ok {
		result, _, err := originProvider.UploadWithOrigin(ctx, req)
		return result, origin, err
	}
	result, err := p.Provider.Upload(ctx, req)
	return result, origin, err
}

// Stream 是一次流式下载的句柄。Body 由调用方负责关闭，读取不整块载入内存。
type Stream struct {
	Body        io.ReadCloser
	ContentType string
	Size        int64
	// Start 与 End 是本次响应在文件中的实际字节范围（含端点）；整段响应时为 -1。
	Start int64
	End   int64
}

// Snapshotter 是提供方的可选接口：返回一份固定视图，
// 让一次任务或一次请求在配置热切换期间仍使用同一份图床配置。
type Snapshotter interface {
	Snapshot() (StreamProvider, bool)
}

// ByteRange 描述一次字节区间请求，Length 为 -1 表示到文件末尾。
type ByteRange struct {
	Start  int64
	Length int64
}

// RangeStreamer 是可选能力：读取远端文件大小并按区间取流，供视频拖拽使用。
type RangeStreamer interface {
	Size(ctx context.Context, fileID string) (int64, error)
	OpenStreamRange(ctx context.Context, fileID string, r ByteRange) (Stream, error)
}

// StreamProvider 是在上传之外提供流式读取的图床接口，供图片网关按远端标识转发。
type StreamProvider interface {
	Provider
	OpenStream(ctx context.Context, fileID string) (Stream, error)
}
