package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/littleseven2003/SeSeGraph/backend/internal/botpool"
	"github.com/littleseven2003/SeSeGraph/backend/internal/settings"
)

// TestBotsEndpointsStoreAndMaskTokens 验证 Bot 池可保存、可读取，且响应里只有掩码没有明文。
func TestBotsEndpointsStoreAndMaskTokens(t *testing.T) {
	var pool *botpool.Pool
	env := newTestEnvWith(t, testSetupToken, func(deps *Deps, handle *sql.DB) {
		pool = botpool.New(settings.NewStore(handle))
		deps.Bots = pool
	})
	cookie := env.setup(t)

	rec := env.do(t, http.MethodGet, "/api/settings/bots", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("读取状态码 = %d，期望 200，响应: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got == "" || json.Valid(rec.Body.Bytes()) == false {
		t.Fatalf("读取响应不是合法 JSON: %s", got)
	}

	tokenA := "111:" + strings.Repeat("a", 24) + "AAAA"
	tokenB := "222:" + strings.Repeat("b", 24) + "BBBB"
	body := fmt.Sprintf(`{"bots":[{"bot_id":111,"alias":"主力","token":%q,"priority":0},`+
		`{"bot_id":222,"alias":"备用","token":%q,"priority":1}]}`, tokenA, tokenB)
	rec = env.do(t, http.MethodPut, "/api/settings/bots", body, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("保存状态码 = %d，期望 200，响应: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); !json.Valid([]byte(got)) {
		t.Fatalf("保存响应不是合法 JSON: %s", got)
	}
	if containsToken(rec.Body.String()) {
		t.Fatalf("保存响应回显了明文令牌: %s", rec.Body.String())
	}

	var parsed botsBody
	if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("解析保存响应失败: %v", err)
	}
	if len(parsed.Bots) != 2 {
		t.Fatalf("Bot 数量 = %d，期望 2", len(parsed.Bots))
	}
	if !parsed.PoolEnabled {
		t.Fatal("两个可写入 Bot 时应启用池")
	}
	if parsed.Bots[0].MaskedToken != "••••AAAA" || parsed.Bots[1].MaskedToken != "••••BBBB" {
		t.Fatalf("掩码 = %q/%q，期望只保留尾部四位", parsed.Bots[0].MaskedToken, parsed.Bots[1].MaskedToken)
	}

	// 令牌留空时沿用原值，仍可正常读取。
	body = `{"bots":[{"bot_id":111,"alias":"主力二","token":"","priority":0},` +
		`{"bot_id":222,"alias":"备用","token":"","priority":1}]}`
	rec = env.do(t, http.MethodPut, "/api/settings/bots", body, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("留空保存状态码 = %d，期望 200，响应: %s", rec.Code, rec.Body.String())
	}
	stored, err := pool.List(context.Background())
	if err != nil {
		t.Fatalf("读取池失败: %v", err)
	}
	if len(stored) != 2 || stored[0].Token != tokenA || stored[0].Alias != "主力二" {
		t.Fatalf("留空保存后池内容不符: %+v", stored)
	}
}

// containsToken 检查响应里是否出现明文令牌片段，复用包内已有的 contains。
func containsToken(body string) bool {
	return contains(body, strings.Repeat("a", 24)) || contains(body, strings.Repeat("b", 24))
}

// TestUpdateBotsRejectsWhenProbeFails 验证新令牌探测失败时不落库，原有配置继续可用。
func TestUpdateBotsRejectsWhenProbeFails(t *testing.T) {
	var pool *botpool.Pool
	env := newTestEnvWith(t, testSetupToken, func(deps *Deps, handle *sql.DB) {
		pool = botpool.New(settings.NewStore(handle))
		deps.Bots = pool
		deps.Settings = settings.NewResolver(settings.NewStore(handle), settings.Env{
			TelegramToken:   "999:existing",
			TelegramChannel: "-100777",
		})
	})
	cookie := env.setup(t)

	original := probeBots
	probeBots = func(context.Context, settings.ProbeOptions) settings.ProbeResult {
		return settings.ProbeResult{Telegram: settings.ProbeTarget{Status: settings.ProbeFailed, Detail: "Bot 不是频道管理员，无法发布"}}
	}
	t.Cleanup(func() { probeBots = original })

	body := `{"bots":[{"bot_id":111,"alias":"主力","token":"111:` + strings.Repeat("c", 24) + `","priority":0}]}`
	rec := env.do(t, http.MethodPut, "/api/settings/bots", body, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("探测失败状态码 = %d，期望 400，响应: %s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), "主力") || !contains(rec.Body.String(), "无法发布") {
		t.Fatalf("失败原因未带别名与说明: %s", rec.Body.String())
	}

	stored, err := pool.List(context.Background())
	if err != nil {
		t.Fatalf("读取池失败: %v", err)
	}
	if len(stored) != 0 {
		t.Fatalf("探测失败不应落库，实际 %+v", stored)
	}
}

// TestTelegramSaveIsAtomicAndChecksBotID 验证频道与 Bot 列表一次提交：
// 探测失败或令牌对应的实际编号与原行不一致时，频道与列表都不会被改动。
func TestTelegramSaveIsAtomicAndChecksBotID(t *testing.T) {
	tg := fakeTelegramByToken(t)
	var pool *botpool.Pool
	var resolver *settings.Resolver
	env := newTestEnvWith(t, testSetupToken, func(deps *Deps, handle *sql.DB) {
		store := settings.NewStore(handle)
		pool = botpool.New(store)
		resolver = settings.NewResolver(store, settings.Env{TelegramAPIBase: tg.URL})
		deps.Bots = pool
		deps.Settings = resolver
	})
	cookie := env.setup(t)
	ctx := context.Background()

	token := "111:" + strings.Repeat("d", 24)
	body := fmt.Sprintf(`{"channel_id":"-100777","bots":[{"bot_id":111,"alias":"主力","token":%q,"priority":0}]}`, token)
	rec := env.do(t, http.MethodPut, "/api/settings/telegram", body, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("首次保存状态码 = %d，期望 200，响应: %s", rec.Code, rec.Body.String())
	}
	effective, err := resolver.Effective(ctx)
	if err != nil || effective.TelegramChannel.Value != "-100777" {
		t.Fatalf("频道 = %q（err=%v），期望 -100777", effective.TelegramChannel.Value, err)
	}

	// 换令牌但编号与原行不一致：整笔拒绝，频道与列表都保持原样。
	body = fmt.Sprintf(`{"channel_id":"-100888","bots":[{"bot_id":222,"alias":"主力","token":%q,"priority":0}]}`, token)
	rec = env.do(t, http.MethodPut, "/api/settings/telegram", body, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("编号不一致状态码 = %d，期望 400，响应: %s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), "不一致") {
		t.Fatalf("失败原因未说明编号不一致: %s", rec.Body.String())
	}
	effective, err = resolver.Effective(ctx)
	if err != nil || effective.TelegramChannel.Value != "-100777" {
		t.Fatalf("拒绝后频道 = %q（err=%v），期望保持 -100777", effective.TelegramChannel.Value, err)
	}
	stored, err := pool.List(ctx)
	if err != nil || len(stored) != 1 || stored[0].BotID != 111 {
		t.Fatalf("拒绝后列表 = %+v（err=%v），期望仍是 111 号", stored, err)
	}

	// 探测失败：频道同样不能被先写进去。
	original := probeBots
	probeBots = func(context.Context, settings.ProbeOptions) settings.ProbeResult {
		return settings.ProbeResult{Telegram: settings.ProbeTarget{Status: settings.ProbeFailed, Detail: "Bot 不是频道管理员，无法发布"}}
	}
	t.Cleanup(func() { probeBots = original })
	body = fmt.Sprintf(`{"channel_id":"-100999","bots":[{"bot_id":111,"alias":"主力","token":%q,"priority":0}]}`, token)
	rec = env.do(t, http.MethodPut, "/api/settings/telegram", body, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("探测失败状态码 = %d，期望 400，响应: %s", rec.Code, rec.Body.String())
	}
	effective, err = resolver.Effective(ctx)
	if err != nil || effective.TelegramChannel.Value != "-100777" {
		t.Fatalf("探测失败后频道 = %q（err=%v），期望保持 -100777", effective.TelegramChannel.Value, err)
	}
}

// fakeTelegramByToken 让 getMe 回传令牌前缀里的真实编号，供轮换核对使用。
func fakeTelegramByToken(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			rest := strings.TrimPrefix(r.URL.Path, "/bot")
			id := rest
			if index := strings.Index(rest, ":"); index > 0 {
				id = rest[:index]
			}
			_, _ = w.Write([]byte(`{"ok":true,"result":{"id":` + id + `,"username":"sese_bot"}}`))
		case strings.HasSuffix(r.URL.Path, "/getChat"):
			_, _ = w.Write([]byte(`{"ok":true,"result":{"id":-100123,"type":"channel","title":"测试频道"}}`))
		case strings.HasSuffix(r.URL.Path, "/getChatMember"):
			_, _ = w.Write([]byte(`{"ok":true,"result":{"status":"administrator","can_post_messages":true}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"ok":false,"description":"not found"}`))
		}
	}))
	t.Cleanup(server.Close)
	return server
}
