package settings

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/littleseven2003/SeSeGraph/backend/internal/netclient"
)

// 配置来源标记，用于向界面说明某项取值来自环境变量、数据库还是默认值。
const (
	SourceEnv     = "env"
	SourceStored  = "stored"
	SourceDefault = "default"
	SourceNone    = "none"
)

// 上游默认地址。
const (
	DefaultTelegramAPIBase  = "https://api.telegram.org"
	DefaultTelegraphAPIBase = "https://api.telegra.ph"
)

// 取值上限，超出后拒绝保存，避免把异常输入写进数据库。
const (
	maxCredentialRunes = 200
	maxChannelRunes    = 100
	maxAuthorRunes     = 100
	maxAPIBaseRunes    = 200
)

// 频道标识允许 @用户名 或 -100 开头的数字编号。
var channelPattern = regexp.MustCompile(`^(?:@[A-Za-z][A-Za-z0-9_]{3,63}|-?\d{5,20})$`)

// 配置错误类别，调用方据此映射到不同状态码。
var (
	// ErrInvalidValue 表示提交的取值不符合要求，具体字段名由 FieldProblem 给出。
	ErrInvalidValue = errors.New("配置取值不合法")
)

// FieldProblem 携带出错的字段名，便于界面定位，不包含任何取值。
type FieldProblem struct {
	Fields []string
}

func (e *FieldProblem) Error() string { return ErrInvalidValue.Error() }

// Is 让调用方可以用 errors.Is(err, ErrInvalidValue) 判断类别。
func (e *FieldProblem) Is(target error) bool { return target == ErrInvalidValue }

// FieldConflict 表示字段由环境变量提供，不能在界面覆盖。
type FieldConflict struct {
	Fields []string
}

func (e *FieldConflict) Error() string { return "字段由环境变量提供" }

// Env 是启动时读取的环境变量，作为数据库配置的兜底，优先级高于数据库。
type Env struct {
	TelegramToken    string
	TelegramChannel  string
	TelegramAPIBase  string
	TelegraphToken   string
	TelegraphAuthor  string
	TelegraphAPIBase string
}

// Value 是一项生效配置及其来源。敏感项只通过 Value 在校验与自检内部使用，不对外输出。
type Value struct {
	Value  string
	Source string
}

// Set 报告该项是否已有可用取值。
func (v Value) Set() bool { return v.Value != "" }

// Effective 是当前生效的配置快照。
type Effective struct {
	TelegramToken    Value
	TelegramChannel  Value
	TelegramAPIBase  Value
	TelegraphToken   Value
	TelegraphAuthor  Value
	TelegraphAPIBase Value
	// ProxyMode 是代理模式，取值 direct / system / custom。
	ProxyMode Value
	// ProxyURL 是自定义代理地址，可能带账密，只在传输层内部使用，任何接口都不回显。
	ProxyURL Value
	// ProxyTelegram 与 ProxyTelegraph 分别控制两项上游是否启用代理。
	ProxyTelegram  bool
	ProxyTelegraph bool
}

// ProxyConfig 把生效配置转成传输层需要的代理配置。
func (e Effective) ProxyConfig() netclient.Config {
	return netclient.Config{
		Mode:      e.ProxyMode.Value,
		URL:       e.ProxyURL.Value,
		Telegram:  e.ProxyTelegram,
		Telegraph: e.ProxyTelegraph,
	}
}

// Resolver 结合环境变量与数据库解析生效配置。
type Resolver struct {
	store *Store
	env   Env
}

// NewResolver 创建配置解析器。store 为空时只使用环境变量与默认值。
func NewResolver(store *Store, env Env) *Resolver {
	return &Resolver{store: store, env: env}
}

// snapshot 读取一次配置表，调用方在同一份数据上解析全部字段。
func (r *Resolver) snapshot(ctx context.Context) (map[string]string, error) {
	if r.store == nil {
		return map[string]string{}, nil
	}
	return r.store.Snapshot(ctx)
}

// resolve 按「环境变量、数据库快照、默认值」的顺序解析单项配置。
func resolve(envValue string, stored map[string]string, key, fallback string) Value {
	if value := strings.TrimSpace(envValue); value != "" {
		return Value{Value: value, Source: SourceEnv}
	}
	if value := strings.TrimSpace(stored[key]); value != "" {
		return Value{Value: value, Source: SourceStored}
	}
	if fallback != "" {
		return Value{Value: fallback, Source: SourceDefault}
	}
	return Value{Source: SourceNone}
}

// resolveEndpoint 解析上游地址：只接受环境变量或官方默认值。
// 上游地址决定令牌发往何处，因此不接受运行期写入，避免把已有凭据导向任意主机。
func resolveEndpoint(envValue, fallback string) Value {
	if value := strings.TrimSpace(envValue); value != "" {
		return Value{Value: value, Source: SourceEnv}
	}
	return Value{Value: fallback, Source: SourceDefault}
}

// effectiveFrom 在一份配置快照上解析生效配置。
func (r *Resolver) effectiveFrom(stored map[string]string) Effective {
	return Effective{
		TelegramToken:    resolve(r.env.TelegramToken, stored, KeyTelegramToken, ""),
		TelegramChannel:  resolve(r.env.TelegramChannel, stored, KeyTelegramChannel, ""),
		TelegramAPIBase:  resolveEndpoint(r.env.TelegramAPIBase, DefaultTelegramAPIBase),
		TelegraphToken:   resolve(r.env.TelegraphToken, stored, KeyTelegraphToken, ""),
		TelegraphAuthor:  resolve(r.env.TelegraphAuthor, stored, KeyTelegraphAuthorName, ""),
		TelegraphAPIBase: resolveEndpoint(r.env.TelegraphAPIBase, DefaultTelegraphAPIBase),
		ProxyMode:        resolveProxyMode(stored[KeyProxyMode]),
		ProxyURL:         resolve("", stored, KeyProxyURL, ""),
		ProxyTelegram:    stored[KeyProxyTelegram] == "1",
		ProxyTelegraph:   stored[KeyProxyTelegraph] == "1",
	}
}

// resolveProxyMode 解析代理模式，未设置时回落到直连。
func resolveProxyMode(raw string) Value {
	if value := strings.TrimSpace(raw); value != "" {
		return Value{Value: netclient.ModeOrDefault(value), Source: SourceStored}
	}
	return Value{Value: netclient.ModeDirect, Source: SourceDefault}
}

// Effective 在单次配置快照上解析当前生效配置。
func (r *Resolver) Effective(ctx context.Context) (Effective, error) {
	stored, err := r.snapshot(ctx)
	if err != nil {
		return Effective{}, err
	}
	return r.effectiveFrom(stored), nil
}

// Patch 描述一次设置更新：nil 表示不变，空串表示清除数据库值并回落到环境变量或默认值。
// 上游地址不在可写字段内：它决定令牌发往何处，只由环境变量或官方默认值提供。
type Patch struct {
	TelegramToken   *string
	TelegramChannel *string
	TelegraphAuthor *string
	// 代理配置：模式、自定义地址、两项上游各自的启用开关。
	ProxyMode      *string
	ProxyURL       *string
	ProxyTelegram  *bool
	ProxyTelegraph *bool
}

// Empty 报告补丁是否没有任何字段。
func (p Patch) Empty() bool {
	return p.TelegramToken == nil && p.TelegramChannel == nil && p.TelegraphAuthor == nil &&
		p.ProxyMode == nil && p.ProxyURL == nil && p.ProxyTelegram == nil && p.ProxyTelegraph == nil
}

// Prepare 校验补丁并返回应用后的生效配置，不写入数据库。
// 保存前可先用它装配候选图床，避免切换失败时落下一份跑不起来的配置。
func (r *Resolver) Prepare(ctx context.Context, patch Patch) (Effective, error) {
	stored, err := r.snapshot(ctx)
	if err != nil {
		return Effective{}, err
	}
	effective, _, _, err := r.classify(stored, patch)
	return effective, err
}

// Update 校验并写入配置。任何字段不合法或与环境变量冲突时整体不写入。
func (r *Resolver) Update(ctx context.Context, patch Patch) error {
	return r.UpdateWithBot(ctx, patch, 0)
}

// UpdateWithBot 校验并写入配置，同时把 Bot 归属写进同一个事务。
// botID 为 0 时不改动归属记录；任一步失败时配置与归属都不会写入，
// 调用方据此保证「数据库里的配置」与「运行时的提供方」不会各自一半是新值。
func (r *Resolver) UpdateWithBot(ctx context.Context, patch Patch, botID int64) error {
	if r.store == nil {
		return errors.New("配置存储不可用")
	}
	stored, err := r.snapshot(ctx)
	if err != nil {
		return err
	}
	_, values, removes, err := r.classify(stored, patch)
	if err != nil {
		return err
	}
	if botID != 0 {
		values[KeyTelegramBotID] = strconv.FormatInt(botID, 10)
	}
	// 删除与写入在同一事务内完成，任一步失败都不会留下半套配置。
	return r.store.Replace(ctx, values, removes)
}

// classify 把补丁归类为写入项、清理项，并返回应用后的生效配置。
func (r *Resolver) classify(stored map[string]string, patch Patch) (Effective, map[string]string, []string, error) {
	current := r.effectiveFrom(stored)

	values := make(map[string]string, 5)
	deletes := make([]string, 0, 5)
	conflicts := make([]string, 0, 5)
	invalid := make([]string, 0, 5)

	apply := func(raw *string, current Value, key, field string, validate func(string) error) {
		if raw == nil {
			return
		}
		value := strings.TrimSpace(*raw)
		if value == current.Value {
			return
		}
		if current.Source == SourceEnv {
			conflicts = append(conflicts, field)
			return
		}
		if value == "" {
			deletes = append(deletes, key)
			return
		}
		if validate != nil {
			if validate(value) != nil {
				invalid = append(invalid, field)
				return
			}
		}
		values[key] = value
	}

	apply(patch.TelegramToken, current.TelegramToken, KeyTelegramToken, "telegram.token", validateCredential)
	apply(patch.TelegramChannel, current.TelegramChannel, KeyTelegramChannel, "telegram.channel_id", validateChannel)
	apply(patch.TelegraphAuthor, current.TelegraphAuthor, KeyTelegraphAuthorName, "telegraph.author_name", validateAuthor)
	applyProxy(patch, current, values, &deletes, &invalid)

	if len(invalid) > 0 {
		return Effective{}, nil, nil, &FieldProblem{Fields: invalid}
	}
	if len(conflicts) > 0 {
		return Effective{}, nil, nil, &FieldConflict{Fields: conflicts}
	}

	merged := make(map[string]string, len(stored)+len(values))
	for key, value := range stored {
		merged[key] = value
	}
	for _, key := range deletes {
		delete(merged, key)
	}
	for key, value := range values {
		merged[key] = value
	}
	return r.effectiveFrom(merged), values, deletes, nil
}

// applyProxy 归类代理配置。模式与地址需要一起校验：
// 自定义模式必须有合法地址，任何模式下的地址都必须能解析。
func applyProxy(patch Patch, current Effective, values map[string]string, deletes *[]string, invalid *[]string) {
	if patch.ProxyMode == nil && patch.ProxyURL == nil && patch.ProxyTelegram == nil && patch.ProxyTelegraph == nil {
		return
	}

	mode := current.ProxyMode.Value
	if patch.ProxyMode != nil {
		mode = strings.TrimSpace(*patch.ProxyMode)
	}
	url := current.ProxyURL.Value
	if patch.ProxyURL != nil {
		url = strings.TrimSpace(*patch.ProxyURL)
	}

	normalized := netclient.ModeOrDefault(mode)
	if raw := strings.TrimSpace(mode); raw != "" && raw != normalized {
		*invalid = append(*invalid, "proxy.mode")
		return
	}
	if url != "" || normalized == netclient.ModeCustom {
		if err := netclient.Validate(netclient.ModeCustom, url); err != nil {
			*invalid = append(*invalid, "proxy.url")
			return
		}
	}

	if normalized != current.ProxyMode.Value {
		values[KeyProxyMode] = normalized
	}
	switch {
	case patch.ProxyURL == nil:
	case url == "":
		*deletes = append(*deletes, KeyProxyURL)
	case url != current.ProxyURL.Value:
		values[KeyProxyURL] = url
	}
	if patch.ProxyTelegram != nil && *patch.ProxyTelegram != current.ProxyTelegram {
		values[KeyProxyTelegram] = proxyFlag(*patch.ProxyTelegram)
	}
	if patch.ProxyTelegraph != nil && *patch.ProxyTelegraph != current.ProxyTelegraph {
		values[KeyProxyTelegraph] = proxyFlag(*patch.ProxyTelegraph)
	}
}

// proxyFlag 把启用开关转成存储取值。
func proxyFlag(enabled bool) string {
	if enabled {
		return "1"
	}
	return "0"
}

// validateCredential 校验令牌的形态：非空、无空白字符、长度受限。
// 不校验具体平台格式，避免把上游后续调整写死在这里。
func validateCredential(value string) error {
	if value == "" || utf8.RuneCountInString(value) > maxCredentialRunes {
		return ErrInvalidValue
	}
	if strings.ContainsAny(value, " \t\r\n") {
		return ErrInvalidValue
	}
	return nil
}

// validateChannel 校验频道标识。
func validateChannel(value string) error {
	if utf8.RuneCountInString(value) > maxChannelRunes || !channelPattern.MatchString(value) {
		return ErrInvalidValue
	}
	return nil
}

// validateAuthor 校验作者名，允许留空。
func validateAuthor(value string) error {
	if utf8.RuneCountInString(value) > maxAuthorRunes {
		return ErrInvalidValue
	}
	return nil
}
