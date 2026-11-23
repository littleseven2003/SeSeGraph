package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

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

// liveBackupPlan 按当前生效配置计算自动备份计划，避免展示进程启动时的旧值。
func liveBackupPlan(deps Deps) (bool, string, string) {
	if deps.Settings == nil {
		// 未装配设置解析器时沿用启动时的展示文案，保持旧行为。
		if deps.BackupSchedule == "" {
			return false, "", ""
		}
		return true, deps.BackupSchedule, ""
	}
	effective, err := deps.Settings.Effective(context.Background())
	if err != nil {
		return false, "", ""
	}
	enabled := strings.EqualFold(strings.TrimSpace(effective.BackupEnabled.Value), "true")
	schedule := effective.BackupTime.Value
	if !enabled {
		return false, schedule, ""
	}
	hour, minute, err := parseScheduleClock(schedule)
	if err != nil {
		return true, schedule, ""
	}
	now := time.Now()
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
	if !next.After(now) {
		next = next.Add(24 * time.Hour)
	}
	return true, schedule, next.Format(time.RFC3339)
}

// parseScheduleClock 解析 HH:MM 时刻。
func parseScheduleClock(raw string) (int, int, error) {
	parts := strings.SplitN(strings.TrimSpace(raw), ":", 2)
	if len(parts) != 2 {
		return 0, 0, errors.New("时刻格式不正确")
	}
	hour, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, err
	}
	minute, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, err
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, 0, errors.New("时刻超出范围")
	}
	return hour, minute, nil
}

// backupStatusHandler 返回最近一次备份信息与定时备份说明。
func backupStatusHandler(deps Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		enabled, schedule, nextRun := liveBackupPlan(deps)
		payload := gin.H{
			"schedule": schedule,
			"enabled":  enabled,
			"next_run": nextRun,
			"timezone": time.Local.String(),
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
