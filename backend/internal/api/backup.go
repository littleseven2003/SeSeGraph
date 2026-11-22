package api

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"
	"github.com/littleseven2003/SeSeGraph/backend/internal/backup"
)

// backupTriggerHandler 立即生成一份热备。已有备份在进行时返回 409。
// 发送失败不影响本地备份，响应里用 warning 说明。
func backupTriggerHandler(deps Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if deps.Backup == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "备份不可用"})
			return
		}
		if err := deps.Backup.Start(); err != nil {
			if errors.Is(err, backup.ErrBusy) {
				c.JSON(http.StatusConflict, gin.H{"error": "备份正在进行中"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "备份失败"})
			return
		}

		result, err := deps.Backup.Run(c.Request.Context())
		if err != nil && result.Name == "" {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "备份失败"})
			return
		}
		payload := gin.H{
			"name":       result.Name,
			"size":       result.Size,
			"created_at": result.CreatedAt,
			"sent":       result.Sent,
			"includes":   result.Includes,
		}
		if err != nil {
			payload["warning"] = backupWarning(err)
		}
		c.JSON(http.StatusOK, payload)
	}
}

// backupWarning 把发送失败的原因转成固定文案，不把上游细节带到响应里。
func backupWarning(err error) string {
	switch {
	case errors.Is(err, backup.ErrTargetPublic):
		return "备份目标是公开频道，已只保留本地备份"
	case errors.Is(err, backup.ErrTargetUnverified):
		return "无法确认备份目标是否私有，已只保留本地备份"
	default:
		return "备份已生成，但发送到频道失败"
	}
}

// backupDownloadHandler 下载最近一份备份。文件以流式方式发送，不整包读入内存。
func backupDownloadHandler(deps Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if deps.Backup == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "备份不可用"})
			return
		}
		path, err := deps.Backup.LatestPath()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				c.Header("Cache-Control", "no-store")
				c.JSON(http.StatusNotFound, gin.H{"error": "还没有备份"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "读取备份失败"})
			return
		}
		c.Header("Cache-Control", "no-store")
		c.FileAttachment(path, filepath.Base(path))
	}
}

// backupStatusHandler 返回最近一次备份信息与定时备份说明。
func backupStatusHandler(deps Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		payload := gin.H{
			"schedule": deps.BackupSchedule,
			"enabled":  deps.BackupSchedule != "",
			"latest":   nil,
		}
		if deps.Backup == nil {
			c.JSON(http.StatusOK, payload)
			return
		}
		if latest, err := deps.Backup.Latest(); err == nil {
			payload["latest"] = gin.H{
				"name":       latest.Name,
				"size":       latest.Size,
				"created_at": latest.CreatedAt,
			}
		}
		c.JSON(http.StatusOK, payload)
	}
}
