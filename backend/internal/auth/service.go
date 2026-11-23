package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// 鉴权流程对外返回的固定错误，调用方据此映射响应，不携带原始输入。
var (
	ErrAlreadyInitialized = errors.New("管理员已初始化")
	ErrNotInitialized     = errors.New("管理员尚未初始化")
	ErrInvalidCredentials = errors.New("账号或密码不正确")
	ErrInvalidInput       = errors.New("账号或密码不符合要求")
	// ErrWrongPassword 表示修改凭据时当前密码校验失败。
	ErrWrongPassword = errors.New("当前密码不正确")
	// ErrConflict 表示账号在本次修改前已被其他请求改写，需要重新读取后再改。
	ErrConflict = errors.New("账号已被其他请求修改")
	// ErrInvalidUsername 表示新用户名不符合字符或长度要求。
	ErrInvalidUsername = errors.New("用户名需为 1–64 位字母、数字、点、下划线或短横线")
	// ErrWeakPassword 表示新密码不符合长度要求。
	ErrWeakPassword = errors.New("新密码至少 12 个字符，且不超过 72 字节")
	// ErrConfirmRequired 表示修改密码时缺少确认值。
	ErrConfirmRequired = errors.New("请再次输入新密码")
	// ErrPasswordMismatch 表示两次输入的新密码不一致。
	ErrPasswordMismatch = errors.New("两次输入的新密码不一致")
)

// FieldError 标记校验失败的字段，接口据此返回字段级提示。
type FieldError struct {
	Field string
	Err   error
}

func (e *FieldError) Error() string { return e.Err.Error() }

func (e *FieldError) Unwrap() error { return e.Err }

// Credentials 是账号名与会话版本。
type Credentials struct {
	Username string
	Version  int
}

// CredentialUpdate 描述一次凭据修改，用户名或新密码留空表示不改该项。
// SessionVersion 是请求通过鉴权时的会话版本，用于拒绝使用过期会话完成修改。
type CredentialUpdate struct {
	CurrentPassword string
	Username        string
	NewPassword     string
	ConfirmPassword string
	SessionVersion  int
}

const (
	// minPasswordLen 是最短密码长度。
	minPasswordLen = 8
	// maxPasswordBytes 是 bcrypt 可处理的密码上限。
	maxPasswordBytes = 72
	// maxUsernameLen 是账号名最大字符数。
	maxUsernameLen = 64
	// minNewPasswordLen 是修改密码时的最短字符数。
	minNewPasswordLen = 12

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

// usernamePattern 限定修改时的用户名可用字符。
var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// ValidateNewUsername 校验修改时的新用户名。
func ValidateNewUsername(username string) error {
	if !usernamePattern.MatchString(username) {
		return &FieldError{Field: "username", Err: ErrInvalidUsername}
	}
	return nil
}

// ValidateNewPassword 校验新密码：至少 12 个字符，且不超过 bcrypt 的 72 字节上限。
func ValidateNewPassword(password string) error {
	if len([]rune(password)) < minNewPasswordLen || len(password) > maxPasswordBytes {
		return &FieldError{Field: "new_password", Err: ErrWeakPassword}
	}
	return nil
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
// 成功时一并返回校验时读到的会话版本，登录直接用它签发 Cookie。
func (s *Service) Authenticate(ctx context.Context, username, password string) (Credentials, error) {
	var (
		creds      Credentials
		storedHash string
	)
	err := s.handle.QueryRowContext(ctx,
		"SELECT username, password_hash, session_version FROM admin_users WHERE id = 1",
	).Scan(&creds.Username, &storedHash, &creds.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return Credentials{}, ErrNotInitialized
	}
	if err != nil {
		return Credentials{}, errors.New("读取管理员账号失败")
	}

	// 账号不匹配时仍执行一次摘要比较，避免通过响应差异判断账号是否存在。
	if creds.Username != strings.TrimSpace(username) {
		_ = bcrypt.CompareHashAndPassword([]byte(storedHash), []byte(password))
		return Credentials{}, ErrInvalidCredentials
	}
	if bcrypt.CompareHashAndPassword([]byte(storedHash), []byte(password)) != nil {
		return Credentials{}, ErrInvalidCredentials
	}
	return creds, nil
}

// Credentials 读取当前账号名与会话版本。
func (s *Service) Credentials(ctx context.Context) (Credentials, error) {
	var creds Credentials
	err := s.handle.QueryRowContext(ctx,
		"SELECT username, session_version FROM admin_users WHERE id = 1",
	).Scan(&creds.Username, &creds.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return Credentials{}, ErrNotInitialized
	}
	if err != nil {
		return Credentials{}, errors.New("读取管理员账号失败")
	}
	return creds, nil
}

// SessionVersion 只读取会话版本，受保护接口用它判断 Cookie 是否仍然有效。
func (s *Service) SessionVersion(ctx context.Context) (int, error) {
	var version int
	err := s.handle.QueryRowContext(ctx,
		"SELECT session_version FROM admin_users WHERE id = 1",
	).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotInitialized
	}
	if err != nil {
		return 0, errors.New("读取管理员账号失败")
	}
	return version, nil
}

// UpdateCredentials 校验当前密码后修改账号名或密码，并递增会话版本。
// 摘要计算在写锁之外完成；带版本条件的更新保证并发修改不会互相覆盖。
func (s *Service) UpdateCredentials(ctx context.Context, update CredentialUpdate) (Credentials, error) {
	username := strings.TrimSpace(update.Username)
	if username == "" && update.NewPassword == "" {
		return Credentials{}, &FieldError{Field: "new_password", Err: ErrInvalidInput}
	}
	if username != "" {
		if err := ValidateNewUsername(username); err != nil {
			return Credentials{}, err
		}
	}
	if update.NewPassword != "" {
		if err := ValidateNewPassword(update.NewPassword); err != nil {
			return Credentials{}, err
		}
		if update.ConfirmPassword == "" {
			return Credentials{}, &FieldError{Field: "confirm_password", Err: ErrConfirmRequired}
		}
		if update.ConfirmPassword != update.NewPassword {
			return Credentials{}, &FieldError{Field: "confirm_password", Err: ErrPasswordMismatch}
		}
	} else if update.ConfirmPassword != "" {
		return Credentials{}, &FieldError{Field: "confirm_password", Err: ErrConfirmRequired}
	}

	var (
		storedName string
		storedHash string
		version    int
	)
	err := s.handle.QueryRowContext(ctx,
		"SELECT username, password_hash, session_version FROM admin_users WHERE id = 1",
	).Scan(&storedName, &storedHash, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return Credentials{}, ErrNotInitialized
	}
	if err != nil {
		return Credentials{}, errors.New("读取管理员账号失败")
	}

	// 请求带来的会话版本必须是当前版本，避免过期会话在并发中完成第二次修改。
	if update.SessionVersion != version {
		return Credentials{}, ErrConflict
	}

	// 当前密码仅用于校验，失败时不写库。
	if bcrypt.CompareHashAndPassword([]byte(storedHash), []byte(update.CurrentPassword)) != nil {
		return Credentials{}, &FieldError{Field: "current_password", Err: ErrWrongPassword}
	}

	targetName := storedName
	if username != "" {
		targetName = username
	}
	targetHash := storedHash
	if update.NewPassword != "" {
		hash, hashErr := bcrypt.GenerateFromPassword([]byte(update.NewPassword), bcrypt.DefaultCost)
		if hashErr != nil {
			return Credentials{}, errors.New("生成密码摘要失败")
		}
		targetHash = string(hash)
	}
	if targetName == storedName && targetHash == storedHash {
		return Credentials{}, &FieldError{Field: "new_password", Err: ErrInvalidInput}
	}

	result, err := s.handle.ExecContext(ctx,
		`UPDATE admin_users
		    SET username = ?, password_hash = ?, session_version = session_version + 1, updated_at = CURRENT_TIMESTAMP
		  WHERE id = 1 AND session_version = ?`,
		targetName, targetHash, update.SessionVersion,
	)
	if err != nil {
		return Credentials{}, errors.New("保存管理员账号失败")
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Credentials{}, errors.New("保存管理员账号失败")
	}
	if affected == 0 {
		return Credentials{}, ErrConflict
	}
	return Credentials{Username: targetName, Version: version + 1}, nil
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
