package api

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
)

const (
	maxTitleRunes       = 200
	maxDescriptionRunes = 5000
	maxAuthorRunes      = 100
	maxImageCount       = 500
	maxTagCount         = 20
	maxTagRunes         = 30
	// maxCoverBytes 是封面缩略图解码后的上限，浏览器生成约 20–40KB。
	maxCoverBytes = 128 << 10
	// maxCreatePostBodyBytes 是建任务请求体的上限，先于 JSON 解码生效。
	maxCreatePostBodyBytes = 256 << 10
)

// createPostRequest 是新建帖子的请求体。
type createPostRequest struct {
	Title          string   `json:"title"`
	Description    string   `json:"description"`
	AuthorName     string   `json:"author_name"`
	UploadMode     string   `json:"upload_mode"`
	ImageCount     int      `json:"image_count"`
	CoverThumbnail string   `json:"cover_thumbnail"`
	Tags           []string `json:"tags"`
}

// createPostHandler 保存帖子元数据与可选封面，随后由上传接口补齐图片。
func createPostHandler(deps Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req createPostRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "请求体过大"})
				return
			}
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式不正确"})
			return
		}

		title := strings.TrimSpace(req.Title)
		switch {
		case title == "" || utf8.RuneCountInString(title) > maxTitleRunes:
			c.JSON(http.StatusBadRequest, gin.H{"error": "标题不能为空且不超过 200 字"})
			return
		case utf8.RuneCountInString(req.Description) > maxDescriptionRunes:
			c.JSON(http.StatusBadRequest, gin.H{"error": "说明不超过 5000 字"})
			return
		case utf8.RuneCountInString(req.AuthorName) > maxAuthorRunes:
			c.JSON(http.StatusBadRequest, gin.H{"error": "作者名不超过 100 字"})
			return
		case req.UploadMode != "" && req.UploadMode != "photo" && req.UploadMode != "document":
			c.JSON(http.StatusBadRequest, gin.H{"error": "画质模式只能是 photo 或 document"})
			return
		case req.ImageCount < 1 || req.ImageCount > maxImageCount:
			c.JSON(http.StatusBadRequest, gin.H{"error": "图片数量需在 1 到 500 之间"})
			return
		}

		tags, tagErr := normalizeTags(req.Tags)
		if tagErr != "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": tagErr})
			return
		}

		cover, decodeErr := decodeCover(req.CoverThumbnail)
		if decodeErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "封面缩略图不合法或过大"})
			return
		}

		mode := req.UploadMode
		if mode == "" {
			mode = "photo"
		}
		id, err := deps.Posts.Create(c.Request.Context(), posts.Post{
			Title:       title,
			Description: req.Description,
			AuthorName:  strings.TrimSpace(req.AuthorName),
			ImageCount:  req.ImageCount,
			UploadMode:  mode,
			Status:      posts.StatusUploading,
			Tags:        tags,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "创建帖子失败"})
			return
		}

		coverPath := ""
		if len(cover) > 0 {
			coverPath, err = posts.SaveCover(deps.DataDir, id, cover)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "保存封面失败"})
				return
			}
			if err := deps.Posts.SetCover(c.Request.Context(), id, coverPath); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "保存封面失败"})
				return
			}
		}

		c.JSON(http.StatusCreated, gin.H{
			"id":               id,
			"title":            title,
			"tags":             tags,
			"status":           posts.StatusUploading,
			"image_count":      req.ImageCount,
			"upload_mode":      mode,
			"cover_thumb_path": coverPath,
		})
	}
}

// decodeCover 解析可选的 base64 封面，允许带 data URI 前缀。
func decodeCover(raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if index := strings.Index(raw, ","); strings.HasPrefix(raw, "data:") && index > 0 {
		raw = raw[index+1:]
	}
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, errors.New("封面解码失败")
	}
	if len(decoded) > maxCoverBytes {
		return nil, errors.New("封面过大")
	}
	return decoded, nil
}

// normalizeTags 去掉空白与重复标签，同时校验数量与长度。
// 返回空字符串表示通过。
func normalizeTags(raw []string) ([]string, string) {
	seen := make(map[string]bool, len(raw))
	tags := make([]string, 0, len(raw))
	for _, item := range raw {
		tag := strings.TrimSpace(item)
		if tag == "" {
			continue
		}
		if utf8.RuneCountInString(tag) > maxTagRunes {
			return nil, "单个标签不超过 30 字"
		}
		key := strings.ToLower(tag)
		if seen[key] {
			continue
		}
		seen[key] = true
		tags = append(tags, tag)
	}
	if len(tags) > maxTagCount {
		return nil, "标签数量不超过 20 个"
	}
	return tags, ""
}
