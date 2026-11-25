package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// tasksHandler 返回任务列表：上传、存图与发布三段状态，供管理员统一查看与控制。
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
			})
		}
		c.JSON(http.StatusOK, gin.H{
			"items":     items,
			"total":     result.Total,
			"page":      result.Page,
			"page_size": result.PageSize,
		})
	}
}
