package api

import (
	"bytes"
	"database/sql"

	"github.com/littleseven2003/SeSeGraph/backend/internal/settings"
)

// zzTestSettingsStore 构造带测试密钥的设置存储：凭据以密文落库。
func zzTestSettingsStore(handle *sql.DB) *settings.Store {
	store := settings.NewStore(handle)
	if handle == nil {
		return store
	}
	if cipher, err := settings.NewCipher(bytes.Repeat([]byte{0x5a}, 32)); err == nil {
		store.SetCipher(cipher)
	}
	return store
}
