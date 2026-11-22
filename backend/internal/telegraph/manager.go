package telegraph

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/littleseven2003/SeSeGraph/backend/internal/settings"
)

// Store 是账户持久化所需的最小接口，便于测试替换。
type Store interface {
	Get(ctx context.Context, key string) (string, bool, error)
	Set(ctx context.Context, key, value string) error
}

// batchStore 可选实现：支持在单个事务内写入多项配置。
type batchStore interface {
	SetMany(ctx context.Context, values map[string]string) error
}

// Status 是对外暴露的账户状态，不含访问令牌。
type Status struct {
	Configured bool   `json:"configured"`
	Source     string `json:"source"`
	AuthorName string `json:"author_name"`
	AuthorURL  string `json:"author_url"`
}

// Manager 负责账户来源（环境变量或数据库）与令牌持久化。
// 建号与客户端缓存的读写都在互斥锁内，避免并发点击重复申请账户。
type Manager struct {
	base   Config
	store  Store
	env    *Client
	mu     sync.Mutex
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
	m.mu.Lock()
	defer m.mu.Unlock()
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
	// 客户端默认作者名优先取数据库中保存的账户资料。
	if strings.TrimSpace(cfg.AuthorName) == "" {
		if author, found, err := m.store.Get(ctx, settings.KeyTelegraphAuthorName); err == nil && found {
			cfg.AuthorName = strings.TrimSpace(author)
		}
	}
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

// Reset 丢弃数据库令牌对应的缓存客户端，设置更新后重新读取。
// 环境变量提供的令牌不受影响。
func (m *Manager) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stored = nil
}

// CreateAccount 申请新账户并原子写入令牌与账户资料。
// 已有账户（环境变量或数据库）时直接复用，不重复申请，避免覆盖导致旧页面失去编辑权。
func (m *Manager) CreateAccount(ctx context.Context, shortName, authorName string) (Status, error) {
	if m.env != nil {
		return m.Status(ctx)
	}
	if m.store == nil {
		return Status{}, ErrNotConfigured
	}

	// 整个建号过程串行化，重复或并发点击只会有一次上游调用。
	m.mu.Lock()
	defer m.mu.Unlock()

	existing, _, err := m.store.Get(ctx, settings.KeyTelegraphToken)
	if err != nil {
		return Status{}, err
	}
	if strings.TrimSpace(existing) != "" {
		return m.statusLocked(ctx)
	}

	account, err := m.tokenless().CreateAccount(ctx, shortName, authorName)
	if err != nil {
		return Status{}, err
	}

	values := map[string]string{settings.KeyTelegraphToken: account.AccessToken}
	if account.AuthorName != "" {
		values[settings.KeyTelegraphAuthorName] = account.AuthorName
	}
	if account.ShortName != "" {
		values[settings.KeyTelegraphShortName] = account.ShortName
	}
	if account.AuthorURL != "" {
		values[settings.KeyTelegraphAuthorURL] = account.AuthorURL
	}
	if err := m.saveLocked(ctx, values); err != nil {
		return Status{}, err
	}

	// 令牌已更新，丢弃旧客户端以便下次按新令牌重建。
	m.stored = nil

	return m.statusLocked(ctx)
}

// saveLocked 优先使用事务写入，存储实现不支持时退化为逐项写入。
func (m *Manager) saveLocked(ctx context.Context, values map[string]string) error {
	if batch, ok := m.store.(batchStore); ok {
		return batch.SetMany(ctx, values)
	}
	for key, value := range values {
		if err := m.store.Set(ctx, key, value); err != nil {
			return err
		}
	}
	return nil
}

// statusLocked 在持锁状态下读取状态，避免与建号写库交错。
func (m *Manager) statusLocked(ctx context.Context) (Status, error) {
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

// IsNotConfigured 判断错误是否为未配置。
func IsNotConfigured(err error) bool {
	return errors.Is(err, ErrNotConfigured)
}
