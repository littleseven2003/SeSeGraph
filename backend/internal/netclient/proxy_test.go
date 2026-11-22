package netclient

import (
	"errors"
	"net/http"
	"testing"
)

func TestValidateModes(t *testing.T) {
	cases := []struct {
		name string
		mode string
		url  string
		want error
	}{
		{name: "默认直连", mode: "", url: ""},
		{name: "直连", mode: ModeDirect, url: ""},
		{name: "系统代理", mode: ModeSystem, url: ""},
		{name: "自定义 http", mode: ModeCustom, url: "http://127.0.0.1:7890"},
		{name: "自定义 https 带账密", mode: ModeCustom, url: "https://user:pass@proxy.test:8443"},
		{name: "自定义 socks5", mode: ModeCustom, url: "socks5://user:pass@127.0.0.1:1080"},
		{name: "自定义缺少地址", mode: ModeCustom, url: "", want: ErrInvalidURL},
		{name: "自定义协议不支持", mode: ModeCustom, url: "ftp://proxy.test:21", want: ErrInvalidURL},
		{name: "自定义缺少主机", mode: ModeCustom, url: "http://", want: ErrInvalidURL},
		{name: "未知模式", mode: "proxy", url: "http://127.0.0.1:7890", want: ErrInvalidMode},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			err := Validate(test.mode, test.url)
			if test.want == nil {
				if err != nil {
					t.Fatalf("校验失败: %v", err)
				}
				return
			}
			if !errors.Is(err, test.want) {
				t.Fatalf("错误 = %v，期望 %v", err, test.want)
			}
		})
	}
}

// TestNewTransportDirectAndDisabled 确认直连与未启用代理时不设置代理。
func TestNewTransportDirectAndDisabled(t *testing.T) {
	transport, err := NewTransport(Config{Mode: ModeDirect, Telegram: true, Telegraph: true}, ServiceTelegram)
	if err != nil {
		t.Fatalf("构建传输层失败: %v", err)
	}
	if transport.Proxy != nil {
		t.Error("直连时不应设置代理")
	}

	transport, err = NewTransport(Config{Mode: ModeCustom, URL: "http://127.0.0.1:7890"}, ServiceTelegraph)
	if err != nil {
		t.Fatalf("构建传输层失败: %v", err)
	}
	if transport.Proxy != nil {
		t.Error("未启用的服务不应走代理")
	}
}

// clearProxyEnv 清空代理相关环境变量，避免宿主机配置干扰断言。
func clearProxyEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy",
		"ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy",
	} {
		t.Setenv(key, "")
	}
}

// systemTransport 返回系统模式下的传输层。
func systemTransport(t *testing.T) *http.Transport {
	t.Helper()
	transport, err := NewTransport(Config{Mode: ModeSystem, Telegram: true}, ServiceTelegram)
	if err != nil {
		t.Fatalf("构建传输层失败: %v", err)
	}
	if transport.Proxy == nil {
		t.Fatal("系统模式应设置代理选择函数")
	}
	return transport
}

// proxyFor 返回目标地址实际使用的代理。
func proxyFor(t *testing.T, transport *http.Transport, target string) string {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	proxyURL, err := transport.Proxy(request)
	if err != nil {
		t.Fatalf("解析代理失败: %v", err)
	}
	if proxyURL == nil {
		return ""
	}
	return proxyURL.String()
}

// TestSystemModeProtocolVariablesFirst 验证协议专属变量优先于 ALL_PROXY。
func TestSystemModeProtocolVariablesFirst(t *testing.T) {
	clearProxyEnv(t)
	t.Setenv("HTTPS_PROXY", "http://secure-proxy.test:8443")
	t.Setenv("HTTP_PROXY", "http://plain-proxy.test:8080")
	t.Setenv("ALL_PROXY", "socks5://fallback.test:1080")
	transport := systemTransport(t)

	if got := proxyFor(t, transport, "https://api.telegram.org/bot1/getMe"); got != "http://secure-proxy.test:8443" {
		t.Errorf("HTTPS 代理 = %q，期望使用 HTTPS_PROXY", got)
	}
	if got := proxyFor(t, transport, "http://telegra.ph/api/createPage"); got != "http://plain-proxy.test:8080" {
		t.Errorf("HTTP 代理 = %q，期望使用 HTTP_PROXY", got)
	}
}

// TestSystemModeFallsBackToAllProxy 验证只设置 ALL_PROXY 时请求也经过代理。
func TestSystemModeFallsBackToAllProxy(t *testing.T) {
	clearProxyEnv(t)
	t.Setenv("ALL_PROXY", "socks5://127.0.0.1:1080")
	transport := systemTransport(t)

	for _, target := range []string{"https://api.telegram.org/bot1/getMe", "http://api.telegra.ph/api/createPage"} {
		if got := proxyFor(t, transport, target); got != "socks5://127.0.0.1:1080" {
			t.Errorf("%s 代理 = %q，期望回退到 ALL_PROXY", target, got)
		}
	}
}

// TestSystemModeHonorsNoProxyAndLoopback 验证 NO_PROXY 与本地地址直连。
func TestSystemModeHonorsNoProxyAndLoopback(t *testing.T) {
	clearProxyEnv(t)
	t.Setenv("ALL_PROXY", "http://proxy.test:3128")
	transport := systemTransport(t)

	cases := []struct {
		name    string
		noProxy string
		target  string
	}{
		{name: "通配", noProxy: "*", target: "https://api.telegram.org/bot1/getMe"},
		{name: "主机名", noProxy: "api.telegram.org", target: "https://api.telegram.org/bot1/getMe"},
		{name: "域名后缀", noProxy: ".telegra.ph", target: "https://api.telegra.ph/api/createPage"},
		{name: "主机带端口", noProxy: "api.telegram.org:443", target: "https://api.telegram.org/bot1/getMe"},
		{name: "本地地址", noProxy: "", target: "http://localhost:8080/api/health"},
		{name: "回环地址", noProxy: "", target: "http://127.0.0.1:8080/api/health"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("NO_PROXY", test.noProxy)
			if got := proxyFor(t, transport, test.target); got != "" {
				t.Errorf("代理 = %q，期望直连", got)
			}
		})
	}

	// 端口不匹配时仍走代理。
	t.Setenv("NO_PROXY", "api.telegram.org:8443")
	if got := proxyFor(t, transport, "https://api.telegram.org/bot1/getMe"); got == "" {
		t.Error("端口不匹配时应继续使用代理")
	}
}

// TestSystemModeRejectsInvalidValue 验证无法解析的代理取值不回显原文。
func TestSystemModeRejectsInvalidValue(t *testing.T) {
	clearProxyEnv(t)
	t.Setenv("ALL_PROXY", "://bad-value")
	transport := systemTransport(t)

	request, err := http.NewRequest(http.MethodGet, "https://api.telegram.org/bot1/getMe", nil)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	if _, err := transport.Proxy(request); !errors.Is(err, ErrInvalidURL) {
		t.Fatalf("错误 = %v，期望 ErrInvalidURL", err)
	}
}

// TestNewTransportCustomAppliesPerService 确认自定义代理按服务分别生效。
func TestNewTransportCustomAppliesPerService(t *testing.T) {
	cfg := Config{Mode: ModeCustom, URL: "socks5://user:pass@127.0.0.1:1080", Telegram: true}
	transport, err := NewTransport(cfg, ServiceTelegram)
	if err != nil {
		t.Fatalf("构建传输层失败: %v", err)
	}
	request, err := http.NewRequest(http.MethodPost, "https://api.telegram.org/bot1/getMe", nil)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	proxyURL, err := transport.Proxy(request)
	if err != nil {
		t.Fatalf("解析代理失败: %v", err)
	}
	if proxyURL == nil || proxyURL.Scheme != "socks5" || proxyURL.Host != "127.0.0.1:1080" {
		t.Fatalf("代理地址 = %v，期望 socks5://127.0.0.1:1080", proxyURL)
	}
	if user := proxyURL.User.Username(); user != "user" {
		t.Errorf("代理用户名 = %q，期望 user", user)
	}

	other, err := NewTransport(cfg, ServiceTelegraph)
	if err != nil {
		t.Fatalf("构建传输层失败: %v", err)
	}
	if other.Proxy != nil {
		t.Error("Telegraph 未启用代理时不应设置代理")
	}
}

// TestNewClientUsesTransport 确认客户端带上传输层，便于配置切换后重建。
func TestNewClientUsesTransport(t *testing.T) {
	client, err := NewClient(Config{Mode: ModeDirect}, ServiceTelegram)
	if err != nil {
		t.Fatalf("构建客户端失败: %v", err)
	}
	if _, ok := client.Transport.(*http.Transport); !ok {
		t.Fatalf("传输层类型 = %T，期望 *http.Transport", client.Transport)
	}
	CloseIdle(client)
	CloseIdle(nil)
}
