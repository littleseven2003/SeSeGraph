package settings

import (
	"context"
	"errors"
	"testing"
)

// newResolver 返回带独立数据库的解析器。
func newResolver(t *testing.T, env Env) *Resolver {
	t.Helper()
	return NewResolver(newStore(t), env)
}

func TestEffectivePrefersEnvThenStoredThenDefault(t *testing.T) {
	ctx := context.Background()
	resolver := newResolver(t, Env{TelegramToken: "env-token", TelegraphAuthor: "env-author"})

	effective, err := resolver.Effective(ctx)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if effective.TelegramToken.Value != "env-token" || effective.TelegramToken.Source != SourceEnv {
		t.Errorf("Telegram 令牌 = %+v，期望环境变量优先", effective.TelegramToken)
	}
	if effective.TelegramChannel.Set() || effective.TelegramChannel.Source != SourceNone {
		t.Errorf("未配置的频道 = %+v，期望 none", effective.TelegramChannel)
	}
	if effective.TelegramAPIBase.Source != SourceDefault || effective.TelegramAPIBase.Value != DefaultTelegramAPIBase {
		t.Errorf("Telegram 地址 = %+v，期望默认值", effective.TelegramAPIBase)
	}
	if effective.TelegraphAPIBase.Source != SourceDefault {
		t.Errorf("Telegraph 地址 = %+v，期望默认值", effective.TelegraphAPIBase)
	}

	// 数据库值在没有环境变量时生效。
	if err := resolver.Update(ctx, Patch{TelegramChannel: ptr("-1001234567890")}); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	effective, err = resolver.Effective(ctx)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if effective.TelegramChannel.Value != "-1001234567890" || effective.TelegramChannel.Source != SourceStored {
		t.Errorf("频道 = %+v，期望数据库值", effective.TelegramChannel)
	}
	if effective.TelegramToken.Source != SourceEnv {
		t.Errorf("环境变量字段不应被数据库覆盖: %+v", effective.TelegramToken)
	}
}

func TestUpdateClearsStoredValueToFallback(t *testing.T) {
	ctx := context.Background()
	resolver := newResolver(t, Env{})

	if err := resolver.Update(ctx, Patch{TelegraphAuthor: ptr("SeSe")}); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	effective, _ := resolver.Effective(ctx)
	if effective.TelegraphAuthor.Source != SourceStored {
		t.Fatalf("作者名 = %+v，期望数据库值", effective.TelegraphAuthor)
	}

	// 空串表示清除数据库值。
	if err := resolver.Update(ctx, Patch{TelegraphAuthor: ptr("")}); err != nil {
		t.Fatalf("清除应成功: %v", err)
	}
	effective, _ = resolver.Effective(ctx)
	if effective.TelegraphAuthor.Set() || effective.TelegraphAuthor.Source != SourceNone {
		t.Errorf("作者名 = %+v，期望清空", effective.TelegraphAuthor)
	}
}

// TestEffectiveIgnoresStoredEndpoint 确认上游地址只取环境变量或官方默认值，
// 历史遗留的数据库地址不会改变令牌的发送目的地。
func TestEffectiveIgnoresStoredEndpoint(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	resolver := NewResolver(store, Env{})
	if err := store.Set(ctx, "telegram.api_base", "http://127.0.0.1:1"); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	if err := store.Set(ctx, "telegraph.api_base", "http://127.0.0.1:1"); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	effective, err := resolver.Effective(ctx)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if effective.TelegramAPIBase.Source != SourceDefault || effective.TelegramAPIBase.Value != DefaultTelegramAPIBase {
		t.Errorf("Telegram 地址 = %+v，期望忽略数据库值", effective.TelegramAPIBase)
	}
	if effective.TelegraphAPIBase.Source != SourceDefault || effective.TelegraphAPIBase.Value != DefaultTelegraphAPIBase {
		t.Errorf("Telegraph 地址 = %+v，期望忽略数据库值", effective.TelegraphAPIBase)
	}
}

func TestUpdateRejectsEnvManagedFields(t *testing.T) {
	ctx := context.Background()
	resolver := newResolver(t, Env{TelegramToken: "env-token", TelegraphToken: "env-telegraph"})

	err := resolver.Update(ctx, Patch{
		TelegramToken:   ptr("stored-token"),
		TelegraphAuthor: ptr("new-author"),
	})
	var conflict *FieldConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("错误 = %v，期望 FieldConflict", err)
	}
	if len(conflict.Fields) != 1 || conflict.Fields[0] != "telegram.token" {
		t.Errorf("冲突字段 = %v，期望只有 telegram.token", conflict.Fields)
	}

	// 与环境变量相同的取值视为不变，不报冲突。
	if err := resolver.Update(ctx, Patch{TelegramToken: ptr("env-token")}); err != nil {
		t.Errorf("重复写入相同值应成功: %v", err)
	}
}

func TestUpdateValidatesValues(t *testing.T) {
	ctx := context.Background()
	resolver := newResolver(t, Env{})

	cases := []struct {
		patch Patch
		field string
	}{
		{Patch{TelegramToken: ptr("bad token")}, "telegram.token"},
		{Patch{TelegramChannel: ptr("not a channel")}, "telegram.channel_id"},
		{Patch{TelegraphAuthor: ptr(string(make([]rune, 101)))}, "telegraph.author_name"},
	}
	for _, tc := range cases {
		err := resolver.Update(ctx, tc.patch)
		var problem *FieldProblem
		if !errors.As(err, &problem) {
			t.Errorf("用例 %v 错误 = %v，期望 FieldProblem", tc.field, err)
			continue
		}
		if len(problem.Fields) != 1 || problem.Fields[0] != tc.field {
			t.Errorf("出错字段 = %v，期望 %s", problem.Fields, tc.field)
		}
		if !errors.Is(err, ErrInvalidValue) {
			t.Errorf("错误 %v 未归入 ErrInvalidValue", err)
		}
	}
}

func TestPatchEmpty(t *testing.T) {
	if !(Patch{}).Empty() {
		t.Error("空补丁应报告为空")
	}
	if (Patch{TelegramToken: ptr("x")}).Empty() {
		t.Error("含字段的补丁不应报告为空")
	}
}

// TestUpdateWithBotWritesOwnershipTogether 验证 Bot 归属与配置落在同一个事务：
// 归属写入失败时配置一并回滚，避免数据库是 B、运行时是 A。
func TestUpdateWithBotWritesOwnershipTogether(t *testing.T) {
	ctx := context.Background()
	resolver := newResolver(t, Env{})

	if err := resolver.UpdateWithBot(ctx, Patch{TelegramToken: ptr("111:test-one")}, 111); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	stored, err := resolver.store.Snapshot(ctx)
	if err != nil {
		t.Fatalf("快照失败: %v", err)
	}
	if stored[KeyTelegramBotID] != "111" {
		t.Errorf("Bot 归属 = %q，期望 111", stored[KeyTelegramBotID])
	}

	// 让本次写入失败，验证归属与配置一起回滚。
	if _, err := resolver.store.handle.ExecContext(ctx,
		`CREATE TRIGGER fail_token BEFORE INSERT ON settings
		 WHEN NEW.key = 'telegram.bot_token' BEGIN SELECT RAISE(ABORT, 'fail'); END`); err != nil {
		t.Fatalf("创建触发器失败: %v", err)
	}
	if err := resolver.UpdateWithBot(ctx, Patch{TelegramToken: ptr("222:test-two")}, 222); err == nil {
		t.Fatal("写入失败时应返回错误")
	}

	stored, err = resolver.store.Snapshot(ctx)
	if err != nil {
		t.Fatalf("快照失败: %v", err)
	}
	if stored[KeyTelegramToken] != "111:test-one" {
		t.Errorf("令牌 = %q，期望保留旧值", stored[KeyTelegramToken])
	}
	if stored[KeyTelegramBotID] != "111" {
		t.Errorf("Bot 归属 = %q，失败时不应写入新编号", stored[KeyTelegramBotID])
	}
}

// ptr 返回指向给定值的指针，便于构造补丁。
func ptr(value string) *string { return &value }
