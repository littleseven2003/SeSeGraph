package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
)

// controlAction 表示一次任务控制操作，便于三个入口共用错误映射。
type controlAction struct {
	name string
	run  func(ctx context.Context, postID int64) error
}

// controlHandler 处理暂停、继续与停止：三个动作共用同一套状态校验与回包。
func controlHandler(deps Deps, action controlAction) gin.HandlerFunc {
	return func(c *gin.Context) {
		postID, parseErr := strconv.ParseInt(c.Param("id"), 10, 64)
		if parseErr != nil || postID <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "帖子编号不合法"})
			return
		}
		if rejectPendingDelete(c, deps, postID) {
			return
		}

		err := action.run(c.Request.Context(), postID)
		switch {
		case err == nil:
		case errors.Is(err, posts.ErrNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "帖子不存在"})
			return
		case errors.Is(err, posts.ErrControlInvalid):
			c.JSON(http.StatusConflict, gin.H{"error": "当前状态不允许" + action.name})
			return
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": action.name + "失败"})
			return
		}

		state, round, err := deps.Posts.Control(c.Request.Context(), postID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "读取任务状态失败"})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"id":            postID,
			"control_state": state,
			"upload_round":  round,
		})
	}
}

// pauseTaskHandler 暂停任务；继续上传只补服务端缺项。
func pauseTaskHandler(deps Deps) gin.HandlerFunc {
	return controlHandler(deps, controlAction{name: "暂停", run: deps.Posts.Pause})
}

// resumeTaskHandler 恢复暂停中的任务，轮次加一使迟到请求失效。
func resumeTaskHandler(deps Deps) gin.HandlerFunc {
	return controlHandler(deps, controlAction{name: "继续", run: deps.Posts.Resume})
}

// stopTaskHandler 停止任务，停止后短码关闭且不再被队列领取。
func stopTaskHandler(deps Deps) gin.HandlerFunc {
	return controlHandler(deps, controlAction{name: "停止", run: deps.Posts.Stop})
}
