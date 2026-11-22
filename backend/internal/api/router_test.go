package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/littleseven2003/SeSeGraph/backend/internal/auth"
	"github.com/littleseven2003/SeSeGraph/backend/internal/db"
	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
)

func init() {
	gin.SetMode(gin.TestMode)
}

const testSetupToken = "setup-once-token"

type testEnv struct {
	router     *gin.Engine
	auth       *auth.Service
	handle     *sql.DB
	logs       *bytes.Buffer
	logger     *log.Logger
	sessions   *auth.Sessions
	setupToken string
	dataDir    string
	posts      *posts.Repository
}

func newTestEnv(t *testing.T, setupToken string) *testEnv {
	t.Helper()
	return newTestEnvWith(t, setupToken, nil)
}

// newTestEnvWith 允许在默认依赖上做额外注入，供新增模块的集成测试使用。
func newTestEnvWith(t *testing.T, setupToken string, mutate func(*Deps, *sql.DB)) *testEnv {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sesegraph.db")
	handle, err := db.Open(path)
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	if err := db.Migrate(context.Background(), handle); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}

	buf := &bytes.Buffer{}
	logger := log.New(buf, "", 0)
	service := auth.NewService(handle)
	sessions := auth.NewSessions([]byte("0123456789abcdef0123456789abcdef"), time.Hour, false)

	dataDir := t.TempDir()
	repository := posts.NewRepository(handle)

	deps := Deps{
		Version:    "v0.1.0-test",
		Commit:     "abc1234",
		Auth:       service,
		Sessions:   sessions,
		SetupToken: setupToken,
		Logger:     logger,
		DataDir:    dataDir,
		Posts:      repository,
	}
	if mutate != nil {
		mutate(&deps, handle)
	}
	router := NewRouter(deps)
	return &testEnv{
		router:     router,
		auth:       service,
		handle:     handle,
		logs:       buf,
		logger:     logger,
		sessions:   sessions,
		setupToken: setupToken,
		dataDir:    dataDir,
		posts:      repository,
	}
}

func (e *testEnv) do(t *testing.T, method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	return e.doWithToken(t, method, path, body, e.setupToken, cookie)
}

func (e *testEnv) doWithToken(t *testing.T, method, path, body, token string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:54321"
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set(setupTokenHeader, token)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

func (e *testEnv) setup(t *testing.T) *http.Cookie {
	t.Helper()
	rec := e.do(t, http.MethodPost, "/api/auth/setup", `{"username":"admin","password":"correct-horse"}`, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("初始化状态码 = %d，期望 201，响应: %s", rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("初始化后应签发 1 个 Cookie，实际 %d", len(cookies))
	}
	return cookies[0]
}

func TestHealthReturnsVersionInfo(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	rec := env.do(t, http.MethodGet, "/api/health", "", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if len(body) != 3 {
		t.Errorf("响应字段数 = %d，期望 3: %v", len(body), body)
	}
	if body["status"] != "ok" || body["version"] != "v0.1.0-test" || body["commit"] != "abc1234" {
		t.Errorf("健康响应内容不符: %v", body)
	}
}

func TestStatusBeforeInitialization(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	rec := env.do(t, http.MethodGet, "/api/auth/status", "", nil)

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if body["initialized"] != false || body["authenticated"] != false {
		t.Errorf("初始状态不符: %v", body)
	}
}

func TestSetupRequiresConfiguredToken(t *testing.T) {
	env := newTestEnv(t, "")
	rec := env.do(t, http.MethodPost, "/api/auth/setup", `{"username":"admin","password":"correct-horse"}`, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("未配置凭证时状态码 = %d，期望 403", rec.Code)
	}
	if ok, _ := env.auth.IsInitialized(context.Background()); ok {
		t.Error("未配置凭证时不应创建管理员")
	}
}

func TestSetupRejectsWrongToken(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	rec := env.doWithToken(t, http.MethodPost, "/api/auth/setup", `{"username":"admin","password":"correct-horse"}`, "wrong-token", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("凭证错误时状态码 = %d，期望 403", rec.Code)
	}
	if ok, _ := env.auth.IsInitialized(context.Background()); ok {
		t.Error("凭证错误时不应创建管理员")
	}
}

func TestSetupAcceptsConfiguredToken(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)

	rec := env.do(t, http.MethodGet, "/api/auth/status", "", cookie)
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if body["initialized"] != true || body["authenticated"] != true {
		t.Errorf("初始化后状态不符: %v", body)
	}
}

func TestSetupTokenCannotBeReused(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	env.setup(t)

	rec := env.do(t, http.MethodPost, "/api/auth/setup", `{"username":"other","password":"another-secret"}`, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("重复初始化状态码 = %d，期望 409", rec.Code)
	}
	if ok, _ := env.auth.IsInitialized(context.Background()); !ok {
		t.Error("管理员应保持已初始化")
	}
	name, err := env.auth.Username(context.Background())
	if err != nil || name != "admin" {
		t.Errorf("账号名 = %q err=%v，不应被二次初始化覆盖", name, err)
	}
}

func TestSetupRejectsWeakPassword(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	rec := env.do(t, http.MethodPost, "/api/auth/setup", `{"username":"admin","password":"short"}`, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("弱密码状态码 = %d，期望 400", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "short") {
		t.Errorf("响应不应回显密码: %s", rec.Body.String())
	}
}

func TestLoginFlow(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	env.setup(t)

	wrong := env.do(t, http.MethodPost, "/api/auth/login", `{"username":"admin","password":"wrong-password"}`, nil)
	if wrong.Code != http.StatusUnauthorized {
		t.Errorf("错误密码状态码 = %d，期望 401", wrong.Code)
	}

	ok := env.do(t, http.MethodPost, "/api/auth/login", `{"username":"admin","password":"correct-horse"}`, nil)
	if ok.Code != http.StatusOK {
		t.Fatalf("正确密码状态码 = %d，期望 200", ok.Code)
	}
	if len(ok.Result().Cookies()) != 1 {
		t.Error("登录成功应签发 Cookie")
	}

	logout := env.do(t, http.MethodPost, "/api/auth/logout", "", nil)
	if logout.Code != http.StatusNoContent {
		t.Errorf("登出状态码 = %d，期望 204", logout.Code)
	}
	cookies := logout.Result().Cookies()
	if len(cookies) != 1 || cookies[0].MaxAge >= 0 {
		t.Errorf("登出应清除 Cookie: %+v", cookies)
	}
}

func TestLoginBeforeInitialization(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	rec := env.do(t, http.MethodPost, "/api/auth/login", `{"username":"admin","password":"correct-horse"}`, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("未初始化时登录状态码 = %d，期望 409", rec.Code)
	}
}

func TestOversizedSetupBodyRejectedBeforeHashing(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	huge := `{"username":"admin","password":"` + strings.Repeat("a", maxAuthBodyBytes*2) + `"}`

	rec := env.do(t, http.MethodPost, "/api/auth/setup", huge, nil)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("超限初始化状态码 = %d，期望 413", rec.Code)
	}
	if ok, _ := env.auth.IsInitialized(context.Background()); ok {
		t.Error("超限请求不应创建管理员")
	}
}

func TestOversizedLoginBodyRejected(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	env.setup(t)

	huge := `{"username":"admin","password":"correct-horse` + strings.Repeat("a", maxAuthBodyBytes*2) + `"}`
	rec := env.do(t, http.MethodPost, "/api/auth/login", huge, nil)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("超限登录状态码 = %d，期望 413", rec.Code)
	}
}

func TestProtectedRouteRequiresSession(t *testing.T) {
	env := newTestEnv(t, testSetupToken)

	anonymous := env.do(t, http.MethodGet, "/api/posts", "", nil)
	if anonymous.Code != http.StatusUnauthorized {
		t.Errorf("未登录访问管理接口状态码 = %d，期望 401", anonymous.Code)
	}

	cookie := env.setup(t)
	authorized := env.do(t, http.MethodGet, "/api/posts", "", cookie)
	if authorized.Code != http.StatusOK {
		t.Errorf("已登录访问管理接口状态码 = %d，期望 200", authorized.Code)
	}
}

func TestAccessLogOmitsCredentialsAndQuery(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)

	rec := env.do(t, http.MethodGet, "/api/posts?token=query-secret", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d", rec.Code)
	}

	logged := env.logs.String()
	for _, secret := range []string{"query-secret", cookie.Value, "correct-horse"} {
		if strings.Contains(logged, secret) {
			t.Errorf("访问日志不应包含 %q，实际: %s", secret, logged)
		}
	}
	if !strings.Contains(logged, "/api/posts") {
		t.Errorf("访问日志应包含路由模板，实际: %s", logged)
	}
}

func TestAccessLogOnUnmatchedRoute(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	rec := env.do(t, http.MethodGet, "/no-such-entry/guessable-code", "", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("未知路径状态码 = %d，期望 404", rec.Code)
	}
	logged := env.logs.String()
	if strings.Contains(logged, "guessable-code") {
		t.Errorf("访问日志不应记录原始路径: %s", logged)
	}
	if !strings.Contains(logged, "unmatched") {
		t.Errorf("访问日志应标记未匹配路由: %s", logged)
	}
}

// TestAccessLogLabelsSPAFallback 验证页面回退与静态资源不再记为未匹配路由，
// 同时仍然不把请求路径写进日志。
func TestAccessLogLabelsSPAFallback(t *testing.T) {
	// 用测试目录作为嵌入资源根，写入入口页与静态资源。
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<!doctype html><title>图集</title>"), 0o600); err != nil {
		t.Fatalf("写入入口页失败: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "static"), 0o750); err != nil {
		t.Fatalf("创建静态目录失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "static", "app.js"), []byte("console.log(1)"), 0o600); err != nil {
		t.Fatalf("写入静态资源失败: %v", err)
	}
	env := newTestEnvWith(t, testSetupToken, func(deps *Deps, _ *sql.DB) {
		deps.WebFS = os.DirFS(root)
	})

	rec := env.do(t, http.MethodGet, "/gallery/secret-code", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("页面回退状态码 = %d，期望 200", rec.Code)
	}
	logged := env.logs.String()
	if !strings.Contains(logged, "spa") {
		t.Errorf("页面回退应记为 spa: %s", logged)
	}
	if strings.Contains(logged, "secret-code") || strings.Contains(logged, "unmatched") {
		t.Errorf("页面回退日志不应包含原始路径或未匹配标记: %s", logged)
	}

	env.logs.Reset()
	rec = env.do(t, http.MethodGet, "/static/app.js", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("静态资源状态码 = %d，期望 200", rec.Code)
	}
	logged = env.logs.String()
	if !strings.Contains(logged, "asset") || strings.Contains(logged, "unmatched") {
		t.Errorf("静态资源应记为 asset: %s", logged)
	}

	env.logs.Reset()
	rec = env.do(t, http.MethodGet, "/api/not-exist", "", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("未知接口状态码 = %d，期望 404", rec.Code)
	}
	if !strings.Contains(env.logs.String(), "unmatched") {
		t.Errorf("未知接口仍应记为未匹配: %s", env.logs.String())
	}
}
