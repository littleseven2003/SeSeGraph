package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
	"github.com/littleseven2003/SeSeGraph/backend/internal/views"
)

// viewsRefreshResponse 是手动刷新浏览量的接口结构。
type viewsRefreshResponse struct {
	PostID         int64   `json:"post_id"`
	ViewsCount     int     `json:"views_count"`
	ViewsUpdatedAt *string `json:"views_updated_at"`
	Cached         bool    `json:"cached"`
}

// refreshViewsHandler 手动刷新单帖浏览量。冷却期内直接返回本地缓存。
func refreshViewsHandler(deps Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if deps.Views == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "浏览量同步不可用"})
			return
		}
		postID, parseErr := strconv.ParseInt(c.Param("id"), 10, 64)
		if parseErr != nil || postID <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "帖子编号不合法"})
			return
		}

		result, err := deps.Views.Refresh(c.Request.Context(), postID)
		switch {
		case errors.Is(err, posts.ErrNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "帖子不存在"})
			return
		case errors.Is(err, views.ErrNotApplicable):
			c.JSON(http.StatusConflict, gin.H{"error": "该图集尚未发布，暂不统计浏览量"})
			return
		case errors.Is(err, views.ErrFetch):
			c.JSON(http.StatusBadGateway, gin.H{"error": "读取浏览量失败，请稍后再试"})
			return
		case err != nil:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "刷新浏览量失败"})
			return
		}

		c.JSON(http.StatusOK, viewsRefreshResponse{
			PostID:         result.PostID,
			ViewsCount:     result.Count,
			ViewsUpdatedAt: optionalTimestamp(result.UpdatedAt),
			Cached:         result.Cached,
		})
	}
}
