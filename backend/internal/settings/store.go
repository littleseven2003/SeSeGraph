// Package settings 保存运行期可修改的配置项，敏感值只落库不回显。
package settings

import (
	"context"
	"database/sql"
	"errors"
)

// 已知配置键。
const (
	KeyTelegraphToken      = "telegraph.access_token"
	KeyTelegraphAuthorName = "telegraph.author_name"
	KeyTelegraphShortName  = "telegraph.short_name"
	KeyTelegraphAuthorURL  = "telegraph.author_url"
)

// Store 基于 settings 表读写键值配置。
type Store struct {
	handle *sql.DB
}

// NewStore 创建配置存储。
func NewStore(handle *sql.DB) *Store {
	return &Store{handle: handle}
}

// Get 读取配置值，不存在时返回空串与 false。
func (s *Store) Get(ctx context.Context, key string) (string, bool, error) {
	if s == nil || s.handle == nil {
		return "", false, errors.New("配置存储不可用")
	}
	var value string
	err := s.handle.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, errors.New("读取配置失败")
	}
	return value, true, nil
}

// Set 写入或覆盖配置值。
func (s *Store) Set(ctx context.Context, key, value string) error {
	if s == nil || s.handle == nil {
		return errors.New("配置存储不可用")
	}
	if _, err := s.handle.ExecContext(ctx,
		`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP`,
		key, value); err != nil {
		return errors.New("保存配置失败")
	}
	return nil
}

// Delete 删除配置项，键不存在时视为成功。
func (s *Store) Delete(ctx context.Context, key string) error {
	if s == nil || s.handle == nil {
		return errors.New("配置存储不可用")
	}
	if _, err := s.handle.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, key); err != nil {
		return errors.New("删除配置失败")
	}
	return nil
}
