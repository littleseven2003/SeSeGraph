package posts

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

// 画廊列表的分页与筛选上限。
const (
	// DefaultGalleryPageSize 是未指定分页大小时的默认条数。
	DefaultGalleryPageSize = 20
	// MaxGalleryPageSize 是单页条数上限，避免一次读出整库。
	MaxGalleryPageSize = 100
	// MaxGalleryTags 是一次筛选允许的标签数量上限。
	MaxGalleryTags = 10
	// MaxGalleryKeywordRunes 是关键词允许的最大字符数。
	MaxGalleryKeywordRunes = 100
)

// GalleryQuery 是画廊列表的筛选与分页条件。
type GalleryQuery struct {
	Keyword  string
	Tags     []string
	Page     int
	PageSize int
}

// Normalize 清洗筛选条件：去空白、去重标签、修正分页范围。
func (q GalleryQuery) Normalize() GalleryQuery {
	normalized := GalleryQuery{
		Keyword:  strings.TrimSpace(q.Keyword),
		Page:     q.Page,
		PageSize: q.PageSize,
	}
	if normalized.Page < 1 {
		normalized.Page = 1
	}
	if normalized.PageSize < 1 {
		normalized.PageSize = DefaultGalleryPageSize
	}
	if normalized.PageSize > MaxGalleryPageSize {
		normalized.PageSize = MaxGalleryPageSize
	}
	seen := make(map[string]bool, len(q.Tags))
	for _, item := range q.Tags {
		tag := strings.TrimSpace(item)
		if tag == "" || seen[tag] {
			continue
		}
		seen[tag] = true
		normalized.Tags = append(normalized.Tags, tag)
		if len(normalized.Tags) >= MaxGalleryTags {
			break
		}
	}
	return normalized
}

// GalleryItem 是画廊卡片需要的字段，不含说明等正文内容。
type GalleryItem struct {
	ID    int64
	Title string
	// DescriptionExcerpt 是列表展示用的一段说明，最多 240 字，详情仍读全文。
	DescriptionExcerpt string
	CoverThumbPath     string
	ImageCount         int
	Status             string
	PublishState       string
	IsPublic           bool
	IsDisabled         bool
	TelegraphURLs      []string
	Tags               []string
	PublishedAt        string
	CreatedAt          string
	UpdatedAt          string
	// ViewsCount 是上次同步到的累计浏览量，ViewsUpdatedAt 为空表示尚未同步。
	ViewsCount     int
	ViewsUpdatedAt string
}

// GalleryPage 是一页画廊结果。
type GalleryPage struct {
	Items    []GalleryItem
	Total    int
	Page     int
	PageSize int
}

// GalleryImage 是详情中的单张图片，只包含展示需要的字段。
type GalleryImage struct {
	SortOrder int
	ShortCode string
	MimeType  string
	FileSize  int64
	Status    string
}

// GalleryDetail 是帖子详情，包含正文、进度与图片清单。
type GalleryDetail struct {
	GalleryItem
	// PendingDelete 表示图集已进入删除流程，界面应停用分享并提供重试入口。
	PendingDelete bool
	Description   string
	AuthorName    string
	UploadMode    string
	ErrorMessage  string
	PublishError  string
	Paths         []string
	Images        []GalleryImage
}

// TagCount 是标签及其关联帖子数量。
type TagCount struct {
	Name  string
	Count int
}

// galleryFilter 生成列表与计数共用的 WHERE 子句。
// 关键词同时匹配标题、说明与标签名；多个标签之间是「同时具备」的关系。
func galleryFilter(query GalleryQuery) (string, []any) {
	clauses := make([]string, 0, len(query.Tags)+1)
	args := make([]any, 0, len(query.Tags)*2+3)

	if query.Keyword != "" {
		pattern := "%" + escapeLike(query.Keyword) + "%"
		clauses = append(clauses, `(p.title LIKE ? ESCAPE '\' OR p.description LIKE ? ESCAPE '\' OR EXISTS (
			SELECT 1 FROM post_tags kpt JOIN tags kt ON kt.id = kpt.tag_id
			WHERE kpt.post_id = p.id AND kt.name LIKE ? ESCAPE '\'))`)
		args = append(args, pattern, pattern, pattern)
	}
	for _, tag := range query.Tags {
		clauses = append(clauses, `EXISTS (
			SELECT 1 FROM post_tags fpt JOIN tags ft ON ft.id = fpt.tag_id
			WHERE fpt.post_id = p.id AND ft.name = ?)`)
		args = append(args, tag)
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

// escapeLike 转义 LIKE 的通配符，避免关键词被当成模式。
func escapeLike(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(value)
}

// ListGallery 按条件分页查询画廊。
func (r *Repository) ListGallery(ctx context.Context, query GalleryQuery) (*GalleryPage, error) {
	query = query.Normalize()
	where, args := galleryFilter(query)

	total := 0
	if err := r.handle.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM posts p`+where, args...).Scan(&total); err != nil {
		return nil, errors.New("统计画廊失败")
	}

	page := &GalleryPage{Total: total, Page: query.Page, PageSize: query.PageSize}
	if total == 0 || (query.Page-1)*query.PageSize >= total {
		page.Items = []GalleryItem{}
		return page, nil
	}

	pageArgs := append(append([]any{}, args...), query.PageSize, (query.Page-1)*query.PageSize)
	rows, err := r.handle.QueryContext(ctx,
		`SELECT p.id, p.title, COALESCE(substr(p.description, 1, 240), ''),
		        p.cover_thumb_path, p.image_count, p.status, p.publish_state,
		        p.is_public, p.is_disabled, p.telegraph_urls,
		        COALESCE(strftime('%Y-%m-%dT%H:%M:%SZ', p.published_at), ''),
		        strftime('%Y-%m-%dT%H:%M:%SZ', p.created_at),
		        strftime('%Y-%m-%dT%H:%M:%SZ', p.updated_at),
		        p.views_count, COALESCE(strftime('%Y-%m-%dT%H:%M:%SZ', p.views_updated_at), '')
		 FROM posts p`+where+` ORDER BY p.created_at DESC, p.id DESC LIMIT ? OFFSET ?`, pageArgs...)
	if err != nil {
		return nil, errors.New("读取画廊失败")
	}
	defer func() { _ = rows.Close() }()

	items := make([]GalleryItem, 0, query.PageSize)
	ids := make([]any, 0, query.PageSize)
	for rows.Next() {
		var item GalleryItem
		var public, disabled int
		var urls string
		if err := rows.Scan(&item.ID, &item.Title, &item.DescriptionExcerpt, &item.CoverThumbPath, &item.ImageCount, &item.Status,
			&item.PublishState, &public, &disabled, &urls, &item.PublishedAt,
			&item.CreatedAt, &item.UpdatedAt, &item.ViewsCount, &item.ViewsUpdatedAt); err != nil {
			return nil, errors.New("读取画廊失败")
		}
		item.IsPublic = public == 1
		item.IsDisabled = disabled == 1
		item.TelegraphURLs = parseStringList(urls)
		item.Tags = []string{}
		items = append(items, item)
		ids = append(ids, item.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("读取画廊失败")
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
	page.Items = items
	return page, nil
}

// tagsByPostIDs 批量读取多帖标签，避免逐帖查询。
func (r *Repository) tagsByPostIDs(ctx context.Context, ids []any) (map[int64][]string, error) {
	result := make(map[int64][]string, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	rows, err := r.handle.QueryContext(ctx,
		`SELECT pt.post_id, t.name FROM post_tags pt JOIN tags t ON t.id = pt.tag_id
		 WHERE pt.post_id IN (`+placeholders+`) ORDER BY t.name`, ids...)
	if err != nil {
		return nil, errors.New("读取标签失败")
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var postID int64
		var name string
		if err := rows.Scan(&postID, &name); err != nil {
			return nil, errors.New("读取标签失败")
		}
		result[postID] = append(result[postID], name)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("读取标签失败")
	}
	return result, nil
}

// GalleryDetail 读取单帖详情，包含图片清单与已建页面路径。
func (r *Repository) GalleryDetail(ctx context.Context, id int64) (*GalleryDetail, error) {
	detail := &GalleryDetail{}
	var public, disabled, pending int
	var urls, paths string
	err := r.handle.QueryRowContext(ctx,
		`SELECT p.id, p.title, p.description, p.author_name, p.cover_thumb_path, p.image_count,
		        p.upload_mode, p.status, p.error_message, p.publish_state, p.publish_error,
		        p.is_public, p.is_disabled, p.telegraph_urls, p.telegraph_paths,
		        COALESCE(strftime('%Y-%m-%dT%H:%M:%SZ', p.published_at), ''),
		        strftime('%Y-%m-%dT%H:%M:%SZ', p.created_at),
		        strftime('%Y-%m-%dT%H:%M:%SZ', p.updated_at),
		        p.views_count, COALESCE(strftime('%Y-%m-%dT%H:%M:%SZ', p.views_updated_at), ''),
		        p.pending_delete
		 FROM posts p WHERE p.id = ?`, id,
	).Scan(&detail.ID, &detail.Title, &detail.Description, &detail.AuthorName, &detail.CoverThumbPath,
		&detail.ImageCount, &detail.UploadMode, &detail.Status, &detail.ErrorMessage,
		&detail.PublishState, &detail.PublishError, &public, &disabled, &urls, &paths,
		&detail.PublishedAt, &detail.CreatedAt, &detail.UpdatedAt,
		&detail.ViewsCount, &detail.ViewsUpdatedAt, &pending)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, errors.New("读取帖子失败")
	}
	detail.IsPublic = public == 1
	detail.IsDisabled = disabled == 1
	detail.PendingDelete = pending == 1
	detail.TelegraphURLs = parseStringList(urls)
	detail.Paths = parseStringList(paths)

	tags, err := r.ListTags(ctx, id)
	if err != nil {
		return nil, err
	}
	detail.Tags = tags

	images, err := r.galleryImages(ctx, id)
	if err != nil {
		return nil, err
	}
	detail.Images = images
	return detail, nil
}

// galleryImages 读取详情的图片清单，按序号升序。
func (r *Repository) galleryImages(ctx context.Context, postID int64) ([]GalleryImage, error) {
	rows, err := r.handle.QueryContext(ctx,
		`SELECT sort_order, short_code, mime_type, file_size, status
		 FROM post_images WHERE post_id = ? ORDER BY sort_order`, postID)
	if err != nil {
		return nil, errors.New("读取图片清失败")
	}
	defer func() { _ = rows.Close() }()

	images := make([]GalleryImage, 0, 8)
	for rows.Next() {
		var image GalleryImage
		if err := rows.Scan(&image.SortOrder, &image.ShortCode, &image.MimeType,
			&image.FileSize, &image.Status); err != nil {
			return nil, errors.New("读取图片清单失败")
		}
		images = append(images, image)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("读取图片清单失败")
	}
	return images, nil
}

// ListTagCounts 按关联帖子数量倒序返回全部标签。
func (r *Repository) ListTagCounts(ctx context.Context) ([]TagCount, error) {
	rows, err := r.handle.QueryContext(ctx,
		`SELECT t.name, COUNT(pt.post_id) FROM tags t
		 LEFT JOIN post_tags pt ON pt.tag_id = t.id
		 GROUP BY t.id ORDER BY COUNT(pt.post_id) DESC, t.name`)
	if err != nil {
		return nil, errors.New("读取标签失败")
	}
	defer func() { _ = rows.Close() }()

	counts := make([]TagCount, 0, 8)
	for rows.Next() {
		var item TagCount
		if err := rows.Scan(&item.Name, &item.Count); err != nil {
			return nil, errors.New("读取标签失败")
		}
		counts = append(counts, item)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("读取标签失败")
	}
	return counts, nil
}

// parseStringList 把库里的 JSON 数组还原成字符串切片，损坏时返回空切片。
func parseStringList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []string{}
	}
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return []string{}
	}
	if values == nil {
		return []string{}
	}
	return values
}
