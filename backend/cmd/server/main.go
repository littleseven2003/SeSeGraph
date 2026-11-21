package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/littleseven2003/SeSeGraph/backend/internal/api"
	"github.com/littleseven2003/SeSeGraph/backend/internal/auth"
	"github.com/littleseven2003/SeSeGraph/backend/internal/config"
	"github.com/littleseven2003/SeSeGraph/backend/internal/db"
	"github.com/littleseven2003/SeSeGraph/backend/internal/web"
)

// 构建时通过 -ldflags 注入，未注入时使用开发默认值。
var (
	version = "dev"
	commit  = "unknown"
)

// databaseFileName 是数据目录下的数据库文件名。
const databaseFileName = "sesegraph.db"

func main() {
	if err := run(os.Args[1:], os.LookupEnv); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, "用法: sesegraph [--addr 监听地址] [--data 数据目录] [--public-url 对外访问地址]")
			return
		}
		fmt.Fprintln(os.Stderr, "启动失败: "+err.Error())
		os.Exit(1)
	}
}

// run 加载配置、准备数据库与鉴权，然后启动 HTTP 服务。
func run(args []string, lookupEnv func(string) (string, bool)) error {
	cfg, err := config.Load(args, lookupEnv)
	if err != nil {
		return err
	}

	gin.SetMode(gin.ReleaseMode)

	store, err := db.Open(filepath.Join(cfg.DataDir, databaseFileName))
	if err != nil {
		return errDatabase
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	if err := db.Migrate(ctx, store); err != nil {
		return errDatabase
	}

	authService := auth.NewService(store)
	secret, err := authService.SessionSecret(ctx)
	if err != nil {
		return errDatabase
	}
	if err := bootstrapAdmin(ctx, authService, lookupEnv); err != nil {
		return err
	}

	assets, err := web.Assets()
	if err != nil {
		return errAssets
	}

	router := api.NewRouter(api.Deps{
		Version:    version,
		Commit:     commit,
		Auth:       authService,
		Sessions:   auth.NewSessions(secret, auth.DefaultSessionTTL, isHTTPS(cfg.PublicURL)),
		SetupToken: readSetupToken(lookupEnv),
		WebFS:      assets,
	})

	// 只记录版本信息，监听地址与数据目录属于本地部署细节，不写入日志。
	log.Printf("服务启动：版本 %s（提交 %s）", version, commit)

	if err := router.Run(cfg.Addr); err != nil {
		// 监听失败的错误会带上地址等原始配置，这里统一替换为固定描述。
		return errServerStart
	}
	return nil
}

// bootstrapAdmin 在配置了环境变量账号密码时完成首次初始化。
// 已初始化时静默跳过，不覆盖已有账号。
func bootstrapAdmin(ctx context.Context, service *auth.Service, lookupEnv func(string) (string, bool)) error {
	username, _ := lookupEnv("ADMIN_USERNAME")
	password, _ := lookupEnv("ADMIN_PASSWORD")
	if strings.TrimSpace(username) == "" || password == "" {
		return nil
	}

	err := service.Create(ctx, username, password)
	switch {
	case err == nil:
		log.Print("管理员账号已通过环境变量完成初始化")
		return nil
	case errors.Is(err, auth.ErrAlreadyInitialized):
		return nil
	case errors.Is(err, auth.ErrInvalidInput):
		return errAdminEnv
	default:
		return errDatabase
	}
}

// readSetupToken 读取网页初始化使用的一次性凭证，未配置时为空。
func readSetupToken(lookupEnv func(string) (string, bool)) string {
	value, _ := lookupEnv("ADMIN_SETUP_TOKEN")
	return strings.TrimSpace(value)
}

// isHTTPS 判断对外地址是否使用 HTTPS，用于决定会话 Cookie 是否带 Secure。
func isHTTPS(publicURL string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(publicURL)), "https://")
}

// errServerStart 是启动失败时对外的固定说明，避免回显配置取值。
var errServerStart = errors.New("HTTP 服务启动失败，请检查监听地址是否可用")

// errDatabase 是数据库或鉴权初始化失败时的固定说明，不包含路径等本地信息。
var errDatabase = errors.New("数据库初始化失败，请检查数据目录是否可写")

// errAdminEnv 说明环境变量中的管理员凭据不符合要求，同样不回显取值。
var errAdminEnv = errors.New("环境变量中的管理员账号或密码不符合要求")

// errAssets 表示嵌入的前端资源无法读取。
var errAssets = errors.New("前端资源加载失败")
