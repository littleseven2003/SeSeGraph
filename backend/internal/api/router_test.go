package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/littleseven2003/SeSeGraph/backend/internal/auth"
	"github.com/littleseven2003/SeSeGraph/backend/internal/db"
)

func init() {
	gin.SetMode(gin.TestMode)
}

type testEnv struct {
	router   *gin.Engine
	auth     *auth.Service
	handle   *sql.DB
	logs     *bytes.Buffer
	logger   *log.Logger
	sessions *auth.Sessions
}

func newTestEnv(t *testing.T, allowRemoteSetup bool) *testEnv {
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

	router := NewRouter(Deps{
		Version:          "v0.1.0-test",
		Commit:           "abc1234",
		Auth:             service,
		Sessions:         sessions,
		AllowRemoteSetup: allowRemoteSetup,
		Logger:           logger,
	})
	return &testEnv{router: router, auth: service, handle: handle, logs: buf, logger: logger, sessions: sessions}
}

func (e *testEnv) do(t *testing.T, method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	return e.doFrom(t, "127.0.0.1:54321", method, path, body, cookie)
}

func (e *testEnv) doFrom(t *testing.T, remoteAddr, method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.RemoteAddr = remoteAddr
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
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
	env := newTestEnv(t, false)
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
	env := newTestEnv(t, false)
	rec := env.do(t, http.MethodGet, "/api/auth/status", "", nil)

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if body["initialized"] != false || body["authenticated"] != false {
		t.Errorf("初始状态不符: %v", body)
	}
}

func TestSetupCreatesAdminAndSession(t *testing.T) {
	env := newTestEnv(t, false)
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

func TestSetupRejectedFromRemoteByDefault(t *testing.T) {
	env := newTestEnv(t, false)
	rec := env.doFrom(t, "203.0.113.9:40000", http.MethodPost, "/api/auth/setup", `{"username":"admin","password":"correct-horse"}`, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("远程初始化状态码 = %d，期望 403", rec.Code)
	}

	allowed := newTestEnv(t, true)
	rec = allowed.doFrom(t, "203.0.113.9:40000", http.MethodPost, "/api/auth/setup", `{"username":"admin","password":"correct-horse"}`, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("允许远程时状态码 = %d，期望 201", rec.Code)
	}
}

func TestSetupTwiceConflicts(t *testing.T) {
	env := newTestEnv(t, false)
	env.setup(t)

	rec := env.do(t, http.MethodPost, "/api/auth/setup", `{"username":"admin","password":"correct-horse"}`, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("重复初始化状态码 = %d，期望 409", rec.Code)
	}
}

func TestSetupRejectsWeakPassword(t *testing.T) {
	env := newTestEnv(t, false)
	rec := env.do(t, http.MethodPost, "/api/auth/setup", `{"username":"admin","password":"short"}`, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("弱密码状态码 = %d，期望 400", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "short") {
		t.Errorf("响应不应回显密码: %s", rec.Body.String())
	}
}

func TestLoginFlow(t *testing.T) {
	env := newTestEnv(t, false)
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
	env := newTestEnv(t, false)
	rec := env.do(t, http.MethodPost, "/api/auth/login", `{"username":"admin","password":"correct-horse"}`, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("未初始化时登录状态码 = %d，期望 409", rec.Code)
	}
}

func TestProtectedRouteRequiresSession(t *testing.T) {
	env := newTestEnv(t, false)

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
	env := newTestEnv(t, false)
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
	env := newTestEnv(t, false)
	rec := env.do(t, http.MethodGet, "/i/guessable-code", "", nil)
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
