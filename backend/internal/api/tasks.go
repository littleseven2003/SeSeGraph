package api

import (
	"context"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/littleseven2003/SeSeGraph/backend/internal/botpool"
)

// tasksHandler 返回任务列表：上传、存图与发布三段状态，供管理员统一查看与控制。
// poolStatus 汇报 Bot 池里已冻结的条目，供任务页提示当前会自动换 Bot。
// 池未启用时返回 enabled=false，前端据此不显示提示。
func poolStatus(ctx context.Context, deps Deps) gin.H {
	frozen := make([]gin.H, 0, 2)
	if deps.Bots == nil {
		return gin.H{"enabled": false, "frozen": frozen}
	}
	bots, err := deps.Bots.List(ctx)
	if err != nil {
		return gin.H{"enabled": false, "frozen": frozen}
	}
	usable := 0
	for _, bot := range bots {
		if bot.Status == "" || bot.Status == botpool.StatusOK {
			usable++
			continue
		}
		if bot.Status == botpool.StatusFrozen {
			frozen = append(frozen, gin.H{"alias": bot.Alias, "note": bot.StatusNote})
		}
	}
	return gin.H{"enabled": usable >= 2, "frozen": frozen}
}

func tasksHandler(deps Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
		pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
		// status 缺省表示全部，未知取值由仓储层回落为全部，旧调用保持原语义。
		status := c.Query("status")
		result, err := deps.Posts.ListTasks(c.Request.Context(), page, pageSize, status)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "读取任务列表失败"})
			return
		}

		items := make([]gin.H, 0, len(result.Items))
		for _, item := range result.Items {
			items = append(items, gin.H{
				"id":               item.ID,
				"title":            item.Title,
				"status":           item.Status,
				"publish_state":    item.PublishState,
				"control_state":    item.ControlState,
				"upload_round":     item.UploadRound,
				"image_count":      item.ImageCount,
				"stored_count":     item.StoredCount,
				"received_count":   item.ReceivedCount,
				"compressed_count": item.CompressedCount,
				"publish_intent":   item.PublishIntent,
				"uploaded_count":   item.UploadedCount,
				"channel_count":    item.ChannelCount,
				"updated_at":       item.UpdatedAt,
				"error":            item.ErrorMessage,
				"publish_error":    item.PublishError,
			})
		}
		c.JSON(http.StatusOK, gin.H{
			"items":     items,
			"pool":      poolStatus(c.Request.Context(), deps),
			"total":     result.Total,
			"page":      result.Page,
			"page_size": result.PageSize,
		})
	}
}
