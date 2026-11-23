package posts

import (
	"context"
	"database/sql"
	"errors"
)

// TaskItem 是任务列表的一行：聚合上传、存图与发布三段状态。
type TaskItem struct {
	ID           int64
	Title        string
	Status       string
	PublishState string
	ControlState string
	UploadRound  int
	ImageCount   int
	StoredCount  int
	UpdatedAt    string
}

// TaskPage 是任务列表的一页。
type TaskPage struct {
	Items    []TaskItem
	Total    int
	Page     int
	PageSize int
}

// ListTasks 分页读取进行中与待处理的任务，按最近更新时间倒序。
func (r *Repository) ListTasks(ctx context.Context, page, pageSize int) (*TaskPage, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}

	const filter = `p.pending_delete = 0 AND (
		p.status IN ('uploading', 'queued', 'publishing', 'failed', 'stopped')
		OR p.publish_state IN ('pending', 'publishing', 'failed'))`

	var total int
	if err := r.handle.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM posts p WHERE `+filter).Scan(&total); err != nil {
		return nil, errors.New("统计任务失败")
	}

	rows, err := r.handle.QueryContext(ctx,
		`SELECT p.id, p.title, p.status, p.publish_state,
		        COALESCE(p.control_state, 'running'), COALESCE(p.upload_round, 0), p.image_count,
		        (SELECT COUNT(*) FROM post_images pi
		          WHERE pi.post_id = p.id AND pi.status IN (?, ?)),
		        strftime('%Y-%m-%dT%H:%M:%SZ', p.updated_at)
		 FROM posts p WHERE `+filter+`
		 ORDER BY p.updated_at DESC, p.id DESC LIMIT ? OFFSET ?`,
		ImageStatusUploaded, ImageStatusStored, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, errors.New("读取任务失败")
	}
	defer func() { _ = rows.Close() }()

	items := make([]TaskItem, 0, pageSize)
	for rows.Next() {
		var item TaskItem
		if err := rows.Scan(&item.ID, &item.Title, &item.Status, &item.PublishState,
			&item.ControlState, &item.UploadRound, &item.ImageCount, &item.StoredCount,
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

// 让代码检查时明确依赖 sql 包中的通用错误类型。
var _ = sql.ErrNoRows
