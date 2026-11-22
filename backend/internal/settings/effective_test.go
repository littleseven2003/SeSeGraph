package settings

import (
	"context"
	"errors"
	"testing"

	"github.com/littleseven2003/SeSeGraph/backend/internal/netclient"
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

// TestProxyDefaultsAndUpdate 验证代理默认直连、保存后按存储值生效。
func TestProxyDefaultsAndUpdate(t *testing.T) {
	ctx := context.Background()
	resolver := newResolver(t, Env{})

	effective, err := resolver.Effective(ctx)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if effective.ProxyMode.Value != netclient.ModeDirect || effective.ProxyMode.Source != SourceDefault {
		t.Errorf("默认代理模式 = %+v，期望 direct", effective.ProxyMode)
	}
	if effective.ProxyURL.Set() || effective.ProxyEnabled(netclient.ServiceTelegram) || effective.ProxyEnabled(netclient.ServiceTelegraph) {
		t.Errorf("未配置时代理地址与开关应为空: %+v", effective.ProxyConfig())
	}

	mode, rawURL := netclient.ModeCustom, "socks5://user:pass@127.0.0.1:1080"
	enabled, disabled := true, false
	if err := resolver.Update(ctx, Patch{
		ProxyMode:      &mode,
		ProxyURL:       &rawURL,
		ProxyTelegram:  &enabled,
		ProxyTelegraph: &disabled,
	}); err != nil {
		t.Fatalf("保存失败: %v", err)
	}

	effective, err = resolver.Effective(ctx)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if effective.ProxyMode.Value != netclient.ModeCustom || effective.ProxyMode.Source != SourceStored {
		t.Errorf("代理模式 = %+v，期望 custom/stored", effective.ProxyMode)
	}
	if effective.ProxyURL.Value != rawURL || effective.ProxyURL.Source != SourceStored {
		t.Errorf("代理地址 = %+v，期望按存储值生效", effective.ProxyURL)
	}
	if !effective.ProxyEnabled(netclient.ServiceTelegram) || effective.ProxyEnabled(netclient.ServiceTelegraph) {
		t.Errorf("开关 = telegram:%v telegraph:%v，期望只对 Telegram 启用",
			effective.ProxyEnabled(netclient.ServiceTelegram), effective.ProxyEnabled(netclient.ServiceTelegraph))
	}
	config := effective.ProxyConfig()
	if config.Mode != netclient.ModeCustom || config.URL != rawURL || !config.Telegram || config.Telegraph {
		t.Errorf("传输层配置 = %+v，与生效配置不一致", config)
	}

	// 自定义模式不允许清空地址：先切回直连才能清除。
	empty := ""
	var problem *FieldProblem
	if err := resolver.Update(ctx, Patch{ProxyURL: &empty}); !errors.As(err, &problem) {
		t.Fatalf("自定义模式清空地址应被拒绝，错误 = %v", err)
	}
	direct := netclient.ModeDirect
	if err := resolver.Update(ctx, Patch{ProxyMode: &direct, ProxyURL: &empty}); err != nil {
		t.Fatalf("切回直连并清空失败: %v", err)
	}
	effective, _ = resolver.Effective(ctx)
	if effective.ProxyURL.Set() {
		t.Errorf("清空后代理地址 = %+v，期望未设置", effective.ProxyURL)
	}
	if effective.ProxyMode.Value != netclient.ModeDirect || effective.ProxyMode.Source != SourceStored {
		t.Errorf("模式 = %+v，期望 direct", effective.ProxyMode)
	}
}

// TestProxyRejectsInvalidValues 验证模式与地址的校验，非法取值整体不写入。
func TestProxyRejectsInvalidValues(t *testing.T) {
	ctx := context.Background()
	resolver := newResolver(t, Env{})

	cases := []struct {
		name  string
		patch Patch
		field string
	}{
		{name: "自定义缺少地址", patch: Patch{ProxyMode: ptr(netclient.ModeCustom)}, field: "proxy.url"},
		{name: "协议不支持", patch: Patch{ProxyMode: ptr(netclient.ModeCustom), ProxyURL: ptr("ftp://127.0.0.1:21")}, field: "proxy.url"},
		{name: "未知模式", patch: Patch{ProxyMode: ptr("proxy")}, field: "proxy.mode"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			err := resolver.Update(ctx, test.patch)
			var problem *FieldProblem
			if !errors.As(err, &problem) {
				t.Fatalf("错误 = %v，期望字段错误", err)
			}
			if len(problem.Fields) != 1 || problem.Fields[0] != test.field {
				t.Fatalf("字段 = %v，期望 %s", problem.Fields, test.field)
			}
		})
	}

	// 非法模式不应写入数据库。
	effective, _ := resolver.Effective(ctx)
	if effective.ProxyMode.Source != SourceDefault {
		t.Errorf("非法模式不应落库: %+v", effective.ProxyMode)
	}
}

// TestPatchEmptyWithProxyFields 验证代理字段也算有效补丁。
func TestPatchEmptyWithProxyFields(t *testing.T) {
	enabled := true
	if (Patch{ProxyMode: ptr(netclient.ModeSystem)}).Empty() {
		t.Error("代理模式补丁不应报告为空")
	}
	if (Patch{ProxyTelegram: &enabled}).Empty() {
		t.Error("代理开关补丁不应报告为空")
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

// TestProxyFromEnvironment 验证代理配置可由环境变量托管：取值优先、来源标记，
// 且模式与地址不允许被数据库覆盖。
func TestProxyFromEnvironment(t *testing.T) {
	ctx := context.Background()
	resolver := NewResolver(newStore(t), Env{
		ProxyMode:      "system",
		ProxyURL:       "http://env-proxy.test:3128",
		ProxyTelegram:  "true",
		ProxyTelegraph: "false",
	})

	effective, err := resolver.Effective(ctx)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if effective.ProxyMode.Value != netclient.ModeSystem || effective.ProxyMode.Source != SourceEnv {
		t.Errorf("模式 = %+v，期望 system/env", effective.ProxyMode)
	}
	if effective.ProxyURL.Value != "http://env-proxy.test:3128" || effective.ProxyURL.Source != SourceEnv {
		t.Errorf("地址 = %+v，期望环境变量取值", effective.ProxyURL)
	}
	if !effective.ProxyEnabled(netclient.ServiceTelegram) || effective.ProxyEnabled(netclient.ServiceTelegraph) {
		t.Error("开关应与环境变量一致")
	}

	custom := netclient.ModeCustom
	err = resolver.Update(ctx, Patch{ProxyMode: &custom})
	var conflict *FieldConflict
	if !errors.As(err, &conflict) || len(conflict.Fields) != 1 || conflict.Fields[0] != "proxy.mode" {
		t.Fatalf("错误 = %v，期望 proxy.mode 冲突", err)
	}

	enabled := false
	err = resolver.Update(ctx, Patch{ProxyTelegram: &enabled})
	if !errors.As(err, &conflict) || len(conflict.Fields) != 1 || conflict.Fields[0] != "proxy.telegram" {
		t.Fatalf("错误 = %v，期望 proxy.telegram 冲突", err)
	}

	// 环境变量只提供开关时，模式与地址仍可写入数据库。
	resolver = NewResolver(newStore(t), Env{ProxyTelegram: "1"})
	if err := resolver.Update(ctx, Patch{ProxyMode: &custom, ProxyURL: ptr("socks5://127.0.0.1:1080")}); err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	effective, _ = resolver.Effective(ctx)
	if effective.ProxyMode.Source != SourceStored || effective.ProxyMode.Value != netclient.ModeCustom {
		t.Errorf("模式 = %+v，期望数据库取值", effective.ProxyMode)
	}
	if !effective.ProxyEnabled(netclient.ServiceTelegram) {
		t.Error("环境变量开关应优先于数据库")
	}
}

// TestProxyMixedSourcesPerField 验证代理字段逐个判断托管状态：
// 只托管地址时模式可改、地址不可改；只托管模式时地址仍可写入。
func TestProxyMixedSourcesPerField(t *testing.T) {
	ctx := context.Background()

	// 只托管地址：模式可以改，地址改动返回冲突，且不会把被遮蔽的值写进数据库。
	resolver := NewResolver(newStore(t), Env{ProxyURL: "http://env-proxy.test:3128"})
	custom := netclient.ModeCustom
	if err := resolver.Update(ctx, Patch{ProxyMode: &custom}); err != nil {
		t.Fatalf("托管地址时仍应允许改模式: %v", err)
	}
	effective, _ := resolver.Effective(ctx)
	if effective.ProxyMode.Value != netclient.ModeCustom || effective.ProxyMode.Source != SourceStored {
		t.Errorf("模式 = %+v，期望数据库取值 custom", effective.ProxyMode)
	}
	if effective.ProxyURL.Source != SourceEnv {
		t.Errorf("地址来源 = %q，期望环境变量", effective.ProxyURL.Source)
	}

	other := "socks5://127.0.0.1:1080"
	err := resolver.Update(ctx, Patch{ProxyURL: &other})
	var conflict *FieldConflict
	if !errors.As(err, &conflict) || len(conflict.Fields) != 1 || conflict.Fields[0] != "proxy.url" {
		t.Fatalf("错误 = %v，期望 proxy.url 冲突", err)
	}
	if stored, _, _ := resolver.store.Get(ctx, KeyProxyURL); stored != "" {
		t.Errorf("被环境变量遮蔽的地址不应落库: %q", stored)
	}

	// 只托管模式：地址可以正常写入。
	resolver = NewResolver(newStore(t), Env{ProxyMode: "custom"})
	if err := resolver.Update(ctx, Patch{ProxyURL: &other}); err != nil {
		t.Fatalf("托管模式时仍应允许写地址: %v", err)
	}
	effective, _ = resolver.Effective(ctx)
	if effective.ProxyURL.Value != other || effective.ProxyURL.Source != SourceStored {
		t.Errorf("地址 = %+v，期望数据库取值", effective.ProxyURL)
	}
	direct := netclient.ModeDirect
	err = resolver.Update(ctx, Patch{ProxyMode: &direct})
	if !errors.As(err, &conflict) || len(conflict.Fields) != 1 || conflict.Fields[0] != "proxy.mode" {
		t.Fatalf("错误 = %v，期望 proxy.mode 冲突", err)
	}
}

// TestUploadModeDefault 验证默认画质来自环境变量、数据库或默认值，非法取值不影响启动。
func TestUploadModeDefault(t *testing.T) {
	ctx := context.Background()

	resolver := newResolver(t, Env{})
	effective, err := resolver.Effective(ctx)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if effective.UploadMode.Value != DefaultUploadMode || effective.UploadMode.Source != SourceDefault {
		t.Errorf("默认画质 = %+v，期望 photo / default", effective.UploadMode)
	}

	envResolver := newResolver(t, Env{UploadMode: "document"})
	effective, err = envResolver.Effective(ctx)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if effective.UploadMode.Value != "document" || effective.UploadMode.Source != SourceEnv {
		t.Errorf("环境变量画质 = %+v，期望 document / env", effective.UploadMode)
	}

	invalid := newResolver(t, Env{UploadMode: "ultra"})
	effective, err = invalid.Effective(ctx)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if effective.UploadMode.Value != DefaultUploadMode {
		t.Errorf("非法环境值应回落默认，实际 %+v", effective.UploadMode)
	}
}

// TestMetaSettingsDefaultsAndUpdate 验证说明文件候选名与解析方式的默认值、规整与校验。
func TestMetaSettingsDefaultsAndUpdate(t *testing.T) {
	ctx := context.Background()
	resolver := newResolver(t, Env{})

	effective, err := resolver.Effective(ctx)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if effective.MetaFilenames.Value != DefaultMetaFilenames || effective.MetaFilenames.Source != SourceDefault {
		t.Errorf("默认说明文件名 = %+v，期望 %q / default", effective.MetaFilenames, DefaultMetaFilenames)
	}
	if effective.MetaParseMode.Value != DefaultMetaParseMode {
		t.Errorf("默认解析方式 = %+v，期望 %q", effective.MetaParseMode, DefaultMetaParseMode)
	}

	names := []string{" Info.TXT ", "meta.txt", "info.txt", "自定义.md"}
	mode := "key-value"
	if err := resolver.Update(ctx, Patch{MetaFilenames: &names, MetaParseMode: &mode}); err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	effective, err = resolver.Effective(ctx)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if effective.MetaFilenames.Value != "info.txt,meta.txt,自定义.md" {
		t.Errorf("规整后文件名 = %q，期望小写去重", effective.MetaFilenames.Value)
	}
	if effective.MetaFilenames.Source != SourceStored || effective.MetaParseMode.Value != "key-value" {
		t.Errorf("保存后配置 = %+v / %+v", effective.MetaFilenames, effective.MetaParseMode)
	}

	// 空列表与空串表示清除数据库值，回落默认。
	empty := []string{}
	emptyMode := ""
	if err := resolver.Update(ctx, Patch{MetaFilenames: &empty, MetaParseMode: &emptyMode}); err != nil {
		t.Fatalf("清除失败: %v", err)
	}
	effective, err = resolver.Effective(ctx)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if effective.MetaFilenames.Value != DefaultMetaFilenames || effective.MetaParseMode.Value != DefaultMetaParseMode {
		t.Errorf("清除后应回落默认，实际 %+v / %+v", effective.MetaFilenames, effective.MetaParseMode)
	}

	// 非法取值整体拒绝。
	bad := []string{"../etc/passwd"}
	if err := resolver.Update(ctx, Patch{MetaFilenames: &bad}); !errors.Is(err, ErrInvalidValue) {
		t.Errorf("非法文件名错误 = %v，期望 ErrInvalidValue", err)
	}
}
