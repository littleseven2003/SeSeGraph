package netclient

import (
	"errors"
	"net/http"
	"reflect"
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

// TestNewTransportSystemFollowsEnvironment 确认系统模式交给环境变量决定。
func TestNewTransportSystemFollowsEnvironment(t *testing.T) {
	transport, err := NewTransport(Config{Mode: ModeSystem, Telegram: true}, ServiceTelegram)
	if err != nil {
		t.Fatalf("构建传输层失败: %v", err)
	}
	if transport.Proxy == nil {
		t.Fatal("系统模式应使用环境变量代理")
	}
	if reflect.ValueOf(transport.Proxy).Pointer() != reflect.ValueOf(http.ProxyFromEnvironment).Pointer() {
		t.Error("系统模式应直接复用 ProxyFromEnvironment")
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
