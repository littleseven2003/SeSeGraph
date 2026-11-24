package api

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/littleseven2003/SeSeGraph/backend/internal/media"
	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
)

// maxThumbBodyBytes 是缩略图上传请求体上限，比硬上限留一点余量用于拒绝超限。
const maxThumbBodyBytes = media.MaxBytes + 1024

// mediaThumbURL 返回某一项的小图地址；没有落盘记录时为空，客户端据此显示占位。
func mediaThumbURL(image *posts.Image) string {
	if image == nil || strings.TrimSpace(image.ThumbPath) == "" || image.MediaVersion == "" {
		return ""
	}
	return "/api/posts/" + strconv.FormatInt(image.PostID, 10) + "/media/" + strconv.Itoa(image.SortOrder) +
		"/thumb?v=" + image.MediaVersion
}

// mediaThumbHandler 读取逐图缩略图：命中时直接返回 WebP，不再回源原媒体。
func mediaThumbHandler(deps Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		postID, sortOrder, ok := thumbTarget(c)
		if !ok {
			return
		}
		image, err := deps.Posts.ImageByOrder(c.Request.Context(), postID, sortOrder)
		if errors.Is(err, posts.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "缩略图不存在"})
			return
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "读取缩略图失败"})
			return
		}
		if strings.TrimSpace(image.ThumbPath) == "" {
			c.JSON(http.StatusNotFound, gin.H{"error": "缩略图不存在"})
			return
		}

		file, info, openErr := media.Open(deps.DataDir, image.ThumbPath)
		if openErr != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "缩略图不存在"})
			return
		}
		defer func() { _ = file.Close() }()

		// 访问时间即 LRU 依据，命中就前移一位。
		media.Touch(deps.DataDir, image.ThumbPath)
		c.Header("Content-Type", "image/webp")
		c.Header("Cache-Control", "private, max-age=60")
		c.Header("X-Content-Type-Options", "nosniff")
		http.ServeContent(c.Writer, c.Request, strconv.Itoa(image.SortOrder)+".webp", info.ModTime(), file)
	}
}

// mediaThumbUploadHandler 接收客户端生成的缩略图并落盘。
// 只接受与当前内容版本一致的写入，迟到的请求会被拒绝，不会覆盖替换后的媒体。
func mediaThumbUploadHandler(deps Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		postID, sortOrder, ok := thumbTarget(c)
		if !ok {
			return
		}
		// 读写共用同一查询参数名，避免前端带 v、后端读 version 时被静默拒收。
		version := strings.TrimSpace(c.Query("v"))
		if version == "" {
			version = strings.TrimSpace(c.Query("version"))
		}
		if !media.ValidVersion(version) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "缩略图版本不合法"})
			return
		}

		// 限长读取放在锁外：慢连接只占自己的请求，不会拖住其他回存、清理与重建。
		body, readErr := io.ReadAll(io.LimitReader(c.Request.Body, maxThumbBodyBytes+1))
		if readErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "读取缩略图失败"})
			return
		}
		if int64(len(body)) > maxThumbBodyBytes {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "缩略图超过上限"})
			return
		}

		// 提交阶段与清理、重建互斥：这里的版本复核、落盘与记录写入不会被半途打断。
		unlock := media.LockCommit()
		defer unlock()

		image, err := deps.Posts.ImageByOrder(c.Request.Context(), postID, sortOrder)
		switch {
		case errors.Is(err, posts.ErrNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "图片不存在"})
			return
		case err != nil:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "读取图片失败"})
			return
		}
		// 请求体读取期间可能发生替换上传或清理，锁内按最新版本重新核对。
		if image.MediaVersion != version {
			c.JSON(http.StatusConflict, gin.H{"error": "媒体内容已更新，请重新生成缩略图"})
			return
		}

		relPath, size, storeErr := media.Store(deps.DataDir, postID, sortOrder, version, body, thumbCacheLimit(deps, c))
		switch {
		case errors.Is(storeErr, media.ErrTooLarge):
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "缩略图超过上限"})
			return
		case errors.Is(storeErr, media.ErrInvalidThumb):
			c.JSON(http.StatusBadRequest, gin.H{"error": "缩略图内容不合法"})
			return
		case errors.Is(storeErr, media.ErrCacheFull):
			c.JSON(http.StatusInsufficientStorage, gin.H{"error": "缩略图缓存已满，请调大上限或清理后重试"})
			return
		case errors.Is(storeErr, media.ErrDiskFull):
			c.JSON(http.StatusInsufficientStorage, gin.H{"error": "磁盘余量不足，暂停写入小图"})
			return
		case storeErr != nil:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存缩略图失败"})
			return
		}

		if err := deps.Posts.SetThumb(c.Request.Context(), postID, sortOrder, version, relPath, size); err != nil {
			// 记录没写成时删掉刚写的文件，避免留下无人引用的副本。
			media.Remove(deps.DataDir, relPath)
			if errors.Is(err, posts.ErrStaleMedia) {
				c.JSON(http.StatusConflict, gin.H{"error": "媒体内容已更新，请重新生成缩略图"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存缩略图失败"})
			return
		}

		// 换成新文件后清掉上一份，磁盘只留当前版本。
		if image.ThumbPath != "" && image.ThumbPath != relPath {
			media.Remove(deps.DataDir, image.ThumbPath)
		}

		c.JSON(http.StatusOK, gin.H{
			"stored":    true,
			"bytes":     size,
			"thumb_url": "/api/posts/" + strconv.FormatInt(postID, 10) + "/media/" + strconv.Itoa(sortOrder) + "/thumb?v=" + version,
		})
	}
}

// mediaThumbResetHandler 清掉某帖的小图并换新内容版本，供设置页按图集重建。
// 换版本会让在途的迟到写入收到 409，避免旧任务把文件重新写回来。
func mediaThumbResetHandler(deps Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		unlock := media.LockCommit()
		defer unlock()

		postID, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || postID <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "帖子编号不合法"})
			return
		}
		if deps.Posts == nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "重置缩略图失败"})
			return
		}
		if err := deps.Posts.ResetPostThumbs(c.Request.Context(), postID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "重置缩略图记录失败"})
			return
		}
		// 只删旧版本文件：重置期间刚落盘的新版本保留，迟到的旧版本写入会被版本校验拒绝。
		keep, err := deps.Posts.MediaVersions(c.Request.Context(), postID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "读取媒体版本失败"})
			return
		}
		removed, freed, err := media.RemovePostVersions(deps.DataDir, postID, keep)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "清理缩略图文件失败"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"removed": removed, "freed_bytes": freed})
	}
}

// thumbCacheLimit 读取当前生效的逐图缓存上限，配置不可用时按不限处理。
func thumbCacheLimit(deps Deps, c *gin.Context) int64 {
	if deps.Settings == nil {
		return 0
	}
	effective, err := deps.Settings.Effective(c.Request.Context())
	if err != nil {
		return 0
	}
	return parseIntValue(effective.MediaThumbLimit.Value)
}

// thumbTarget 解析路径里的帖子编号与序号。
func thumbTarget(c *gin.Context) (int64, int, bool) {
	postID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || postID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "帖子编号不合法"})
		return 0, 0, false
	}
	sortOrder, err := strconv.Atoi(c.Param("sort"))
	if err != nil || sortOrder <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "序号不合法"})
		return 0, 0, false
	}
	return postID, sortOrder, true
}
