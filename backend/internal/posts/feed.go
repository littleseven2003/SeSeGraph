package posts

import (
	"context"
	"errors"
	"net/url"
	"strings"
)

// FeedItem 是订阅源可公开的字段，不含令牌、频道标识与远端文件标识。
type FeedItem struct {
	ID          int64
	Title       string
	Description string
	Tags        []string
	// PublishedAt 是 UTC 时间，格式与画廊接口一致。
	PublishedAt string
	Link        string
}

// feedMaxItems 是单次订阅查询的上限，避免一次取出整库。
const feedMaxItems = 50

// ListFeed 返回最新的公开已发布图集，只保留具有合法 Telegraph 首篇链接的条目。
// 查询只读取公开字段，结果按发布时间与 ID 倒序。
func (r *Repository) ListFeed(ctx context.Context, limit int) ([]FeedItem, error) {
	if limit <= 0 || limit > feedMaxItems {
		limit = feedMaxItems
	}
	rows, err := r.handle.QueryContext(ctx,
		`SELECT p.id, p.title, COALESCE(p.description, ''), p.telegraph_urls,
		        COALESCE(strftime('%Y-%m-%dT%H:%M:%SZ', p.published_at), '')
		   FROM posts p
		  WHERE p.publish_state = 'published'
		    AND p.is_public = 1
		    AND p.is_disabled = 0
		    AND p.published_at IS NOT NULL
		    AND p.telegraph_urls NOT IN ('', '[]')
		  ORDER BY p.published_at DESC, p.id DESC
		  LIMIT ?`, limit)
	if err != nil {
		return nil, errors.New("读取订阅源失败")
	}
	defer func() { _ = rows.Close() }()

	items := make([]FeedItem, 0, limit)
	ids := make([]any, 0, limit)
	for rows.Next() {
		var (
			item FeedItem
			urls string
		)
		if err := rows.Scan(&item.ID, &item.Title, &item.Description, &urls, &item.PublishedAt); err != nil {
			return nil, errors.New("读取订阅源失败")
		}
		item.Link = firstTelegraphLink(parseStringList(urls))
		if item.Link == "" || item.PublishedAt == "" {
			continue
		}
		item.Tags = []string{}
		items = append(items, item)
		ids = append(ids, item.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("读取订阅源失败")
	}

	tagMap, err := r.tagsByPostIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	for index := range items {
		if tags, ok := tagMap[items[index].ID]; ok {
			items[index].Tags = tags
		}
	}
	return items, nil
}

// firstTelegraphLink 只接受规范 Telegraph 地址，其他链接一律忽略。
func firstTelegraphLink(links []string) string {
	for _, raw := range links {
		parsed, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || parsed.Scheme != "https" {
			continue
		}
		host := strings.ToLower(parsed.Hostname())
		if host == "telegra.ph" || strings.HasSuffix(host, ".telegra.ph") {
			return parsed.String()
		}
	}
	return ""
}
