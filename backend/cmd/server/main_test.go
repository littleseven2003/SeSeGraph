package main

import (
	"bytes"
	"context"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/littleseven2003/SeSeGraph/backend/internal/auth"
	"github.com/littleseven2003/SeSeGraph/backend/internal/db"
	"github.com/littleseven2003/SeSeGraph/backend/internal/worker"
)

// noEnv 模拟没有任何环境变量参与配置。
func noEnv(string) (string, bool) { return "", false }

func occupyPort(t *testing.T) (addr string, closeFn func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("无法占用测试端口: %v", err)
	}
	return ln.Addr().String(), func() { _ = ln.Close() }
}

func TestRunHidesConfigOnListenFailure(t *testing.T) {
	addr, closeFn := occupyPort(t)
	defer closeFn()

	dataDir := t.TempDir()
	err := run([]string{"--addr", addr, "--data", dataDir}, noEnv)
	if err == nil {
		t.Fatal("端口已被占用时应返回错误")
	}
	msg := err.Error()
	for _, value := range []string{addr, dataDir} {
		if strings.Contains(msg, value) {
			t.Errorf("错误信息不应回显配置原值 %q，实际: %s", value, msg)
		}
	}
}

func TestStartupLogHidesConfig(t *testing.T) {
	addr, closeFn := occupyPort(t)
	defer closeFn()

	var buf bytes.Buffer
	original := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(original)

	dataDir := t.TempDir()
	_ = run([]string{"--addr", addr, "--data", dataDir}, noEnv)

	logged := buf.String()
	for _, value := range []string{addr, dataDir} {
		if strings.Contains(logged, value) {
			t.Errorf("启动日志不应包含配置原值 %q，实际: %s", value, logged)
		}
	}
}

func TestRunInitializesDatabase(t *testing.T) {
	addr, closeFn := occupyPort(t)
	defer closeFn()

	dataDir := t.TempDir()
	if err := run([]string{"--addr", addr, "--data", dataDir}, noEnv); err == nil {
		t.Fatal("端口已被占用时应返回错误")
	}

	store, err := db.Open(filepath.Join(dataDir, databaseFileName))
	if err != nil {
		t.Fatalf("启动后应生成可打开的数据库文件: %v", err)
	}
	defer store.Close()

	var version int
	if err := store.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("读取结构版本失败: %v", err)
	}
	if version != db.SchemaVersion {
		t.Errorf("user_version = %d，期望 %d", version, db.SchemaVersion)
	}

	var table string
	if err := store.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='table' AND name='posts'",
	).Scan(&table); err != nil {
		t.Errorf("启动后应存在 posts 表: %v", err)
	}
}

func TestRunKeepsDatabaseAcrossRestarts(t *testing.T) {
	addr, closeFn := occupyPort(t)
	defer closeFn()
	dataDir := t.TempDir()

	if err := run([]string{"--addr", addr, "--data", dataDir}, noEnv); err == nil {
		t.Fatal("首次启动应因端口占用失败")
	}
	store, err := db.Open(filepath.Join(dataDir, databaseFileName))
	if err != nil {
		t.Fatalf("首次启动后打开数据库失败: %v", err)
	}
	if _, err := store.Exec("INSERT INTO posts (title) VALUES ('重启前写入')"); err != nil {
		t.Fatalf("写入帖子失败: %v", err)
	}
	_ = store.Close()

	if err := run([]string{"--addr", addr, "--data", dataDir}, noEnv); err == nil {
		t.Fatal("再次启动应因端口占用失败")
	}

	again, err := db.Open(filepath.Join(dataDir, databaseFileName))
	if err != nil {
		t.Fatalf("再次启动后打开数据库失败: %v", err)
	}
	defer again.Close()

	var title string
	if err := again.QueryRow("SELECT title FROM posts LIMIT 1").Scan(&title); err != nil {
		t.Fatalf("重复启动后应保留已有数据: %v", err)
	}
	if title != "重启前写入" {
		t.Errorf("title = %q，期望保留原数据", title)
	}
}

func TestRunHidesDatabaseFailure(t *testing.T) {
	base := t.TempDir()
	blocker := filepath.Join(base, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("准备文件失败: %v", err)
	}
	dataDir := filepath.Join(blocker, "data")

	err := run([]string{"--addr", "127.0.0.1:0", "--data", dataDir}, noEnv)
	if err == nil {
		t.Fatal("数据目录不可用时应返回错误")
	}
	if err.Error() != errDatabase.Error() {
		t.Errorf("错误信息 = %q，期望固定说明且不回显路径", err.Error())
	}
	if strings.Contains(err.Error(), dataDir) {
		t.Errorf("错误信息不应回显数据目录: %s", err.Error())
	}
}

func TestRunReportsInvalidConfig(t *testing.T) {
	if err := run([]string{"--public-url", "ftp://example.com"}, noEnv); err == nil {
		t.Fatal("非法协议应返回错误")
	}
	if err := run([]string{"--addr", ""}, noEnv); err == nil {
		t.Fatal("空监听地址应返回错误")
	}
}

func envLookup(pairs map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := pairs[key]
		return value, ok
	}
}

func TestEnvBootstrapCreatesAdminWithoutLoggingPassword(t *testing.T) {
	addr, closeFn := occupyPort(t)
	defer closeFn()
	dataDir := t.TempDir()
	password := "env-bootstrap-pass"

	var buf bytes.Buffer
	original := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(original)

	env := envLookup(map[string]string{"ADMIN_USERNAME": "admin", "ADMIN_PASSWORD": password})
	if err := run([]string{"--addr", addr, "--data", dataDir}, env); err == nil {
		t.Fatal("端口被占用时应返回错误")
	}

	if strings.Contains(buf.String(), password) {
		t.Errorf("启动日志不应包含密码，实际: %s", buf.String())
	}

	store, err := db.Open(filepath.Join(dataDir, databaseFileName))
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	defer store.Close()

	var name, hash string
	if err := store.QueryRow("SELECT username, password_hash FROM admin_users WHERE id = 1").Scan(&name, &hash); err != nil {
		t.Fatalf("读取管理员失败: %v", err)
	}
	if name != "admin" {
		t.Errorf("账号名 = %q，期望 admin", name)
	}
	if strings.Contains(hash, password) {
		t.Error("密码必须以摘要形式保存")
	}

	service := auth.NewService(store)
	if err := service.Authenticate(context.Background(), "admin", password); err != nil {
		t.Errorf("环境变量初始化的账号应可登录: %v", err)
	}
}

func TestEnvBootstrapRejectsWeakPassword(t *testing.T) {
	env := envLookup(map[string]string{"ADMIN_USERNAME": "admin", "ADMIN_PASSWORD": "short"})
	err := run([]string{"--addr", "127.0.0.1:0", "--data", t.TempDir()}, env)
	if err == nil {
		t.Fatal("弱密码应返回错误")
	}
	if err != errAdminEnv {
		t.Errorf("错误 = %v，期望固定说明", err)
	}
	if strings.Contains(err.Error(), "short") {
		t.Errorf("错误信息不应回显密码: %s", err.Error())
	}
}

func TestEnvBootstrapKeepsExistingAdmin(t *testing.T) {
	addr, closeFn := occupyPort(t)
	defer closeFn()
	dataDir := t.TempDir()

	first := envLookup(map[string]string{"ADMIN_USERNAME": "admin", "ADMIN_PASSWORD": "first-password"})
	_ = run([]string{"--addr", addr, "--data", dataDir}, first)

	second := envLookup(map[string]string{"ADMIN_USERNAME": "intruder", "ADMIN_PASSWORD": "second-password"})
	_ = run([]string{"--addr", addr, "--data", dataDir}, second)

	store, err := db.Open(filepath.Join(dataDir, databaseFileName))
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	defer store.Close()

	service := auth.NewService(store)
	if err := service.Authenticate(context.Background(), "admin", "first-password"); err != nil {
		t.Errorf("应保留首次初始化的账号: %v", err)
	}
	if err := service.Authenticate(context.Background(), "intruder", "second-password"); err == nil {
		t.Error("不应被后一次环境变量覆盖")
	}
}

func TestIsHTTPS(t *testing.T) {
	cases := map[string]bool{
		"https://img.example.com": true,
		"HTTPS://img.example.com": true,
		"http://img.example.com":  false,
		"":                        false,
	}
	for input, want := range cases {
		if got := isHTTPS(input); got != want {
			t.Errorf("isHTTPS(%q) = %v，期望 %v", input, got, want)
		}
	}
}

func TestReadSetupToken(t *testing.T) {
	if got := readSetupToken(noEnv); got != "" {
		t.Errorf("未配置时应为空，实际 %q", got)
	}
	env := envLookup(map[string]string{"ADMIN_SETUP_TOKEN": "  abc123  "})
	if got := readSetupToken(env); got != "abc123" {
		t.Errorf("应去除首尾空白，实际 %q", got)
	}
}

func TestReadTelegramConfig(t *testing.T) {
	token, chatID, interval := readTelegramConfig(noEnv)
	if token != "" || chatID != "" {
		t.Errorf("未配置时应为空: %q %q", token, chatID)
	}
	if interval != worker.DefaultInterval {
		t.Errorf("默认间隔 = %s，期望 %s", interval, worker.DefaultInterval)
	}

	env := envLookup(map[string]string{
		"TELEGRAM_BOT_TOKEN":  "  123:abc  ",
		"TELEGRAM_CHANNEL_ID": " -100123 ",
		"UPLOAD_INTERVAL_MS":  "2500",
	})
	token, chatID, interval = readTelegramConfig(env)
	if token != "123:abc" || chatID != "-100123" {
		t.Errorf("应去除首尾空白: %q %q", token, chatID)
	}
	if interval != 2500*time.Millisecond {
		t.Errorf("间隔 = %s，期望 2.5s", interval)
	}

	invalid := envLookup(map[string]string{"UPLOAD_INTERVAL_MS": "abc"})
	if _, _, got := readTelegramConfig(invalid); got != worker.DefaultInterval {
		t.Errorf("非法间隔应回落到默认值，实际 %s", got)
	}
}

func TestReadTelegramAPIBase(t *testing.T) {
	if got := readTelegramAPIBase(noEnv); got != "" {
		t.Errorf("未配置时应为空，实际 %q", got)
	}
	env := envLookup(map[string]string{"TELEGRAM_API_BASE": " http://127.0.0.1:9090 "})
	if got := readTelegramAPIBase(env); got != "http://127.0.0.1:9090" {
		t.Errorf("应去除首尾空白，实际 %q", got)
	}
}
