package api

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/littleseven2003/SeSeGraph/backend/internal/rss"
)

// rssMaxItems 与订阅查询上限保持一致。
const rssMaxItems = 50

// subscriptionURL 返回对外订阅地址；未启用或公开地址不合法时为空。
func subscriptionURL(deps Deps) string {
	if !deps.RSSEnabled {
		return ""
	}
	base := publicBaseURL(deps.PublicURL)
	if base == "" {
		return ""
	}
	return base + "/rss.xml"
}

// siteHandler 汇报站点级公开配置，供前端按启用状态渲染入口。
func siteHandler(deps Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-cache")
		c.JSON(http.StatusOK, gin.H{"rss_url": subscriptionURL(deps)})
	}
}

// sanitizeCategories 对标签逐个脱敏：标签同样是公开文本，可能被写入直链或令牌。
// 清洗后为空或重复的标签直接丢弃，避免输出空分类。
func sanitizeCategories(tags []string) []string {
	out := make([]string, 0, len(tags))
	seen := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		clean := strings.Join(strings.Fields(rss.SanitizeText(tag)), " ")
		if clean == "" {
			continue
		}
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}
	return out
}

// rssHandler 输出站点图集的 RSS 2.0 订阅源。
// 只有启用且配置了合法 HTTPS 公开地址时才提供；内容只含公开字段。
func rssHandler(deps Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		base := publicBaseURL(deps.PublicURL)
		if deps.Posts == nil || subscriptionURL(deps) == "" {
			c.Status(http.StatusNotFound)
			return
		}

		items, err := deps.Posts.ListFeed(c.Request.Context(), rssMaxItems)
		if err != nil {
			c.Status(http.StatusInternalServerError)
			return
		}

		feed := rss.Feed{
			Title:       "SeSe 图册",
			Link:        base + "/",
			Description: "自托管私人图册的 Telegraph 图集更新。",
			Language:    "zh-CN",
		}
		feed.Items = make([]rss.Item, 0, len(items))
		for _, item := range items {
			published, parseErr := time.Parse(time.RFC3339, item.PublishedAt)
			if parseErr != nil {
				continue
			}
			feed.Items = append(feed.Items, rss.Item{
				Title:       rss.SanitizeText(item.Title),
				Description: rss.SanitizeText(item.Description),
				Link:        item.Link,
				GUID:        item.Link,
				Published:   published,
				Categories:  sanitizeCategories(item.Tags),
			})
			if feed.LastBuild.Before(published) {
				feed.LastBuild = published
			}
		}

		body, err := feed.Marshal()
		if err != nil {
			c.Status(http.StatusInternalServerError)
			return
		}

		sum := sha256.Sum256(body)
		etag := `"` + hex.EncodeToString(sum[:]) + `"`
		c.Header("ETag", etag)
		c.Header("Cache-Control", "public, no-cache")
		c.Header("Content-Type", "application/rss+xml; charset=utf-8")
		if match := c.GetHeader("If-None-Match"); match != "" && match == etag {
			c.Status(http.StatusNotModified)
			return
		}
		c.Header("Content-Length", strconv.Itoa(len(body)))
		if c.Request.Method == http.MethodHead {
			c.Status(http.StatusOK)
			return
		}
		c.Data(http.StatusOK, "application/rss+xml; charset=utf-8", body)
	}
}
