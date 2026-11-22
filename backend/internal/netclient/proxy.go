// Package netclient 按代理配置构建上游传输层。
// 支持直连、跟随系统环境变量与自定义代理三种模式，并按服务分别启用。
package netclient

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// 代理模式。
const (
	// ModeDirect 直连，不使用任何代理。
	ModeDirect = "direct"
	// ModeSystem 跟随系统或容器环境变量 HTTP_PROXY / HTTPS_PROXY / ALL_PROXY。
	ModeSystem = "system"
	// ModeCustom 使用界面保存的自定义代理，允许 http、https 与 socks5。
	ModeCustom = "custom"
)

// 传输层参数。
const (
	dialTimeout         = 10 * time.Second
	keepAlive           = 30 * time.Second
	tlsHandshakeTimeout = 10 * time.Second
	idleConnTimeout     = 90 * time.Second
	maxIdleConns        = 32
)

// 自定义地址的长度上限，避免把异常输入写进数据库。
const maxURLLength = 300

// ErrInvalidMode 表示代理模式不在允许的取值内。
var ErrInvalidMode = errors.New("代理模式不合法")

// ErrInvalidURL 表示自定义代理地址不合法。
var ErrInvalidURL = errors.New("代理地址不合法")

// Service 是需要访问上游的服务标识，代理按服务分别启用。
type Service int

// 上游服务。
const (
	ServiceTelegram Service = iota
	ServiceTelegraph
)

// Config 是一份生效的代理配置。账号密码写在 URL 内，任何接口都不回显。
type Config struct {
	Mode      string
	URL       string
	Telegram  bool
	Telegraph bool
}

// ModeOrDefault 返回规范化后的模式，空值视为直连。
func ModeOrDefault(mode string) string {
	switch value := strings.TrimSpace(mode); value {
	case ModeSystem, ModeCustom:
		return value
	default:
		return ModeDirect
	}
}

// Validate 校验模式与自定义地址：自定义模式必须给出 http、https 或 socks5 地址。
func Validate(mode, rawURL string) error {
	normalized := ModeOrDefault(mode)
	if value := strings.TrimSpace(mode); value != "" && value != normalized {
		return ErrInvalidMode
	}
	if normalized != ModeCustom {
		return nil
	}
	_, err := parseProxyURL(rawURL)
	return err
}

// parseProxyURL 解析并校验自定义代理地址，允许带账密认证。
func parseProxyURL(rawURL string) (*url.URL, error) {
	value := strings.TrimSpace(rawURL)
	if value == "" || len(value) > maxURLLength {
		return nil, ErrInvalidURL
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return nil, ErrInvalidURL
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "socks5":
	default:
		return nil, ErrInvalidURL
	}
	if _, err := net.ResolveTCPAddr("tcp", parsed.Host); err != nil {
		// 域名解析失败时仍接受，交给运行期连接处理。
		if parsed.Hostname() == "" {
			return nil, ErrInvalidURL
		}
	}
	return parsed, nil
}

// EnabledFor 报告某项服务是否启用代理。
func (c Config) EnabledFor(service Service) bool {
	switch service {
	case ServiceTelegram:
		return c.Telegram
	case ServiceTelegraph:
		return c.Telegraph
	default:
		return false
	}
}

// NewTransport 按配置为一项服务构建传输层，返回前已完成代理设置。
// 直连或该服务未启用代理时不设置 Proxy，系统模式下由环境变量决定。
func NewTransport(cfg Config, service Service) (*http.Transport, error) {
	dialer := &net.Dialer{Timeout: dialTimeout, KeepAlive: keepAlive}
	transport := &http.Transport{
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          maxIdleConns,
		IdleConnTimeout:       idleConnTimeout,
		TLSHandshakeTimeout:   tlsHandshakeTimeout,
		ExpectContinueTimeout: time.Second,
	}

	mode := ModeOrDefault(cfg.Mode)
	if mode == ModeDirect || !cfg.EnabledFor(service) {
		return transport, nil
	}
	if mode == ModeSystem {
		// ProxyFromEnvironment 读取进程环境变量，容器里的 HTTP_PROXY 由此生效。
		transport.Proxy = http.ProxyFromEnvironment
		return transport, nil
	}
	proxyURL, err := parseProxyURL(cfg.URL)
	if err != nil {
		return nil, err
	}
	transport.Proxy = http.ProxyURL(proxyURL)
	return transport, nil
}

// NewClient 为一项服务构建带代理传输层的客户端。
// 重定向策略由各上游客户端自行收紧，这里只负责传输层。
func NewClient(cfg Config, service Service) (*http.Client, error) {
	transport, err := NewTransport(cfg, service)
	if err != nil {
		return nil, err
	}
	return &http.Client{Transport: transport}, nil
}

// CloseIdle 释放传输层中空闲的连接，配置切换后调用以回收资源。
func CloseIdle(client *http.Client) {
	if client == nil {
		return
	}
	if transport, ok := client.Transport.(*http.Transport); ok && transport != nil {
		transport.CloseIdleConnections()
	}
}
