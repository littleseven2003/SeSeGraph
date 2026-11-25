package botpool

import (
	"context"
	"sync"
	"testing"
)

// memStore 是配置表的内存替身。
type memStore struct {
	mu   sync.Mutex
	data map[string]string
}

func newMemStore() *memStore { return &memStore{data: map[string]string{}} }

func (m *memStore) Get(_ context.Context, key string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	value, ok := m.data[key]
	return value, ok, nil
}

func (m *memStore) Set(_ context.Context, key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = value
	return nil
}

func TestReplaceValidatesAndOrders(t *testing.T) {
	pool := New(newMemStore())
	ctx := context.Background()

	if err := pool.Replace(ctx, []Bot{
		{BotID: 300, Alias: " 备用 ", Token: "300:token-c"},
		{BotID: 200, Alias: "主力", Token: "200:token-b"},
	}); err != nil {
		t.Fatalf("保存 Bot 池失败: %v", err)
	}

	bots, err := pool.List(ctx)
	if err != nil {
		t.Fatalf("读取 Bot 池失败: %v", err)
	}
	if len(bots) != 2 {
		t.Fatalf("Bot 数量 = %d，期望 2", len(bots))
	}
	if bots[0].BotID != 300 || bots[0].Alias != "备用" || bots[0].Priority != 0 {
		t.Fatalf("首个 Bot = %+v，期望按提交顺序且别名去空白", bots[0])
	}
	if bots[0].Status != StatusOK {
		t.Fatalf("默认状态 = %q，期望 %q", bots[0].Status, StatusOK)
	}
}

func TestReplaceRejectsBadInput(t *testing.T) {
	pool := New(newMemStore())
	ctx := context.Background()

	cases := []struct {
		name string
		bots []Bot
	}{
		{"格式错误", []Bot{{Token: "abc"}}},
		{"令牌重复", []Bot{{Token: "100:same"}, {Token: "100:same"}}},
		{"编号重复", []Bot{{BotID: 10, Token: "10:a"}, {BotID: 10, Token: "11:b"}}},
	}
	for _, item := range cases {
		if err := pool.Replace(ctx, item.bots); err == nil {
			t.Errorf("%s：期望拒绝，实际通过", item.name)
		}
	}
}

func TestFreezeSkipsBotAndKeepsTokenOnResubmit(t *testing.T) {
	pool := New(newMemStore())
	ctx := context.Background()

	if err := pool.Replace(ctx, []Bot{
		{BotID: 100, Token: "100:token-a", Alias: "A"},
		{BotID: 200, Token: "200:token-b", Alias: "B"},
	}); err != nil {
		t.Fatalf("保存 Bot 池失败: %v", err)
	}
	if err := pool.MarkFrozen(ctx, 100, "上游拒绝，账号受限"); err != nil {
		t.Fatalf("标记冻结失败: %v", err)
	}

	bots, err := pool.List(ctx)
	if err != nil {
		t.Fatalf("读取 Bot 池失败: %v", err)
	}
	usable := Usable(bots)
	if len(usable) != 1 || usable[0].BotID != 200 {
		t.Fatalf("可用 Bot = %+v，期望只剩 200", usable)
	}

	// 重新提交同一令牌时保留冻结标记，保存配置不会把状态洗白。
	if err := pool.Replace(ctx, []Bot{
		{BotID: 100, Token: "100:token-a", Alias: "A"},
		{BotID: 200, Token: "200:token-b", Alias: "B"},
	}); err != nil {
		t.Fatalf("重新保存失败: %v", err)
	}
	bots, _ = pool.List(ctx)
	if bots[0].Status != StatusFrozen || bots[0].StatusNote == "" {
		t.Fatalf("重新保存后状态 = %+v，期望保留冻结说明", bots[0])
	}

	if err := pool.MarkHealthy(ctx, 100); err != nil {
		t.Fatalf("恢复健康失败: %v", err)
	}
	bots, _ = pool.List(ctx)
	if len(Usable(bots)) != 2 {
		t.Fatalf("恢复后可用数量 = %d，期望 2", len(Usable(bots)))
	}
}
