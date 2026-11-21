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

// Issue 为账号签发会话令牌。
func (s *Sessions) Issue(username string) string {
	expires := time.Now().Add(s.ttl).Unix()
	payload := base64.RawURLEncoding.EncodeToString([]byte(username)) +
		"|" + strconv.FormatInt(expires, 10)
	return payload + "." + s.sign(payload)
}

// Verify 校验令牌并返回账号名。
func (s *Sessions) Verify(token string) (string, error) {
	if token == "" {
		return "", ErrInvalidSession
	}
	payload, signature, found := strings.Cut(token, ".")
	if !found || payload == "" || signature == "" {
		return "", ErrInvalidSession
	}
	if !hmac.Equal([]byte(signature), []byte(s.sign(payload))) {
		return "", ErrInvalidSession
	}

	encodedName, expiresText, found := strings.Cut(payload, "|")
	if !found {
		return "", ErrInvalidSession
	}
	expires, err := strconv.ParseInt(expiresText, 10, 64)
	if err != nil {
		return "", ErrInvalidSession
	}
	if time.Now().Unix() >= expires {
		return "", ErrInvalidSession
	}

	nameBytes, err := base64.RawURLEncoding.DecodeString(encodedName)
	if err != nil || len(nameBytes) == 0 {
		return "", ErrInvalidSession
	}
	return string(nameBytes), nil
}

// SetCookie 把登录状态写入 HttpOnly Cookie。
func (s *Sessions) SetCookie(w http.ResponseWriter, username string) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    s.Issue(username),
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

// AuthenticateRequest 读取请求 Cookie 并返回登录账号名。
func (s *Sessions) AuthenticateRequest(r *http.Request) (string, error) {
	cookie, err := r.Cookie(CookieName)
	if err != nil {
		return "", ErrInvalidSession
	}
	return s.Verify(cookie.Value)
}

// sign 计算载荷的 HMAC-SHA256 签名。
func (s *Sessions) sign(payload string) string {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
