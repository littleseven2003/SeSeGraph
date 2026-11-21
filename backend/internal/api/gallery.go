package api

import (
	"errors"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/littleseven2003/SeSeGraph/backend/internal/config"
	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
)

// galleryMaxTagRunes 与建帖时的标签长度限制保持一致。
const galleryMaxTagRunes = 30

// thumbNamePattern 限定封面文件名为封面写入时使用的格式。
var thumbNamePattern = regexp.MustCompile(`^[0-9]+\.webp$`)

// galleryItemResponse 是画廊卡片的接口结构。
type galleryItemResponse struct {
	ID             int64    `json:"id"`
	Title          string   `json:"title"`
	CoverThumbPath string   `json:"cover_thumb_path"`
	CoverURL       string   `json:"cover_url"`
	ImageCount     int      `json:"image_count"`
	Status         string   `json:"status"`
	PublishState   string   `json:"publish_state"`
	Public         bool     `json:"public"`
	Disabled       bool     `json:"disabled"`
	Tags           []string `json:"tags"`
	TelegraphURLs  []string `json:"telegraph_urls"`
	PublishedAt    string   `json:"published_at"`
	CreatedAt      string   `json:"created_at"`
	UpdatedAt      string   `json:"updated_at"`
}

// galleryImageResponse 是详情中的单图结构，不暴露远端标识。
type galleryImageResponse struct {
	SortOrder int    `json:"sort_order"`
	ShortCode string `json:"short_code"`
	MIME      string `json:"mime_type"`
	FileSize  int64  `json:"file_size"`
	Status    string `json:"status"`
}

// galleryDetailResponse 是帖子详情的接口结构。
type galleryDetailResponse struct {
	galleryItemResponse
	Description  string                 `json:"description"`
	AuthorName   string                 `json:"author_name"`
	UploadMode   string                 `json:"upload_mode"`
	ErrorMessage string                 `json:"error_message,omitempty"`
	PublishError string                 `json:"publish_error,omitempty"`
	Paths        []string               `json:"telegraph_paths"`
	Images       []galleryImageResponse `json:"images"`
	// PublicBaseURL 是拼接单图外链用的公开地址，未配置或结构不合法时为空。
	PublicBaseURL string `json:"public_base_url"`
}

// coverURL 把库内封面路径转成接口地址，只保留文件名部分。
func coverURL(stored string) string {
	stored = strings.TrimSpace(stored)
	if stored == "" {
		return ""
	}
	name := path.Base(stored)
	if !thumbNamePattern.MatchString(name) {
		return ""
	}
	return "/api/thumbs/" + name
}

// publicBaseURL 返回可拼接图片路径的公开地址，去掉末尾斜杠；未配置或不合法的返回空串。
func publicBaseURL(raw string) string {
	if !config.PublicURLIsHTTPS(raw) {
		return ""
	}
	return strings.TrimRight(strings.TrimSpace(raw), "/")
}

// toGalleryItem 把仓储结果转成接口结构。
func toGalleryItem(item posts.GalleryItem) galleryItemResponse {
	tags := item.Tags
	if tags == nil {
		tags = []string{}
	}
	urls := item.TelegraphURLs
	if urls == nil {
		urls = []string{}
	}
	return galleryItemResponse{
		ID:             item.ID,
		Title:          item.Title,
		CoverThumbPath: item.CoverThumbPath,
		CoverURL:       coverURL(item.CoverThumbPath),
		ImageCount:     item.ImageCount,
		Status:         item.Status,
		PublishState:   item.PublishState,
		Public:         item.IsPublic,
		Disabled:       item.IsDisabled,
		Tags:           tags,
		TelegraphURLs:  urls,
		PublishedAt:    item.PublishedAt,
		CreatedAt:      item.CreatedAt,
		UpdatedAt:      item.UpdatedAt,
	}
}

// parseGalleryQuery 解析列表的筛选与分页参数，返回空字符串表示通过。
func parseGalleryQuery(c *gin.Context) (posts.GalleryQuery, string) {
	query := posts.GalleryQuery{
		Keyword: strings.TrimSpace(c.Query("q")),
		Tags:    galleryTags(c),
	}
	if len([]rune(query.Keyword)) > posts.MaxGalleryKeywordRunes {
		return query, "关键词不超过 100 字"
	}
	if len(query.Tags) > posts.MaxGalleryTags {
		return query, "标签筛选不超过 10 个"
	}
	for _, tag := range query.Tags {
		if len([]rune(tag)) > galleryMaxTagRunes {
			return query, "单个标签不超过 30 字"
		}
	}

	page, err := strconv.Atoi(strings.TrimSpace(c.DefaultQuery("page", "1")))
	if err != nil || page < 1 {
		return query, "页码不合法"
	}
	size, err := strconv.Atoi(strings.TrimSpace(c.DefaultQuery("page_size", "20")))
	if err != nil || size < 1 || size > posts.MaxGalleryPageSize {
		return query, "分页大小需在 1 到 100 之间"
	}
	query.Page = page
	query.PageSize = size
	return query, ""
}

// galleryTags 同时接受重复的 tag 参数与逗号分隔的 tags 参数。
func galleryTags(c *gin.Context) []string {
	values := make([]string, 0, 4)
	values = append(values, c.QueryArray("tag")...)
	for _, group := range c.QueryArray("tags") {
		values = append(values, strings.Split(group, ",")...)
	}
	tags := make([]string, 0, len(values))
	for _, value := range values {
		tag := strings.TrimSpace(value)
		if tag != "" {
			tags = append(tags, tag)
		}
	}
	return tags
}

// listPostsHandler 返回画廊列表，支持关键词、多标签与分页。
func listPostsHandler(deps Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		query, message := parseGalleryQuery(c)
		if message != "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": message})
			return
		}

		page, err := deps.Posts.ListGallery(c.Request.Context(), query)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "读取画廊失败"})
			return
		}

		items := make([]galleryItemResponse, 0, len(page.Items))
		for _, item := range page.Items {
			items = append(items, toGalleryItem(item))
		}
		c.JSON(http.StatusOK, gin.H{
			"items":     items,
			"total":     page.Total,
			"page":      page.Page,
			"page_size": page.PageSize,
		})
	}
}

// postDetailHandler 返回单帖详情，包含图片清单与分享链接。
func postDetailHandler(deps Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		postID, parseErr := strconv.ParseInt(c.Param("id"), 10, 64)
		if parseErr != nil || postID <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "帖子编号不合法"})
			return
		}

		detail, err := deps.Posts.GalleryDetail(c.Request.Context(), postID)
		if errors.Is(err, posts.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "帖子不存在"})
			return
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "读取帖子失败"})
			return
		}

		images := make([]galleryImageResponse, 0, len(detail.Images))
		for _, image := range detail.Images {
			images = append(images, galleryImageResponse{
				SortOrder: image.SortOrder,
				ShortCode: image.ShortCode,
				MIME:      image.MimeType,
				FileSize:  image.FileSize,
				Status:    image.Status,
			})
		}
		paths := detail.Paths
		if paths == nil {
			paths = []string{}
		}
		c.JSON(http.StatusOK, galleryDetailResponse{
			galleryItemResponse: toGalleryItem(detail.GalleryItem),
			Description:         detail.Description,
			AuthorName:          detail.AuthorName,
			UploadMode:          detail.UploadMode,
			ErrorMessage:        detail.ErrorMessage,
			PublishError:        detail.PublishError,
			Paths:               paths,
			Images:              images,
			PublicBaseURL:       publicBaseURL(deps.PublicURL),
		})
	}
}
