package config

import (
	"errors"
	"flag"
	"strings"
	"testing"
)

func envOf(pairs map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		v, ok := pairs[key]
		return v, ok
	}
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(nil, envOf(nil))
	if err != nil {
		t.Fatalf("期望加载成功，实际错误: %v", err)
	}
	if cfg.Addr != DefaultAddr {
		t.Errorf("Addr = %q，期望 %q", cfg.Addr, DefaultAddr)
	}
	if cfg.DataDir != DefaultDataDir {
		t.Errorf("DataDir = %q，期望 %q", cfg.DataDir, DefaultDataDir)
	}
	if cfg.PublicURL != "" {
		t.Errorf("PublicURL = %q，期望为空", cfg.PublicURL)
	}
}

func TestLoadEnvOverridesDefault(t *testing.T) {
	env := envOf(map[string]string{
		"LISTEN_ADDR": "127.0.0.1:9000",
		"DATA_DIR":    "/srv/sesegraph",
		"PUBLIC_URL":  "https://img.example.com",
	})
	cfg, err := Load(nil, env)
	if err != nil {
		t.Fatalf("期望加载成功，实际错误: %v", err)
	}
	if cfg.Addr != "127.0.0.1:9000" {
		t.Errorf("Addr = %q", cfg.Addr)
	}
	if cfg.DataDir != "/srv/sesegraph" {
		t.Errorf("DataDir = %q", cfg.DataDir)
	}
	if cfg.PublicURL != "https://img.example.com" {
		t.Errorf("PublicURL = %q", cfg.PublicURL)
	}
}

func TestLoadFlagOverridesEnv(t *testing.T) {
	env := envOf(map[string]string{
		"LISTEN_ADDR": "127.0.0.1:9000",
		"DATA_DIR":    "/srv/sesegraph",
		"PUBLIC_URL":  "https://env.example.com",
	})
	args := []string{"--addr", ":7000", "--data", "/data/x", "--public-url", "https://flag.example.com"}
	cfg, err := Load(args, env)
	if err != nil {
		t.Fatalf("期望加载成功，实际错误: %v", err)
	}
	if cfg.Addr != ":7000" {
		t.Errorf("Addr = %q，期望命令行参数生效", cfg.Addr)
	}
	if cfg.DataDir != "/data/x" {
		t.Errorf("DataDir = %q，期望命令行参数生效", cfg.DataDir)
	}
	if cfg.PublicURL != "https://flag.example.com" {
		t.Errorf("PublicURL = %q，期望命令行参数生效", cfg.PublicURL)
	}
}

func TestLoadIgnoresBlankEnv(t *testing.T) {
	env := envOf(map[string]string{
		"LISTEN_ADDR": "   ",
		"DATA_DIR":    "",
	})
	cfg, err := Load(nil, env)
	if err != nil {
		t.Fatalf("期望加载成功，实际错误: %v", err)
	}
	if cfg.Addr != DefaultAddr {
		t.Errorf("Addr = %q，空环境变量应回落到默认值", cfg.Addr)
	}
	if cfg.DataDir != DefaultDataDir {
		t.Errorf("DataDir = %q，空环境变量应回落到默认值", cfg.DataDir)
	}
}

func TestLoadRejectsBlankAddrAndDataDir(t *testing.T) {
	if _, err := Load([]string{"--addr", " "}, envOf(nil)); err == nil {
		t.Error("空监听地址应被拒绝")
	}
	if _, err := Load([]string{"--data", " "}, envOf(nil)); err == nil {
		t.Error("空数据目录应被拒绝")
	}
}

func TestLoadAcceptsValidPublicURL(t *testing.T) {
	cases := []string{
		"https://img.example.com",
		"http://img.example.com:8080",
		"https://img.example.com/base",
	}
	for _, raw := range cases {
		if _, err := Load([]string{"--public-url", raw}, envOf(nil)); err != nil {
			t.Errorf("%q 应被接受，实际错误: %v", raw, err)
		}
	}
}

func TestLoadRejectsInvalidPublicURLWithoutEcho(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"缺少协议", "img.example.com"},
		{"协议不支持", "ftp://img.example.com"},
		{"缺少主机", "https://"},
		{"内嵌凭据", "https://user@img.example.com"},
		{"含空白", "https://img.example.com/a b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load([]string{"--public-url", tc.raw}, envOf(nil))
			if err == nil {
				t.Fatalf("%q 应被拒绝", tc.name)
			}
			msg := err.Error()
			for _, leak := range []string{tc.raw, "user@"} {
				if leak != "" && strings.Contains(msg, leak) {
					t.Errorf("错误信息不应回显原始取值，实际包含 %q: %s", leak, msg)
				}
			}
		})
	}
}

func TestLoadUnknownFlagDoesNotEchoInput(t *testing.T) {
	_, err := Load([]string{"--unknown-flag=hidden-value"}, envOf(nil))
	if err == nil {
		t.Fatal("未知参数应返回错误")
	}
	if strings.Contains(err.Error(), "hidden-value") {
		t.Errorf("错误信息不应回显参数取值: %s", err.Error())
	}
}

func TestLoadHelpRequest(t *testing.T) {
	_, err := Load([]string{"--help"}, envOf(nil))
	if !errors.Is(err, flag.ErrHelp) {
		t.Errorf("期望返回 flag.ErrHelp，实际: %v", err)
	}
}
