package web

import (
	"embed"
	"io/fs"
)

// embedded 保存前端构建产物。未构建时目录内保留占位文件，保证干净检出可直接编译。
//
//go:embed all:assets
var embedded embed.FS

// Assets 返回嵌入的前端文件系统，根目录对应前端构建输出目录。
func Assets() (fs.FS, error) {
	return fs.Sub(embedded, "assets")
}
