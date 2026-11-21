package config

import (
	"errors"
	"flag"
	"io"
	"net/url"
	"strings"
)

// 默认配置值。
const (
	DefaultAddr    = ":8080"
	DefaultDataDir = "./data"
)

// Config 保存服务运行所需的基础配置。
type Config struct {
	Addr      string
	DataDir   string
	PublicURL string
}

// Load 解析启动配置，优先级为命令行参数、环境变量、默认值。
// lookupEnv 通常传入 os.LookupEnv，测试时可替换。
func Load(args []string, lookupEnv func(string) (string, bool)) (*Config, error) {
	cfg := &Config{
		Addr:    DefaultAddr,
		DataDir: DefaultDataDir,
	}

	if lookupEnv != nil {
		if v, ok := lookupEnv("LISTEN_ADDR"); ok && strings.TrimSpace(v) != "" {
			cfg.Addr = strings.TrimSpace(v)
		}
		if v, ok := lookupEnv("DATA_DIR"); ok && strings.TrimSpace(v) != "" {
			cfg.DataDir = strings.TrimSpace(v)
		}
		if v, ok := lookupEnv("PUBLIC_URL"); ok && strings.TrimSpace(v) != "" {
			cfg.PublicURL = strings.TrimSpace(v)
		}
	}

	fs := flag.NewFlagSet("sesegraph", flag.ContinueOnError)
	// 丢弃 flag 包的默认输出，避免把用户输入的原始值写入日志或终端。
	fs.SetOutput(io.Discard)
	addr := fs.String("addr", cfg.Addr, "监听地址")
	dataDir := fs.String("data", cfg.DataDir, "数据目录")
	publicURL := fs.String("public-url", cfg.PublicURL, "对外访问地址")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, flag.ErrHelp
		}
		return nil, errors.New("命令行参数解析失败")
	}

	cfg.Addr = strings.TrimSpace(*addr)
	cfg.DataDir = strings.TrimSpace(*dataDir)
	cfg.PublicURL = strings.TrimSpace(*publicURL)

	if cfg.Addr == "" {
		return nil, errors.New("监听地址不能为空")
	}
	if cfg.DataDir == "" {
		return nil, errors.New("数据目录不能为空")
	}
	if err := validatePublicURL(cfg.PublicURL); err != nil {
		return nil, err
	}

	return cfg, nil
}

// ValidatePublicURL 校验对外访问地址的结构。空值表示尚未确定，允许通过。
// 地址会被直接拼上图片路径，因此查询串、片段与内嵌凭据都必须拒绝，
// 否则生成出来的看图链接无法访问。校验失败时只返回固定描述，不回显具体取值。
func ValidatePublicURL(raw string) error {
	if raw == "" {
		return nil
	}
	if strings.ContainsAny(raw, "\r\n\t ") {
		return errors.New("对外访问地址不能包含空白字符")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return errors.New("对外访问地址格式不合法")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("对外访问地址的协议必须是 http 或 https")
	}
	if parsed.Host == "" {
		return errors.New("对外访问地址缺少主机名")
	}
	if parsed.User != nil {
		return errors.New("对外访问地址不允许包含内嵌凭据")
	}
	if parsed.RawQuery != "" || parsed.ForceQuery {
		return errors.New("对外访问地址不能包含查询串")
	}
	if parsed.Fragment != "" {
		return errors.New("对外访问地址不能包含片段")
	}
	return nil
}

// PublicURLIsHTTPS 判断对外访问地址是否可作为公开看图地址。
// 除协议为 HTTPS 外，还要求地址结构完整，避免拼接出无法访问的图片链接。
func PublicURLIsHTTPS(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || ValidatePublicURL(trimmed) != nil {
		return false
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return false
	}
	return parsed.Scheme == "https"
}

// validatePublicURL 保留包内调用入口，便于启动参数与单元测试复用同一套规则。
func validatePublicURL(raw string) error {
	return ValidatePublicURL(raw)
}
