package settings

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"strings"
)

// ErrSecretKeyMissing 表示部署端没有注入加密密钥：可以读旧配置，但拒绝写入新的凭据。
var ErrSecretKeyMissing = errors.New("未配置加密密钥，无法保存凭据")

// secretPrefix 标记加密值，便于识别历史明文并幂等改写。
const secretPrefix = "enc:v1:"

// Cipher 用部署端注入的密钥加密需要落库的凭据（AES-256-GCM）。
type Cipher struct {
	aead cipher.AEAD
}

// NewCipher 用 32 字节密钥构造加密器。
func NewCipher(key []byte) (*Cipher, error) {
	if len(key) != 32 {
		return nil, errors.New("加密密钥需为 32 字节")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.New("加密密钥不可用")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errors.New("加密密钥不可用")
	}
	return &Cipher{aead: aead}, nil
}

// CipherFromEnv 从部署环境读取密钥：直接值为 base64 或十六进制，
// 也可用 *_FILE 指向只读密钥文件。两者都没有时返回空表示未配置。
func CipherFromEnv(lookup func(string) (string, bool)) (*Cipher, error) {
	raw := ""
	if value, ok := lookup("SESEGRAPH_SECRET_KEY"); ok {
		raw = strings.TrimSpace(value)
	}
	if raw == "" {
		if path, ok := lookup("SESEGRAPH_SECRET_KEY_FILE"); ok && strings.TrimSpace(path) != "" {
			data, err := os.ReadFile(strings.TrimSpace(path))
			if err != nil {
				return nil, errors.New("读取加密密钥失败")
			}
			raw = strings.TrimSpace(string(data))
		}
	}
	if raw == "" {
		return nil, nil
	}
	key, err := decodeSecretKey(raw)
	if err != nil {
		return nil, err
	}
	return NewCipher(key)
}

func decodeSecretKey(text string) ([]byte, error) {
	for _, encoding := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding,
	} {
		if data, err := encoding.DecodeString(text); err == nil && len(data) == 32 {
			return data, nil
		}
	}
	if data, err := hex.DecodeString(text); err == nil && len(data) == 32 {
		return data, nil
	}
	return nil, errors.New("加密密钥格式不正确")
}

// Seal 把明文凭据加密成带前缀的文本；空值原样返回。
func (c *Cipher) Seal(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	if c == nil {
		return "", ErrSecretKeyMissing
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", errors.New("加密凭据失败")
	}
	sealed := c.aead.Seal(nil, nonce, []byte(plain), nil)
	return secretPrefix + base64.RawURLEncoding.EncodeToString(append(nonce, sealed...)), nil
}

// Open 解密带前缀的值；历史明文原样返回，交给启动清理统一改写。
func (c *Cipher) Open(value string) (string, error) {
	if !IsSealed(value) {
		return value, nil
	}
	if c == nil {
		return "", ErrSecretKeyMissing
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, secretPrefix))
	if err != nil {
		return "", errors.New("解密凭据失败")
	}
	size := c.aead.NonceSize()
	if len(raw) < size {
		return "", errors.New("解密凭据失败")
	}
	plain, err := c.aead.Open(nil, raw[:size], raw[size:], nil)
	if err != nil {
		return "", errors.New("解密凭据失败")
	}
	return string(plain), nil
}

// IsSealed 报告值是否已经是密文。
func IsSealed(value string) bool {
	return strings.HasPrefix(value, secretPrefix)
}
