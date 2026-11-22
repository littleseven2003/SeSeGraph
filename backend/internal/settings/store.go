// Package settings 保存运行期可修改的配置项，敏感值只落库不回显。
package settings

import (
	"context"
	"database/sql"
	"errors"
)

// 已知配置键。敏感值（Bot Token、Telegraph 令牌）只落库，任何接口都不回显。
const (
	KeyTelegraphToken      = "telegraph.access_token"
	KeyTelegraphAuthorName = "telegraph.author_name"
	KeyTelegraphShortName  = "telegraph.short_name"
	KeyTelegraphAuthorURL  = "telegraph.author_url"
	KeyTelegramToken       = "telegram.bot_token"
	KeyTelegramChannel     = "telegram.channel_id"
	KeyTelegramBotID       = "telegram.bot_id"
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

// Snapshot 在单次查询中读取全部配置，供一次解析取得同一份快照。
func (s *Store) Snapshot(ctx context.Context) (map[string]string, error) {
	if s == nil || s.handle == nil {
		return nil, errors.New("配置存储不可用")
	}
	rows, err := s.handle.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return nil, errors.New("读取配置失败")
	}
	defer func() { _ = rows.Close() }()

	values := make(map[string]string, 8)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, errors.New("读取配置失败")
		}
		values[key] = value
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("读取配置失败")
	}
	return values, nil
}

// Replace 在单个事务内完成删除与写入，任意一步失败时整体回滚。
func (s *Store) Replace(ctx context.Context, values map[string]string, removes []string) error {
	if s == nil || s.handle == nil {
		return errors.New("配置存储不可用")
	}
	if len(values) == 0 && len(removes) == 0 {
		return nil
	}
	tx, err := s.handle.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("保存配置失败")
	}
	defer func() { _ = tx.Rollback() }()

	for _, key := range removes {
		if _, err := tx.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, key); err != nil {
			return errors.New("保存配置失败")
		}
	}
	for key, value := range values {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)
			 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP`,
			key, value); err != nil {
			return errors.New("保存配置失败")
		}
	}
	if err := tx.Commit(); err != nil {
		return errors.New("保存配置失败")
	}
	return nil
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

// SetMany 在单个事务内写入多项配置，任意一项失败时整体回滚。
func (s *Store) SetMany(ctx context.Context, values map[string]string) error {
	if s == nil || s.handle == nil {
		return errors.New("配置存储不可用")
	}
	if len(values) == 0 {
		return nil
	}
	tx, err := s.handle.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("保存配置失败")
	}
	defer func() { _ = tx.Rollback() }()

	for key, value := range values {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)
			 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP`,
			key, value); err != nil {
			return errors.New("保存配置失败")
		}
	}
	if err := tx.Commit(); err != nil {
		return errors.New("保存配置失败")
	}
	return nil
}
