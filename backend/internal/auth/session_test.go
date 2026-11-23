package auth

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func newTestSessions() *Sessions {
	return NewSessions([]byte("0123456789abcdef0123456789abcdef"), time.Hour, false)
}

func TestSessionRoundTrip(t *testing.T) {
	sessions := newTestSessions()
	token := sessions.Issue("admin", 3)

	name, version, err := sessions.Verify(token)
	if err != nil {
		t.Fatalf("有效令牌应校验通过: %v", err)
	}
	if name != "admin" {
		t.Errorf("账号名 = %q，期望 admin", name)
	}
	if version != 3 {
		t.Errorf("会话版本 = %d，期望 3", version)
	}
}

func TestSessionRejectsTampering(t *testing.T) {
	sessions := newTestSessions()
	token := sessions.Issue("admin", 0)

	tamperedPayload := "YWRtaW4%3D|9999999999." + strings.SplitN(token, ".", 2)[1]
	if _, _, err := sessions.Verify(tamperedPayload); err == nil {
		t.Error("被篡改载荷的令牌应被拒绝")
	}

	parts := strings.SplitN(token, ".", 2)
	if _, _, err := sessions.Verify(parts[0] + ".AAAA"); err == nil {
		t.Error("被篡改签名的令牌应被拒绝")
	}

	other := NewSessions([]byte("ffffffffffffffffffffffffffffffff"), time.Hour, false)
	if _, _, err := other.Verify(token); err == nil {
		t.Error("换用其他密钥的令牌应被拒绝")
	}

	// 版本位也被签名覆盖，改成更高版本同样无效。
	payload, _, _ := strings.Cut(token, ".")
	bumped := payload + "0|9"
	if _, _, err := sessions.Verify(bumped + "." + sessions.sign(bumped)); err == nil {
		t.Error("被篡改版本的令牌应被拒绝")
	}
}

func TestSessionRejectsExpiredAndMalformed(t *testing.T) {
	sessions := newTestSessions()
	expiredPayload := base64.RawURLEncoding.EncodeToString([]byte("admin")) +
		"|" + strconv.FormatInt(time.Now().Add(-time.Minute).Unix(), 10)
	if _, _, err := sessions.Verify(expiredPayload + "." + sessions.sign(expiredPayload)); err == nil {
		t.Error("过期令牌应被拒绝")
	}

	for _, token := range []string{"", "garbage", "a.b.c", "|123.zzz", "abc."} {
		if _, _, err := newTestSessions().Verify(token); err == nil {
			t.Errorf("格式非法的令牌 %q 应被拒绝", token)
		}
	}
}

func TestLegacyTokenWithoutVersionCountsAsZero(t *testing.T) {
	sessions := newTestSessions()
	legacyPayload := base64.RawURLEncoding.EncodeToString([]byte("admin")) +
		"|" + strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)
	token := legacyPayload + "." + sessions.sign(legacyPayload)

	name, version, err := sessions.Verify(token)
	if err != nil || name != "admin" || version != 0 {
		t.Errorf("升级前的令牌应按版本 0 通过: name=%q version=%d err=%v", name, version, err)
	}
}

func TestSetCookieAttributes(t *testing.T) {
	rec := httptest.NewRecorder()
	NewSessions([]byte("0123456789abcdef0123456789abcdef"), time.Hour, true).SetCookie(rec, "admin", 2)

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("Cookie 数量 = %d，期望 1", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Name != CookieName {
		t.Errorf("Cookie 名 = %q", cookie.Name)
	}
	if !cookie.HttpOnly {
		t.Error("Cookie 必须是 HttpOnly")
	}
	if !cookie.Secure {
		t.Error("HTTPS 部署下 Cookie 必须带 Secure")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v", cookie.SameSite)
	}
	if cookie.Path != "/" {
		t.Errorf("Path = %q", cookie.Path)
	}
	if cookie.MaxAge <= 0 {
		t.Errorf("MaxAge = %d，应为正数", cookie.MaxAge)
	}
}

func TestClearCookieExpiresSession(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestSessions().ClearCookie(rec)

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("Cookie 数量 = %d，期望 1", len(cookies))
	}
	if cookies[0].MaxAge >= 0 {
		t.Errorf("MaxAge = %d，应让浏览器立即失效", cookies[0].MaxAge)
	}
}

func TestAuthenticateRequest(t *testing.T) {
	sessions := newTestSessions()

	req := httptest.NewRequest(http.MethodGet, "/api/posts", nil)
	if _, _, err := sessions.AuthenticateRequest(req); err == nil {
		t.Error("无 Cookie 的请求应校验失败")
	}

	req.AddCookie(&http.Cookie{Name: CookieName, Value: sessions.Issue("admin", 4)})
	name, version, err := sessions.AuthenticateRequest(req)
	if err != nil || name != "admin" || version != 4 {
		t.Errorf("带有效 Cookie 的请求应通过: name=%q version=%d err=%v", name, version, err)
	}
}
