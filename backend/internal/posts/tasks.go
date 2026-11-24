package posts

import (
	"context"
	"errors"
)

// 任务列表的筛选值：空串表示全部，保持旧调用的缺省语义。
const (
	TaskFilterAll      = ""
	TaskFilterActive   = "active"
	TaskFilterPaused   = "paused"
	TaskFilterStopped  = "stopped"
	TaskFilterFinished = "finished"
)

// TaskItem 是任务列表的一行：上传、存图与发布三段状态各自独立。
type TaskItem struct {
	ID           int64
	Title        string
	Status       string
	PublishState string
	ControlState string
	UploadRound  int
	ImageCount   int
	// ReceivedCount 是「已接收」张数，等于已上传加已存入频道，字段名沿用 stored_count。
	ReceivedCount int
	// UploadedCount 只统计仍在服务器、尚未存入频道的图片。
	UploadedCount int
	// ChannelCount 只统计已存入 Telegram 频道的图片。
	ChannelCount int
	UpdatedAt    string
}

// TaskPage 是任务列表的一页。
type TaskPage struct {
	Items    []TaskItem
	Total    int
	Page     int
	PageSize int
}

// taskWhere 把筛选值翻译成 WHERE 片段；未知取值回落为全部，避免旧调用失效。
// 暂停与停止优先于完成：control_state 一旦置位就只进对应分组。
func taskWhere(filter string) string {
	const base = `p.pending_delete = 0 AND `
	switch filter {
	case TaskFilterActive:
		return base + `COALESCE(p.control_state, 'running') = 'running' AND (
			p.status IN ('uploading', 'queued', 'publishing')
			OR p.publish_state IN ('pending', 'publishing'))`
	case TaskFilterPaused:
		return base + `p.control_state = 'paused'`
	case TaskFilterStopped:
		return base + `p.control_state = 'stopped'`
	case TaskFilterFinished:
		return base + `COALESCE(p.control_state, 'running') = 'running' AND p.publish_state = 'published'`
	default:
		// 全部覆盖五类：已发布与已完成也在其中，避免筛选页之外看不到已完成任务。
		return base + `(p.status IN ('uploading', 'queued', 'publishing', 'failed', 'stopped', 'completed')
			OR p.publish_state IN ('pending', 'publishing', 'failed', 'published'))`
	}
}

// ListTasks 按筛选条件分页读取任务，按最近更新时间倒序。
func (r *Repository) ListTasks(ctx context.Context, page, pageSize int, filter string) (*TaskPage, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}

	where := taskWhere(filter)
	var total int
	if err := r.handle.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM posts p WHERE `+where).Scan(&total); err != nil {
		return nil, errors.New("统计任务失败")
	}

	rows, err := r.handle.QueryContext(ctx,
		`SELECT p.id, p.title, p.status, p.publish_state,
		        COALESCE(p.control_state, 'running'), COALESCE(p.upload_round, 0), p.image_count,
		        (SELECT COUNT(*) FROM post_images pi
		          WHERE pi.post_id = p.id AND pi.status IN (?, ?)),
		        (SELECT COUNT(*) FROM post_images pi
		          WHERE pi.post_id = p.id AND pi.status = ?),
		        (SELECT COUNT(*) FROM post_images pi
		          WHERE pi.post_id = p.id AND pi.status = ?),
		        strftime('%Y-%m-%dT%H:%M:%SZ', p.updated_at)
		 FROM posts p WHERE `+where+`
		 ORDER BY p.updated_at DESC, p.id DESC LIMIT ? OFFSET ?`,
		ImageStatusUploaded, ImageStatusStored,
		ImageStatusUploaded, ImageStatusStored,
		pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, errors.New("读取任务失败")
	}
	defer func() { _ = rows.Close() }()

	items := make([]TaskItem, 0, pageSize)
	for rows.Next() {
		var item TaskItem
		if err := rows.Scan(&item.ID, &item.Title, &item.Status, &item.PublishState,
			&item.ControlState, &item.UploadRound, &item.ImageCount,
			&item.ReceivedCount, &item.UploadedCount, &item.ChannelCount,
			&item.UpdatedAt); err != nil {
			return nil, errors.New("读取任务失败")
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("读取任务失败")
	}
	return &TaskPage{Items: items, Total: total, Page: page, PageSize: pageSize}, nil
}
