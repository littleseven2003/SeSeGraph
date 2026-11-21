package db

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func openTemp(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sesegraph.db")
	handle, err := Open(path)
	if err != nil {
		t.Fatalf("打开临时数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	return handle, path
}

func TestOpenRejectsEmptyPath(t *testing.T) {
	if _, err := Open(""); err == nil {
		t.Fatal("空路径应返回错误")
	}
}

func TestOpenAppliesConnectionPragmas(t *testing.T) {
	handle, _ := openTemp(t)

	var journalMode string
	if err := handle.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("读取 journal_mode 失败: %v", err)
	}
	if strings.ToLower(journalMode) != "wal" {
		t.Errorf("journal_mode = %q，期望 wal", journalMode)
	}

	var busyTimeout int
	if err := handle.QueryRow("PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		t.Fatalf("读取 busy_timeout 失败: %v", err)
	}
	if busyTimeout != busyTimeoutMS {
		t.Errorf("busy_timeout = %d，期望 %d", busyTimeout, busyTimeoutMS)
	}

	var foreignKeys int
	if err := handle.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatalf("读取 foreign_keys 失败: %v", err)
	}
	if foreignKeys != 1 {
		t.Errorf("foreign_keys = %d，期望 1", foreignKeys)
	}
}

func TestOpenUsesSingleConnection(t *testing.T) {
	handle, _ := openTemp(t)
	if got := handle.Stats().MaxOpenConnections; got != 1 {
		t.Errorf("最大连接数 = %d，期望 1", got)
	}
}

func TestOpenCreatesParentDirectory(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "nested", "deeper", "sesegraph.db")
	handle, err := Open(path)
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	defer handle.Close()

	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Errorf("数据目录应被创建: %v", err)
	}
}

func TestOpenKeepsExistingData(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "sesegraph.db")

	first, err := Open(path)
	if err != nil {
		t.Fatalf("首次打开失败: %v", err)
	}
	if _, err := first.Exec("CREATE TABLE probe (id INTEGER PRIMARY KEY, note TEXT)"); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	if _, err := first.Exec("INSERT INTO probe (note) VALUES ('kept')"); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("关闭失败: %v", err)
	}

	second, err := Open(path)
	if err != nil {
		t.Fatalf("再次打开失败: %v", err)
	}
	defer second.Close()

	var note string
	if err := second.QueryRow("SELECT note FROM probe").Scan(&note); err != nil {
		t.Fatalf("读取已有数据失败: %v", err)
	}
	if note != "kept" {
		t.Errorf("note = %q，期望 kept", note)
	}
}

func TestOpenErrorHidesPath(t *testing.T) {
	base := t.TempDir()
	blocker := filepath.Join(base, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("准备文件失败: %v", err)
	}
	broken := filepath.Join(blocker, "data", "sesegraph.db")

	_, err := Open(broken)
	if err == nil {
		t.Fatal("父路径是普通文件时应返回错误")
	}
	if strings.Contains(err.Error(), broken) || strings.Contains(err.Error(), blocker) {
		t.Errorf("错误信息不应回显路径，实际: %s", err.Error())
	}
}
