package api

import (
	"encoding/base64"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
	"github.com/littleseven2003/SeSeGraph/backend/internal/settings"
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
	Title       string `json:"title"`
	Description string `json:"description"`
	AuthorName  string `json:"author_name"`
	UploadMode  string `json:"upload_mode"`
	// CoverSortOrder 是封面来源序号，与缩略图一起提交。
	CoverSortOrder int      `json:"cover_sort_order"`
	ImageCount     int      `json:"image_count"`
	CoverThumbnail string   `json:"cover_thumbnail"`
	Tags           []string `json:"tags"`
}

// rejectPendingDelete 在帖子进入删除流程后阻止恢复、编辑与重新发布。
func rejectPendingDelete(c *gin.Context, deps Deps, postID int64) bool {
	pending, err := deps.Posts.PendingDelete(c.Request.Context(), postID)
	switch {
	case errors.Is(err, posts.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "帖子不存在"})
		return true
	case err != nil:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取帖子状态失败"})
		return true
	case pending:
		c.JSON(http.StatusConflict, gin.H{"error": "帖子正在删除，无法继续操作"})
		return true
	}
	return false
}

// deletePostHandler 永久删除帖子：先停用短码并清空远端页面，成功后才删本地记录。
// 远端清理失败时保留待删除标记，短码继续 404，可再次调用重试。
func deletePostHandler(deps Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		postID, parseErr := strconv.ParseInt(c.Param("id"), 10, 64)
		if parseErr != nil || postID <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "帖子编号不合法"})
			return
		}

		paths, err := deps.Posts.MarkPendingDelete(c.Request.Context(), postID)
		switch {
		case errors.Is(err, posts.ErrNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "帖子不存在"})
			return
		case errors.Is(err, posts.ErrTaskRunning):
			c.JSON(http.StatusConflict, gin.H{"error": "任务正在进行中，完成或失败后再删除"})
			return
		case err != nil:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "删除帖子失败"})
			return
		}

		// 先清空远端页面：昵称、图片与互链导航都会被替换成撤回提示。
		if len(paths) > 0 {
			if deps.Editor == nil {
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Telegraph 未配置，暂时无法撤回远端页面"})
				return
			}
			if err := deps.Editor.Withdraw(c.Request.Context(), paths); err != nil {
				c.JSON(http.StatusBadGateway, gin.H{"error": "远端页面撤回未完成，已保留待删除记录，可稍后重试"})
				return
			}
		}

		post, postErr := deps.Posts.Get(c.Request.Context(), postID)

		// 先清理本地文件；失败时保留待删除记录，短码继续 404，可再次重试。
		if postErr == nil && post.CoverThumbPath != "" {
			if err := os.Remove(filepath.Join(deps.DataDir, post.CoverThumbPath)); err != nil && !errors.Is(err, fs.ErrNotExist) {
				c.JSON(http.StatusBadGateway, gin.H{"error": "本地文件清理失败，已保留待删除记录，可稍后重试"})
				return
			}
		}
		if err := os.RemoveAll(filepath.Join(deps.DataDir, "tmp", strconv.FormatInt(postID, 10))); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "本地文件清理失败，已保留待删除记录，可稍后重试"})
			return
		}

		if err := deps.Posts.Delete(c.Request.Context(), postID); err != nil {
			switch {
			case errors.Is(err, posts.ErrNotFound):
				c.JSON(http.StatusNotFound, gin.H{"error": "帖子不存在"})
			case errors.Is(err, posts.ErrTaskRunning):
				c.JSON(http.StatusConflict, gin.H{"error": "任务正在进行中，完成或失败后再删除"})
			default:
				c.JSON(http.StatusInternalServerError, gin.H{"error": "删除帖子失败"})
			}
			return
		}
		c.Status(http.StatusNoContent)
	}
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

		if req.CoverSortOrder < 0 || req.CoverSortOrder > req.ImageCount {
			c.JSON(http.StatusBadRequest, gin.H{"error": "封面序号超出图片数量"})
			return
		}

		mode := req.UploadMode
		if mode == "" {
			// 未指定时沿用全局默认画质（UPLOAD_MODE 或设置页保存值）。
			mode = settings.DefaultUploadMode
			if deps.Settings != nil {
				if effective, err := deps.Settings.Effective(c.Request.Context()); err == nil && effective.UploadMode.Value != "" {
					mode = effective.UploadMode.Value
				}
			}
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
		}
		if len(cover) > 0 || req.CoverSortOrder > 0 {
			if err := deps.Posts.SetCoverSelection(c.Request.Context(), id, req.CoverSortOrder, coverPath); err != nil {
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
