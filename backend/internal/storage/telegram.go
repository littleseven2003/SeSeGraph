package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// DefaultAPIBase 是 Telegram Bot API 的默认地址。
	DefaultAPIBase = "https://api.telegram.org"
	// DefaultPhotoMaxBytes 是 sendPhoto 的单张上限。
	DefaultPhotoMaxBytes = 10 << 20
	// DefaultDocumentMaxBytes 是 sendDocument 的单张上限。
	DefaultDocumentMaxBytes = 20 << 20
	// defaultTimeout 覆盖大图上传的常规耗时。
	defaultTimeout = 120 * time.Second
	// DefaultPathTTL 是远端文件路径的缓存时长。
	DefaultPathTTL = 45 * time.Minute
	// DefaultPathCacheSize 是路径缓存的条目上限。
	DefaultPathCacheSize = 512
)

// TelegramConfig 是 Telegram 图床配置。Token 只在本类型内部使用，不对外暴露。
type TelegramConfig struct {
	Token            string
	ChatID           string
	APIBase          string
	HTTPClient       *http.Client
	PhotoMaxBytes    int64
	DocumentMaxBytes int64
	// PathTTL 与 PathCacheSize 控制下载路径缓存，未设置时使用默认值。
	PathTTL       time.Duration
	PathCacheSize int
	// Now 便于测试替换时钟，未设置时使用 time.Now。
	Now func() time.Time
}

// Telegram 通过 Bot API 把图片存入私有频道。
type Telegram struct {
	token    string
	chatID   string
	apiBase  string
	client   *http.Client
	photoMax int64
	docMax   int64
	cache    *pathCache
	now      func() time.Time
}

// NewTelegram 创建 Telegram 提供方。
func NewTelegram(cfg TelegramConfig) (*Telegram, error) {
	token := strings.TrimSpace(cfg.Token)
	chatID := strings.TrimSpace(cfg.ChatID)
	if token == "" || chatID == "" {
		return nil, ErrNotConfigured
	}

	base := strings.TrimRight(strings.TrimSpace(cfg.APIBase), "/")
	if base == "" {
		base = DefaultAPIBase
	}
	client := &http.Client{Timeout: defaultTimeout, CheckRedirect: blockRedirects}
	if cfg.HTTPClient != nil {
		cloned := *cfg.HTTPClient
		cloned.CheckRedirect = blockRedirects
		// 注入的客户端通常只带传输层；未指定超时时沿用默认值，避免上游无响应时永久等待。
		if cloned.Timeout <= 0 {
			cloned.Timeout = defaultTimeout
		}
		client = &cloned
	}
	photoMax := cfg.PhotoMaxBytes
	if photoMax <= 0 {
		photoMax = DefaultPhotoMaxBytes
	}
	docMax := cfg.DocumentMaxBytes
	if docMax <= 0 {
		docMax = DefaultDocumentMaxBytes
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	return &Telegram{
		token:    token,
		chatID:   chatID,
		apiBase:  base,
		client:   client,
		photoMax: photoMax,
		docMax:   docMax,
		cache:    newPathCache(cfg.PathTTL, cfg.PathCacheSize),
		now:      now,
	}, nil
}

// Name 返回提供方标识。
func (t *Telegram) Name() string { return "telegram" }

// CloseIdleConnections 释放空闲连接。配置切换后旧提供方不再使用，及时回收。
func (t *Telegram) CloseIdleConnections() {
	if t == nil || t.client == nil {
		return
	}
	if transport, ok := t.client.Transport.(*http.Transport); ok && transport != nil {
		transport.CloseIdleConnections()
	}
}

// BotID 通过 getMe 校验令牌并返回 Bot 编号。
// 编号用于确认切换前后的 Bot 是否为同一个：Telegram 的 file_id 不能跨 Bot 使用。
func (t *Telegram) BotID(ctx context.Context) (int64, error) {
	if t == nil {
		return 0, ErrNotConfigured
	}
	endpoint := t.apiBase + "/bot" + t.token + "/getMe"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return 0, ErrUpstream
	}
	response, err := t.client.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return 0, ctxErr
		}
		return 0, errTransport
	}
	defer func() { _ = response.Body.Close() }()

	var payload struct {
		OK     bool `json:"ok"`
		Result struct {
			ID int64 `json:"id"`
		} `json:"result"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&payload); err != nil {
		return 0, ErrUpstream
	}
	if !payload.OK || payload.Result.ID == 0 {
		return 0, ErrUpstream
	}
	return payload.Result.ID, nil
}

// ChatInfo 是 getChat 返回的会话属性，用于确认备份目标是否为公开会话。
type ChatInfo struct {
	ID       int64  `json:"id"`
	Type     string `json:"type"`
	Username string `json:"username"`
	Title    string `json:"title"`
}

// Private 报告会话是否只对成员可见。
// 带公开用户名的频道或群任何人都能访问，私聊与无公开用户名的会话视为私有。
func (c ChatInfo) Private() bool {
	if c.Username != "" {
		return false
	}
	switch c.Type {
	case "private", "group", "supergroup", "channel":
		return true
	default:
		return false
	}
}

// ChatInfo 通过 getChat 读取目标会话属性，供备份发送前确认目标是否私有用。
func (t *Telegram) ChatInfo(ctx context.Context) (ChatInfo, error) {
	if t == nil {
		return ChatInfo{}, ErrNotConfigured
	}
	form := url.Values{"chat_id": {t.chatID}}
	endpoint := t.apiBase + "/bot" + t.token + "/getChat"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint,
		strings.NewReader(form.Encode()))
	if err != nil {
		return ChatInfo{}, ErrUpstream
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	response, err := t.client.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ChatInfo{}, ctxErr
		}
		return ChatInfo{}, errTransport
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		_ = response.Body.Close()
	}()

	var payload apiResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&payload); err != nil {
		return ChatInfo{}, ErrUpstream
	}
	if !payload.OK {
		return ChatInfo{}, t.errorFor(payload)
	}
	var info ChatInfo
	if err := json.Unmarshal(payload.Result, &info); err != nil {
		return ChatInfo{}, ErrUpstream
	}
	if info.Type == "" {
		return ChatInfo{}, ErrUpstream
	}
	return info, nil
}

// Upload 以流式 multipart 方式上传单张图片，不把文件整体载入内存。
func (t *Telegram) Upload(ctx context.Context, req UploadRequest) (UploadResult, error) {
	mode := req.Mode
	if mode == "" {
		mode = ModePhoto
	}

	var (
		method string
		field  string
		limit  int64
	)
	switch mode {
	case ModePhoto:
		method, field, limit = "sendPhoto", "photo", t.photoMax
	case ModeDocument:
		method, field, limit = "sendDocument", "document", t.docMax
	default:
		return UploadResult{}, ErrUnsupportedMode
	}

	if req.Reader == nil {
		return UploadResult{}, ErrUpstream
	}
	if req.Size > limit {
		return UploadResult{}, ErrTooLarge
	}

	body, contentType, writeDone := t.streamBody(req, field, limit)

	endpoint := t.apiBase + "/bot" + t.token + "/" + method
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		// 请求未能构造时关闭管道，让写入协程及时退出。
		_ = body.Close()
		return UploadResult{}, ErrUpstream
	}
	httpReq.Header.Set("Content-Type", contentType)

	response, err := t.client.Do(httpReq)
	if err != nil {
		// 关闭读端让写入协程结束，再取它的结果判断是否是超限。
		_ = body.Close()
		var writeErr error
		select {
		case writeErr = <-writeDone:
		case <-ctx.Done():
			return UploadResult{}, ctx.Err()
		}
		if errors.Is(writeErr, ErrTooLarge) {
			return UploadResult{}, ErrTooLarge
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return UploadResult{}, ctxErr
		}
		return UploadResult{}, errTransport
	}
	defer func() {
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		_ = body.Close()
	}()

	var payload apiResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload); err != nil {
		return UploadResult{}, ErrUpstream
	}
	// 上游提前返回的限流或错误优先于写入结果，避免被管道错误覆盖。
	if !payload.OK {
		return UploadResult{}, t.errorFor(payload)
	}

	// 上游返回成功后仍需确认本地管道已完整写出，避免把中途中断当成成功。
	select {
	case writeErr := <-writeDone:
		if writeErr != nil {
			if errors.Is(writeErr, ErrTooLarge) {
				return UploadResult{}, ErrTooLarge
			}
			return UploadResult{}, ErrUpstream
		}
	case <-ctx.Done():
		return UploadResult{}, ctx.Err()
	}

	return t.resultFrom(payload.Result, mode)
}

// streamBody 在后台把请求体写入管道，从而避免占用整块内存。
func (t *Telegram) streamBody(req UploadRequest, field string, limit int64) (*io.PipeReader, string, <-chan error) {
	reader, writer := io.Pipe()
	form := multipart.NewWriter(writer)
	contentType := form.FormDataContentType()

	done := make(chan error, 1)
	go func() {
		var writeErr error
		defer func() {
			if err := form.Close(); err != nil && writeErr == nil {
				writeErr = err
			}
			if writeErr != nil {
				_ = writer.CloseWithError(writeErr)
			} else {
				_ = writer.Close()
			}
			done <- writeErr
		}()

		if err := form.WriteField("chat_id", t.chatID); err != nil {
			writeErr = err
			return
		}
		if req.Caption != "" {
			if err := form.WriteField("caption", req.Caption); err != nil {
				writeErr = err
				return
			}
		}
		part, err := form.CreateFormFile(field, req.FileName)
		if err != nil {
			writeErr = err
			return
		}
		written, err := io.Copy(part, io.LimitReader(req.Reader, limit+1))
		if err != nil {
			writeErr = err
			return
		}
		if written > limit {
			writeErr = ErrTooLarge
		}
	}()

	return reader, contentType, done
}

type apiResponse struct {
	OK          bool   `json:"ok"`
	ErrorCode   int    `json:"error_code"`
	Description string `json:"description"`
	Parameters  struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
	Result json.RawMessage `json:"result"`
}

type messageResult struct {
	MessageID int64 `json:"message_id"`
	Photo     []struct {
		FileID   string `json:"file_id"`
		FileSize int64  `json:"file_size"`
	} `json:"photo"`
	Document *struct {
		FileID   string `json:"file_id"`
		FileSize int64  `json:"file_size"`
		MIMEType string `json:"mime_type"`
	} `json:"document"`
}

// errorFor 把上游失败转换为对外错误，不带原始描述与请求地址。
// 429 会转成带等待时长的限流错误，重试排期由调用方决定。
func (t *Telegram) errorFor(payload apiResponse) error {
	if payload.ErrorCode == http.StatusTooManyRequests {
		return &RateLimitError{
			RetryAfter: time.Duration(payload.Parameters.RetryAfter) * time.Second,
		}
	}
	return fmt.Errorf("%w：上游返回 %d", ErrUpstream, payload.ErrorCode)
}

// errTransport 是所有网络层失败的统一对外说明，不携带地址、令牌或原始错误。
var errTransport = fmt.Errorf("%w：网络请求未完成", ErrUpstream)

// blockRedirects 禁止跟随重定向，避免把带令牌的请求带到其他地址。
func blockRedirects(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

func (t *Telegram) resultFrom(raw json.RawMessage, mode string) (UploadResult, error) {
	var message messageResult
	if err := json.Unmarshal(raw, &message); err != nil {
		return UploadResult{}, ErrUpstream
	}

	result := UploadResult{
		Provider:  t.Name(),
		MessageID: message.MessageID,
	}
	if mode == ModeDocument {
		if message.Document == nil || message.Document.FileID == "" {
			return UploadResult{}, ErrUpstream
		}
		result.FileID = message.Document.FileID
		result.FileSize = message.Document.FileSize
		result.MIMEType = message.Document.MIMEType
		return result, nil
	}

	if len(message.Photo) == 0 {
		return UploadResult{}, ErrUpstream
	}
	largest := message.Photo[len(message.Photo)-1]
	if largest.FileID == "" {
		return UploadResult{}, ErrUpstream
	}
	result.FileID = largest.FileID
	result.FileSize = largest.FileSize
	return result, nil
}

// SendMessage 发送一条纯文本消息，用于流量预警等运维通知。
// 只发文本，不带图片或文件；失败时返回统一的上游错误，不携带地址与令牌。
func (t *Telegram) SendMessage(ctx context.Context, text string) error {
	if t == nil {
		return ErrNotConfigured
	}
	body := strings.TrimSpace(text)
	if body == "" {
		return errors.New("消息内容为空")
	}

	form := url.Values{
		"chat_id":                  {t.chatID},
		"text":                     {body},
		"disable_web_page_preview": {"true"},
	}
	endpoint := t.apiBase + "/bot" + t.token + "/sendMessage"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint,
		strings.NewReader(form.Encode()))
	if err != nil {
		return ErrUpstream
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	response, err := t.client.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return errTransport
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		_ = response.Body.Close()
	}()

	var payload apiResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload); err != nil {
		return ErrUpstream
	}
	if !payload.OK {
		return t.errorFor(payload)
	}
	return nil
}
