package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
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
)

// TelegramConfig 是 Telegram 图床配置。Token 只在本类型内部使用，不对外暴露。
type TelegramConfig struct {
	Token            string
	ChatID           string
	APIBase          string
	HTTPClient       *http.Client
	PhotoMaxBytes    int64
	DocumentMaxBytes int64
}

// Telegram 通过 Bot API 把图片存入私有频道。
type Telegram struct {
	token    string
	chatID   string
	apiBase  string
	client   *http.Client
	photoMax int64
	docMax   int64
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
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: defaultTimeout}
	}
	photoMax := cfg.PhotoMaxBytes
	if photoMax <= 0 {
		photoMax = DefaultPhotoMaxBytes
	}
	docMax := cfg.DocumentMaxBytes
	if docMax <= 0 {
		docMax = DefaultDocumentMaxBytes
	}

	return &Telegram{
		token:    token,
		chatID:   chatID,
		apiBase:  base,
		client:   client,
		photoMax: photoMax,
		docMax:   docMax,
	}, nil
}

// Name 返回提供方标识。
func (t *Telegram) Name() string { return "telegram" }

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

	body, contentType, writeErr := t.streamBody(req, field, limit)

	endpoint := t.apiBase + "/bot" + t.token + "/" + method
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return UploadResult{}, ErrUpstream
	}
	httpReq.Header.Set("Content-Type", contentType)

	response, err := t.client.Do(httpReq)
	if err != nil {
		// 请求体在流式写入阶段就失败时（例如超过上限），优先返回对应的业务错误。
		if streamErr := writeErr(); streamErr != nil && errors.Is(streamErr, ErrTooLarge) {
			return UploadResult{}, ErrTooLarge
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return UploadResult{}, ctxErr
		}
		return UploadResult{}, t.sanitizeError(err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
	}()

	if streamErr := writeErr(); streamErr != nil {
		if errors.Is(streamErr, ErrTooLarge) {
			return UploadResult{}, ErrTooLarge
		}
		return UploadResult{}, ErrUpstream
	}

	var payload apiResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload); err != nil {
		return UploadResult{}, ErrUpstream
	}
	if !payload.OK {
		return UploadResult{}, t.errorFor(payload)
	}

	return t.resultFrom(payload.Result, mode)
}

// streamBody 在后台把请求体写入管道，从而避免占用整块内存。
func (t *Telegram) streamBody(req UploadRequest, field string, limit int64) (io.Reader, string, func() error) {
	reader, writer := io.Pipe()
	form := multipart.NewWriter(writer)
	contentType := form.FormDataContentType()

	var writeErr error
	go func() {
		defer func() {
			if err := form.Close(); err != nil && writeErr == nil {
				writeErr = err
			}
			if writeErr != nil {
				_ = writer.CloseWithError(writeErr)
				return
			}
			_ = writer.Close()
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

	return reader, contentType, func() error { return writeErr }
}

type apiResponse struct {
	OK          bool            `json:"ok"`
	ErrorCode   int             `json:"error_code"`
	Description string          `json:"description"`
	Result      json.RawMessage `json:"result"`
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
func (t *Telegram) errorFor(payload apiResponse) error {
	return fmt.Errorf("%w：上游返回 %d", ErrUpstream, payload.ErrorCode)
}

// sanitizeError 去掉错误文本里可能出现的请求地址与令牌。
func (t *Telegram) sanitizeError(err error) error {
	return errors.New(strings.ReplaceAll(err.Error(), t.token, "***"))
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
