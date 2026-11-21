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

	if err := resolver.Update(ctx, Patch{TelegramAPIBase: ptr("http://127.0.0.1:9090")}); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	effective, _ := resolver.Effective(ctx)
	if effective.TelegramAPIBase.Source != SourceStored {
		t.Fatalf("地址 = %+v，期望数据库值", effective.TelegramAPIBase)
	}

	// 空串表示清除数据库值并回落到默认地址。
	if err := resolver.Update(ctx, Patch{TelegramAPIBase: ptr("")}); err != nil {
		t.Fatalf("清除应成功: %v", err)
	}
	effective, _ = resolver.Effective(ctx)
	if effective.TelegramAPIBase.Value != DefaultTelegramAPIBase || effective.TelegramAPIBase.Source != SourceDefault {
		t.Errorf("地址 = %+v，期望回落到默认值", effective.TelegramAPIBase)
	}

	// 环境变量提供的字段不能用空串清除。
	envResolver := newResolver(t, Env{TelegramAPIBase: "http://127.0.0.1:9090"})
	if err := envResolver.Update(ctx, Patch{TelegramAPIBase: ptr("")}); !errors.Is(err, ErrInvalidValue) && err == nil {
		t.Error("环境变量字段的空串清除应被拒绝")
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
		{Patch{TelegramAPIBase: ptr("ftp://example.com")}, "telegram.api_base"},
		{Patch{TelegraphAPIBase: ptr("https://example.com/?a=1")}, "telegraph.api_base"},
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

// ptr 返回指向给定值的指针，便于构造补丁。
func ptr(value string) *string { return &value }
