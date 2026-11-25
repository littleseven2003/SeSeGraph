// Package telegraph 封装 Telegraph API 客户端与账户管理，令牌只留在服务端。
package telegraph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// DefaultAPIBase 是 Telegraph API 的默认地址。
	DefaultAPIBase = "https://api.telegra.ph"
	// defaultTimeout 覆盖创建与编辑页面的常规耗时。
	defaultTimeout = 30 * time.Second
	// maxResponseBytes 限制响应体读取量，避免异常上游把内存打满。
	maxResponseBytes = 1 << 20
)

var (
	// ErrNotConfigured 表示尚未配置访问令牌。
	ErrNotConfigured = errors.New("Telegraph 未配置")
	// ErrUpstream 表示上游请求失败，具体原因不回显。
	ErrUpstream = errors.New("Telegraph 请求失败")
	// ErrInvalidInput 表示调用参数不合法。
	ErrInvalidInput = errors.New("Telegraph 参数不合法")
)

// errTransport 是所有网络层失败的统一说明，不携带地址与令牌。
var errTransport = fmt.Errorf("%w：网络请求未完成", ErrUpstream)

// StatusError 保留上游状态码，便于调用方给出可读原因而不回显原始响应。
type StatusError struct {
	Code int
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("%s：上游返回 %d", ErrUpstream.Error(), e.Code)
}

// Unwrap 让调用方仍可用 errors.Is(err, ErrUpstream) 判断类别。
func (e *StatusError) Unwrap() error { return ErrUpstream }

// blockRedirects 禁止跟随重定向，避免把带令牌的请求带到其他地址。
func blockRedirects(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

// Config 是客户端配置。AccessToken 为空表示只用于创建账户。
type Config struct {
	AccessToken string
	AuthorName  string
	APIBase     string
	HTTPClient  *http.Client
	Timeout     time.Duration
}

// Client 是 Telegraph API 客户端。
type Client struct {
	token      string
	authorName string
	apiBase    string
	client     *http.Client
}

// New 创建客户端。APIBase 为空时使用官方地址，客户端统一禁止重定向。
func New(cfg Config) *Client {
	base := strings.TrimRight(strings.TrimSpace(cfg.APIBase), "/")
	if base == "" {
		base = DefaultAPIBase
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	client := &http.Client{Timeout: timeout, CheckRedirect: blockRedirects}
	if cfg.HTTPClient != nil {
		cloned := *cfg.HTTPClient
		cloned.CheckRedirect = blockRedirects
		// 注入的客户端通常只带传输层；未指定超时时沿用默认值，避免上游无响应时永久等待。
		if cloned.Timeout <= 0 {
			cloned.Timeout = timeout
		}
		client = &cloned
	}
	return &Client{
		token:      strings.TrimSpace(cfg.AccessToken),
		authorName: strings.TrimSpace(cfg.AuthorName),
		apiBase:    base,
		client:     client,
	}
}

// Configured 表示客户端是否带有访问令牌。
func (c *Client) Configured() bool { return c.token != "" }

// AuthorName 返回默认作者名。
func (c *Client) AuthorName() string { return c.authorName }

// Account 是 createAccount 的返回结果，访问令牌只交给持久化层。
type Account struct {
	AccessToken string
	AuthorName  string
	ShortName   string
	AuthorURL   string
}

// CreateAccount 申请新的 Telegraph 账户。
func (c *Client) CreateAccount(ctx context.Context, shortName, authorName string) (Account, error) {
	shortName = strings.TrimSpace(shortName)
	authorName = strings.TrimSpace(authorName)
	if shortName == "" {
		shortName = "sesegraph"
	}
	if len(shortName) > 32 {
		return Account{}, ErrInvalidInput
	}

	form := url.Values{}
	form.Set("short_name", shortName)
	if authorName != "" {
		form.Set("author_name", authorName)
	}

	var result struct {
		ShortName   string `json:"short_name"`
		AuthorName  string `json:"author_name"`
		AuthorURL   string `json:"author_url"`
		AccessToken string `json:"access_token"`
	}
	if err := c.post(ctx, "createAccount", form, &result); err != nil {
		return Account{}, err
	}
	if result.AccessToken == "" {
		return Account{}, ErrUpstream
	}
	return Account{
		AccessToken: result.AccessToken,
		AuthorName:  result.AuthorName,
		ShortName:   result.ShortName,
		AuthorURL:   result.AuthorURL,
	}, nil
}

// apiEnvelope 是 Telegraph 的统一响应结构。
type apiEnvelope struct {
	OK     bool            `json:"ok"`
	Error  string          `json:"error"`
	Result json.RawMessage `json:"result"`
}

// post 发送表单请求到 apiBase/<method>。method 可以带上路径段，
// 例如编辑页面使用 editPage/{path}，与上游的接口形态一致。
func (c *Client) post(ctx context.Context, method string, form url.Values, out any) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	endpoint := c.apiBase + "/" + method
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return ErrUpstream
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	response, err := c.client.Do(request)
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

	if response.StatusCode != http.StatusOK {
		return &StatusError{Code: response.StatusCode}
	}

	var envelope apiEnvelope
	if err := json.NewDecoder(io.LimitReader(response.Body, maxResponseBytes)).Decode(&envelope); err != nil {
		return ErrUpstream
	}
	if !envelope.OK {
		return ErrUpstream
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(envelope.Result, out); err != nil {
		return ErrUpstream
	}
	return nil
}
