package settings

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ErrCleanupIncomplete 表示历史明文还没清理干净，服务不能带明文就绪。
var ErrCleanupIncomplete = errors.New("凭据清理未完成")

// KeySecretCleanupPending 是待清理标记：改写提交后置位，清理确认完成后才清除。
const KeySecretCleanupPending = "secrets.cleanup_pending"

// 清理重试参数：测试可覆盖，避免拖慢用例。
var (
	cleanupAttempts = 20
	cleanupDelay    = 250 * time.Millisecond
)

// secretKeys 是需要密文落库的字段，清理与写入共用同一份清单。
var secretKeys = []string{KeyTelegramToken, KeyTelegraphToken, KeyTelegramBots, KeyProxyURL}

// SanitizeSecrets 把历史明文凭据改成密文，可在每次启动重复执行。
// 改写与「待清理」标记在同一事务提交；只有 WAL 检查点与整库重写都成功后才清除标记，
// 否则返回 ErrCleanupIncomplete，由调用方阻止服务就绪。
func SanitizeSecrets(ctx context.Context, store *Store) (int, error) {
	if store == nil || store.handle == nil {
		return 0, errors.New("配置存储不可用")
	}
	pending, err := pendingPlaintext(ctx, store.handle)
	if err != nil {
		return 0, err
	}
	marked, err := cleanupMarked(ctx, store.handle)
	if err != nil {
		return 0, err
	}
	if len(pending) == 0 && !marked {
		return 0, nil
	}
	if len(pending) > 0 {
		if store.cipher == nil {
			return 0, ErrSecretKeyMissing
		}
		if err := sealPending(ctx, store, pending); err != nil {
			return 0, err
		}
	}
	if err := finishCleanup(ctx, store.handle); err != nil {
		return 0, err
	}
	return len(pending), nil
}

type plainSecret struct {
	key   string
	value string
}

// pendingPlaintext 读出仍是明文的凭据字段。
func pendingPlaintext(ctx context.Context, handle *sql.DB) ([]plainSecret, error) {
	query := `SELECT key, value FROM settings WHERE key IN (?, ?, ?, ?)`
	rows, err := handle.QueryContext(ctx, query, secretKeys[0], secretKeys[1], secretKeys[2], secretKeys[3])
	if err != nil {
		return nil, errors.New("读取凭据配置失败")
	}
	defer func() { _ = rows.Close() }()
	var pending []plainSecret
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, errors.New("读取凭据配置失败")
		}
		if value == "" || IsSealed(value) {
			continue
		}
		pending = append(pending, plainSecret{key: key, value: value})
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("读取凭据配置失败")
	}
	return pending, nil
}

func cleanupMarked(ctx context.Context, handle *sql.DB) (bool, error) {
	var value string
	err := handle.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, KeySecretCleanupPending).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, errors.New("读取清理标记失败")
	}
	return value != "", nil
}

// sealPending 在同一事务里改写明文并置待清理标记，任何一步失败都整体回滚。
func sealPending(ctx context.Context, store *Store, pending []plainSecret) error {
	tx, err := store.handle.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("改写凭据失败")
	}
	defer func() { _ = tx.Rollback() }()
	for _, item := range pending {
		sealed, err := store.cipher.Seal(item.value)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE settings SET value = ?, updated_at = CURRENT_TIMESTAMP WHERE key = ?`,
			sealed, item.key); err != nil {
			return errors.New("改写凭据失败")
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO settings (key, value, updated_at) VALUES (?, '1', CURRENT_TIMESTAMP)
		 ON CONFLICT(key) DO UPDATE SET value = '1', updated_at = CURRENT_TIMESTAMP`,
		KeySecretCleanupPending); err != nil {
		return errors.New("改写凭据失败")
	}
	if err := tx.Commit(); err != nil {
		return errors.New("改写凭据失败")
	}
	return nil
}

// finishCleanup 反复尝试截断 WAL 与整库重写，确认没有读者挡住检查点后再清标记。
// 清理期间缩短忙等待，读者很多时也能尽快给出结论，结束后恢复默认值。
func finishCleanup(ctx context.Context, handle *sql.DB) error {
	if _, err := handle.ExecContext(ctx, `PRAGMA busy_timeout = 200`); err != nil {
		return errors.New("清理数据库日志失败")
	}
	defer func() { _, _ = handle.ExecContext(ctx, `PRAGMA busy_timeout = 5000`) }()
	for attempt := 0; attempt < cleanupAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		busy, err := checkpoint(ctx, handle)
		if err != nil {
			return err
		}
		if busy {
			time.Sleep(cleanupDelay)
			continue
		}
		if _, err := handle.ExecContext(ctx, `VACUUM`); err != nil {
			time.Sleep(cleanupDelay)
			continue
		}
		busy, err = checkpoint(ctx, handle)
		if err != nil {
			return err
		}
		if busy {
			time.Sleep(cleanupDelay)
			continue
		}
		if _, err := handle.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, KeySecretCleanupPending); err != nil {
			return errors.New("清除清理标记失败")
		}
		return nil
	}
	return ErrCleanupIncomplete
}

// checkpoint 返回检查点是否因其他读者而未能完成。
func checkpoint(ctx context.Context, handle *sql.DB) (bool, error) {
	var busy, logFrames, checkpointed int
	if err := handle.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).
		Scan(&busy, &logFrames, &checkpointed); err != nil {
		return false, errors.New("清理数据库日志失败")
	}
	return busy != 0, nil
}
