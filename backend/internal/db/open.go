package db

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

const (
	// driverName 是纯 Go 驱动注册名，允许 CGO_ENABLED=0 静态构建。
	driverName = "sqlite"
	// busyTimeoutMS 是遇到锁时等待的毫秒数。
	busyTimeoutMS = 5000
)

// Open 打开 SQLite 数据库文件。
// 连接参数启用 WAL 日志、忙等待 5000ms、外键约束与 NORMAL 同步级别；
// 连接池固定为单连接，保证写事务串行执行，避免并发写触发的锁冲突。
func Open(path string) (*sql.DB, error) {
	if path == "" {
		return nil, errors.New("数据库路径不能为空")
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, errors.New("创建数据目录失败")
		}
	}

	dsn := path + "?_pragma=busy_timeout(5000)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=foreign_keys(1)" +
		"&_pragma=synchronous(NORMAL)"

	handle, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, errors.New("打开数据库失败")
	}
	handle.SetMaxOpenConns(1)
	handle.SetMaxIdleConns(1)
	handle.SetConnMaxLifetime(0)

	if err := handle.Ping(); err != nil {
		_ = handle.Close()
		return nil, errors.New("连接数据库失败")
	}
	return handle, nil
}
