package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// credentialBody 组装修改凭据的请求体。
func credentialBody(current, username, password string) string {
	payload := map[string]string{
		"current_password": current,
		"username":         username,
		"new_password":     password,
	}
	raw, _ := json.Marshal(payload)
	return string(raw)
}

func TestUpdateCredentialsRequiresLogin(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	rec := env.do(t, http.MethodPut, "/api/settings/admin-credentials",
		credentialBody("correct-horse", "ops", "new-password-123"), nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("未登录状态码 = %d，期望 401", rec.Code)
	}
}

func TestUpdateCredentialsChangesBothAndInvalidatesSession(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)

	rec := env.do(t, http.MethodPut, "/api/settings/admin-credentials",
		credentialBody("correct-horse", "ops.admin", "new-password-123"), cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200，响应: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if body["username"] != "ops.admin" || body["reauthenticate"] != true {
		t.Errorf("响应内容不符: %v", body)
	}
	if strings.Contains(rec.Body.String(), "new-password-123") {
		t.Error("响应不得回显新密码")
	}

	// 旧 Cookie 因会话版本递增立即失效。
	status := env.do(t, http.MethodGet, "/api/auth/status", "", cookie)
	var state map[string]any
	if err := json.Unmarshal(status.Body.Bytes(), &state); err != nil {
		t.Fatalf("状态响应不是合法 JSON: %v", err)
	}
	if state["authenticated"] != false {
		t.Errorf("改密后旧会话应失效: %v", state)
	}
	if blocked := env.do(t, http.MethodGet, "/api/settings", "", cookie); blocked.Code != http.StatusUnauthorized {
		t.Errorf("旧 Cookie 访问管理接口状态码 = %d，期望 401", blocked.Code)
	}

	// 旧密码与旧账号名都不可再用，新凭据可以登录。
	if old := env.do(t, http.MethodPost, "/api/auth/login",
		`{"username":"admin","password":"correct-horse"}`, nil); old.Code != http.StatusUnauthorized {
		t.Errorf("旧账号登录状态码 = %d，期望 401", old.Code)
	}
	if oldPass := env.do(t, http.MethodPost, "/api/auth/login",
		`{"username":"ops.admin","password":"correct-horse"}`, nil); oldPass.Code != http.StatusUnauthorized {
		t.Errorf("旧密码登录状态码 = %d，期望 401", oldPass.Code)
	}
	fresh := env.do(t, http.MethodPost, "/api/auth/login",
		`{"username":"ops.admin","password":"new-password-123"}`, nil)
	if fresh.Code != http.StatusOK {
		t.Fatalf("新凭据登录状态码 = %d，期望 200，响应: %s", fresh.Code, fresh.Body.String())
	}
	if strings.Contains(env.logs.String(), "new-password-123") || strings.Contains(env.logs.String(), "correct-horse") {
		t.Error("日志不得出现明文密码")
	}
}

func TestUpdateCredentialsWrongPasswordKeepsAccount(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)

	rec := env.do(t, http.MethodPut, "/api/settings/admin-credentials",
		credentialBody("wrong-current", "ops.admin", "new-password-123"), cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("状态码 = %d，期望 400", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	fields, _ := body["fields"].([]any)
	if len(fields) != 1 || fields[0] != "current_password" {
		t.Errorf("字段错误不符: %v", body)
	}

	// 账号未变，旧凭据仍可登录。
	login := env.do(t, http.MethodPost, "/api/auth/login",
		`{"username":"admin","password":"correct-horse"}`, nil)
	if login.Code != http.StatusOK {
		t.Errorf("错误密码不应改动账号，登录状态码 = %d", login.Code)
	}
}

func TestUpdateCredentialsRejectsWeakPasswordAndBadUsername(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)

	cases := []struct {
		name   string
		body   string
		field  string
		status int
	}{
		{"密码过短", credentialBody("correct-horse", "", "short"), "new_password", http.StatusBadRequest},
		{"用户名非法", credentialBody("correct-horse", "bad name", ""), "username", http.StatusBadRequest},
		{"空更新", credentialBody("correct-horse", "", ""), "new_password", http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := env.do(t, http.MethodPut, "/api/settings/admin-credentials", tc.body, cookie)
			if rec.Code != tc.status {
				t.Fatalf("状态码 = %d，期望 %d，响应: %s", rec.Code, tc.status, rec.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("响应不是合法 JSON: %v", err)
			}
			fields, _ := body["fields"].([]any)
			if len(fields) != 1 || fields[0] != tc.field {
				t.Errorf("字段错误 = %v，期望 %s", body["fields"], tc.field)
			}
		})
	}
}

func TestUpdateCredentialsUsernameOnlyKeepsPassword(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)

	rec := env.do(t, http.MethodPut, "/api/settings/admin-credentials",
		credentialBody("correct-horse", "solo.admin", ""), cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200，响应: %s", rec.Code, rec.Body.String())
	}
	login := env.do(t, http.MethodPost, "/api/auth/login",
		`{"username":"solo.admin","password":"correct-horse"}`, nil)
	if login.Code != http.StatusOK {
		t.Errorf("只改用户名后原密码应仍可登录，状态码 = %d", login.Code)
	}
}
