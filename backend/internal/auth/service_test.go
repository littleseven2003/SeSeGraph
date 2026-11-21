package auth

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/littleseven2003/SeSeGraph/backend/internal/db"
)

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
	if err := svc.Authenticate(ctx, "admin", "correct-horse"); !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("未初始化时登录应返回 ErrNotInitialized，实际: %v", err)
	}

	if err := svc.Create(ctx, "admin", "correct-horse"); err != nil {
		t.Fatalf("创建管理员失败: %v", err)
	}
	if ok, _ := svc.IsInitialized(ctx); !ok {
		t.Fatal("创建后应为已初始化")
	}

	if err := svc.Authenticate(ctx, "admin", "correct-horse"); err != nil {
		t.Errorf("正确密码应登录成功: %v", err)
	}
	if err := svc.Authenticate(ctx, "admin", "wrong-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("错误密码应返回 ErrInvalidCredentials，实际: %v", err)
	}
	if err := svc.Authenticate(ctx, "someone", "correct-horse"); !errors.Is(err, ErrInvalidCredentials) {
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
	if err := svc.Authenticate(ctx, "admin", "correct-horse"); err != nil {
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
