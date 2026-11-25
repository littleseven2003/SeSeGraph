package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
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
