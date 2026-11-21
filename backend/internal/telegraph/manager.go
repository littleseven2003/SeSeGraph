package telegraph

import (
	"context"
	"errors"
	"strings"

	"github.com/littleseven2003/SeSeGraph/backend/internal/settings"
)

// Store 是账户持久化所需的最小接口，便于测试替换。
type Store interface {
	Get(ctx context.Context, key string) (string, bool, error)
	Set(ctx context.Context, key, value string) error
}

// Status 是对外暴露的账户状态，不含访问令牌。
type Status struct {
	Configured bool   `json:"configured"`
	Source     string `json:"source"`
	AuthorName string `json:"author_name"`
	AuthorURL  string `json:"author_url"`
}

// Manager 负责账户来源（环境变量或数据库）与令牌持久化。
type Manager struct {
	base   Config
	store  Store
	env    *Client
	stored *Client
}

// NewManager 创建账户管理。base.AccessToken 非空时优先使用环境变量令牌。
func NewManager(base Config, store Store) *Manager {
	manager := &Manager{base: base, store: store}
	if strings.TrimSpace(base.AccessToken) != "" {
		manager.env = New(base)
	}
	return manager
}

// tokenless 返回不带令牌的客户端，用于申请账户。
func (m *Manager) tokenless() *Client {
	return New(Config{AuthorName: m.base.AuthorName, APIBase: m.base.APIBase, HTTPClient: m.base.HTTPClient, Timeout: m.base.Timeout})
}

// Client 返回带令牌的客户端。环境变量优先，其次使用数据库中的令牌。
func (m *Manager) Client(ctx context.Context) (*Client, error) {
	if m.env != nil {
		return m.env, nil
	}
	if m.stored != nil {
		return m.stored, nil
	}
	if m.store == nil {
		return nil, ErrNotConfigured
	}
	token, ok, err := m.store.Get(ctx, settings.KeyTelegraphToken)
	if err != nil {
		return nil, err
	}
	if !ok || strings.TrimSpace(token) == "" {
		return nil, ErrNotConfigured
	}
	cfg := m.base
	cfg.AccessToken = strings.TrimSpace(token)
	m.stored = New(cfg)
	return m.stored, nil
}

// Status 返回当前账户来源与作者名，不会回传令牌。
func (m *Manager) Status(ctx context.Context) (Status, error) {
	if m.env != nil {
		return Status{Configured: true, Source: "env", AuthorName: m.env.AuthorName()}, nil
	}
	if m.store == nil {
		return Status{Configured: false, Source: "none"}, nil
	}
	token, ok, err := m.store.Get(ctx, settings.KeyTelegraphToken)
	if err != nil {
		return Status{}, err
	}
	if !ok || strings.TrimSpace(token) == "" {
		return Status{Configured: false, Source: "none"}, nil
	}
	author, _, err := m.store.Get(ctx, settings.KeyTelegraphAuthorName)
	if err != nil {
		return Status{}, err
	}
	authorURL, _, err := m.store.Get(ctx, settings.KeyTelegraphAuthorURL)
	if err != nil {
		return Status{}, err
	}
	return Status{Configured: true, Source: "stored", AuthorName: author, AuthorURL: authorURL}, nil
}

// CreateAccount 申请新账户并把令牌写入数据库，返回不含令牌的状态。
func (m *Manager) CreateAccount(ctx context.Context, shortName, authorName string) (Status, error) {
	if m.env != nil {
		// 已通过环境变量提供令牌时不再申请新账户，避免覆盖手填配置。
		return m.Status(ctx)
	}
	if m.store == nil {
		return Status{}, ErrNotConfigured
	}

	account, err := m.tokenless().CreateAccount(ctx, shortName, authorName)
	if err != nil {
		return Status{}, err
	}
	if err := m.store.Set(ctx, settings.KeyTelegraphToken, account.AccessToken); err != nil {
		return Status{}, err
	}
	if account.AuthorName != "" {
		if err := m.store.Set(ctx, settings.KeyTelegraphAuthorName, account.AuthorName); err != nil {
			return Status{}, err
		}
	}
	if account.ShortName != "" {
		if err := m.store.Set(ctx, settings.KeyTelegraphShortName, account.ShortName); err != nil {
			return Status{}, err
		}
	}
	if account.AuthorURL != "" {
		if err := m.store.Set(ctx, settings.KeyTelegraphAuthorURL, account.AuthorURL); err != nil {
			return Status{}, err
		}
	}

	// 令牌已更新，丢弃旧客户端以便下次按新令牌重建。
	m.stored = nil

	author, _, err := m.store.Get(ctx, settings.KeyTelegraphAuthorName)
	if err != nil {
		return Status{}, err
	}
	authorURL, _, err := m.store.Get(ctx, settings.KeyTelegraphAuthorURL)
	if err != nil {
		return Status{}, err
	}
	return Status{Configured: true, Source: "stored", AuthorName: author, AuthorURL: authorURL}, nil
}

// IsNotConfigured 判断错误是否为未配置。
func IsNotConfigured(err error) bool {
	return errors.Is(err, ErrNotConfigured)
}
