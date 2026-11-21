package api

import (
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
)

// spaHandler 提供嵌入的前端页面：命中的静态文件直出，其余路径回退到入口页。
func spaHandler(webFS fs.FS) gin.HandlerFunc {
	fileServer := http.FileServer(http.FS(webFS))
	index, indexErr := fs.ReadFile(webFS, "index.html")

	return func(c *gin.Context) {
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
			c.JSON(http.StatusNotFound, gin.H{"error": "未找到"})
			return
		}

		// 接口与图片网关路径不参与页面回退。先按原始路径判断，
		// 避免 ../ 之类的路径段在清理后把接口路径伪装成页面路径。
		raw := c.Request.URL.Path
		if raw == "/api" || strings.HasPrefix(raw, "/api/") || raw == "/i" || strings.HasPrefix(raw, "/i/") {
			c.JSON(http.StatusNotFound, gin.H{"error": "未找到"})
			return
		}

		requested := strings.TrimPrefix(path.Clean("/"+raw), "/")
		if strings.HasPrefix(requested, "api/") || strings.HasPrefix(requested, "i/") {
			c.JSON(http.StatusNotFound, gin.H{"error": "未找到"})
			return
		}

		if requested != "" && fileExists(webFS, requested) {
			setAssetCache(c, requested)
			fileServer.ServeHTTP(c.Writer, c.Request)
			return
		}

		// 静态资源缺失时不回退入口页，避免旧哈希资源拿到 200 的 HTML。
		if strings.HasPrefix(requested, "static/") {
			c.JSON(http.StatusNotFound, gin.H{"error": "未找到"})
			return
		}

		if indexErr != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "前端页面尚未构建"})
			return
		}
		c.Header("Cache-Control", "no-cache")
		c.Data(http.StatusOK, "text/html; charset=utf-8", index)
	}
}

func fileExists(webFS fs.FS, name string) bool {
	info, err := fs.Stat(webFS, name)
	return err == nil && !info.IsDir()
}

// setAssetCache 为带内容哈希的静态资源设置长缓存，入口页保持不缓存。
func setAssetCache(c *gin.Context, name string) {
	if strings.HasPrefix(name, "static/") {
		c.Header("Cache-Control", "public, max-age=31536000, immutable")
		return
	}
	c.Header("Cache-Control", "no-cache")
}
