package auth

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/littleseven2003/SeSeGraph/backend/internal/db"
)

// initialPassword 是各测试共用的初始管理员密码。
const initialPassword = "correct-horse"

func newTestService(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sesegraph.db")
	handle, err := db.Open(path)
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	if err := db.Migrate(context.Background(), handle); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	return NewService(handle), handle
}

func TestValidateCredentials(t *testing.T) {
	cases := []struct {
		name     string
		user     string
		pass     string
		wantFail bool
	}{
		{"正常", "admin", "correct-horse", false},
		{"空账号", "  ", "correct-horse", true},
		{"过短密码", "admin", "short", true},
		{"密码过长", "admin", string(make([]byte, 73)), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateCredentials(tc.user, tc.pass)
			if tc.wantFail && err == nil {
				t.Errorf("%s 应校验失败", tc.name)
			}
			if !tc.wantFail && err != nil {
				t.Errorf("%s 应校验通过，实际: %v", tc.name, err)
			}
		})
	}
}

func TestCreateAndAuthenticate(t *testing.T) {
	svc, handle := newTestService(t)
	ctx := context.Background()

	if ok, err := svc.IsInitialized(ctx); err != nil || ok {
		t.Fatalf("初始状态应为未初始化: ok=%v err=%v", ok, err)
	}
	if _, err := svc.Authenticate(ctx, "admin", "correct-horse"); !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("未初始化时登录应返回 ErrNotInitialized，实际: %v", err)
	}

	if err := svc.Create(ctx, "admin", "correct-horse"); err != nil {
		t.Fatalf("创建管理员失败: %v", err)
	}
	if ok, _ := svc.IsInitialized(ctx); !ok {
		t.Fatal("创建后应为已初始化")
	}

	if _, err := svc.Authenticate(ctx, "admin", "correct-horse"); err != nil {
		t.Errorf("正确密码应登录成功: %v", err)
	}
	if _, err := svc.Authenticate(ctx, "admin", "wrong-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("错误密码应返回 ErrInvalidCredentials，实际: %v", err)
	}
	if _, err := svc.Authenticate(ctx, "someone", "correct-horse"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("错误账号应返回 ErrInvalidCredentials，实际: %v", err)
	}

	var stored string
	if err := handle.QueryRow("SELECT password_hash FROM admin_users WHERE id = 1").Scan(&stored); err != nil {
		t.Fatalf("读取密码摘要失败: %v", err)
	}
	if stored == "correct-horse" || len(stored) < 20 {
		t.Error("密码必须以摘要形式存储")
	}
}

func TestCreateTwiceRejected(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	if err := svc.Create(ctx, "admin", "correct-horse"); err != nil {
		t.Fatalf("首次创建失败: %v", err)
	}
	if err := svc.Create(ctx, "other", "another-secret"); !errors.Is(err, ErrAlreadyInitialized) {
		t.Errorf("重复创建应返回 ErrAlreadyInitialized，实际: %v", err)
	}
}

func TestConcurrentCreateOnlyOneWins(t *testing.T) {
	svc, handle := newTestService(t)
	ctx := context.Background()

	const workers = 8
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		success int
		already int
		other   []error
	)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			err := svc.Create(ctx, "admin", "correct-horse")
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				success++
			case errors.Is(err, ErrAlreadyInitialized):
				already++
			default:
				other = append(other, err)
			}
		}(i)
	}
	wg.Wait()

	if success != 1 {
		t.Errorf("成功次数 = %d，期望 1", success)
	}
	if already != workers-1 {
		t.Errorf("已初始化次数 = %d，期望 %d", already, workers-1)
	}
	if len(other) > 0 {
		t.Errorf("出现意外错误: %v", other)
	}

	var total int
	if err := handle.QueryRow("SELECT COUNT(*) FROM admin_users").Scan(&total); err != nil {
		t.Fatalf("统计管理员失败: %v", err)
	}
	if total != 1 {
		t.Errorf("管理员记录数 = %d，期望 1", total)
	}
}

func TestAdminPersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sesegraph.db")
	ctx := context.Background()

	first, err := db.Open(path)
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	if err := db.Migrate(ctx, first); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	if err := NewService(first).Create(ctx, "admin", "correct-horse"); err != nil {
		t.Fatalf("创建管理员失败: %v", err)
	}
	_ = first.Close()

	second, err := db.Open(path)
	if err != nil {
		t.Fatalf("重新打开数据库失败: %v", err)
	}
	defer second.Close()

	svc := NewService(second)
	if ok, err := svc.IsInitialized(ctx); err != nil || !ok {
		t.Fatalf("重启后应保持已初始化: ok=%v err=%v", ok, err)
	}
	if _, err := svc.Authenticate(ctx, "admin", "correct-horse"); err != nil {
		t.Errorf("重启后应仍可登录: %v", err)
	}
	name, err := svc.Username(ctx)
	if err != nil || name != "admin" {
		t.Errorf("账号名 = %q err=%v", name, err)
	}
}

func TestSessionSecretIsStableAndPersisted(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()

	first, err := svc.SessionSecret(ctx)
	if err != nil {
		t.Fatalf("首次读取会话密钥失败: %v", err)
	}
	if len(first) != sessionSecretBytes {
		t.Errorf("会话密钥长度 = %d，期望 %d", len(first), sessionSecretBytes)
	}
	second, err := svc.SessionSecret(ctx)
	if err != nil {
		t.Fatalf("再次读取会话密钥失败: %v", err)
	}
	if string(first) != string(second) {
		t.Error("同一数据库的会话密钥应保持一致")
	}
}

func TestValidateNewUsernameAndPassword(t *testing.T) {
	valid := []string{"admin", "ops.team", "sese-dev", "a"}
	for _, name := range valid {
		if err := ValidateNewUsername(name); err != nil {
			t.Errorf("%q 应通过用户名校验: %v", name, err)
		}
	}
	invalid := []string{"", "含中文", "with space", "admin@host", strings.Repeat("a", 65)}
	for _, name := range invalid {
		err := ValidateNewUsername(name)
		var fieldErr *FieldError
		if !errors.As(err, &fieldErr) || fieldErr.Field != "username" {
			t.Errorf("%q 应返回用户名字段错误，实际: %v", name, err)
		}
	}

	if err := ValidateNewPassword(strings.Repeat("b", minNewPasswordLen)); err != nil {
		t.Errorf("12 位密码应通过: %v", err)
	}
	if err := ValidateNewPassword(strings.Repeat("a", minNewPasswordLen-1)); err == nil {
		t.Error("11 位密码应被拒绝")
	}
	if err := ValidateNewPassword(strings.Repeat("a", 73)); err == nil {
		t.Error("超过 72 字节的密码应被拒绝")
	}
}

func TestUpdateCredentialsUsernameOnly(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	if err := svc.Create(ctx, "admin", "{{SECRET_f7bfzeux}}"); err != nil {
		t.Fatalf("创建管理员失败: %v", err)
	}

	creds, err := svc.UpdateCredentials(ctx, CredentialUpdate{
		CurrentPassword: "{{SECRET_f7bfzeux}}",
		Username:        "ops.admin",
	})
	if err != nil {
		t.Fatalf("只改用户名应成功: %v", err)
	}
	if creds.Username != "ops.admin" || creds.Version != 1 {
		t.Errorf("返回凭据 = %+v，期望 ops.admin 版本 1", creds)
	}

	// 旧账号名不再可用，新账号名沿用原密码。
	if _, err := svc.Authenticate(ctx, "admin", "{{SECRET_f7bfzeux}}"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("旧账号名应失效，实际: %v", err)
	}
	again, err := svc.Authenticate(ctx, "ops.admin", "{{SECRET_f7bfzeux}}")
	if err != nil || again.Version != 1 {
		t.Errorf("新账号名应可登录: creds=%+v err=%v", again, err)
	}
}

func TestUpdateCredentialsPasswordOnly(t *testing.T) {
	svc, handle := newTestService(t)
	ctx := context.Background()
	if err := svc.Create(ctx, "admin", "{{SECRET_f7bfzeux}}"); err != nil {
		t.Fatalf("创建管理员失败: %v", err)
	}
	before, err := svc.Authenticate(ctx, "admin", "{{SECRET_f7bfzeux}}")
	if err != nil {
		t.Fatalf("初始登录失败: %v", err)
	}

	creds, err := svc.UpdateCredentials(ctx, CredentialUpdate{
		CurrentPassword: "{{SECRET_f7bfzeux}}",
		NewPassword:     "{{SECRET_4w4dwb3d}}",
	})
	if err != nil {
		t.Fatalf("只改密码应成功: %v", err)
	}
	if creds.Username != "admin" || creds.Version != before.Version+1 {
		t.Errorf("返回凭据 = %+v，期望沿用账号名与递增版本", creds)
	}

	if _, err := svc.Authenticate(ctx, "admin", "{{SECRET_f7bfzeux}}"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("旧密码应失效，实际: %v", err)
	}
	if _, err := svc.Authenticate(ctx, "admin", "{{SECRET_4w4dwb3d}}"); err != nil {
		t.Errorf("新密码应可登录: %v", err)
	}

	var stored string
	if err := handle.QueryRow("SELECT password_hash FROM admin_users WHERE id = 1").Scan(&stored); err != nil {
		t.Fatalf("读取摘要失败: %v", err)
	}
	if stored == "{{SECRET_4w4dwb3d}}" || len(stored) < 20 {
		t.Error("新密码必须以摘要形式存储")
	}
}

func TestUpdateCredentialsWrongPasswordWritesNothing(t *testing.T) {
	svc, handle := newTestService(t)
	ctx := context.Background()
	if err := svc.Create(ctx, "admin", "{{SECRET_f7bfzeux}}"); err != nil {
		t.Fatalf("创建管理员失败: %v", err)
	}
	var (
		hashBefore    string
		versionBefore int
	)
	if err := handle.QueryRow("SELECT password_hash, session_version FROM admin_users WHERE id = 1").Scan(&hashBefore, &versionBefore); err != nil {
		t.Fatalf("读取初始凭据失败: %v", err)
	}

	_, err := svc.UpdateCredentials(ctx, CredentialUpdate{
		CurrentPassword: "{{SECRET_hbdtsur0}}",
		Username:        "intruder",
		NewPassword:     "{{SECRET_4w4dwb3d}}",
	})
	var fieldErr *FieldError
	if !errors.As(err, &fieldErr) || fieldErr.Field != "current_password" {
		t.Fatalf("错误当前密码应返回字段错误，实际: %v", err)
	}

	var (
		hashAfter    string
		versionAfter int
		username     string
	)
	if err := handle.QueryRow("SELECT username, password_hash, session_version FROM admin_users WHERE id = 1").Scan(&username, &hashAfter, &versionAfter); err != nil {
		t.Fatalf("读取凭据失败: %v", err)
	}
	if username != "admin" || hashAfter != hashBefore || versionAfter != versionBefore {
		t.Error("校验失败时不应写入任何凭据变更")
	}
}

func TestUpdateCredentialsRejectsInvalidInput(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	if err := svc.Create(ctx, "admin", "{{SECRET_f7bfzeux}}"); err != nil {
		t.Fatalf("创建管理员失败: %v", err)
	}

	cases := []struct {
		name  string
		req   CredentialUpdate
		field string
	}{
		{"空更新", CredentialUpdate{CurrentPassword: "{{SECRET_f7bfzeux}}"}, "new_password"},
		{"用户名非法字符", CredentialUpdate{CurrentPassword: "{{SECRET_f7bfzeux}}", Username: "管理员"}, "username"},
		{"用户名过长", CredentialUpdate{CurrentPassword: "{{SECRET_f7bfzeux}}", Username: strings.Repeat("a", 65)}, "username"},
		{"密码过短", CredentialUpdate{CurrentPassword: initialPassword, NewPassword: strings.Repeat("a", minNewPasswordLen-1)}, "new_password"},
		{"密码过长", CredentialUpdate{CurrentPassword: "{{SECRET_f7bfzeux}}", NewPassword: strings.Repeat("a", 73)}, "new_password"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.UpdateCredentials(ctx, tc.req)
			var fieldErr *FieldError
			if !errors.As(err, &fieldErr) || fieldErr.Field != tc.field {
				t.Errorf("期望 %s 字段错误，实际: %v", tc.field, err)
			}
		})
	}
}

func TestConcurrentCredentialUpdatesDoNotLoseWrites(t *testing.T) {
	svc, _ := newTestService(t)
	ctx := context.Background()
	if err := svc.Create(ctx, "admin", "{{SECRET_f7bfzeux}}"); err != nil {
		t.Fatalf("创建管理员失败: %v", err)
	}

	const workers = 6
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		succeeded int
		conflicts int
		other     []error
	)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			// 只改用户名：密码保持初值，并发请求都能通过当前密码校验，
			// 由带版本条件的更新决定谁先落库，其余返回冲突。
			_, err := svc.UpdateCredentials(ctx, CredentialUpdate{
				CurrentPassword: "{{SECRET_f7bfzeux}}",
				Username:        "ops-" + strconv.Itoa(n),
			})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				succeeded++
			case errors.Is(err, ErrConflict):
				conflicts++
			default:
				other = append(other, err)
			}
		}(i)
	}
	wg.Wait()

	if len(other) > 0 {
		t.Fatalf("出现意外错误: %v", other)
	}
	if succeeded == 0 {
		t.Fatal("至少应有一次修改成功")
	}
	if succeeded+conflicts != workers {
		t.Errorf("成功 %d + 冲突 %d ≠ %d", succeeded, conflicts, workers)
	}
	version, err := svc.SessionVersion(ctx)
	if err != nil {
		t.Fatalf("读取会话版本失败: %v", err)
	}
	if version != succeeded {
		t.Errorf("会话版本 = %d，期望与成功次数 %d 一致", version, succeeded)
	}
}
