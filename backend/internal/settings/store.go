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
	// 发帖默认值、链接生成方式与压缩包说明文件解析规则。
	KeyUploadMode        = "post.upload_mode"
	KeyTelegraphPathMode = "telegraph.path_mode"
	KeyMetaFilenames     = "post.meta_filenames"
	KeyMetaParseMode     = "post.meta_parse_mode"
	// 代理配置键：模式、自定义地址（可能带账密，不回显）与两项上游的启用开关。
	KeyProxyMode      = "proxy.mode"
	KeyProxyURL       = "proxy.url"
	KeyProxyTelegram  = "proxy.telegram"
	KeyProxyTelegraph = "proxy.telegraph"
	// 流量看护与自动备份：月度两级阈值、每日备份时刻与保留份数。
	KeyTrafficAlert  = "traffic.alert_bytes"
	KeyTrafficLimit  = "traffic.limit_bytes"
	KeyBackupEnabled = "backup.enabled"
	KeyBackupTime    = "backup.time"
	KeyBackupKeep    = "backup.keep"
	// KeyMediaThumbLimit 是逐图缩略图缓存上限（字节）。
	KeyMediaThumbLimit = "media_thumbs.limit_bytes"
	// KeyTelegramBots 与 botpool.KeyBots 同值：整份 Bot 池 JSON（含各令牌）。
	KeyTelegramBots = "telegram.bots"
)

// sensitiveKeys 是必须以密文落库的配置键：令牌、Bot 池整份 JSON 与 Telegraph 令牌。
// 其余配置不含凭据，保持明文便于运维排查。
var sensitiveKeys = map[string]bool{
	KeyTelegramToken:  true,
	KeyTelegraphToken: true,
	KeyTelegramBots:   true,
	// 自定义代理地址允许带账密，同样必须密文落库。
	KeyProxyURL: true,
}

// IsSensitiveKey 报告该配置键是否存放凭据。
func IsSensitiveKey(key string) bool { return sensitiveKeys[key] }

// Store 基于 settings 表读写键值配置；持有加密器时对凭据键自动加解密。
type Store struct {
	handle *sql.DB
	cipher *Cipher
}

// SetCipher 注入部署端加密器；为空时拒绝写入凭据键。
func (s *Store) SetCipher(cipher *Cipher) {
	if s != nil {
		s.cipher = cipher
	}
}

// Cipher 返回当前加密器，未配置时为 nil。
func (s *Store) Cipher() *Cipher {
	if s == nil {
		return nil
	}
	return s.cipher
}

// protect 把凭据键加密成落库值；未配置密钥时拒绝写入，绝不落明文。
func (s *Store) protect(key, value string) (string, error) {
	if key == "" || !IsSensitiveKey(key) || value == "" {
		return value, nil
	}
	if s.cipher == nil {
		return "", ErrSecretKeyMissing
	}
	return s.cipher.Seal(value)
}

// reveal 把凭据键的落库值还原成明文；历史明文原样返回，由启动清理统一改写。
func (s *Store) reveal(key, value string) (string, error) {
	if key == "" || !IsSensitiveKey(key) || !IsSealed(value) {
		return value, nil
	}
	if s.cipher == nil {
		return "", ErrSecretKeyMissing
	}
	return s.cipher.Open(value)
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
	opened, err := s.reveal(key, value)
	if err != nil {
		return "", false, err
	}
	return opened, true, nil
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
		opened, err := s.reveal(key, value)
		if err != nil {
			return nil, err
		}
		values[key] = opened
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
		stored, err := s.protect(key, value)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)
			 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP`,
			key, stored); err != nil {
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
	stored, err := s.protect(key, value)
	if err != nil {
		return err
	}
	if _, err := s.handle.ExecContext(ctx,
		`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP`,
		key, stored); err != nil {
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
		stored, err := s.protect(key, value)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)
			 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP`,
			key, stored); err != nil {
			return errors.New("保存配置失败")
		}
	}
	if err := tx.Commit(); err != nil {
		return errors.New("保存配置失败")
	}
	return nil
}
