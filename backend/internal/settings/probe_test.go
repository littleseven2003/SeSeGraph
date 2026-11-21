package settings

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeTelegram 返回一个只读 Bot API 上游：getMe、getChat、getChatMember。
type fakeTelegram struct {
	mu          sync.Mutex
	calls       []string
	meStatus    int
	meOK        bool
	chatType    string
	memberState string
	canPost     bool
	chatError   bool
	delay       time.Duration
}

func (f *fakeTelegram) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		f.mu.Lock()
		f.calls = append(f.calls, method)
		f.mu.Unlock()
		if f.delay > 0 {
			time.Sleep(f.delay)
		}
		w.Header().Set("Content-Type", "application/json")
		switch method {
		case "getMe":
			if f.meStatus != 0 {
				w.WriteHeader(f.meStatus)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"ok": false, "error_code": f.meStatus, "description": "Unauthorized",
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true, "result": map[string]any{"id": 42, "username": "sese_bot"},
			})
		case "getChat":
			if f.chatError {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"ok": false, "error_code": 400, "description": "chat not found",
				})
				return
			}
			chatType := f.chatType
			if chatType == "" {
				chatType = "channel"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true, "result": map[string]any{"id": -100123, "type": chatType, "title": "测试频道"},
			})
		case "getChatMember":
			status := f.memberState
			if status == "" {
				status = "administrator"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true, "result": map[string]any{"status": status, "can_post_messages": f.canPost},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "description": "not found"})
		}
	})
}

func (f *fakeTelegram) callList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// fakeTelegraph 返回只读账户信息上游。
type fakeTelegraph struct {
	invalid bool
	calls   int
	mu      sync.Mutex
	delay   time.Duration
}

func (f *fakeTelegraph) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.calls++
		f.mu.Unlock()
		if f.delay > 0 {
			time.Sleep(f.delay)
		}
		w.Header().Set("Content-Type", "application/json")
		if f.invalid {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "ACCESS_TOKEN_INVALID"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true, "result": map[string]any{"author_name": "SeSe", "author_url": "https://telegra.ph/SeSe"},
		})
	})
}

// probeOptions 组装指向假上游的检测参数。
func probeOptions(tgURL, tgToken, tgChannel, tpURL, tpToken string) ProbeOptions {
	return ProbeOptions{
		TelegramToken:    tgToken,
		TelegramChannel:  tgChannel,
		TelegramAPIBase:  tgURL,
		TelegraphToken:   tpToken,
		TelegraphAPIBase: tpURL,
		Timeout:          2 * time.Second,
	}
}

func TestProbeReportsBothTargets(t *testing.T) {
	tg := &fakeTelegram{canPost: true}
	tp := &fakeTelegraph{}
	tgServer := httptest.NewServer(tg.handler())
	defer tgServer.Close()
	tpServer := httptest.NewServer(tp.handler())
	defer tpServer.Close()

	result := Probe(context.Background(), probeOptions(tgServer.URL, "123:token", "@sese", tpServer.URL, "tp-token"))

	if result.CheckedAt == "" {
		t.Error("缺少检测时间")
	}
	if result.Telegram.Status != ProbeOK {
		t.Errorf("Telegram 状态 = %+v，期望 ok", result.Telegram)
	}
	if result.Telegraph.Status != ProbeOK {
		t.Errorf("Telegraph 状态 = %+v，期望 ok", result.Telegraph)
	}
	if result.Telegram.LatencyMS < 0 || result.Telegraph.LatencyMS < 0 {
		t.Error("耗时不应为负")
	}
	// 只允许只读接口，不得调用任何发送方法。
	for _, call := range tg.callList() {
		switch call {
		case "getMe", "getChat", "getChatMember":
		default:
			t.Errorf("检测调用了非只读接口 %s", call)
		}
	}
	if strings.Contains(result.Telegram.Detail, "123:token") || strings.Contains(result.Telegraph.Detail, "tp-token") {
		t.Error("检测结果不应包含令牌")
	}
}

func TestProbeTelegramFailures(t *testing.T) {
	cases := []struct {
		name string
		bot  *fakeTelegram
		want string
	}{
		{"令牌无效", &fakeTelegram{meStatus: http.StatusUnauthorized}, "Bot Token 无效或已失效"},
		{"频道不可访问", &fakeTelegram{chatError: true}, "频道不可访问"},
		{"不是管理员", &fakeTelegram{memberState: "member"}, "Bot 不是频道管理员"},
		{"缺少发布权限", &fakeTelegram{canPost: false}, "缺少发布消息权限"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(tc.bot.handler())
			defer server.Close()
			tp := &fakeTelegraph{}
			tpServer := httptest.NewServer(tp.handler())
			defer tpServer.Close()

			result := Probe(context.Background(), probeOptions(server.URL, "123:token", "-1001234567890", tpServer.URL, "tp"))
			if result.Telegram.Status != ProbeFailed {
				t.Fatalf("状态 = %+v，期望 failed", result.Telegram)
			}
			if !strings.Contains(result.Telegram.Detail, tc.want) {
				t.Errorf("说明 = %q，期望包含 %q", result.Telegram.Detail, tc.want)
			}
		})
	}
}

func TestProbeTelegraphInvalidToken(t *testing.T) {
	tg := &fakeTelegram{canPost: true}
	tgServer := httptest.NewServer(tg.handler())
	defer tgServer.Close()
	tpServer := httptest.NewServer((&fakeTelegraph{invalid: true}).handler())
	defer tpServer.Close()

	result := Probe(context.Background(), probeOptions(tgServer.URL, "123:token", "@sese", tpServer.URL, "bad"))
	if result.Telegraph.Status != ProbeFailed || !strings.Contains(result.Telegraph.Detail, "令牌无效") {
		t.Errorf("Telegraph 结果 = %+v，期望令牌无效", result.Telegraph)
	}
	if result.Telegram.Status != ProbeOK {
		t.Errorf("Telegram 结果 = %+v，单侧失败不应影响另一侧", result.Telegram)
	}
}

func TestProbeUnconfigured(t *testing.T) {
	result := Probe(context.Background(), ProbeOptions{})
	if result.Telegram.Status != ProbeUnconfigured || result.Telegraph.Status != ProbeUnconfigured {
		t.Errorf("未配置结果 = %+v，期望两项都是 unconfigured", result)
	}
	if result.Telegram.LatencyMS != 0 || result.Telegraph.LatencyMS != 0 {
		t.Error("未配置时不应产生耗时")
	}
}

func TestProbeNetworkFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := server.URL
	server.Close()

	result := Probe(context.Background(), probeOptions(url, "123:token", "-1001234567890", url, "tp"))
	if result.Telegram.Status != ProbeFailed || !strings.Contains(result.Telegram.Detail, "Telegram") {
		t.Errorf("Telegram 结果 = %+v，期望连接失败", result.Telegram)
	}
	if result.Telegraph.Status != ProbeFailed {
		t.Errorf("Telegraph 结果 = %+v，期望连接失败", result.Telegraph)
	}
}

// TestProbeRunsTargetsInParallel 让两侧各有可观测延迟：串行需要约 600ms，
// 并行时总耗时接近较长的一侧。
func TestProbeRunsTargetsInParallel(t *testing.T) {
	tg := &fakeTelegram{canPost: true, delay: 100 * time.Millisecond}
	tgServer := httptest.NewServer(tg.handler())
	defer tgServer.Close()
	tpServer := httptest.NewServer((&fakeTelegraph{delay: 300 * time.Millisecond}).handler())
	defer tpServer.Close()

	started := time.Now()
	result := Probe(context.Background(), probeOptions(tgServer.URL, "123:token", "@sese", tpServer.URL, "tp"))
	elapsed := time.Since(started)

	if result.Telegram.Status != ProbeOK || result.Telegraph.Status != ProbeOK {
		t.Fatalf("结果 = %+v，期望两项都 ok", result)
	}
	if elapsed >= 550*time.Millisecond {
		t.Errorf("总耗时 %s，期望并行执行接近单侧耗时", elapsed)
	}
}
