package api

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"
	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
)

// tagsHandler 返回全部标签及其关联帖子数量。
func tagsHandler(deps Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		counts, err := deps.Posts.ListTagCounts(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "读取标签失败"})
			return
		}
		items := make([]gin.H, 0, len(counts))
		for _, item := range counts {
			items = append(items, gin.H{"name": item.Name, "count": item.Count})
		}
		c.JSON(http.StatusOK, gin.H{"items": items})
	}
}

// thumbHandler 读取封面缩略图，只接受封面写入时使用的文件名。
func thumbHandler(deps Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		name := c.Param("filename")
		if !thumbNamePattern.MatchString(name) {
			c.JSON(http.StatusNotFound, gin.H{"error": "封面不存在"})
			return
		}
		filePath := filepath.Join(deps.DataDir, posts.CoverDir, name)
		file, err := os.Open(filePath)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "封面不存在"})
			return
		}
		defer func() { _ = file.Close() }()

		info, err := file.Stat()
		if err != nil || info.IsDir() {
			c.JSON(http.StatusNotFound, gin.H{"error": "封面不存在"})
			return
		}
		c.Header("Content-Type", "image/webp")
		c.Header("Cache-Control", "private, max-age=604800")
		c.Header("X-Content-Type-Options", "nosniff")
		http.ServeContent(c.Writer, c.Request, name, info.ModTime(), file)
	}
}
