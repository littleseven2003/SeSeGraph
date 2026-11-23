package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	// CookieName 是会话 Cookie 的名称。
	CookieName = "sesegraph_session"
	// DefaultSessionTTL 是登录状态的有效期。
	DefaultSessionTTL = 24 * time.Hour
)

// ErrInvalidSession 表示会话缺失、被篡改或已过期。
var ErrInvalidSession = errors.New("会话无效或已过期")

// Sessions 使用 HMAC 签名的 Cookie 保存登录状态，不依赖服务端会话表。
type Sessions struct {
	secret []byte
	ttl    time.Duration
	secure bool
}

// NewSessions 创建会话管理器。secure 为 true 时 Cookie 仅通过 HTTPS 传输。
func NewSessions(secret []byte, ttl time.Duration, secure bool) *Sessions {
	if ttl <= 0 {
		ttl = DefaultSessionTTL
	}
	return &Sessions{secret: secret, ttl: ttl, secure: secure}
}

// Issue 为账号签发带会话版本的令牌。
func (s *Sessions) Issue(username string, version int) string {
	expires := time.Now().Add(s.ttl).Unix()
	payload := base64.RawURLEncoding.EncodeToString([]byte(username)) +
		"|" + strconv.FormatInt(expires, 10) +
		"|" + strconv.Itoa(version)
	return payload + "." + s.sign(payload)
}

// Verify 校验令牌并返回账号名与会话版本。
// 升级前签发的两段令牌没有版本字段，按版本 0 处理，改密后自然失效。
func (s *Sessions) Verify(token string) (string, int, error) {
	if token == "" {
		return "", 0, ErrInvalidSession
	}
	payload, signature, found := strings.Cut(token, ".")
	if !found || payload == "" || signature == "" {
		return "", 0, ErrInvalidSession
	}
	if !hmac.Equal([]byte(signature), []byte(s.sign(payload))) {
		return "", 0, ErrInvalidSession
	}

	parts := strings.Split(payload, "|")
	if len(parts) < 2 || len(parts) > 3 {
		return "", 0, ErrInvalidSession
	}
	version := 0
	if len(parts) == 3 {
		parsed, err := strconv.Atoi(parts[2])
		if err != nil || parsed < 0 {
			return "", 0, ErrInvalidSession
		}
		version = parsed
	}
	expires, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return "", 0, ErrInvalidSession
	}
	if time.Now().Unix() >= expires {
		return "", 0, ErrInvalidSession
	}

	nameBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(nameBytes) == 0 {
		return "", 0, ErrInvalidSession
	}
	return string(nameBytes), version, nil
}

// SetCookie 把登录状态写入 HttpOnly Cookie，令牌带当前会话版本。
func (s *Sessions) SetCookie(w http.ResponseWriter, username string, version int) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    s.Issue(username, version),
		Path:     "/",
		MaxAge:   int(s.ttl.Seconds()),
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// ClearCookie 让浏览器立即丢弃会话 Cookie。
func (s *Sessions) ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// AuthenticateRequest 读取请求 Cookie 并返回登录账号名与会话版本。
func (s *Sessions) AuthenticateRequest(r *http.Request) (string, int, error) {
	cookie, err := r.Cookie(CookieName)
	if err != nil {
		return "", 0, ErrInvalidSession
	}
	return s.Verify(cookie.Value)
}

// sign 计算载荷的 HMAC-SHA256 签名。
func (s *Sessions) sign(payload string) string {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
