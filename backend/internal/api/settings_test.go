package api

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/littleseven2003/SeSeGraph/backend/internal/settings"
)

// newSettingsEnv 装配带配置解析器的环境，可注入环境变量优先项。
func newSettingsEnv(t *testing.T, envValues settings.Env) (*testEnv, *settings.Resolver) {
	t.Helper()
	var resolver *settings.Resolver
	env := newTestEnvWith(t, testSetupToken, func(deps *Deps, handle *sql.DB) {
		resolver = settings.NewResolver(settings.NewStore(handle), envValues)
		deps.Settings = resolver
	})
	return env, resolver
}

// settingsBody 解码设置读取响应。
type settingsBody struct {
	PublicURL string `json:"public_url"`
	Telegram  struct {
		TokenSet      bool   `json:"token_set"`
		TokenSource   string `json:"token_source"`
		ChannelID     string `json:"channel_id"`
		ChannelSource string `json:"channel_source"`
		APIBase       string `json:"api_base"`
		APIBaseSource string `json:"api_base_source"`
	} `json:"telegram"`
	Telegraph struct {
		TokenSet      bool   `json:"token_set"`
		TokenSource   string `json:"token_source"`
		AuthorName    string `json:"author_name"`
		AuthorSource  string `json:"author_source"`
		APIBase       string `json:"api_base"`
		APIBaseSource string `json:"api_base_source"`
	} `json:"telegraph"`
	EnvManaged []string `json:"env_managed"`
}

func TestSettingsRequireAuth(t *testing.T) {
	env, _ := newSettingsEnv(t, settings.Env{})
	cases := []struct {
		method, path string
	}{
		{http.MethodGet, "/api/settings"},
		{http.MethodPut, "/api/settings"},
		{http.MethodPost, "/api/settings/test-connection"},
	}
	for _, tc := range cases {
		if rec := env.do(t, tc.method, tc.path, "", nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s 状态码 = %d，期望 401", tc.method, tc.path, rec.Code)
		}
	}
}

func TestSettingsReadMasksSecrets(t *testing.T) {
	env, resolver := newSettingsEnv(t, settings.Env{})
	ctx := context.Background()
	cookie := env.setup(t)

	if err := resolver.Update(ctx, settings.Patch{
		TelegramToken:   strPtr("123:stored-secret"),
		TelegramChannel: strPtr("-1001234567890"),
		TelegraphAuthor: strPtr("SeSe"),
	}); err != nil {
		t.Fatalf("写入配置失败: %v", err)
	}

	rec := env.do(t, http.MethodGet, "/api/settings", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，响应: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "stored-secret") {
		t.Fatal("响应回显了 Bot Token")
	}
	var body settingsBody
	if err := decodeJSON(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if !body.Telegram.TokenSet || body.Telegram.TokenSource != settings.SourceStored {
		t.Errorf("Telegram 令牌 = set %v source %s，期望已设置且来自数据库", body.Telegram.TokenSet, body.Telegram.TokenSource)
	}
	if body.Telegram.ChannelID != "-1001234567890" || body.Telegram.ChannelSource != settings.SourceStored {
		t.Errorf("频道 = %+v，期望数据库值", body.Telegram)
	}
	if body.Telegram.APIBaseSource != settings.SourceDefault || body.Telegram.APIBase == "" {
		t.Errorf("Telegram 地址 = %+v，期望默认值", body.Telegram)
	}
	if body.Telegraph.TokenSet {
		t.Error("未配置的 Telegraph 令牌不应报告已设置")
	}
	if body.Telegraph.AuthorName != "SeSe" {
		t.Errorf("作者名 = %q，期望 SeSe", body.Telegraph.AuthorName)
	}
	if len(body.EnvManaged) != 0 {
		t.Errorf("环境变量托管字段 = %v，期望为空", body.EnvManaged)
	}
}

func TestSettingsWriteUpdatesView(t *testing.T) {
	env, _ := newSettingsEnv(t, settings.Env{})
	cookie := env.setup(t)

	payload := `{"telegram":{"token":"123:new-secret","channel_id":"@sese_channel"},` +
		`"telegraph":{"author_name":"SeSe"}}`
	rec := env.do(t, http.MethodPut, "/api/settings", payload, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，响应: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "new-secret") {
		t.Fatal("保存响应回显了 Bot Token")
	}

	read := env.do(t, http.MethodGet, "/api/settings", "", cookie)
	var body settingsBody
	if err := decodeJSON(read.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if !body.Telegram.TokenSet || body.Telegram.ChannelID != "@sese_channel" {
		t.Errorf("Telegram = %+v，期望保存后的值", body.Telegram)
	}
	if body.Telegraph.AuthorName != "SeSe" {
		t.Errorf("Telegraph = %+v，期望保存后的作者名", body.Telegraph)
	}
	if body.Telegraph.APIBase != settings.DefaultTelegraphAPIBase || body.Telegraph.APIBaseSource != settings.SourceDefault {
		t.Errorf("Telegraph 地址 = %+v，期望保持默认值", body.Telegraph)
	}

	// 空串清除数据库值。
	rec = env.do(t, http.MethodPut, "/api/settings", `{"telegraph":{"author_name":""}}`, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("清除状态码 = %d，响应: %s", rec.Code, rec.Body.String())
	}
	read = env.do(t, http.MethodGet, "/api/settings", "", cookie)
	if err := decodeJSON(read.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if body.Telegraph.AuthorName != "" || body.Telegraph.AuthorSource != settings.SourceNone {
		t.Errorf("作者名 = %+v，期望已清空", body.Telegraph)
	}
}

func TestSettingsRejectsEnvManagedField(t *testing.T) {
	env, _ := newSettingsEnv(t, settings.Env{TelegramToken: "env-secret"})
	cookie := env.setup(t)

	read := env.do(t, http.MethodGet, "/api/settings", "", cookie)
	var body settingsBody
	if err := decodeJSON(read.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if body.Telegram.TokenSource != settings.SourceEnv {
		t.Errorf("令牌来源 = %q，期望 env", body.Telegram.TokenSource)
	}
	found := false
	for _, field := range body.EnvManaged {
		if field == "telegram.token" {
			found = true
		}
	}
	if !found {
		t.Errorf("env_managed = %v，期望包含 telegram.token", body.EnvManaged)
	}

	rec := env.do(t, http.MethodPut, "/api/settings",
		`{"telegram":{"token":"other-secret"}}`, cookie)
	if rec.Code != http.StatusConflict {
		t.Fatalf("状态码 = %d，期望 409，响应: %s", rec.Code, rec.Body.String())
	}
	var problem struct {
		Error  string   `json:"error"`
		Fields []string `json:"fields"`
	}
	if err := decodeJSON(rec.Body.Bytes(), &problem); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if len(problem.Fields) != 1 || problem.Fields[0] != "telegram.token" {
		t.Errorf("冲突字段 = %v，期望 telegram.token", problem.Fields)
	}
	if strings.Contains(rec.Body.String(), "other-secret") {
		t.Error("冲突响应回显了提交的令牌")
	}
}

func TestSettingsRejectsInvalidValues(t *testing.T) {
	env, _ := newSettingsEnv(t, settings.Env{})
	cookie := env.setup(t)

	cases := []string{
		`{"telegram":{"channel_id":"not a channel"}}`,
		`{"telegram":{"token":"bad token"}}`,
		`{"telegraph":{"author_name":"` + strings.Repeat("a", 101) + `"}}`,
	}
	for _, payload := range cases {
		rec := env.do(t, http.MethodPut, "/api/settings", payload, cookie)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("请求 %s 状态码 = %d，期望 400", payload, rec.Code)
		}
	}

	rec := env.do(t, http.MethodPut, "/api/settings", `{}`, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("空补丁状态码 = %d，期望 400", rec.Code)
	}

	// 上游地址不在可写字段内：提交后被忽略，不影响令牌发送目的地。
	rec = env.do(t, http.MethodPut, "/api/settings",
		`{"telegram":{"api_base":"http://127.0.0.1:1"},"telegraph":{"api_base":"http://127.0.0.1:1"}}`, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("仅提交地址状态码 = %d，期望 400", rec.Code)
	}
	read := env.do(t, http.MethodGet, "/api/settings", "", cookie)
	var view settingsBody
	if err := decodeJSON(read.Body.Bytes(), &view); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if view.Telegram.APIBase != settings.DefaultTelegramAPIBase || view.Telegraph.APIBase != settings.DefaultTelegraphAPIBase {
		t.Errorf("地址 = %+v / %+v，期望保持官方默认值", view.Telegram, view.Telegraph)
	}

	oversized := `{"telegram":{"token":"` + strings.Repeat("a", maxSettingsBodyBytes) + `"}}`
	rec = env.do(t, http.MethodPut, "/api/settings", oversized, cookie)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("超限请求体状态码 = %d，期望 413", rec.Code)
	}
}

// fakeTelegramAPI 提供只读的 Bot API 响应。
func fakeTelegramAPI(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			_, _ = w.Write([]byte(`{"ok":true,"result":{"id":42,"username":"sese_bot"}}`))
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

// fakeTelegraphAPI 提供只读账户信息响应。
func fakeTelegraphAPI(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/getAccountInfo" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"ok":false,"error":"NOT_FOUND"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"author_name":"SeSe"}}`))
	}))
	t.Cleanup(server.Close)
	return server
}

func TestConnectivityReportsBothTargets(t *testing.T) {
	tg := fakeTelegramAPI(t)
	tp := fakeTelegraphAPI(t)
	env, _ := newSettingsEnv(t, settings.Env{
		TelegramToken:    "123:env-secret",
		TelegramChannel:  "@sese_channel",
		TelegramAPIBase:  tg.URL,
		TelegraphToken:   "tp-secret",
		TelegraphAPIBase: tp.URL,
	})
	cookie := env.setup(t)

	rec := env.do(t, http.MethodPost, "/api/settings/test-connection", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，响应: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "env-secret") || strings.Contains(rec.Body.String(), "tp-secret") {
		t.Fatal("自检响应回显了令牌")
	}
	var result settings.ProbeResult
	if err := decodeJSON(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if result.CheckedAt == "" {
		t.Error("缺少检测时间")
	}
	if result.Telegram.Status != settings.ProbeOK || result.Telegraph.Status != settings.ProbeOK {
		t.Errorf("自检结果 = %+v，期望两项都 ok", result)
	}
}

func TestConnectivityUnconfigured(t *testing.T) {
	env, _ := newSettingsEnv(t, settings.Env{})
	cookie := env.setup(t)

	rec := env.do(t, http.MethodPost, "/api/settings/test-connection", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，响应: %s", rec.Code, rec.Body.String())
	}
	var result settings.ProbeResult
	if err := decodeJSON(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if result.Telegram.Status != settings.ProbeUnconfigured || result.Telegraph.Status != settings.ProbeUnconfigured {
		t.Errorf("自检结果 = %+v，期望两项都 unconfigured", result)
	}
}

// strPtr 返回指向给定字符串的指针。
func strPtr(value string) *string { return &value }
