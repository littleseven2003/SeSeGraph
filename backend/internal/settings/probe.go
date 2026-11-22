package settings

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 连通性检测状态。
const (
	ProbeOK           = "ok"
	ProbeFailed       = "failed"
	ProbeUnconfigured = "unconfigured"
)

// 检测参数默认值。
const (
	// DefaultProbeTimeout 是单项检测的耗时上限。
	DefaultProbeTimeout = 8 * time.Second
	maxProbeBodyBytes   = 64 << 10
)

// ProbeTarget 是单项检测结果，不包含任何凭据或上游原始地址。
type ProbeTarget struct {
	Status    string `json:"status"`
	LatencyMS int64  `json:"latency_ms"`
	Detail    string `json:"detail"`
}

// ProbeResult 是一次自检的结果。两项检测并行执行，各自记录耗时。
type ProbeResult struct {
	CheckedAt string      `json:"checked_at"`
	Telegram  ProbeTarget `json:"telegram"`
	Telegraph ProbeTarget `json:"telegraph"`
}

// ProbeOptions 是检测所需的生效配置与网络参数。
type ProbeOptions struct {
	TelegramToken    string
	TelegramChannel  string
	TelegramAPIBase  string
	TelegraphToken   string
	TelegraphAPIBase string
	// HTTPClient 是两项检测共用的客户端；按服务区分代理时用下面两项覆盖。
	HTTPClient      *http.Client
	TelegramClient  *http.Client
	TelegraphClient *http.Client
	Now             func() time.Time
	Timeout         time.Duration
}

// Probe 并行检测 Telegram 与 Telegraph 的可用性，只做只读请求。
// 两项检测互不阻塞，任一项失败都不影响另一项的结果。
func Probe(ctx context.Context, opts ProbeOptions) ProbeResult {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultProbeTimeout
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	telegramClient := probeClientFor(opts.TelegramClient, opts.HTTPClient)
	telegraphClient := probeClientFor(opts.TelegraphClient, opts.HTTPClient)

	var telegram, telegraph ProbeTarget
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		telegram = probeTelegram(ctx, telegramClient, timeout, opts)
	}()
	go func() {
		defer wg.Done()
		telegraph = probeTelegraph(ctx, telegraphClient, timeout, opts)
	}()
	wg.Wait()

	return ProbeResult{
		CheckedAt: now().UTC().Format(time.RFC3339),
		Telegram:  telegram,
		Telegraph: telegraph,
	}
}

// probeClientFor 选择检测用的客户端，并禁止跟随重定向：
// 令牌随请求发出，跳转到其他主机等同于泄露凭据。
func probeClientFor(primary, fallback *http.Client) *http.Client {
	client := primary
	if client == nil {
		client = fallback
	}
	if client == nil {
		return &http.Client{CheckRedirect: stopRedirect}
	}
	cloned := *client
	cloned.CheckRedirect = stopRedirect
	return &cloned
}

// probeTelegram 依次校验 Bot 身份、频道可访问性与发布权限。
// 三步都是只读接口，不会向频道发送任何内容。
func probeTelegram(ctx context.Context, client *http.Client, timeout time.Duration, opts ProbeOptions) ProbeTarget {
	if strings.TrimSpace(opts.TelegramToken) == "" || strings.TrimSpace(opts.TelegramChannel) == "" {
		return ProbeTarget{Status: ProbeUnconfigured, Detail: "Telegram 未配置"}
	}
	started := time.Now()
	base := strings.TrimRight(strings.TrimSpace(opts.TelegramAPIBase), "/")
	if base == "" {
		base = DefaultTelegramAPIBase
	}
	call := func(method string, params url.Values, result any) error {
		return telegramCall(ctx, client, timeout, base, opts.TelegramToken, method, params, result)
	}

	var me struct {
		ID int64 `json:"id"`
	}
	if err := call("getMe", nil, &me); err != nil {
		return telegramFailure(started, err, "Bot Token 无效或已失效")
	}

	var chat struct {
		Type  string `json:"type"`
		Title string `json:"title"`
	}
	if err := call("getChat", url.Values{"chat_id": {opts.TelegramChannel}}, &chat); err != nil {
		return telegramFailure(started, err, "频道不可访问，请确认 Bot 已加入该频道")
	}

	var member struct {
		Status          string `json:"status"`
		CanPostMessages bool   `json:"can_post_messages"`
	}
	if err := call("getChatMember", url.Values{
		"chat_id": {opts.TelegramChannel},
		"user_id": {strconv.FormatInt(me.ID, 10)},
	}, &member); err != nil {
		return telegramFailure(started, err, "无法读取 Bot 在频道内的权限")
	}
	switch member.Status {
	case "creator":
	case "administrator":
		if chat.Type == "channel" && !member.CanPostMessages {
			return ProbeTarget{Status: ProbeFailed, LatencyMS: elapsedMS(started), Detail: "Bot 是频道管理员但缺少发布消息权限"}
		}
	default:
		return ProbeTarget{Status: ProbeFailed, LatencyMS: elapsedMS(started), Detail: "Bot 不是频道管理员，无法发布"}
	}
	return ProbeTarget{Status: ProbeOK, LatencyMS: elapsedMS(started), Detail: "Bot 可向频道发布消息"}
}

// probeTelegraph 用账户信息接口校验令牌，同样只读。
func probeTelegraph(ctx context.Context, client *http.Client, timeout time.Duration, opts ProbeOptions) ProbeTarget {
	if strings.TrimSpace(opts.TelegraphToken) == "" {
		return ProbeTarget{Status: ProbeUnconfigured, Detail: "Telegraph 未配置"}
	}
	started := time.Now()
	base := strings.TrimRight(strings.TrimSpace(opts.TelegraphAPIBase), "/")
	if base == "" {
		base = DefaultTelegraphAPIBase
	}
	body := url.Values{
		"access_token": {strings.TrimSpace(opts.TelegraphToken)},
		"fields":       {`["author_name","author_url"]`},
	}
	checkCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(checkCtx, http.MethodPost, base+"/getAccountInfo",
		strings.NewReader(body.Encode()))
	if err != nil {
		return ProbeTarget{Status: ProbeFailed, LatencyMS: elapsedMS(started), Detail: "Telegraph 检测请求构造失败"}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return telegraphFailure(started, err)
	}
	defer func() { _ = resp.Body.Close() }()

	var envelope struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		Error       string          `json:"error"`
		Description string          `json:"description"`
	}
	if err := decodeLimited(resp.Body, &envelope); err != nil {
		return ProbeTarget{Status: ProbeFailed, LatencyMS: elapsedMS(started), Detail: "Telegraph 返回内容无法解析"}
	}
	if !envelope.OK {
		if strings.EqualFold(envelope.Error, "ACCESS_TOKEN_INVALID") {
			return ProbeTarget{Status: ProbeFailed, LatencyMS: elapsedMS(started), Detail: "Telegraph 令牌无效"}
		}
		return ProbeTarget{Status: ProbeFailed, LatencyMS: elapsedMS(started), Detail: "Telegraph 拒绝了本次检测"}
	}
	return ProbeTarget{Status: ProbeOK, LatencyMS: elapsedMS(started), Detail: "Telegraph 账户可用"}
}

// telegramCall 发起一次 Bot API 只读请求。错误信息只保留类别，不带原始地址或令牌。
func telegramCall(ctx context.Context, client *http.Client, timeout time.Duration,
	base, token, method string, params url.Values, result any) error {
	checkCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	endpoint := base + "/bot" + token + "/" + method
	var body io.Reader
	if params != nil {
		body = strings.NewReader(params.Encode())
	}
	req, err := http.NewRequestWithContext(checkCtx, http.MethodPost, endpoint, body)
	if err != nil {
		return errProbeRequest
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return errProbeNetwork
	}
	defer func() { _ = resp.Body.Close() }()

	var envelope struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
	}
	if err := decodeLimited(resp.Body, &envelope); err != nil {
		return errProbeDecode
	}
	if !envelope.OK {
		return errProbeRejected
	}
	if result != nil {
		if err := json.Unmarshal(envelope.Result, result); err != nil {
			return errProbeDecode
		}
	}
	return nil
}

// stopRedirect 让检测请求把 3xx 当作最终响应，不携带凭据跳转到新地址。
func stopRedirect(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

// decodeLimited 限制响应体大小后解码，避免异常上游把内存吃满。
func decodeLimited(body io.Reader, target any) error {
	return json.NewDecoder(io.LimitReader(body, maxProbeBodyBytes)).Decode(target)
}

// 检测过程中的错误类别，只用于区分文案。
var (
	errProbeRequest  = &probeError{"请求构造失败"}
	errProbeNetwork  = &probeError{"无法连接上游"}
	errProbeDecode   = &probeError{"上游返回内容无法解析"}
	errProbeRejected = &probeError{"上游拒绝了请求"}
)

type probeError struct{ detail string }

func (e *probeError) Error() string { return e.detail }

// telegramFailure 把检测错误转换成固定文案，避免回显原始地址与令牌。
func telegramFailure(started time.Time, err error, rejected string) ProbeTarget {
	if errors.Is(err, errProbeRejected) {
		return ProbeTarget{Status: ProbeFailed, LatencyMS: elapsedMS(started), Detail: rejected}
	}
	return ProbeTarget{Status: ProbeFailed, LatencyMS: elapsedMS(started), Detail: networkDetail(err, "Telegram")}
}

// telegraphFailure 生成 Telegraph 侧的失败文案。
func telegraphFailure(started time.Time, err error) ProbeTarget {
	return ProbeTarget{Status: ProbeFailed, LatencyMS: elapsedMS(started), Detail: networkDetail(err, "Telegraph")}
}

// networkDetail 区分超时、连接失败与响应异常。
func networkDetail(err error, platform string) string {
	if isTimeout(err) {
		return platform + " 检测超时"
	}
	switch {
	case errors.Is(err, errProbeNetwork):
		return "无法连接 " + platform
	case errors.Is(err, errProbeDecode):
		return platform + " 返回内容无法解析"
	default:
		return platform + " 检测失败"
	}
}

// isTimeout 判断错误是否由超时引起。
func isTimeout(err error) bool {
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout()
	}
	return false
}

// elapsedMS 返回从 started 到现在的毫秒数，向下取整。
func elapsedMS(started time.Time) int64 {
	return time.Since(started).Milliseconds()
}
