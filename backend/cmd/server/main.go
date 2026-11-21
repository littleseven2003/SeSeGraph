package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"
	"github.com/littleseven2003/SeSeGraph/backend/internal/api"
	"github.com/littleseven2003/SeSeGraph/backend/internal/config"
	"github.com/littleseven2003/SeSeGraph/backend/internal/db"
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

// run 加载配置并启动 HTTP 服务，便于测试与 main 分离。
func run(args []string, lookupEnv func(string) (string, bool)) error {
	cfg, err := config.Load(args, lookupEnv)
	if err != nil {
		return err
	}

	gin.SetMode(gin.ReleaseMode)
	router := api.NewRouter(version, commit)

	store, err := db.Open(filepath.Join(cfg.DataDir, databaseFileName))
	if err != nil {
		return errDatabase
	}
	defer func() { _ = store.Close() }()

	if err := db.Migrate(context.Background(), store); err != nil {
		return errDatabase
	}

	// 只记录版本信息，监听地址与数据目录属于本地部署细节，不写入日志。
	log.Printf("服务启动：版本 %s（提交 %s）", version, commit)

	if err := router.Run(cfg.Addr); err != nil {
		// 监听失败的错误会带上地址等原始配置，这里统一替换为固定描述。
		return errServerStart
	}
	return nil
}

// errServerStart 是启动失败时对外的固定说明，避免回显配置取值。
var errServerStart = errors.New("HTTP 服务启动失败，请检查监听地址是否可用")

// errDatabase 是数据库初始化失败时的固定说明，不包含路径等本地信息。
var errDatabase = errors.New("数据库初始化失败，请检查数据目录是否可写")
