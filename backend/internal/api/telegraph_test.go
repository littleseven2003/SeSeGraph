package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/littleseven2003/SeSeGraph/backend/internal/settings"
	"github.com/littleseven2003/SeSeGraph/backend/internal/telegraph"
)

// fakeTelegraph 模拟 createAccount 接口。
type fakeTelegraph struct {
	mu       sync.Mutex
	server   *httptest.Server
	status   int
	lastForm map[string]string
	calls    int
}

func newFakeTelegraph(t *testing.T) *fakeTelegraph {
	t.Helper()
	fake := &fakeTelegraph{status: http.StatusOK, lastForm: map[string]string{}}
	fake.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()

		fake.mu.Lock()
		fake.calls++
		for key := range r.PostForm {
			fake.lastForm[key] = r.PostForm.Get(key)
		}
		status := fake.status
		fake.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status != http.StatusOK {
			_, _ = w.Write([]byte(`{"ok":false,"error":"UPSTREAM_SECRET"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"result": map[string]any{
				"short_name":   "sese",
				"author_name":  "SeSe",
				"author_url":   "https://telegra.ph/SeSe",
				"access_token": "stored-token-value",
			},
		})
	}))
	t.Cleanup(fake.server.Close)
	return fake
}

func (f *fakeTelegraph) formValue(key string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastForm[key]
}

// telegraphEnv 构建带 Telegraph 账户管理的路由环境。
func telegraphEnv(t *testing.T, upstream string) (*testEnv, *settings.Store) {
	t.Helper()
	env := newTestEnvWith(t, testSetupToken, func(deps *Deps, handle *sql.DB) {
		deps.Telegraph = telegraph.NewManager(telegraph.Config{APIBase: upstream, AuthorName: "SeSe"}, settings.NewStore(nil))
	})
	return env, nil
}

func TestTelegraphStatusDefaultsToNotConfigured(t *testing.T) {
	env := newTestEnvWith(t, testSetupToken, func(deps *Deps, handle *sql.DB) {
		deps.Telegraph = telegraph.NewManager(telegraph.Config{APIBase: "http://127.0.0.1:1"}, nil)
	})
	cookie := env.setup(t)

	rec := env.do(t, http.MethodGet, "/api/telegraph/status", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", rec.Code)
	}
	var status struct {
		Configured bool   `json:"configured"`
		Source     string `json:"source"`
	}
	if err := decodeJSON(rec.Body.Bytes(), &status); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if status.Configured || status.Source != "none" {
		t.Errorf("初始状态 = %+v，期望未配置", status)
	}
}

func TestTelegraphCreateAccountPersistsTokenWithoutEcho(t *testing.T) {
	fake := newFakeTelegraph(t)
	env := newTestEnvWith(t, testSetupToken, func(deps *Deps, handle *sql.DB) {
		deps.Telegraph = telegraph.NewManager(telegraph.Config{APIBase: fake.server.URL}, settings.NewStore(handle))
	})
	cookie := env.setup(t)

	rec := env.do(t, http.MethodPost, "/api/settings/telegraph/create-account", `{"short_name":"sese","author_name":"SeSe"}`, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("状态码 = %d，期望 201，响应: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "stored-token-value") || strings.Contains(body, "access_token") {
		t.Fatalf("响应不应回显访问令牌: %s", body)
	}
	if !strings.Contains(body, "SeSe") {
		t.Errorf("响应应包含作者名: %s", body)
	}
	if fake.formValue("short_name") != "sese" {
		t.Errorf("上游收到 short_name = %q", fake.formValue("short_name"))
	}

	status := env.do(t, http.MethodGet, "/api/telegraph/status", "", cookie)
	if status.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", status.Code)
	}
	var parsed struct {
		Configured bool   `json:"configured"`
		Source     string `json:"source"`
		AuthorName string `json:"author_name"`
	}
	if err := decodeJSON(status.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if !parsed.Configured || parsed.Source != "stored" || parsed.AuthorName != "SeSe" {
		t.Errorf("状态 = %+v，期望已配置且来自数据库", parsed)
	}
	if strings.Contains(status.Body.String(), "stored-token-value") {
		t.Error("状态接口不应回显访问令牌")
	}

	// 日志同样不应出现令牌。
	if strings.Contains(env.logs.String(), "stored-token-value") {
		t.Errorf("日志泄露访问令牌: %s", env.logs.String())
	}
}

func TestTelegraphCreateAccountRequiresSession(t *testing.T) {
	fake := newFakeTelegraph(t)
	env := newTestEnvWith(t, testSetupToken, func(deps *Deps, handle *sql.DB) {
		deps.Telegraph = telegraph.NewManager(telegraph.Config{APIBase: fake.server.URL}, settings.NewStore(handle))
	})

	rec := env.do(t, http.MethodPost, "/api/settings/telegraph/create-account", `{"short_name":"sese"}`, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("未登录状态码 = %d，期望 401", rec.Code)
	}
	if fake.formValue("short_name") != "" {
		t.Error("未登录请求不应访问上游")
	}
}

func TestTelegraphCreateAccountMapsUpstreamFailure(t *testing.T) {
	fake := newFakeTelegraph(t)
	fake.status = http.StatusInternalServerError
	env := newTestEnvWith(t, testSetupToken, func(deps *Deps, handle *sql.DB) {
		deps.Telegraph = telegraph.NewManager(telegraph.Config{APIBase: fake.server.URL}, settings.NewStore(handle))
	})
	cookie := env.setup(t)

	rec := env.do(t, http.MethodPost, "/api/settings/telegraph/create-account", `{"short_name":"sese"}`, cookie)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("状态码 = %d，期望 502，响应: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "UPSTREAM_SECRET") || strings.Contains(rec.Body.String(), fake.server.URL) {
		t.Errorf("响应泄露了上游细节: %s", rec.Body.String())
	}
}

func TestTelegraphEnvTokenTakesPriority(t *testing.T) {
	fake := newFakeTelegraph(t)
	env := newTestEnvWith(t, testSetupToken, func(deps *Deps, handle *sql.DB) {
		deps.Telegraph = telegraph.NewManager(telegraph.Config{
			AccessToken: "env-token",
			AuthorName:  "环境作者",
			APIBase:     fake.server.URL,
		}, settings.NewStore(handle))
	})
	cookie := env.setup(t)

	status := env.do(t, http.MethodGet, "/api/telegraph/status", "", cookie)
	var parsed struct {
		Configured bool   `json:"configured"`
		Source     string `json:"source"`
		AuthorName string `json:"author_name"`
	}
	if err := decodeJSON(status.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if !parsed.Configured || parsed.Source != "env" || parsed.AuthorName != "环境作者" {
		t.Errorf("状态 = %+v，期望来自环境变量", parsed)
	}

	// 已有环境变量令牌时不再申请新账户。
	rec := env.do(t, http.MethodPost, "/api/settings/telegraph/create-account", `{"short_name":"x"}`, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("状态码 = %d，期望 201", rec.Code)
	}
	if fake.formValue("short_name") != "" {
		t.Error("环境变量令牌已存在时不应调用 createAccount")
	}
}
