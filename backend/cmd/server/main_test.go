package main

import (
	"bytes"
	"log"
	"net"
	"strings"
	"testing"
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

	const markedDataDir = "/tmp/sesegraph-marked-datadir"

	err := run([]string{"--addr", addr, "--data", markedDataDir}, noEnv)
	if err == nil {
		t.Fatal("端口已被占用时应返回错误")
	}
	msg := err.Error()
	for _, value := range []string{addr, markedDataDir} {
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

	const markedDataDir = "/tmp/sesegraph-marked-logdir"
	_ = run([]string{"--addr", addr, "--data", markedDataDir}, noEnv)

	logged := buf.String()
	for _, value := range []string{addr, markedDataDir} {
		if strings.Contains(logged, value) {
			t.Errorf("启动日志不应包含配置原值 %q，实际: %s", value, logged)
		}
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
