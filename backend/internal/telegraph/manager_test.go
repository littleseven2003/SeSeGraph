package telegraph

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/littleseven2003/SeSeGraph/backend/internal/settings"
)

// mapStore 是内存版配置存储。
type mapStore struct {
	mu      sync.Mutex
	values  map[string]string
	batches int
}

func newMapStore() *mapStore {
	return &mapStore{values: map[string]string{}}
}

func (m *mapStore) Get(_ context.Context, key string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	value, ok := m.values[key]
	return value, ok, nil
}

func (m *mapStore) Set(_ context.Context, key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.values[key] = value
	return nil
}

func (m *mapStore) SetMany(_ context.Context, values map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.batches++
	for key, value := range values {
		m.values[key] = value
	}
	return nil
}

func fakeAccountServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/createAccount") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"result": map[string]any{
				"short_name":   "sese",
				"author_name":  "SeSe",
				"author_url":   "https://telegra.ph/SeSe",
				"access_token": "secret-token",
			},
		})
	}))
	t.Cleanup(server.Close)
	return server
}

func TestManagerStatusAndCreateAccount(t *testing.T) {
	store := newMapStore()
	manager := NewManager(Config{APIBase: fakeAccountServer(t).URL}, store)
	ctx := context.Background()

	status, err := manager.Status(ctx)
	if err != nil {
		t.Fatalf("读取状态失败: %v", err)
	}
	if status.Configured || status.Source != "none" {
		t.Fatalf("初始状态 = %+v，期望未配置", status)
	}

	if _, err := manager.Client(ctx); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("未配置时应返回 ErrNotConfigured，实际: %v", err)
	}

	created, err := manager.CreateAccount(ctx, "sese", "SeSe")
	if err != nil {
		t.Fatalf("申请账户失败: %v", err)
	}
	if !created.Configured || created.Source != "stored" || created.AuthorName != "SeSe" {
		t.Errorf("创建后状态 = %+v", created)
	}
	if created.AuthorURL != "https://telegra.ph/SeSe" {
		t.Errorf("作者主页 = %q", created.AuthorURL)
	}
	if store.values[settings.KeyTelegraphToken] != "secret-token" {
		t.Errorf("令牌未持久化: %v", store.values)
	}

	client, err := manager.Client(ctx)
	if err != nil {
		t.Fatalf("读取客户端失败: %v", err)
	}
	if !client.Configured() {
		t.Error("持久化后客户端应带令牌")
	}

	// 状态序列化结果不得包含令牌。
	raw, err := json.Marshal(created)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	if strings.Contains(string(raw), "secret-token") || strings.Contains(string(raw), "access_token") {
		t.Errorf("状态输出泄露令牌: %s", raw)
	}
}

func TestManagerEnvTokenTakesPriority(t *testing.T) {
	store := newMapStore()
	manager := NewManager(Config{AccessToken: "env-token", AuthorName: "环境作者"}, store)
	ctx := context.Background()

	status, err := manager.Status(ctx)
	if err != nil {
		t.Fatalf("读取状态失败: %v", err)
	}
	if !status.Configured || status.Source != "env" || status.AuthorName != "环境作者" {
		t.Errorf("状态 = %+v，期望来自环境变量", status)
	}

	if _, err := manager.CreateAccount(ctx, "x", "y"); err != nil {
		t.Fatalf("已配置时调用申请账户应成功返回状态: %v", err)
	}
	if len(store.values) != 0 {
		t.Errorf("已有环境变量令牌时不应写入数据库: %v", store.values)
	}
}

func TestManagerUpstreamFailureIsDesensitized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"ok":false,"error":"TELEGRAPH_TOKEN_LEAK"}`))
	}))
	defer server.Close()

	manager := NewManager(Config{APIBase: server.URL}, newMapStore())
	_, err := manager.CreateAccount(context.Background(), "sese", "SeSe")
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("错误类别 = %v，期望 ErrUpstream", err)
	}
	for _, secret := range []string{"TELEGRAPH_TOKEN_LEAK", server.URL} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("错误信息泄露了 %q: %v", secret, err)
		}
	}
}

// countingAccountServer 统计 createAccount 调用次数，并可切换令牌。
type countingAccountServer struct {
	mu     sync.Mutex
	calls  int
	token  string
	server *httptest.Server
}

func newCountingAccountServer(t *testing.T, token string) *countingAccountServer {
	t.Helper()
	cs := &countingAccountServer{token: token}
	cs.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cs.mu.Lock()
		cs.calls++
		current := cs.token
		cs.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"result": map[string]any{
				"short_name":   "sese",
				"author_name":  "SeSe",
				"author_url":   "https://telegra.ph/SeSe",
				"access_token": current,
			},
		})
	}))
	t.Cleanup(cs.server.Close)
	return cs
}

func (c *countingAccountServer) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func TestManagerCreateAccountReusesExisting(t *testing.T) {
	server := newCountingAccountServer(t, "first-token")
	store := newMapStore()
	manager := NewManager(Config{APIBase: server.server.URL}, store)
	ctx := context.Background()

	if _, err := manager.CreateAccount(ctx, "sese", "SeSe"); err != nil {
		t.Fatalf("首次建号失败: %v", err)
	}
	first := store.values[settings.KeyTelegraphToken]

	// 重复点击不应再次申请账户或覆盖令牌。
	status, err := manager.CreateAccount(ctx, "other", "其他作者")
	if err != nil {
		t.Fatalf("重复建号失败: %v", err)
	}
	if server.callCount() != 1 {
		t.Errorf("上游调用 %d 次，期望 1 次", server.callCount())
	}
	if store.values[settings.KeyTelegraphToken] != first {
		t.Errorf("令牌被覆盖: %q -> %q", first, store.values[settings.KeyTelegraphToken])
	}
	if !status.Configured || status.AuthorName != "SeSe" {
		t.Errorf("状态 = %+v，期望保留原账户资料", status)
	}
}

func TestManagerCreateAccountConcurrentCallsOnce(t *testing.T) {
	server := newCountingAccountServer(t, "concurrent-token")
	store := newMapStore()
	manager := NewManager(Config{APIBase: server.server.URL}, store)
	ctx := context.Background()

	var wg sync.WaitGroup
	statuses := make([]Status, 4)
	errs := make([]error, 4)
	for index := 0; index < 4; index++ {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			statuses[slot], errs[slot] = manager.CreateAccount(ctx, "sese", "SeSe")
		}(index)
	}
	wg.Wait()

	for index, err := range errs {
		if err != nil {
			t.Fatalf("并发建号第 %d 次失败: %v", index, err)
		}
		if !statuses[index].Configured {
			t.Errorf("并发建号第 %d 次状态未配置", index)
		}
	}
	if calls := server.callCount(); calls != 1 {
		t.Errorf("上游调用 %d 次，期望并发下只调用 1 次", calls)
	}
	if store.values[settings.KeyTelegraphToken] != "concurrent-token" {
		t.Errorf("令牌 = %q", store.values[settings.KeyTelegraphToken])
	}
}

func TestManagerClientUsesStoredAuthor(t *testing.T) {
	store := newMapStore()
	store.values[settings.KeyTelegraphToken] = "stored-token"
	store.values[settings.KeyTelegraphAuthorName] = "贴库作者"

	manager := NewManager(Config{}, store)
	client, err := manager.Client(context.Background())
	if err != nil {
		t.Fatalf("读取客户端失败: %v", err)
	}
	if !client.Configured() {
		t.Fatal("客户端应带令牌")
	}
	if client.AuthorName() != "贴库作者" {
		t.Errorf("客户端作者名 = %q，期望取数据库保存值", client.AuthorName())
	}
}

func TestManagerCreateAccountWritesAtomically(t *testing.T) {
	server := newCountingAccountServer(t, "atomic-token")
	store := newMapStore()
	manager := NewManager(Config{APIBase: server.server.URL}, store)

	if _, err := manager.CreateAccount(context.Background(), "sese", "原子作者"); err != nil {
		t.Fatalf("建号失败: %v", err)
	}
	// mapStore 实现了批量写入，四项资料应在一次调用内落库。
	if store.batches != 1 {
		t.Errorf("批量写入次数 = %d，期望 1", store.batches)
	}
	for _, key := range []string{
		settings.KeyTelegraphToken,
		settings.KeyTelegraphAuthorName,
		settings.KeyTelegraphShortName,
		settings.KeyTelegraphAuthorURL,
	} {
		if _, ok := store.values[key]; !ok {
			t.Errorf("缺少配置项 %q", key)
		}
	}
}
