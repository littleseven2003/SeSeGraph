package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/littleseven2003/SeSeGraph/backend/internal/provider"
	"github.com/littleseven2003/SeSeGraph/backend/internal/settings"
	"github.com/littleseven2003/SeSeGraph/backend/internal/storage"
	"github.com/littleseven2003/SeSeGraph/backend/internal/traffic"
)

// newSettingsEnv 装配带配置解析器的环境，可注入环境变量优先项。
func newSettingsEnv(t *testing.T, envValues settings.Env) (*testEnv, *settings.Resolver) {
	t.Helper()
	env, resolver, _ := newSettingsEnvWithReload(t, envValues, nil)
	return env, resolver
}

// newSettingsEnvWithReload 允许注入保存后的热更新回调，返回可观测的调用次数。
func newSettingsEnvWithReload(t *testing.T, envValues settings.Env, reload func(context.Context) error) (*testEnv, *settings.Resolver, *int) {
	t.Helper()
	var resolver *settings.Resolver
	calls := 0
	env := newTestEnvWith(t, testSetupToken, func(deps *Deps, handle *sql.DB) {
		resolver = settings.NewResolver(settings.NewStore(handle), envValues)
		deps.Settings = resolver
		if reload != nil {
			deps.Reload = func(ctx context.Context) error {
				calls++
				return reload(ctx)
			}
		}
	})
	return env, resolver, &calls
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

// TestSettingsMasksCustomEndpoints 确认自定义上游地址（可能含反代凭据）不会回显，
// 服务端仍按生效地址工作。
func TestSettingsMasksCustomEndpoints(t *testing.T) {
	const custom = "http://svc-user:svc-pass-7788@127.0.0.1:18085"
	env, resolver := newSettingsEnv(t, settings.Env{
		TelegramAPIBase:  custom,
		TelegraphAPIBase: custom,
	})
	cookie := env.setup(t)

	effective, err := resolver.Effective(context.Background())
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if effective.TelegramAPIBase.Value != custom || effective.TelegraphAPIBase.Value != custom {
		t.Fatal("服务端生效地址应保留完整取值")
	}

	read := env.do(t, http.MethodGet, "/api/settings", "", cookie)
	if read.Code != http.StatusOK {
		t.Fatalf("读取状态码 = %d", read.Code)
	}
	for _, secret := range []string{"svc-pass-7788", "svc-user", "127.0.0.1:18085"} {
		if strings.Contains(read.Body.String(), secret) {
			t.Fatalf("读取响应回显了自定义地址片段 %q", secret)
		}
	}
	var view settingsBody
	if err := decodeJSON(read.Body.Bytes(), &view); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if view.Telegram.APIBase != "" || view.Telegram.APIBaseSource != settings.SourceEnv {
		t.Errorf("Telegram 地址视图 = %+v，期望只报来源", view.Telegram)
	}
	if view.Telegraph.APIBase != "" || view.Telegraph.APIBaseSource != settings.SourceEnv {
		t.Errorf("Telegraph 地址视图 = %+v，期望只报来源", view.Telegraph)
	}

	saved := env.do(t, http.MethodPut, "/api/settings", `{"telegraph":{"author_name":"SeSe"}}`, cookie)
	if saved.Code != http.StatusOK {
		t.Fatalf("保存状态码 = %d，响应: %s", saved.Code, saved.Body.String())
	}
	for _, secret := range []string{"svc-pass-7788", "svc-user", "127.0.0.1:18085"} {
		if strings.Contains(saved.Body.String(), secret) {
			t.Fatalf("保存响应回显了自定义地址片段 %q", secret)
		}
	}
}

// TestSettingsProxyDefaultsAndMasking 验证代理配置的默认值、按服务开关，
// 以及自定义地址（可能带账密）在读写响应里都不回显。
func TestSettingsProxyDefaultsAndMasking(t *testing.T) {
	env, resolver := newSettingsEnv(t, settings.Env{})
	cookie := env.setup(t)

	rec := env.do(t, http.MethodGet, "/api/settings", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("读取状态码 = %d", rec.Code)
	}
	var view struct {
		Proxy struct {
			Mode             string `json:"mode"`
			ModeSource       string `json:"mode_source"`
			URLSet           bool   `json:"url_set"`
			URLSource        string `json:"url_source"`
			TelegramEnabled  bool   `json:"telegram_enabled"`
			TelegraphEnabled bool   `json:"telegraph_enabled"`
		} `json:"proxy"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if view.Proxy.Mode != "direct" || view.Proxy.ModeSource != "default" {
		t.Errorf("默认代理 = %+v，期望 direct/default", view.Proxy)
	}
	if view.Proxy.URLSet || view.Proxy.URLSource != "none" || view.Proxy.TelegramEnabled {
		t.Errorf("默认代理开关 = %+v，期望未配置", view.Proxy)
	}

	secretURL := "socks5://ops:222:test-two@10.8.0.9:1080"
	body := `{"proxy":{"mode":"custom","url":"` + secretURL + `","telegram_enabled":true,"telegraph_enabled":false}}`
	rec = env.do(t, http.MethodPut, "/api/settings", body, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("保存状态码 = %d，响应: %s", rec.Code, rec.Body.String())
	}
	requireNoSecret(t, rec.Body.String(), secretURL)

	rec = env.do(t, http.MethodGet, "/api/settings", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("读取状态码 = %d", rec.Code)
	}
	requireNoSecret(t, rec.Body.String(), secretURL)
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if view.Proxy.Mode != "custom" || view.Proxy.ModeSource != "stored" {
		t.Errorf("代理模式 = %+v，期望 custom/stored", view.Proxy)
	}
	if !view.Proxy.URLSet || view.Proxy.URLSource != "stored" {
		t.Errorf("代理地址状态 = %+v，期望已设置且来自数据库", view.Proxy)
	}
	if !view.Proxy.TelegramEnabled || view.Proxy.TelegraphEnabled {
		t.Errorf("代理开关 = %+v，期望只对 Telegram 启用", view.Proxy)
	}

	// 服务端仍持有完整地址，供传输层使用。
	effective, err := resolver.Effective(context.Background())
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if effective.ProxyURL.Value != secretURL {
		t.Errorf("服务端代理地址 = %q，期望保存的完整取值", effective.ProxyURL.Value)
	}
}

// TestSettingsProxyFromEnvironment 验证环境变量托管的代理配置只读：
// 响应列出托管字段，修改返回 409。
func TestSettingsProxyFromEnvironment(t *testing.T) {
	env, _ := newSettingsEnv(t, settings.Env{
		ProxyMode:      "system",
		ProxyTelegram:  "true",
		ProxyTelegraph: "true",
	})
	cookie := env.setup(t)

	rec := env.do(t, http.MethodGet, "/api/settings", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("读取状态码 = %d", rec.Code)
	}
	var view struct {
		Proxy struct {
			Mode             string `json:"mode"`
			ModeSource       string `json:"mode_source"`
			TelegramEnabled  bool   `json:"telegram_enabled"`
			TelegraphEnabled bool   `json:"telegraph_enabled"`
		} `json:"proxy"`
		EnvManaged []string `json:"env_managed"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if view.Proxy.Mode != "system" || view.Proxy.ModeSource != "env" {
		t.Errorf("代理模式 = %+v，期望 system/env", view.Proxy)
	}
	if !view.Proxy.TelegramEnabled || !view.Proxy.TelegraphEnabled {
		t.Errorf("开关 = %+v，期望两项都启用", view.Proxy)
	}
	managed := strings.Join(view.EnvManaged, ",")
	for _, field := range []string{"proxy.mode", "proxy.telegram", "proxy.telegraph"} {
		if !strings.Contains(managed, field) {
			t.Errorf("托管字段应包含 %s，实际: %s", field, managed)
		}
	}

	rec = env.do(t, http.MethodPut, "/api/settings", `{"proxy":{"mode":"direct"}}`, cookie)
	if rec.Code != http.StatusConflict {
		t.Fatalf("修改托管字段状态码 = %d，期望 409，响应: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "环境变量") {
		t.Errorf("响应 = %s，期望说明由环境变量提供", rec.Body.String())
	}
}

// TestSettingsRejectsInvalidProxy 验证非法代理配置返回字段级错误且不写入。
func TestSettingsRejectsInvalidProxy(t *testing.T) {
	env, resolver := newSettingsEnv(t, settings.Env{})
	cookie := env.setup(t)

	cases := []struct {
		body  string
		field string
	}{
		{body: `{"proxy":{"mode":"custom"}}`, field: "proxy.url"},
		{body: `{"proxy":{"mode":"custom","url":"ftp://127.0.0.1:21"}}`, field: "proxy.url"},
		{body: `{"proxy":{"mode":"proxy"}}`, field: "proxy.mode"},
	}
	for _, test := range cases {
		rec := env.do(t, http.MethodPut, "/api/settings", test.body, cookie)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("状态码 = %d，期望 400，响应: %s", rec.Code, rec.Body.String())
		}
		var problem struct {
			Fields []string `json:"fields"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
			t.Fatalf("解析响应失败: %v", err)
		}
		if len(problem.Fields) != 1 || problem.Fields[0] != test.field {
			t.Errorf("字段 = %v，期望 %s", problem.Fields, test.field)
		}
	}

	effective, err := resolver.Effective(context.Background())
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if effective.ProxyMode.Value != "direct" || effective.ProxyURL.Set() {
		t.Errorf("非法配置不应落库: %+v", effective.ProxyConfig())
	}
}

// requireNoSecret 确认响应体里没有出现凭据或地址片段。
func requireNoSecret(t *testing.T, body, secret string) {
	t.Helper()
	fragments := []string{secret, "10.8.0.9", "ops", "socks5"}
	for _, fragment := range fragments {
		if strings.Contains(body, fragment) {
			t.Fatalf("响应回显了代理地址片段 %q: %s", fragment, body)
		}
	}
}

// TestSettingsSaveTriggersReload 确认保存成功后触发热更新，校验失败时不触发。
func TestSettingsSaveTriggersReload(t *testing.T) {
	env, _, calls := newSettingsEnvWithReload(t, settings.Env{}, func(context.Context) error { return nil })
	cookie := env.setup(t)

	if rec := env.do(t, http.MethodPut, "/api/settings", `{"telegraph":{"author_name":"SeSe"}}`, cookie); rec.Code != http.StatusOK {
		t.Fatalf("保存状态码 = %d，响应: %s", rec.Code, rec.Body.String())
	}
	if *calls != 1 {
		t.Errorf("热更新调用 = %d，期望 1", *calls)
	}

	if rec := env.do(t, http.MethodPut, "/api/settings", `{"telegram":{"channel_id":"bad channel"}}`, cookie); rec.Code != http.StatusBadRequest {
		t.Fatalf("非法取值状态码 = %d，期望 400", rec.Code)
	}
	if *calls != 1 {
		t.Errorf("校验失败不应触发热更新，调用次数 = %d", *calls)
	}
}

// TestSettingsReloadFailureKeepsOldProvider 确认热更新失败时返回 500，
// 旧提供方继续服务图片。
func TestSettingsReloadFailureKeepsOldProvider(t *testing.T) {
	streams := &fakeStreamProvider{}
	env := newTestEnvWith(t, testSetupToken, func(deps *Deps, handle *sql.DB) {
		deps.Settings = settings.NewResolver(settings.NewStore(handle), settings.Env{})
		deps.Streams = streams
		deps.Reload = func(context.Context) error { return errors.New("切换失败") }
	})
	cookie := env.setup(t)
	postID := publishedPost(t, env)
	code := shortCodeOf(t, env, postID)

	rec := env.do(t, http.MethodPut, "/api/settings", `{"telegraph":{"author_name":"SeSe"}}`, cookie)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("状态码 = %d，期望 500，响应: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "配置已保存") {
		t.Errorf("响应 = %s，期望说明已保存但生效失败", rec.Body.String())
	}

	// 旧提供方仍在服务。
	before := streams.callCount()
	if got := env.do(t, http.MethodGet, "/i/"+code, "", cookie); got.Code != http.StatusOK {
		t.Errorf("旧提供方状态码 = %d，期望 200", got.Code)
	}
	if streams.callCount() != before+1 {
		t.Errorf("旧提供方调用 = %d，期望 %d", streams.callCount(), before+1)
	}
}

// TestSettingsRejectsUnverifiableProviderBeforeSaving 确认凭据校验失败时返回 409，
// 数据库保持原值，未写入新的 Bot。
func TestSettingsRejectsUnverifiableProviderBeforeSaving(t *testing.T) {
	env, resolver := newSettingsEnv(t, settings.Env{})
	env.router = NewRouter(Deps{
		Version: "test", Commit: "abc", Auth: env.auth, Sessions: env.sessions,
		SetupToken: env.setupToken, Logger: env.logger, DataDir: env.dataDir, Posts: env.posts,
		Settings: resolver,
		PrepareProvider: func(context.Context, settings.Effective) (*provider.Candidate, error) {
			return nil, provider.ErrVerify
		},
	})
	cookie := env.setup(t)

	rec := env.do(t, http.MethodPut, "/api/settings", `{"telegram":{"token":"111:token-b"}}`, cookie)
	if rec.Code != http.StatusConflict {
		t.Fatalf("状态码 = %d，期望 409，响应: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "无法验证新的 Bot 凭据") {
		t.Errorf("响应 = %s，期望说明凭据无法验证", rec.Body.String())
	}
	effective, err := resolver.Effective(context.Background())
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if effective.TelegramToken.Set() {
		t.Errorf("令牌 = %+v，校验失败时不应写入", effective.TelegramToken)
	}
}

// TestSettingsBotMismatchKeepsImages 模拟图库已有图片时换成另一个 Bot：
// 保存被拒绝，原短码仍可访问。
func TestSettingsBotMismatchKeepsImages(t *testing.T) {
	streams := &fakeStreamProvider{}
	var resolver *settings.Resolver
	env := newTestEnvWith(t, testSetupToken, func(deps *Deps, handle *sql.DB) {
		resolver = settings.NewResolver(settings.NewStore(handle), settings.Env{})
		deps.Settings = resolver
		deps.Streams = streams
		deps.PrepareProvider = func(context.Context, settings.Effective) (*provider.Candidate, error) {
			return nil, provider.ErrBotMismatch
		}
	})
	cookie := env.setup(t)
	postID := publishedPost(t, env)
	code := shortCodeOf(t, env, postID)

	if rec := env.do(t, http.MethodGet, "/i/"+code, "", cookie); rec.Code != http.StatusOK {
		t.Fatalf("切换前短码状态 = %d，期望 200", rec.Code)
	}
	rec := env.do(t, http.MethodPut, "/api/settings", `{"telegram":{"token":"111:token-b"}}`, cookie)
	if rec.Code != http.StatusConflict {
		t.Fatalf("切换状态码 = %d，期望 409，响应: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "已有其他 Bot") {
		t.Errorf("响应 = %s，期望说明图片归属冲突", rec.Body.String())
	}

	effective, err := resolver.Effective(context.Background())
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if effective.TelegramToken.Set() {
		t.Errorf("令牌 = %+v，冲突时不应写入", effective.TelegramToken)
	}
	if rec := env.do(t, http.MethodGet, "/i/"+code, "", cookie); rec.Code != http.StatusOK {
		t.Errorf("拒绝切换后短码状态 = %d，期望仍为 200", rec.Code)
	}
}

// TestSettingsProxyChangeRebuildsTelegramTransport 验证影响 Telegram 的代理改动
// 也会重新校验并重建图床传输层，而只影响 Telegraph 的改动不会。
func TestSettingsProxyChangeRebuildsTelegramTransport(t *testing.T) {
	prepare := func(t *testing.T, body string) (int, int) {
		t.Helper()
		prepares := 0
		var resolver *settings.Resolver
		env := newTestEnvWith(t, testSetupToken, func(deps *Deps, handle *sql.DB) {
			resolver = settings.NewResolver(settings.NewStore(handle), settings.Env{})
			deps.Settings = resolver
			deps.PrepareProvider = func(context.Context, settings.Effective) (*provider.Candidate, error) {
				prepares++
				return nil, provider.ErrVerify
			}
		})
		cookie := env.setup(t)
		rec := env.do(t, http.MethodPut, "/api/settings", body, cookie)
		return rec.Code, prepares
	}

	// 对 Telegram 启用代理：即使令牌没变也要重建传输层。
	code, calls := prepare(t, `{"proxy":{"mode":"custom","url":"http://127.0.0.1:7890","telegram_enabled":true}}`)
	if code != http.StatusConflict || calls != 1 {
		t.Errorf("Telegram 代理改动：状态码 = %d，重建次数 = %d，期望 409 与 1", code, calls)
	}

	// 只对 Telegraph 启用代理：图床不受影响，不应重复校验 Bot。
	code, calls = prepare(t, `{"proxy":{"mode":"custom","url":"http://127.0.0.1:7890","telegraph_enabled":true}}`)
	if code != http.StatusOK || calls != 0 {
		t.Errorf("Telegraph 代理改动：状态码 = %d，重建次数 = %d，期望 200 与 0", code, calls)
	}
}

// TestSettingsSavesApplyInOrder 确认并发保存串行生效：先落库的请求先热切换，
// 结束后读回与运行时看到同一版本。
func TestSettingsSavesApplyInOrder(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var observed []string
	var observedMu sync.Mutex
	first := true

	var resolver *settings.Resolver
	env := newTestEnvWith(t, testSetupToken, func(deps *Deps, handle *sql.DB) {
		resolver = settings.NewResolver(settings.NewStore(handle), settings.Env{})
		deps.Settings = resolver
		deps.SettingsLock = &sync.Mutex{}
		deps.Reload = func(ctx context.Context) error {
			effective, err := resolver.Effective(ctx)
			if err != nil {
				return err
			}
			observedMu.Lock()
			observed = append(observed, effective.TelegramToken.Value)
			blocked := first
			first = false
			observedMu.Unlock()
			if blocked {
				close(started)
				<-release
			}
			return nil
		}
	})
	cookie := env.setup(t)

	done := make(chan int, 2)
	save := func(token string) {
		rec := env.do(t, http.MethodPut, "/api/settings", `{"telegram":{"token":"`+token+`"}}`, cookie)
		done <- rec.Code
	}
	go save("111:token-a")
	<-started
	go save("111:token-b")
	time.Sleep(100 * time.Millisecond)
	observedMu.Lock()
	pending := len(observed)
	observedMu.Unlock()
	if pending != 1 {
		t.Errorf("第一个请求热切换期间观察到 %d 次切换，期望后一个请求仍在等待", pending)
	}
	close(release)

	for i := 0; i < 2; i++ {
		if code := <-done; code != http.StatusOK {
			t.Fatalf("保存状态码 = %d，期望 200", code)
		}
	}

	observedMu.Lock()
	defer observedMu.Unlock()
	if len(observed) != 2 || observed[0] != "111:token-a" || observed[1] != "111:token-b" {
		t.Errorf("热切换顺序 = %v，期望按提交顺序生效", observed)
	}
	effective, err := resolver.Effective(context.Background())
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if effective.TelegramToken.Value != "111:token-b" {
		t.Errorf("最终令牌 = %q，期望最后一次提交", effective.TelegramToken.Value)
	}
}

// TestSettingsReportsBusyWhenUploadInFlight 验证上传任务在途时换 Bot 返回 409，
// 既不写入配置，也不激活候选提供方。
func TestSettingsReportsBusyWhenUploadInFlight(t *testing.T) {
	env, resolver := newSettingsEnv(t, settings.Env{})
	commits := 0
	env.router = NewRouter(Deps{
		Version: "test", Commit: "abc", Auth: env.auth, Sessions: env.sessions,
		SetupToken: env.setupToken, Logger: env.logger, DataDir: env.dataDir, Posts: env.posts,
		Settings: resolver,
		PrepareProvider: func(context.Context, settings.Effective) (*provider.Candidate, error) {
			return nil, provider.ErrUploadBusy
		},
		CommitProvider: func(context.Context, *provider.Candidate) { commits++ },
	})
	cookie := env.setup(t)

	rec := env.do(t, http.MethodPut, "/api/settings", `{"telegram":{"token":"222:test-two"}}`, cookie)
	if rec.Code != http.StatusConflict {
		t.Fatalf("状态码 = %d，期望 409，响应: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "有图片正在上传") {
		t.Errorf("响应 = %s，期望说明有上传任务在途", rec.Body.String())
	}
	if commits != 0 {
		t.Errorf("提交次数 = %d，被拒绝时不应切换运行时", commits)
	}
	effective, err := resolver.Effective(context.Background())
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if effective.TelegramToken.Set() {
		t.Errorf("令牌 = %+v，被拒绝时不应写入", effective.TelegramToken)
	}
}

// TestSettingsPersistFailureKeepsRuntimeAndDB 验证落库失败时配置与 Bot 归属都不写入，
// 运行时也保持原来的提供方。
func TestSettingsPersistFailureKeepsRuntimeAndDB(t *testing.T) {
	// 上游按令牌返回 Bot 编号：旧令牌属于 111，新令牌属于 222。
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := 222
		if strings.Contains(r.URL.Path, "111:test-one") {
			id = 111
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"ok":true,"result":{"id":%d}}`, id)
	}))
	t.Cleanup(upstream.Close)

	var resolver *settings.Resolver
	var guard *provider.Guard
	var store *settings.Store
	commits := 0
	env := newTestEnvWith(t, testSetupToken, func(deps *Deps, handle *sql.DB) {
		store = settings.NewStore(handle)
		resolver = settings.NewResolver(store, settings.Env{})
		guard = provider.NewGuard(storage.NewSwitcher(), store,
			func(context.Context) (int64, error) { return 0, nil }, storage.NewUploadGate())
		deps.Settings = resolver
		deps.SettingsLock = &sync.Mutex{}
		deps.PrepareProvider = func(ctx context.Context, expected settings.Effective) (*provider.Candidate, error) {
			candidate, err := storage.NewTelegram(storage.TelegramConfig{
				Token:   expected.TelegramToken.Value,
				ChatID:  expected.TelegramChannel.Value,
				APIBase: upstream.URL,
			})
			if err != nil {
				return nil, err
			}
			return guard.Prepare(ctx, candidate)
		}
		deps.CommitProvider = func(ctx context.Context, candidate *provider.Candidate) {
			commits++
			guard.Commit(ctx, candidate)
		}

		// 先激活旧配置，再让后续写入令牌与归属都失败。
		token, channel := "111:test-one", "-1001234567890"
		ctx := context.Background()
		if err := resolver.Update(ctx, settings.Patch{TelegramToken: &token, TelegramChannel: &channel}); err != nil {
			t.Fatalf("准备配置失败: %v", err)
		}
		if err := guard.Activate(ctx, mustCandidate(t, upstream.URL, token, channel)); err != nil {
			t.Fatalf("准备激活失败: %v", err)
		}
		for _, stmt := range []string{
			`CREATE TRIGGER fail_token_insert BEFORE INSERT ON settings
			 WHEN NEW.key = 'telegram.bot_token' BEGIN SELECT RAISE(ABORT, 'fail'); END`,
			`CREATE TRIGGER fail_token_update BEFORE UPDATE ON settings
			 WHEN NEW.key = 'telegram.bot_token' BEGIN SELECT RAISE(ABORT, 'fail'); END`,
			`CREATE TRIGGER fail_bot_insert BEFORE INSERT ON settings
			 WHEN NEW.key = 'telegram.bot_id' BEGIN SELECT RAISE(ABORT, 'fail'); END`,
			`CREATE TRIGGER fail_bot_update BEFORE UPDATE ON settings
			 WHEN NEW.key = 'telegram.bot_id' BEGIN SELECT RAISE(ABORT, 'fail'); END`,
		} {
			if _, err := handle.ExecContext(ctx, stmt); err != nil {
				t.Fatalf("创建触发器失败: %v", err)
			}
		}
	})
	cookie := env.setup(t)

	rec := env.do(t, http.MethodPut, "/api/settings", `{"telegram":{"token":"222:test-two"}}`, cookie)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("状态码 = %d，期望 500，响应: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "保存配置失败") {
		t.Errorf("响应 = %s，期望说明保存失败", rec.Body.String())
	}
	if commits != 0 {
		t.Errorf("提交次数 = %d，落库失败时不应切换运行时", commits)
	}

	effective, err := resolver.Effective(context.Background())
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if effective.TelegramToken.Value != "111:test-one" {
		t.Errorf("令牌 = %q，期望保留旧值", effective.TelegramToken.Value)
	}
	stored, err := store.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("快照失败: %v", err)
	}
	if stored[settings.KeyTelegramBotID] != "111" {
		t.Errorf("Bot 归属 = %q，期望保留旧值 111", stored[settings.KeyTelegramBotID])
	}
	if guard.BotID() != 111 {
		t.Errorf("运行时 Bot 编号 = %d，期望保持 111", guard.BotID())
	}
}

// mustCandidate 按配置创建提供方，供测试准备阶段使用。
func mustCandidate(t *testing.T, apiBase, token, channel string) *storage.Telegram {
	t.Helper()
	candidate, err := storage.NewTelegram(storage.TelegramConfig{Token: token, ChatID: channel, APIBase: apiBase})
	if err != nil {
		t.Fatalf("创建提供方失败: %v", err)
	}
	return candidate
}

// TestTrafficStatsHandler 验证流量统计接口返回当月数据与阈值。
func TestTrafficStatsHandler(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)

	meter := traffic.New(nil, traffic.Options{AlertBytes: 3 * traffic.GB, LimitBytes: 5 * traffic.GB})
	meter.Add(context.Background(), 2*traffic.GB)
	env.router = NewRouter(Deps{
		Version: "test", Commit: "abc", Auth: env.auth, Sessions: env.sessions,
		SetupToken: env.setupToken, Logger: env.logger, DataDir: env.dataDir, Posts: env.posts,
		Traffic: meter,
	})

	rec := env.do(t, http.MethodGet, "/api/stats/traffic", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", rec.Code)
	}
	var body struct {
		Month     string `json:"month"`
		BytesOut  int64  `json:"bytes_out"`
		Requests  int64  `json:"request_count"`
		AlertByte int64  `json:"alert_bytes"`
		LimitByte int64  `json:"limit_bytes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if body.Month == "" || body.BytesOut != 2*traffic.GB || body.Requests != 1 {
		t.Errorf("统计 = %+v，期望当月 2GB / 1 次", body)
	}
	if body.AlertByte != 3*traffic.GB || body.LimitByte != 5*traffic.GB {
		t.Errorf("阈值 = %d / %d，期望 3GB / 5GB", body.AlertByte, body.LimitByte)
	}
}

// TestSettingsMetaUpdate 验证说明文件名与解析方式的读写与回落。
func TestSettingsMetaUpdate(t *testing.T) {
	env := newTestEnvWith(t, testSetupToken, func(deps *Deps, handle *sql.DB) {
		deps.Settings = settings.NewResolver(settings.NewStore(handle), settings.Env{})
	})
	cookie := env.setup(t)

	rec := env.do(t, http.MethodGet, "/api/settings", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("读取设置状态 = %d", rec.Code)
	}
	var view map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	meta := view["meta"].(map[string]any)
	if meta["parse_mode"] != settings.DefaultMetaParseMode {
		t.Errorf("默认解析方式 = %v，期望 %q", meta["parse_mode"], settings.DefaultMetaParseMode)
	}
	filenames := meta["filenames"].([]any)
	if len(filenames) != 3 || filenames[0] != "info.txt" {
		t.Errorf("默认说明文件名 = %v", filenames)
	}
	upload := view["upload"].(map[string]any)
	if upload["default_mode"] != settings.DefaultUploadMode {
		t.Errorf("默认画质 = %v，期望 photo", upload["default_mode"])
	}

	body := `{"meta":{"filenames":["Info.TXT","notes.md"],"parse_mode":"key-value"}}`
	rec = env.do(t, http.MethodPut, "/api/settings", body, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("保存状态 = %d，响应: %s", rec.Code, rec.Body.String())
	}
	_, effective := readEffectiveSettings(t, env, cookie)
	if effective.MetaFilenames.Value != "info.txt,notes.md" || effective.MetaParseMode.Value != "key-value" {
		t.Errorf("保存后配置 = %q / %q", effective.MetaFilenames.Value, effective.MetaParseMode.Value)
	}

	// 空值清除，回落默认。
	rec = env.do(t, http.MethodPut, "/api/settings", `{"meta":{"filenames":[],"parse_mode":""}}`, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("清除状态 = %d", rec.Code)
	}
	_, effective = readEffectiveSettings(t, env, cookie)
	if effective.MetaFilenames.Value != settings.DefaultMetaFilenames ||
		effective.MetaParseMode.Value != settings.DefaultMetaParseMode {
		t.Errorf("清除后配置 = %q / %q", effective.MetaFilenames.Value, effective.MetaParseMode.Value)
	}

	// 非法解析方式整体拒绝。
	rec = env.do(t, http.MethodPut, "/api/settings", `{"meta":{"parse_mode":"regex"}}`, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("非法解析方式状态 = %d，期望 400", rec.Code)
	}
}

// readEffectiveSettings 读取设置视图并返回解析后的生效配置。
func readEffectiveSettings(t *testing.T, env *testEnv, cookie *http.Cookie) (map[string]any, settings.Effective) {
	t.Helper()
	rec := env.do(t, http.MethodGet, "/api/settings", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("读取设置状态 = %d", rec.Code)
	}
	var view map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	effective, err := settings.NewResolver(settings.NewStore(env.handle), settings.Env{}).Effective(context.Background())
	if err != nil {
		t.Fatalf("解析生效配置失败: %v", err)
	}
	return view, effective
}
