package posts

import (
	"context"
	"database/sql"
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
	// StoredCount 是对外兼容口径：已上传加已存入频道，不含存频道失败项。
	StoredCount int
	// ReceivedCount 是真实已接收：上传、入库与存频道失败都算服务端收到过。
	ReceivedCount int
	// UploadedCount 只统计仍在服务器、尚未存入频道的图片。
	UploadedCount int
	// ChannelCount 只统计已存入 Telegram 频道的图片。
	ChannelCount int
	// ErrorMessage 是任务失败的可读原因，未失败时为空。
	ErrorMessage string
	// PublishError 是发布失败的可读原因，未失败时为空。
	PublishError string
	// CompressedCount 是提交时浏览器已处理的项数；nil 表示旧任务没有这项记录。
	CompressedCount *int
	// PublishIntent 是提交时选择的发布意图：auto 表示同步完成后自动入队，空串表示旧任务或仅存图。
	PublishIntent string
	UpdatedAt       string
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
// 筛选值缺省时返回全部历史任务（含已完成与已发布），分页越界会钳到最后一页。
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
	// 命中数在翻页期间可能变少：把请求页钳到最后一页，避免返回空页让界面误判为空。
	lastPage := (total + pageSize - 1) / pageSize
	if lastPage < 1 {
		lastPage = 1
	}
	if page > lastPage {
		page = lastPage
	}

	rows, err := r.handle.QueryContext(ctx,
		`SELECT p.id, p.title, p.status, p.publish_state,
		        COALESCE(p.control_state, 'running'), COALESCE(p.upload_round, 0), p.image_count,
		        (SELECT COUNT(*) FROM post_images pi
		          WHERE pi.post_id = p.id AND pi.status IN (?, ?, ?)),
		        (SELECT COUNT(*) FROM post_images pi
		          WHERE pi.post_id = p.id AND pi.status IN (?, ?)),
		        (SELECT COUNT(*) FROM post_images pi
		          WHERE pi.post_id = p.id AND pi.status = ?),
		        (SELECT COUNT(*) FROM post_images pi
		          WHERE pi.post_id = p.id AND pi.status = ?),
		        strftime('%Y-%m-%dT%H:%M:%SZ', p.updated_at),
		        p.compressed_count, COALESCE(p.publish_intent, ''),
		        COALESCE(p.error_message, ''), COALESCE(p.publish_error, '')
		 FROM posts p WHERE `+where+`
		 ORDER BY p.updated_at DESC, p.id DESC LIMIT ? OFFSET ?`,
		ImageStatusUploaded, ImageStatusStored, ImageStatusFailed,
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
		var compressed sql.NullInt64
		if err := rows.Scan(&item.ID, &item.Title, &item.Status, &item.PublishState,
			&item.ControlState, &item.UploadRound, &item.ImageCount,
			&item.ReceivedCount, &item.StoredCount, &item.UploadedCount, &item.ChannelCount,
			&item.UpdatedAt, &compressed, &item.PublishIntent,
			&item.ErrorMessage, &item.PublishError); err != nil {
			return nil, errors.New("读取任务失败")
		}
		if compressed.Valid {
			count := int(compressed.Int64)
			item.CompressedCount = &count
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("读取任务失败")
	}
	return &TaskPage{Items: items, Total: total, Page: page, PageSize: pageSize}, nil
}
