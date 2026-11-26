package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultAPIBase 是 Telegram Bot API 的默认地址。
	DefaultAPIBase = "https://api.telegram.org"
	// DefaultPhotoMaxBytes 是 sendPhoto 的单张上限。
	DefaultPhotoMaxBytes = 10 << 20
	// DefaultDocumentMaxBytes 是 sendDocument 的单张上限。
	DefaultDocumentMaxBytes = 20 << 20
	// MaxVideoBytes 是 sendVideo 的上限，同时也是 Telegram 云端 getFile 的硬限制。
	MaxVideoBytes = 20000000
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
	version  string

	mu       sync.Mutex
	botID    int64
	botKnown bool
	cache    *pathCache
	now      func() time.Time
}

// NewTelegram 创建 Telegram 提供方。
// CredentialVersion 用令牌与上游地址算出短指纹，用于核对迟到的发送结果属于哪一版凭据。
func CredentialVersion(token, apiBase string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token) + "|" + strings.TrimSpace(apiBase)))
	return hex.EncodeToString(sum[:6])
}

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
		version:  CredentialVersion(token, base),
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
	t.mu.Lock()
	cached, known := t.botID, t.botKnown
	t.mu.Unlock()
	if known {
		return cached, nil
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
	t.mu.Lock()
	t.botID = payload.Result.ID
	t.botKnown = true
	t.mu.Unlock()
	return payload.Result.ID, nil
}

// CredentialVersion 返回本实例所用凭据的短指纹。
func (t *Telegram) CredentialVersion() string {
	if t == nil {
		return ""
	}
	return t.version
}

// BotIDValue 返回已确认的 Bot 编号，未确认时为 0；供逐图来源记录使用。
func (t *Telegram) BotIDValue() int64 {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.botID
}

// ChatID 返回配置的目标频道编号，与原消息编号一起定位可接管的旧消息。
func (t *Telegram) ChatID() string {
	if t == nil {
		return ""
	}
	return t.chatID
}

// Origin 让提供方满足 storage.Origin：编号与频道都不触发新请求。
func (t *Telegram) Origin() (int64, string) { return t.BotIDValue(), t.ChatID() }

// ChatInfoOf 读取指定会话的属性，供接管前确认维护频道是独立私有会话。
func (t *Telegram) ChatInfoOf(ctx context.Context, chatID string) (ChatInfo, error) {
	if t == nil {
		return ChatInfo{}, ErrNotConfigured
	}
	trimmed := strings.TrimSpace(chatID)
	if trimmed == "" {
		return ChatInfo{}, ErrNotConfigured
	}
	return t.chatInfo(ctx, trimmed)
}

// ChatInfo 是 getChat 返回的会话属性，用于确认备份目标是否为公开会话。
type ChatInfo struct {
	ID       int64  `json:"id"`
	Type     string `json:"type"`
	Username string `json:"username"`
	Title    string `json:"title"`
	// ActiveUsernames 是可直接通过 t.me 访问的别名，任意一个存在即为公开会话。
	ActiveUsernames []string `json:"active_usernames"`
}

// Private 报告会话是否只对成员可见。
// 带公开用户名的频道或群任何人都能访问，私聊与无公开用户名的会话视为私有。
func (c ChatInfo) Private() bool {
	if c.Username != "" || len(c.ActiveUsernames) > 0 {
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
	return t.chatInfo(ctx, t.chatID)
}

// chatInfo 读取指定会话的属性。
func (t *Telegram) chatInfo(ctx context.Context, chatID string) (ChatInfo, error) {
	form := url.Values{"chat_id": {chatID}}
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

// SendDocument 把本地文件作为文档上传，用于自动备份推送。
// 提供方实例自带固定的令牌与目标频道，校验与发送应复用同一个实例。
func (t *Telegram) SendDocument(ctx context.Context, path, caption string) error {
	if t == nil {
		return ErrNotConfigured
	}
	file, err := os.Open(path)
	if err != nil {
		return errors.New("读取备份文件失败")
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || info.IsDir() {
		return errors.New("读取备份文件失败")
	}
	_, err = t.Upload(ctx, UploadRequest{
		FileName: filepath.Base(path),
		MIMEType: "application/zip",
		Size:     info.Size(),
		Mode:     ModeDocument,
		Caption:  caption,
		Reader:   file,
	})
	return err
}

// Upload 以流式 multipart 方式上传单张图片，不把文件整体载入内存。
func (t *Telegram) Upload(ctx context.Context, req UploadRequest) (UploadResult, error) {
	result, _, err := t.UploadWithOrigin(ctx, req)
	return result, err
}

// UploadWithOrigin 发送单张图片并附带本次使用的 Bot、频道与凭据指纹。
func (t *Telegram) UploadWithOrigin(ctx context.Context, req UploadRequest) (UploadResult, SendOrigin, error) {
	origin := SendOrigin{CredentialVersion: t.CredentialVersion()}
	if t != nil {
		origin.BotID, origin.ChatID = t.Origin()
	}
	result, err := t.upload(ctx, req)
	return result, origin, err
}

func (t *Telegram) upload(ctx context.Context, req UploadRequest) (UploadResult, error) {
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
	case ModeVideo:
		method, field, limit = "sendVideo", "video", MaxVideoBytes
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
		if errors.Is(err, context.DeadlineExceeded) {
			return UploadResult{}, &UploadError{Stage: "存图", Kind: KindTimeout, Note: "远端结果未确认，不自动重传"}
		}
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return UploadResult{}, &UploadError{Stage: "存图", Kind: KindTimeout, Note: "远端结果未确认，不自动重传"}
		}
		return UploadResult{}, &UploadError{Stage: "存图", Kind: KindNetwork, Note: "请求未完成"}
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
		part, err := form.CreatePart(uploadPartHeader(field, req))
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

// uploadPartHeader 显式声明文件名与内容类型：视频补齐 .mp4，
// 避免临时文件的 .part 后缀让上游按 octet-stream 处理。
func uploadPartHeader(field string, req UploadRequest) textproto.MIMEHeader {
	name := filepath.Base(req.FileName)
	if req.Mode == ModeVideo && strings.ToLower(filepath.Ext(name)) != ".mp4" {
		name = strings.TrimSuffix(name, filepath.Ext(name)) + ".mp4"
	}
	contentType := strings.TrimSpace(req.MIMEType)
	if contentType == "" {
		if req.Mode == ModeVideo {
			contentType = "video/mp4"
		} else {
			contentType = "application/octet-stream"
		}
	}
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, field, name))
	header.Set("Content-Type", contentType)
	return header
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
	// FileID 是解析出的媒体编号，不参与反序列化，由调用方按媒体类型补齐。
	FileID string `json:"-"`
	Photo  []struct {
		FileID   string `json:"file_id"`
		FileSize int64  `json:"file_size"`
	} `json:"photo"`
	Document *struct {
		FileID   string `json:"file_id"`
		FileSize int64  `json:"file_size"`
		MIMEType string `json:"mime_type"`
	} `json:"document"`
	Video *struct {
		FileID   string `json:"file_id"`
		FileSize int64  `json:"file_size"`
		MIMEType string `json:"mime_type"`
	} `json:"video"`
}

// errorFor 把上游失败转换为对外错误，不带原始描述与请求地址。
// 429 会转成带等待时长的限流错误，重试排期由调用方决定。
func (t *Telegram) errorFor(payload apiResponse) error {
	if payload.ErrorCode == http.StatusTooManyRequests {
		return &RateLimitError{
			RetryAfter: time.Duration(payload.Parameters.RetryAfter) * time.Second,
		}
	}
	// 只保留类别与状态码：描述里可能夹带上游细节，不进日志也不进界面。
	return &UploadError{
		Stage:  "存图",
		Kind:   KindForTelegram(payload.ErrorCode, payload.Description),
		Status: payload.ErrorCode,
	}
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
	if mode == ModeVideo {
		if message.Video == nil || message.Video.FileID == "" {
			return UploadResult{}, ErrUpstream
		}
		result.FileID = message.Video.FileID
		result.FileSize = message.Video.FileSize
		result.MIMEType = message.Video.MIMEType
		return result, nil
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

// takeoverDeleteTimeout 是清理临时转发的上限：删不掉也不能把后台任务拖住。
const takeoverDeleteTimeout = 10 * time.Second

// TakeoverFile 借用本 Bot 把原消息转发到指定维护频道，取得属于本 Bot 的文件编号，
// 再删掉这条临时消息。转发目标与图床频道分开，避免公开图集里出现临时消息。
func (t *Telegram) TakeoverFile(ctx context.Context, destChatID, fromChatID string, messageID int64) (string, error) {
	if t == nil {
		return "", ErrNotConfigured
	}
	dest := strings.TrimSpace(destChatID)
	from := strings.TrimSpace(fromChatID)
	if dest == "" || from == "" || messageID <= 0 {
		return "", ErrUpstream
	}
	forwarded, err := t.forwardMessage(ctx, dest, from, messageID)
	if err != nil {
		return "", err
	}
	// 清理单独设时限，并脱离请求取消：客户端断开也要把临时消息收掉。
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), takeoverDeleteTimeout)
		defer cancel()
		_, _ = t.callAPI(cleanupCtx, "deleteMessage", url.Values{
			"chat_id":    {dest},
			"message_id": {strconv.FormatInt(forwarded.MessageID, 10)},
		})
	}()
	if forwarded.FileID == "" {
		return "", ErrUpstream
	}
	return forwarded.FileID, nil
}

// forwardMessage 把一条频道消息转发到指定会话，并解析出归属本 Bot 的文件编号与消息编号。
func (t *Telegram) forwardMessage(ctx context.Context, destChatID, fromChatID string, messageID int64) (messageResult, error) {
	payload, err := t.callAPI(ctx, "forwardMessage", url.Values{
		"chat_id":      {destChatID},
		"from_chat_id": {fromChatID},
		"message_id":   {strconv.FormatInt(messageID, 10)},
	})
	if err != nil {
		return messageResult{}, err
	}
	var message messageResult
	if err := json.Unmarshal(payload, &message); err != nil {
		return messageResult{}, ErrUpstream
	}
	message.FileID = firstFileID(message)
	return message, nil
}

// callAPI 发送一次最小的表单请求并返回上游结果，失败时只给出统一错误。
func (t *Telegram) callAPI(ctx context.Context, method string, form url.Values) (json.RawMessage, error) {
	endpoint := t.apiBase + "/bot" + t.token + "/" + method
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint,
		strings.NewReader(form.Encode()))
	if err != nil {
		return nil, ErrUpstream
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	response, err := t.client.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, errTransport
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		_ = response.Body.Close()
	}()

	var payload apiResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload); err != nil {
		return nil, ErrUpstream
	}
	if !payload.OK {
		return nil, t.errorFor(payload)
	}
	return payload.Result, nil
}

// firstFileID 取消息里任意一种媒体的文件编号，供接管与删除临时消息复用。
func firstFileID(message messageResult) string {
	switch {
	case len(message.Photo) > 0:
		return message.Photo[len(message.Photo)-1].FileID
	case message.Document != nil:
		return message.Document.FileID
	case message.Video != nil:
		return message.Video.FileID
	}
	return ""
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
