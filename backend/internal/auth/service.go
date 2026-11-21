package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// 鉴权流程对外返回的固定错误，调用方据此映射响应，不携带原始输入。
var (
	ErrAlreadyInitialized = errors.New("管理员已初始化")
	ErrNotInitialized     = errors.New("管理员尚未初始化")
	ErrInvalidCredentials = errors.New("账号或密码不正确")
	ErrInvalidInput       = errors.New("账号或密码不符合要求")
)

const (
	// minPasswordLen 是最短密码长度。
	minPasswordLen = 8
	// maxPasswordBytes 是 bcrypt 可处理的密码上限。
	maxPasswordBytes = 72
	// maxUsernameLen 是账号名最大字符数。
	maxUsernameLen = 64

	// sessionSecretKey 是会话签名密钥在 settings 表中的键名。
	sessionSecretKey = "session_secret"
	// sessionSecretBytes 是会话签名密钥的字节数。
	sessionSecretBytes = 32
)

// Service 负责单管理员账号的初始化与密码校验。
type Service struct {
	handle *sql.DB
}

// NewService 基于现有数据库连接创建鉴权服务。
func NewService(handle *sql.DB) *Service {
	return &Service{handle: handle}
}

// ValidateCredentials 校验账号与密码的基本格式。
func ValidateCredentials(username, password string) error {
	if strings.TrimSpace(username) == "" || len([]rune(strings.TrimSpace(username))) > maxUsernameLen {
		return ErrInvalidInput
	}
	if len(password) < minPasswordLen || len(password) > maxPasswordBytes {
		return ErrInvalidInput
	}
	return nil
}

// IsInitialized 表示管理员账号是否已创建。
func (s *Service) IsInitialized(ctx context.Context) (bool, error) {
	var total int
	if err := s.handle.QueryRowContext(ctx, "SELECT COUNT(*) FROM admin_users").Scan(&total); err != nil {
		return false, errors.New("读取管理员状态失败")
	}
	return total > 0, nil
}

// Create 创建唯一的管理员账号，仅在尚未初始化时成功。
// 并发调用时依赖主键约束保证只有一个调用成功，其余返回 ErrAlreadyInitialized。
func (s *Service) Create(ctx context.Context, username, password string) error {
	username = strings.TrimSpace(username)
	if err := ValidateCredentials(username, password); err != nil {
		return err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return errors.New("生成密码摘要失败")
	}

	if _, err := s.handle.ExecContext(ctx,
		"INSERT INTO admin_users (id, username, password_hash) VALUES (1, ?, ?)",
		username, string(hash),
	); err != nil {
		if strings.Contains(err.Error(), "constraint failed") {
			return ErrAlreadyInitialized
		}
		return errors.New("保存管理员账号失败")
	}
	return nil
}

// Username 返回当前管理员账号名。
func (s *Service) Username(ctx context.Context) (string, error) {
	var name string
	err := s.handle.QueryRowContext(ctx, "SELECT username FROM admin_users WHERE id = 1").Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotInitialized
	}
	if err != nil {
		return "", errors.New("读取管理员账号失败")
	}
	return name, nil
}

// Authenticate 校验账号与密码，失败时统一返回 ErrInvalidCredentials。
func (s *Service) Authenticate(ctx context.Context, username, password string) error {
	var (
		storedName string
		storedHash string
	)
	err := s.handle.QueryRowContext(ctx,
		"SELECT username, password_hash FROM admin_users WHERE id = 1",
	).Scan(&storedName, &storedHash)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotInitialized
	}
	if err != nil {
		return errors.New("读取管理员账号失败")
	}

	// 账号不匹配时仍执行一次摘要比较，避免通过响应差异判断账号是否存在。
	if storedName != strings.TrimSpace(username) {
		_ = bcrypt.CompareHashAndPassword([]byte(storedHash), []byte(password))
		return ErrInvalidCredentials
	}
	if bcrypt.CompareHashAndPassword([]byte(storedHash), []byte(password)) != nil {
		return ErrInvalidCredentials
	}
	return nil
}

// SessionSecret 返回持久化的会话签名密钥，首次调用时生成并保存。
func (s *Service) SessionSecret(ctx context.Context) ([]byte, error) {
	var encoded string
	err := s.handle.QueryRowContext(ctx,
		"SELECT value FROM settings WHERE key = ?", sessionSecretKey,
	).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		raw := make([]byte, sessionSecretBytes)
		if _, err := rand.Read(raw); err != nil {
			return nil, errors.New("生成会话密钥失败")
		}
		if _, err := s.handle.ExecContext(ctx,
			"INSERT OR IGNORE INTO settings (key, value) VALUES (?, ?)",
			sessionSecretKey, hex.EncodeToString(raw),
		); err != nil {
			return nil, errors.New("保存会话密钥失败")
		}
		// 并发首次访问时可能由其他调用先写入，重新读取以保证使用同一密钥。
		if err := s.handle.QueryRowContext(ctx,
			"SELECT value FROM settings WHERE key = ?", sessionSecretKey,
		).Scan(&encoded); err != nil {
			return nil, errors.New("读取会话密钥失败")
		}
	} else if err != nil {
		return nil, errors.New("读取会话密钥失败")
	}

	secret, err := hex.DecodeString(encoded)
	if err != nil || len(secret) != sessionSecretBytes {
		return nil, errors.New("会话密钥格式不正确")
	}
	return secret, nil
}
