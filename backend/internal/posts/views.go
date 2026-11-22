package posts

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// ViewTarget 是一条待同步浏览量的图集：只取首篇 Telegraph 路径，与分享入口保持一致。
type ViewTarget struct {
	PostID int64
	Path   string
}

// ViewSnapshot 是库里缓存的浏览量，UpdatedAt 为空表示尚未同步过。
type ViewSnapshot struct {
	Count     int
	UpdatedAt string
}

// maxViewPathRunes 限制参与请求的路径长度，避免异常数据打满上游 URL。
const maxViewPathRunes = 200

// viewPathUsable 判断路径能否安全拼进上游地址。
func viewPathUsable(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" || len([]rune(path)) > maxViewPathRunes {
		return false
	}
	if strings.ContainsAny(path, "/\\?# \t\r\n") {
		return false
	}
	return true
}

// firstViewPath 只认首篇路径，与分享入口保持一致；首篇缺失或不可用时返回空串。
func firstViewPath(raw string) string {
	var paths []string
	if err := json.Unmarshal([]byte(raw), &paths); err != nil || len(paths) == 0 {
		return ""
	}
	path := strings.TrimSpace(paths[0])
	if !viewPathUsable(path) {
		return ""
	}
	return path
}

// viewEligible 判断一帖是否在统计范围内：公开、已发布、未撤回。
func viewEligible(state string, public, disabled bool) bool {
	return state == PublishPublished && public && !disabled
}

// ListViewTargets 按 ID 升序小批读取待同步的图集，读完即关闭游标再联网。
func (r *Repository) ListViewTargets(ctx context.Context, afterID int64, limit int) ([]ViewTarget, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.handle.QueryContext(ctx,
		`SELECT id, telegraph_paths FROM posts
		 WHERE id > ? AND publish_state = ? AND is_public = 1 AND is_disabled = 0
		   AND telegraph_paths IS NOT NULL AND telegraph_paths <> '[]'
		 ORDER BY id LIMIT ?`, afterID, PublishPublished, limit)
	if err != nil {
		return nil, errors.New("读取浏览量同步目标失败")
	}
	defer func() { _ = rows.Close() }()

	targets := make([]ViewTarget, 0, limit)
	for rows.Next() {
		var id int64
		var raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, errors.New("读取浏览量同步目标失败")
		}
		if path := firstViewPath(raw); path != "" {
			targets = append(targets, ViewTarget{PostID: id, Path: path})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("读取浏览量同步目标失败")
	}
	return targets, nil
}

// ViewTargetByID 读取单帖的同步目标。eligible 为 false 表示状态不适用，仍可用于判断存在性。
func (r *Repository) ViewTargetByID(ctx context.Context, postID int64) (target ViewTarget, eligible bool, err error) {
	var state string
	var public, disabled int
	var raw string
	err = r.handle.QueryRowContext(ctx,
		`SELECT publish_state, is_public, is_disabled, telegraph_paths FROM posts WHERE id = ?`, postID).
		Scan(&state, &public, &disabled, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return ViewTarget{}, false, ErrNotFound
	}
	if err != nil {
		return ViewTarget{}, false, errors.New("读取图集状态失败")
	}
	path := firstViewPath(raw)
	return ViewTarget{PostID: postID, Path: path},
		viewEligible(state, public == 1, disabled == 1) && path != "", nil
}

// ReadViews 读取单帖缓存的浏览量。
func (r *Repository) ReadViews(ctx context.Context, postID int64) (ViewSnapshot, error) {
	var snapshot ViewSnapshot
	err := r.handle.QueryRowContext(ctx,
		`SELECT views_count, COALESCE(strftime('%Y-%m-%dT%H:%M:%SZ', views_updated_at), '')
		 FROM posts WHERE id = ?`, postID).Scan(&snapshot.Count, &snapshot.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ViewSnapshot{}, ErrNotFound
	}
	if err != nil {
		return ViewSnapshot{}, errors.New("读取浏览量失败")
	}
	return snapshot, nil
}

// SaveViews 写回浏览量。写回前在事务内复核状态与首篇路径，
// 避免在途请求覆盖期间发生的撤回或改链。不修改 updated_at，浏览量不算编辑。
func (r *Repository) SaveViews(ctx context.Context, target ViewTarget, count int, at time.Time) (bool, error) {
	tx, err := r.handle.BeginTx(ctx, nil)
	if err != nil {
		return false, errors.New("写回浏览量失败")
	}
	defer func() { _ = tx.Rollback() }()

	var state string
	var public, disabled int
	var raw string
	err = tx.QueryRowContext(ctx,
		`SELECT publish_state, is_public, is_disabled, telegraph_paths FROM posts WHERE id = ?`, target.PostID).
		Scan(&state, &public, &disabled, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, errors.New("写回浏览量失败")
	}
	if !viewEligible(state, public == 1, disabled == 1) || firstViewPath(raw) != target.Path {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE posts SET views_count = ?, views_updated_at = ? WHERE id = ?`,
		count, at.UTC().Format("2006-01-02T15:04:05Z"), target.PostID); err != nil {
		return false, errors.New("写回浏览量失败")
	}
	if err := tx.Commit(); err != nil {
		return false, errors.New("写回浏览量失败")
	}
	return true, nil
}
