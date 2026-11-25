package storage

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
)

// 失败类别：界面与日志只出现这些固定说法，不带原始上游响应、地址或令牌。
const (
	KindTooLarge  = "尺寸或格式"
	KindAuth      = "权限或配置"
	KindRateLimit = "限流"
	KindTimeout   = "超时"
	KindNetwork   = "网络"
	KindUpstream  = "上游拒绝"
	KindAccount   = "账号受限"
	KindUnknown   = "未识别的上游响应"
)

// UploadError 是带阶段与类别的可读失败说明，可直接落到任务与逐图错误字段。
type UploadError struct {
	// Stage 取「存图」或「发布」，说明失败发生在哪一段。
	Stage string
	Kind  string
	// Status 是上游可公开的状态码，未知时为 0。
	Status int
	// Note 是可选的脱敏补充，例如照片上限。
	Note string
}

// Unwrap 让调用方继续用 errors.Is(err, ErrUpstream) 判断是上游失败。
func (e *UploadError) Unwrap() error { return ErrUpstream }

func (e *UploadError) Error() string {
	text := e.Stage + "失败：" + e.Kind
	if e.Note != "" {
		text += "（" + e.Note + "）"
	}
	if e.Status > 0 {
		text += fmt.Sprintf("，上游状态 %d", e.Status)
	}
	return text
}

// KindForTelegram 把上游状态码与描述归到固定类别；只匹配已知关键字。
func KindForTelegram(status int, description string) string {
	text := strings.ToLower(description)
	switch {
	case status == 429:
		return KindRateLimit
	case status == 401 || status == 403,
		strings.Contains(text, "unauthorized"),
		strings.Contains(text, "chat not found"),
		strings.Contains(text, "not enough rights"),
		strings.Contains(text, "bot was blocked"):
		return KindAuth
	case strings.Contains(text, "frozen_method_invalid"),
		strings.Contains(text, "account is frozen"),
		strings.Contains(text, "frozen"):
		return KindAccount
	case strings.Contains(text, "file is too big"),
		strings.Contains(text, "photo_invalid_dimensions"),
		strings.Contains(text, "image_process_failed"),
		strings.Contains(text, "webpage_curl_failed"):
		return KindTooLarge
	case status >= 500:
		return KindUpstream
	default:
		return KindUnknown
	}
}

// DescribeUpload 把任意上传错误翻成一句可展示的说明，绝不回显原始错误文本。
func DescribeUpload(err error) string {
	if err == nil {
		return ""
	}
	var typed *UploadError
	if errors.As(err, &typed) {
		if typed.Kind == KindAccount {
			return typed.Error() + "（账号被冻结时只能读取，请解冻或更换 Bot）"
		}
		return typed.Error()
	}
	var limited *RateLimitError
	if errors.As(err, &limited) {
		return "存图失败：" + KindRateLimit + "，已按建议等待并重试，仍未成功"
	}
	switch {
	case errors.Is(err, ErrTooLarge):
		return (&UploadError{Stage: "存图", Kind: KindTooLarge, Note: "照片上限 10MB，文档与视频上限 20MB"}).Error()
	case errors.Is(err, ErrUnsupportedMode):
		return (&UploadError{Stage: "存图", Kind: KindTooLarge, Note: "画质模式不受支持"}).Error()
	case errors.Is(err, ErrNotConfigured):
		return (&UploadError{Stage: "存图", Kind: KindAuth, Note: "图床尚未配置"}).Error()
	case errors.Is(err, context.DeadlineExceeded):
		return (&UploadError{Stage: "存图", Kind: KindTimeout, Note: "远端结果未确认，不自动重传"}).Error()
	case errors.Is(err, context.Canceled):
		return (&UploadError{Stage: "存图", Kind: KindNetwork, Note: "请求已取消"}).Error()
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return (&UploadError{Stage: "存图", Kind: KindTimeout, Note: "远端结果未确认，不自动重传"}).Error()
	}
	if errors.Is(err, ErrUpstream) {
		return (&UploadError{Stage: "存图", Kind: KindUnknown}).Error()
	}
	return (&UploadError{Stage: "存图", Kind: KindUnknown}).Error()
}
