package settings

import (
	"context"
	"errors"
	"fmt"
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

// 发帖默认值与说明文件解析规则的默认取值。
const (
	// DefaultUploadMode 是新任务的默认画质模式。
	DefaultUploadMode = "photo"
	// DefaultMetaFilenames 是压缩包内说明文件的默认候选名。
	DefaultMetaFilenames = "info.txt,meta.txt,readme.txt"
	// DefaultMetaParseMode 是说明文件的默认解析方式。
	DefaultMetaParseMode = "first-line"
	// 链接生成方式：按标题生成，或先建英文页再改回原题。
	PathModeTitle = "title"
	PathModeSlug  = "slug"
	// DefaultPathMode 沿用按标题生成，升级后行为不变。
	DefaultPathMode = PathModeTitle
)

// 说明文件解析方式。
const (
	MetaParseFirstLine = "first-line"
	MetaParseKeyValue  = "key-value"
)

// 取值上限，超出后拒绝保存，避免把异常输入写进数据库。
const (
	maxCredentialRunes = 200
	maxChannelRunes    = 100
	maxAuthorRunes     = 100
	maxAPIBaseRunes    = 200
	// 说明文件名最多 10 个、每个不超过 64 字符。
	maxMetaFilenames = 10
	maxMetaNameRunes = 64
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
	// 代理配置：模式、自定义地址与两项上游的启用开关（true / false）。
	ProxyMode      string
	ProxyURL       string
	ProxyTelegram  string
	ProxyTelegraph string
	// UploadMode 是新任务的默认画质模式，photo 或 document。
	UploadMode string
	// TelegraphPathMode 是 Telegraph 链接生成方式，title 或 slug。
	TelegraphPathMode string
	// 流量看护与自动备份：字节阈值、是否启用、每日时刻与保留份数。
	TrafficAlertBytes string
	TrafficLimitBytes string
	BackupEnabled     string
	BackupTime        string
	BackupKeep        string
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
	// ProxyTelegram 与 ProxyTelegraph 是两项上游的启用开关与来源（true / false）。
	ProxyTelegram  Value
	ProxyTelegraph Value
	// UploadMode 是新任务的默认画质模式。
	UploadMode Value
	// TelegraphPathMode 是 Telegraph 链接生成方式。
	TelegraphPathMode Value
	// TrafficAlertBytes 与 TrafficLimitBytes 是月度预警与硬上限，0 表示关闭。
	TrafficAlertBytes Value
	TrafficLimitBytes Value
	// BackupEnabled、BackupTime 与 BackupKeep 控制自动备份。
	BackupEnabled Value
	BackupTime    Value
	BackupKeep    Value
	// MetaFilenames 是压缩包内说明文件的候选名（逗号分隔）。
	MetaFilenames Value
	// MetaParseMode 是说明文件的默认解析方式。
	MetaParseMode Value
}

// ProxyEnabled 报告某项服务是否启用代理。
func (e Effective) ProxyEnabled(service netclient.Service) bool {
	value := e.ProxyTelegram
	if service == netclient.ServiceTelegraph {
		value = e.ProxyTelegraph
	}
	return strings.EqualFold(strings.TrimSpace(value.Value), "true")
}

// ProxyConfig 把生效配置转成传输层需要的代理配置。
func (e Effective) ProxyConfig() netclient.Config {
	return netclient.Config{
		Mode:      e.ProxyMode.Value,
		URL:       e.ProxyURL.Value,
		Telegram:  e.ProxyEnabled(netclient.ServiceTelegram),
		Telegraph: e.ProxyEnabled(netclient.ServiceTelegraph),
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
		TelegramToken:     resolve(r.env.TelegramToken, stored, KeyTelegramToken, ""),
		TelegramChannel:   resolve(r.env.TelegramChannel, stored, KeyTelegramChannel, ""),
		TelegramAPIBase:   resolveEndpoint(r.env.TelegramAPIBase, DefaultTelegramAPIBase),
		TelegraphToken:    resolve(r.env.TelegraphToken, stored, KeyTelegraphToken, ""),
		TelegraphAuthor:   resolve(r.env.TelegraphAuthor, stored, KeyTelegraphAuthorName, ""),
		TelegraphAPIBase:  resolveEndpoint(r.env.TelegraphAPIBase, DefaultTelegraphAPIBase),
		ProxyMode:         resolveProxyMode(r.env.ProxyMode, stored[KeyProxyMode]),
		ProxyURL:          resolve(r.env.ProxyURL, stored, KeyProxyURL, ""),
		ProxyTelegram:     resolveFlag(r.env.ProxyTelegram, stored[KeyProxyTelegram]),
		ProxyTelegraph:    resolveFlag(r.env.ProxyTelegraph, stored[KeyProxyTelegraph]),
		UploadMode:        resolveUploadMode(r.env.UploadMode, stored[KeyUploadMode]),
		TelegraphPathMode: resolvePathMode(r.env.TelegraphPathMode, stored[KeyTelegraphPathMode]),
		TrafficAlertBytes: resolveNumber(r.env.TrafficAlertBytes, stored[KeyTrafficAlert], "0"),
		TrafficLimitBytes: resolveNumber(r.env.TrafficLimitBytes, stored[KeyTrafficLimit], "0"),
		BackupEnabled:     resolveFlag(r.env.BackupEnabled, stored[KeyBackupEnabled]),
		BackupTime:        resolveBackupTime(r.env.BackupTime, stored[KeyBackupTime]),
		BackupKeep:        resolveNumber(r.env.BackupKeep, stored[KeyBackupKeep], "7"),
		MetaFilenames:     resolve("", stored, KeyMetaFilenames, DefaultMetaFilenames),
		MetaParseMode:     resolveMetaParseMode(stored[KeyMetaParseMode]),
	}
}

// resolveUploadMode 解析新任务默认画质：环境变量优先，其次数据库，非法取值回落到 photo。
// 环境变量写错时只回落默认值，不影响服务启动。
func resolveUploadMode(envMode, storedMode string) Value {
	if value := normalizeUploadMode(envMode); value != "" {
		return Value{Value: value, Source: SourceEnv}
	}
	if value := normalizeUploadMode(storedMode); value != "" {
		return Value{Value: value, Source: SourceStored}
	}
	return Value{Value: DefaultUploadMode, Source: SourceDefault}
}

// resolvePathMode 解析 Telegraph 链接生成方式：环境变量优先，非法取值回落默认。
func resolvePathMode(envMode, storedMode string) Value {
	if value := normalizePathMode(envMode); value != "" {
		return Value{Value: value, Source: SourceEnv}
	}
	if value := normalizePathMode(storedMode); value != "" {
		return Value{Value: value, Source: SourceStored}
	}
	return Value{Value: DefaultPathMode, Source: SourceDefault}
}

// normalizePathMode 把取值归一化为 title 或 slug，非法时返回空串。
func normalizePathMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case PathModeTitle:
		return PathModeTitle
	case PathModeSlug:
		return PathModeSlug
	default:
		return ""
	}
}

// resolveNumber 解析十进制数值配置：环境变量优先，非法值回落默认。
func resolveNumber(envValue, stored, fallback string) Value {
	if value := normalizeNumber(envValue); value != "" {
		return Value{Value: value, Source: SourceEnv}
	}
	if value := normalizeNumber(stored); value != "" {
		return Value{Value: value, Source: SourceStored}
	}
	return Value{Value: fallback, Source: SourceDefault}
}

// normalizeNumber 只接受非负十进制整数，其余返回空串。
func normalizeNumber(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return ""
	}
	return strconv.FormatInt(value, 10)
}

// resolveBackupTime 解析每日备份时刻，非法值回落到默认三点。
func resolveBackupTime(envValue, stored string) Value {
	if value := normalizeBackupTime(envValue); value != "" {
		return Value{Value: value, Source: SourceEnv}
	}
	if value := normalizeBackupTime(stored); value != "" {
		return Value{Value: value, Source: SourceStored}
	}
	return Value{Value: DefaultBackupTime, Source: SourceDefault}
}

// normalizeBackupTime 校验 HH:MM 形式并以两位数字返回。
func normalizeBackupTime(raw string) string {
	raw = strings.TrimSpace(raw)
	if matched, _ := regexp.MatchString(`^([01]?[0-9]|2[0-3]):[0-5][0-9]$`, raw); !matched {
		return ""
	}
	parts := strings.SplitN(raw, ":", 2)
	hour, _ := strconv.Atoi(parts[0])
	minute, _ := strconv.Atoi(parts[1])
	return fmt.Sprintf("%02d:%02d", hour, minute)
}

// validatePathMode 校验链接生成方式只取两个约定值。
func validatePathMode(value string) error {
	if normalizePathMode(value) == "" {
		return errors.New("链接生成方式不合法")
	}
	return nil
}

// validateUploadMode 校验画质模式只取两个约定值。
func validateUploadMode(value string) error {
	if normalizeUploadMode(value) == "" {
		return errors.New("画质模式不合法")
	}
	return nil
}

// normalizeUploadMode 把取值归一化为 photo 或 document，非法时返回空串。
func normalizeUploadMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "photo":
		return "photo"
	case "document":
		return "document"
	default:
		return ""
	}
}

// resolveMetaParseMode 解析说明文件默认解析方式，非法值回落到首行标题模式。
func resolveMetaParseMode(stored string) Value {
	switch strings.ToLower(strings.TrimSpace(stored)) {
	case MetaParseKeyValue:
		return Value{Value: MetaParseKeyValue, Source: SourceStored}
	case MetaParseFirstLine:
		return Value{Value: MetaParseFirstLine, Source: SourceStored}
	default:
		return Value{Value: DefaultMetaParseMode, Source: SourceDefault}
	}
}

// resolveProxyMode 解析代理模式：环境变量优先，其次数据库，最后回落到直连。
func resolveProxyMode(envMode, storedMode string) Value {
	if value := strings.TrimSpace(envMode); value != "" {
		return Value{Value: netclient.ModeOrDefault(value), Source: SourceEnv}
	}
	if value := strings.TrimSpace(storedMode); value != "" {
		return Value{Value: netclient.ModeOrDefault(value), Source: SourceStored}
	}
	return Value{Value: netclient.ModeDirect, Source: SourceDefault}
}

// resolveFlag 解析布尔开关，未设置时按未启用处理。
func resolveFlag(envValue, storedValue string) Value {
	if value := strings.TrimSpace(envValue); value != "" {
		return Value{Value: normalizeFlag(value), Source: SourceEnv}
	}
	if value := strings.TrimSpace(storedValue); value != "" {
		return Value{Value: normalizeFlag(value), Source: SourceStored}
	}
	return Value{Value: "false", Source: SourceDefault}
}

// normalizeFlag 把常见的开关写法归一化为 true / false。
func normalizeFlag(raw string) string {
	if strings.EqualFold(raw, "true") || raw == "1" || strings.EqualFold(raw, "yes") {
		return "true"
	}
	return "false"
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
	// 压缩包说明文件：候选文件名与默认解析方式。
	MetaFilenames *[]string
	MetaParseMode *string
	// 发帖默认画质与 Telegraph 链接生成方式。
	UploadMode        *string
	TelegraphPathMode *string
	// 流量看护与自动备份。
	TrafficAlertBytes *int64
	TrafficLimitBytes *int64
	BackupEnabled     *bool
	BackupTime        *string
	BackupKeep        *int64
}

// Empty 报告补丁是否没有任何字段。
func (p Patch) Empty() bool {
	return p.TelegramToken == nil && p.TelegramChannel == nil && p.TelegraphAuthor == nil &&
		p.ProxyMode == nil && p.ProxyURL == nil && p.ProxyTelegram == nil && p.ProxyTelegraph == nil &&
		p.MetaFilenames == nil && p.MetaParseMode == nil &&
		p.UploadMode == nil && p.TelegraphPathMode == nil &&
		p.TrafficAlertBytes == nil && p.TrafficLimitBytes == nil &&
		p.BackupEnabled == nil && p.BackupTime == nil && p.BackupKeep == nil
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
	applyProxy(patch, current, values, &deletes, &invalid, &conflicts)
	applyMeta(patch, current, values, &deletes, &invalid)
	apply(patch.UploadMode, current.UploadMode, KeyUploadMode, "post.upload_mode", validateUploadMode)
	apply(patch.TelegraphPathMode, current.TelegraphPathMode, KeyTelegraphPathMode, "telegraph.path_mode", validatePathMode)
	applyNumber(patch.TrafficAlertBytes, current.TrafficAlertBytes, KeyTrafficAlert, "traffic.alert_bytes", values, &deletes, &conflicts, &invalid)
	applyNumber(patch.TrafficLimitBytes, current.TrafficLimitBytes, KeyTrafficLimit, "traffic.limit_bytes", values, &deletes, &conflicts, &invalid)
	applyBackup(patch, current, values, &deletes, &conflicts, &invalid)

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
func applyProxy(patch Patch, current Effective, values map[string]string, deletes *[]string, invalid *[]string, conflicts *[]string) {
	if patch.ProxyMode == nil && patch.ProxyURL == nil && patch.ProxyTelegram == nil && patch.ProxyTelegraph == nil {
		return
	}

	// 模式与地址各自判断托管状态：只托管其中一项时，另一项仍可正常写入。
	mode := current.ProxyMode.Value
	if patch.ProxyMode != nil {
		raw := strings.TrimSpace(*patch.ProxyMode)
		normalized := netclient.ModeOrDefault(raw)
		if raw != "" && raw != normalized {
			*invalid = append(*invalid, "proxy.mode")
			return
		}
		mode = normalized
	}
	url := current.ProxyURL.Value
	urlChanged := false
	if patch.ProxyURL != nil {
		url = strings.TrimSpace(*patch.ProxyURL)
		urlChanged = url != current.ProxyURL.Value
	}

	// 交叉校验：自定义模式需要能解析的地址，其余模式下写入的地址也必须合法。
	if mode == netclient.ModeCustom {
		if err := netclient.Validate(netclient.ModeCustom, url); err != nil {
			*invalid = append(*invalid, "proxy.url")
			return
		}
	} else if urlChanged && url != "" {
		if err := netclient.Validate(netclient.ModeCustom, url); err != nil {
			*invalid = append(*invalid, "proxy.url")
			return
		}
	}

	if patch.ProxyMode != nil && mode != current.ProxyMode.Value {
		if current.ProxyMode.Source == SourceEnv {
			*conflicts = append(*conflicts, "proxy.mode")
		} else {
			values[KeyProxyMode] = mode
		}
	}
	if patch.ProxyURL != nil && urlChanged {
		if current.ProxyURL.Source == SourceEnv {
			*conflicts = append(*conflicts, "proxy.url")
		} else if url == "" {
			*deletes = append(*deletes, KeyProxyURL)
		} else {
			values[KeyProxyURL] = url
		}
	}

	applyToggle(patch.ProxyTelegram, current.ProxyTelegram, KeyProxyTelegram, "proxy.telegram", values, conflicts)
	applyToggle(patch.ProxyTelegraph, current.ProxyTelegraph, KeyProxyTelegraph, "proxy.telegraph", values, conflicts)
}

// applyMeta 归类说明文件名与解析方式：空列表或空串表示清除数据库值、回落默认。
func applyMeta(patch Patch, current Effective, values map[string]string, deletes *[]string, invalid *[]string) {
	if patch.MetaFilenames != nil {
		joined, err := normalizeMetaFilenames(*patch.MetaFilenames)
		if err != nil {
			*invalid = append(*invalid, "meta.filenames")
		} else if joined != current.MetaFilenames.Value {
			if joined == "" {
				*deletes = append(*deletes, KeyMetaFilenames)
			} else {
				values[KeyMetaFilenames] = joined
			}
		}
	}
	if patch.MetaParseMode != nil {
		mode := strings.ToLower(strings.TrimSpace(*patch.MetaParseMode))
		if mode != "" && mode != MetaParseFirstLine && mode != MetaParseKeyValue {
			*invalid = append(*invalid, "meta.parse_mode")
			return
		}
		if mode != current.MetaParseMode.Value {
			if mode == "" {
				*deletes = append(*deletes, KeyMetaParseMode)
			} else {
				values[KeyMetaParseMode] = mode
			}
		}
	}
}

// normalizeMetaFilenames 校验并规整说明文件名：转小写、去重、禁止路径与空白字符。
// 空列表返回空串，表示回落到默认候选名。
func normalizeMetaFilenames(names []string) (string, error) {
	cleaned := make([]string, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, raw := range names {
		name := strings.ToLower(strings.TrimSpace(raw))
		if name == "" {
			continue
		}
		if utf8.RuneCountInString(name) > maxMetaNameRunes || strings.ContainsAny(name, "/\\:*?\"<>|") {
			return "", ErrInvalidValue
		}
		if strings.ContainsAny(name, " \t\r\n") {
			return "", ErrInvalidValue
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		cleaned = append(cleaned, name)
	}
	if len(cleaned) == 0 {
		return "", nil
	}
	if len(cleaned) > maxMetaFilenames {
		return "", ErrInvalidValue
	}
	return strings.Join(cleaned, ","), nil
}

// applyToggle 归类代理开关：环境变量提供时不可修改，数据库值只在变化时写入。
func applyToggle(raw *bool, current Value, key, field string, values map[string]string, conflicts *[]string) {
	if raw == nil {
		return
	}
	enabled := "false"
	if *raw {
		enabled = "true"
	}
	if enabled == current.Value {
		return
	}
	if current.Source == SourceEnv {
		*conflicts = append(*conflicts, field)
		return
	}
	values[key] = proxyFlag(*raw)
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

// DefaultBackupTime 是自动备份的默认每日时刻。
const DefaultBackupTime = "03:00"

// applyNumber 归类非负数值配置：0 表示清除数据库值并回落默认。
func applyNumber(raw *int64, current Value, key, field string, values map[string]string, deletes *[]string, conflicts *[]string, invalid *[]string) {
	if raw == nil {
		return
	}
	if *raw < 0 {
		*invalid = append(*invalid, field)
		return
	}
	value := strconv.FormatInt(*raw, 10)
	if value == current.Value {
		return
	}
	if current.Source == SourceEnv {
		*conflicts = append(*conflicts, field)
		return
	}
	if *raw == 0 {
		*deletes = append(*deletes, key)
		return
	}
	values[key] = value
}

// applyBackup 归类自动备份开关、每日时刻与保留份数。
func applyBackup(patch Patch, current Effective, values map[string]string, deletes *[]string, conflicts *[]string, invalid *[]string) {
	if patch.BackupEnabled != nil {
		value := "false"
		if *patch.BackupEnabled {
			value = "true"
		}
		if value != current.BackupEnabled.Value {
			if current.BackupEnabled.Source == SourceEnv {
				*conflicts = append(*conflicts, "backup.enabled")
			} else {
				values[KeyBackupEnabled] = value
			}
		}
	}
	if patch.BackupTime != nil {
		normalized := normalizeBackupTime(*patch.BackupTime)
		if normalized == "" {
			*invalid = append(*invalid, "backup.time")
		} else if normalized != current.BackupTime.Value {
			if current.BackupTime.Source == SourceEnv {
				*conflicts = append(*conflicts, "backup.time")
			} else {
				values[KeyBackupTime] = normalized
			}
		}
	}
	if patch.BackupKeep != nil {
		if *patch.BackupKeep < 1 || *patch.BackupKeep > 60 {
			*invalid = append(*invalid, "backup.keep")
			return
		}
		value := strconv.FormatInt(*patch.BackupKeep, 10)
		if value != current.BackupKeep.Value {
			if current.BackupKeep.Source == SourceEnv {
				*conflicts = append(*conflicts, "backup.keep")
			} else {
				values[KeyBackupKeep] = value
			}
		}
	}
}
