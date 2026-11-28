package settings

import (
	"context"
	"database/sql"
	"errors"
)

// SanitizeSecrets 把历史明文凭据改成密文，可在每次启动重复执行。
// 没有注入密钥却存在明文时返回 ErrSecretKeyMissing，由调用方阻止带明文启动。
func SanitizeSecrets(ctx context.Context, store *Store) (int, error) {
	if store == nil || store.handle == nil {
		return 0, errors.New("配置存储不可用")
	}
	rows, err := store.handle.QueryContext(ctx,
		`SELECT key, value FROM settings WHERE key IN (?, ?, ?)`,
		KeyTelegramToken, KeyTelegraphToken, KeyTelegramBots)
	if err != nil {
		return 0, errors.New("读取凭据配置失败")
	}
	type plainItem struct {
		key   string
		value string
	}
	var pending []plainItem
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			_ = rows.Close()
			return 0, errors.New("读取凭据配置失败")
		}
		if value == "" || IsSealed(value) {
			continue
		}
		pending = append(pending, plainItem{key: key, value: value})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, errors.New("读取凭据配置失败")
	}
	_ = rows.Close()

	if len(pending) == 0 {
		return 0, nil
	}
	if store.cipher == nil {
		return 0, ErrSecretKeyMissing
	}
	for _, item := range pending {
		sealed, err := store.cipher.Seal(item.value)
		if err != nil {
			return 0, err
		}
		if _, err := store.handle.ExecContext(ctx,
			`UPDATE settings SET value = ?, updated_at = CURRENT_TIMESTAMP WHERE key = ?`,
			sealed, item.key); err != nil {
			return 0, errors.New("改写凭据失败")
		}
	}
	if err := compactSecretFootprint(ctx, store.handle); err != nil {
		return 0, err
	}
	return len(pending), nil
}

// compactSecretFootprint 让改写前的明文页尽快从主库和 WAL 里消失：
// 先把 WAL 截断到主库，再整库重写，最后再截断一次。
func compactSecretFootprint(ctx context.Context, handle *sql.DB) error {
	for attempt := 0; attempt < 2; attempt++ {
		var busy, logFrames, checkpointed int
		if err := handle.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).
			Scan(&busy, &logFrames, &checkpointed); err != nil {
			return errors.New("清理数据库日志失败")
		}
		if attempt == 0 {
			if _, err := handle.ExecContext(ctx, `VACUUM`); err != nil {
				return errors.New("整理数据库失败")
			}
		}
	}
	return nil
}
