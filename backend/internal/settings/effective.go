package settings

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
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

// resolve 按「环境变量、数据库、默认值」的顺序解析单项配置。
func (r *Resolver) resolve(ctx context.Context, envValue, key, fallback string) (Value, error) {
	if value := strings.TrimSpace(envValue); value != "" {
		return Value{Value: value, Source: SourceEnv}, nil
	}
	if r.store != nil {
		stored, ok, err := r.store.Get(ctx, key)
		if err != nil {
			return Value{}, err
		}
		if ok {
			if value := strings.TrimSpace(stored); value != "" {
				return Value{Value: value, Source: SourceStored}, nil
			}
		}
	}
	if fallback != "" {
		return Value{Value: fallback, Source: SourceDefault}, nil
	}
	return Value{Source: SourceNone}, nil
}

// Effective 解析当前生效配置。
func (r *Resolver) Effective(ctx context.Context) (Effective, error) {
	var effective Effective
	fields := []struct {
		dst      *Value
		envValue string
		key      string
		fallback string
	}{
		{&effective.TelegramToken, r.env.TelegramToken, KeyTelegramToken, ""},
		{&effective.TelegramChannel, r.env.TelegramChannel, KeyTelegramChannel, ""},
		{&effective.TelegramAPIBase, r.env.TelegramAPIBase, KeyTelegramAPIBase, DefaultTelegramAPIBase},
		{&effective.TelegraphToken, r.env.TelegraphToken, KeyTelegraphToken, ""},
		{&effective.TelegraphAuthor, r.env.TelegraphAuthor, KeyTelegraphAuthorName, ""},
		{&effective.TelegraphAPIBase, r.env.TelegraphAPIBase, KeyTelegraphAPIBase, DefaultTelegraphAPIBase},
	}
	for _, field := range fields {
		resolved, resolveErr := r.resolve(ctx, field.envValue, field.key, field.fallback)
		if resolveErr != nil {
			return Effective{}, resolveErr
		}
		*field.dst = resolved
	}
	return effective, nil
}

// Patch 描述一次设置更新：nil 表示不变，空串表示清除数据库值并回落到环境变量或默认值。
type Patch struct {
	TelegramToken    *string
	TelegramChannel  *string
	TelegramAPIBase  *string
	TelegraphAuthor  *string
	TelegraphAPIBase *string
}

// Empty 报告补丁是否没有任何字段。
func (p Patch) Empty() bool {
	return p.TelegramToken == nil && p.TelegramChannel == nil && p.TelegramAPIBase == nil &&
		p.TelegraphAuthor == nil && p.TelegraphAPIBase == nil
}

// Update 校验并写入配置。任何字段不合法或与环境变量冲突时整体不写入。
func (r *Resolver) Update(ctx context.Context, patch Patch) error {
	if r.store == nil {
		return errors.New("配置存储不可用")
	}
	current, err := r.Effective(ctx)
	if err != nil {
		return err
	}

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
	apply(patch.TelegramAPIBase, current.TelegramAPIBase, KeyTelegramAPIBase, "telegram.api_base", validateAPIBase)
	apply(patch.TelegraphAuthor, current.TelegraphAuthor, KeyTelegraphAuthorName, "telegraph.author_name", validateAuthor)
	apply(patch.TelegraphAPIBase, current.TelegraphAPIBase, KeyTelegraphAPIBase, "telegraph.api_base", validateAPIBase)

	if len(invalid) > 0 {
		return &FieldProblem{Fields: invalid}
	}
	if len(conflicts) > 0 {
		return &FieldConflict{Fields: conflicts}
	}
	for _, key := range deletes {
		if err := r.store.Delete(ctx, key); err != nil {
			return err
		}
	}
	if len(values) > 0 {
		if err := r.store.SetMany(ctx, values); err != nil {
			return err
		}
	}
	return nil
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

// validateAPIBase 校验上游地址：只接受 http/https，且不能带查询串、片段或内嵌凭据。
func validateAPIBase(value string) error {
	if value == "" || utf8.RuneCountInString(value) > maxAPIBaseRunes {
		return ErrInvalidValue
	}
	if strings.ContainsAny(value, " \t\r\n") {
		return ErrInvalidValue
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return ErrInvalidValue
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return ErrInvalidValue
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || strings.Contains(value, "#") {
		return ErrInvalidValue
	}
	return nil
}
