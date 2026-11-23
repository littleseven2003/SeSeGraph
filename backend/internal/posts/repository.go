package posts

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

// 帖子状态取值。
const (
	StatusUploading  = "uploading"
	StatusQueued     = "queued"
	StatusPublishing = "publishing"
	StatusCompleted  = "completed"
	StatusFailed     = "failed"
)

// 发布状态取值：idle 未发布、pending 待发布、publishing 发布中、published 已发布、failed 发布失败。
const (
	PublishIdle       = "idle"
	PublishPending    = "pending"
	PublishPublishing = "publishing"
	PublishPublished  = "published"
	PublishFailed     = "failed"
)

// 图片状态取值。
const (
	ImageStatusUploaded = "uploaded"
	ImageStatusStored   = "stored"
	ImageStatusFailed   = "failed"
)

var (
	// ErrNotFound 表示帖子或图片记录不存在。
	ErrNotFound = errors.New("帖子不存在")
	// ErrNotUploading 表示帖子已不在可上传状态。
	ErrNotUploading = errors.New("当前状态不允许继续上传")
	// ErrAlreadyQueued 表示帖子已经入队，重复提交视为成功。
	ErrAlreadyQueued = errors.New("帖子已入队")
	// ErrPublishBusy 表示帖子正在发布队列中或已有可复用的远端页面，暂不可本地改写。
	ErrPublishBusy = errors.New("帖子正在发布流程中")
	// ErrTaskRunning 表示帖子仍在进行中的任务里，删除会与队列交错。
	ErrTaskRunning = errors.New("任务正在进行中")
)

// Post 是帖子元数据。
type Post struct {
	ID             int64
	Title          string
	Description    string
	AuthorName     string
	CoverThumbPath string
	ImageCount     int
	UploadMode     string
	Status         string
	Tags           []string
}

// PublishInfo 是发布一帖所需的元数据与已存图列表。
type PublishInfo struct {
	PostID      int64
	Title       string
	Description string
	AuthorName  string
	State       string
	Images      []Image
	Paths       []string
	// CoverSortOrder 是封面来源序号，0 表示未选择封面。
	CoverSortOrder int
	// PathMode 是入队时冻结的链接生成方式，Slug 是随之冻结的随机短码。
	PathMode string
	Slug     string
}

// PublicImage 是网关可以对外转发的图片信息，仅包含必要字段。
type PublicImage struct {
	ImageID      int64
	PostID       int64
	RemoteFileID string
	MIMEType     string
	UploadMode   string
}

// Image 是帖子中的单张图片记录。
type Image struct {
	ID           int64
	PostID       int64
	SortOrder    int
	ShortCode    string
	MimeType     string
	FileSize     int64
	TempFilePath string
	Status       string
	ErrorMessage string
}

// Repository 负责帖子与图片记录的读写。
type Repository struct {
	handle *sql.DB
}

// NewRepository 基于现有数据库连接创建仓储。
func NewRepository(handle *sql.DB) *Repository {
	return &Repository{handle: handle}
}

// Create 保存帖子元数据，返回新记录编号。
func (r *Repository) Create(ctx context.Context, post Post) (int64, error) {
	if post.UploadMode == "" {
		post.UploadMode = "photo"
	}
	if post.Status == "" {
		post.Status = StatusUploading
	}
	tx, err := r.handle.BeginTx(ctx, nil)
	if err != nil {
		return 0, errors.New("保存帖子失败")
	}
	defer func() { _ = tx.Rollback() }()

	result, err := tx.ExecContext(ctx,
		`INSERT INTO posts (title, description, author_name, cover_thumb_path, image_count, upload_mode, status)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		post.Title, post.Description, post.AuthorName, post.CoverThumbPath, post.ImageCount, post.UploadMode, post.Status,
	)
	if err != nil {
		return 0, errors.New("保存帖子失败")
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, errors.New("保存帖子失败")
	}
	if err := attachTags(ctx, tx, id, post.Tags); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, errors.New("保存帖子失败")
	}
	return id, nil
}

// attachTags 写入标签关联，标签名重复时复用已有记录。
func attachTags(ctx context.Context, tx *sql.Tx, postID int64, tags []string) error {
	for _, tag := range tags {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO tags (name) VALUES (?) ON CONFLICT(name) DO NOTHING`, tag); err != nil {
			return errors.New("保存标签失败")
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO post_tags (post_id, tag_id) SELECT ?, id FROM tags WHERE name = ?`,
			postID, tag); err != nil {
			return errors.New("保存标签失败")
		}
	}
	return nil
}

// ListTags 按名称顺序返回帖子的标签。
func (r *Repository) ListTags(ctx context.Context, postID int64) ([]string, error) {
	rows, err := r.handle.QueryContext(ctx,
		`SELECT t.name FROM post_tags pt JOIN tags t ON t.id = pt.tag_id
		 WHERE pt.post_id = ? ORDER BY t.name`, postID)
	if err != nil {
		return nil, errors.New("读取标签失败")
	}
	defer func() { _ = rows.Close() }()

	tags := make([]string, 0, 4)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, errors.New("读取标签失败")
		}
		tags = append(tags, name)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("读取标签失败")
	}
	return tags, nil
}

// SetDisabled 把单帖熔断状态写成目标值，返回写入后的禁用与公开标记。
// 显式目标值让重复提交保持同一结果：首次撤回成功但响应丢失时，重试不会把图片重新公开。
// 只改 is_disabled 与变化时的 updated_at，不动 is_public，恢复后沿用原有公开授权。
func (r *Repository) SetDisabled(ctx context.Context, postID int64, disabled bool) (bool, bool, error) {
	target := 0
	if disabled {
		target = 1
	}
	var gotDisabled, public int
	err := r.handle.QueryRowContext(ctx,
		`UPDATE posts SET is_disabled = ?,
		        updated_at = CASE WHEN is_disabled = ? THEN updated_at ELSE CURRENT_TIMESTAMP END
		 WHERE id = ? AND pending_delete = 0 RETURNING is_disabled, is_public`,
		target, target, postID).Scan(&gotDisabled, &public)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, r.missingOrPending(ctx, postID)
	}
	if err != nil {
		return false, false, errors.New("更新熔断状态失败")
	}
	return gotDisabled == 1, public == 1, nil
}

// ErrPendingDelete 表示帖子正处于删除流程中。
var ErrPendingDelete = errors.New("帖子正在删除")

// missingOrPending 在条件更新未命中时区分「不存在」与「正在删除」。
func (r *Repository) missingOrPending(ctx context.Context, postID int64) error {
	var pending int
	err := r.handle.QueryRowContext(ctx, `SELECT pending_delete FROM posts WHERE id = ?`, postID).Scan(&pending)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return errors.New("读取帖子状态失败")
	}
	if pending == 1 {
		return ErrPendingDelete
	}
	return ErrNotFound
}

// missingOrBusy 区分「不存在」「正在删除」与「发布状态已变化」。
func (r *Repository) missingOrBusy(ctx context.Context, postID int64) error {
	var pending int
	err := r.handle.QueryRowContext(ctx, `SELECT pending_delete FROM posts WHERE id = ?`, postID).Scan(&pending)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return errors.New("读取帖子状态失败")
	}
	if pending == 1 {
		return ErrPendingDelete
	}
	return ErrPublishBusy
}

// MarkPendingDelete 在短事务里标记待删除并立即停用短码，返回待清空的远端页面路径。
// 已经处于待删除状态时直接复用现有标记，供失败后重试。
func (r *Repository) MarkPendingDelete(ctx context.Context, postID int64) ([]string, error) {
	tx, err := r.handle.BeginTx(ctx, nil)
	if err != nil {
		return nil, errors.New("标记删除失败")
	}
	defer func() { _ = tx.Rollback() }()

	var (
		status       string
		publishState string
		urls         string
		pending      int
	)
	err = tx.QueryRowContext(ctx,
		`SELECT status, publish_state, telegraph_paths, pending_delete FROM posts WHERE id = ?`, postID).
		Scan(&status, &publishState, &urls, &pending)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, errors.New("标记删除失败")
	}

	if pending == 0 {
		switch status {
		case StatusUploading, StatusQueued, StatusPublishing:
			return nil, ErrTaskRunning
		}
		switch publishState {
		case PublishPending, PublishPublishing:
			return nil, ErrTaskRunning
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE posts SET pending_delete = 1, is_disabled = 1, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
			postID); err != nil {
			return nil, errors.New("标记删除失败")
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, errors.New("标记删除失败")
	}
	return parseStringList(urls), nil
}

// PendingDelete 报告帖子是否处于删除流程中。
func (r *Repository) PendingDelete(ctx context.Context, postID int64) (bool, error) {
	var pending int
	err := r.handle.QueryRowContext(ctx, `SELECT pending_delete FROM posts WHERE id = ?`, postID).Scan(&pending)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, errors.New("读取删除状态失败")
	}
	return pending == 1, nil
}

// Delete 删除帖子及其图片、标签关联。上传或发布进行中时拒绝删除。
func (r *Repository) Delete(ctx context.Context, postID int64) error {
	tx, err := r.handle.BeginTx(ctx, nil)
	if err != nil {
		return errors.New("删除帖子失败")
	}
	defer func() { _ = tx.Rollback() }()

	var status, publishState string
	err = tx.QueryRowContext(ctx, `SELECT status, publish_state FROM posts WHERE id = ?`, postID).
		Scan(&status, &publishState)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return errors.New("删除帖子失败")
	}
	switch status {
	case StatusUploading, StatusQueued, StatusPublishing:
		return ErrTaskRunning
	}
	// 存图完成但 Telegraph 待发布或发布中时同样拒绝，避免发布队列拿着已删记录。
	switch publishState {
	case PublishPending, PublishPublishing:
		return ErrTaskRunning
	}

	// 图片与标签关联显式删除，不依赖外键级联的具体实现。
	if _, err := tx.ExecContext(ctx, `DELETE FROM post_images WHERE post_id = ?`, postID); err != nil {
		return errors.New("删除帖子失败")
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM post_tags WHERE post_id = ?`, postID); err != nil {
		return errors.New("删除帖子失败")
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM posts WHERE id = ?`, postID); err != nil {
		return errors.New("删除帖子失败")
	}
	if err := tx.Commit(); err != nil {
		return errors.New("删除帖子失败")
	}
	return nil
}

// CountStoredImages 统计已存入图床的图片数量，用于判断换 Bot 是否会破坏已有图片。
func (r *Repository) CountStoredImages(ctx context.Context) (int64, error) {
	var count int64
	err := r.handle.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM post_images WHERE status = ? AND remote_file_id <> ''`,
		ImageStatusStored).Scan(&count)
	if err != nil {
		return 0, errors.New("统计已存图片失败")
	}
	return count, nil
}

// SampleStoredFileIDs 返回若干已存图片的远端文件标识，用于确认旧库图片是否属于同一个 Bot。
// 远端文件被删除时这些标识可能失效，因此取多条并允许调用方逐条尝试。
func (r *Repository) SampleStoredFileIDs(ctx context.Context, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 1
	}
	rows, err := r.handle.QueryContext(ctx,
		`SELECT remote_file_id FROM post_images
		 WHERE status = ? AND remote_file_id <> ''
		 ORDER BY id DESC LIMIT ?`,
		ImageStatusStored, limit)
	if err != nil {
		return nil, errors.New("读取已存图片失败")
	}
	defer func() { _ = rows.Close() }()

	fileIDs := make([]string, 0, limit)
	for rows.Next() {
		var fileID string
		if err := rows.Scan(&fileID); err != nil {
			return nil, errors.New("读取已存图片失败")
		}
		fileIDs = append(fileIDs, fileID)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("读取已存图片失败")
	}
	return fileIDs, nil
}

// Get 读取帖子元数据。
func (r *Repository) Get(ctx context.Context, id int64) (*Post, error) {
	post := &Post{ID: id}
	err := r.handle.QueryRowContext(ctx,
		`SELECT title, description, author_name, cover_thumb_path, image_count, upload_mode, status
		 FROM posts WHERE id = ?`, id,
	).Scan(&post.Title, &post.Description, &post.AuthorName, &post.CoverThumbPath,
		&post.ImageCount, &post.UploadMode, &post.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, errors.New("读取帖子失败")
	}
	return post, nil
}

// PublicImage 按短码查询公开图片：帖子被显式标记为公开且未禁用，图片已存图并有远端标识。
// 公开标记默认关闭，旧版本已存图的任务不会自动对外，必须经管理员确认后开启。
// 任一条件不满足都返回 ErrNotFound，让网关在请求上游前就返回 404。
func (r *Repository) PublicImage(ctx context.Context, shortCode string) (*PublicImage, error) {
	if shortCode == "" {
		return nil, ErrNotFound
	}
	image := &PublicImage{}
	err := r.handle.QueryRowContext(ctx,
		`SELECT i.id, i.post_id, i.remote_file_id, i.mime_type, p.upload_mode
		 FROM post_images i JOIN posts p ON p.id = i.post_id
		 WHERE i.short_code = ? AND i.status = ? AND i.remote_file_id <> ''
		   AND p.is_public = 1 AND p.is_disabled = 0 AND p.pending_delete = 0`,
		shortCode, ImageStatusStored,
	).Scan(&image.ImageID, &image.PostID, &image.RemoteFileID, &image.MIMEType, &image.UploadMode)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, errors.New("读取图片记录失败")
	}
	return image, nil
}

// RequestPublish 由管理员确认发布：标记公开并把发布任务置为待处理。
// 只有图片全部存图的帖子才能发布，旧版存图任务必须显式调用。
func (r *Repository) RequestPublish(ctx context.Context, postID int64) error {
	result, err := r.handle.ExecContext(ctx,
		`UPDATE posts SET is_public = 1, publish_state = ?, publish_error = '', updated_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND pending_delete = 0 AND status = ? AND publish_state IN (?, ?)`,
		PublishPending, postID, StatusCompleted, PublishIdle, PublishFailed)
	if err != nil {
		return errors.New("更新发布状态失败")
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return r.missingOrPending(ctx, postID)
	}
	return nil
}

// ClaimPublishable 领取一条待发布任务，串行发布由调用方保证。
func (r *Repository) ClaimPublishable(ctx context.Context) (int64, bool, error) {
	var id int64
	err := r.handle.QueryRowContext(ctx,
		`UPDATE posts SET publish_state = ?, updated_at = CURRENT_TIMESTAMP
		 WHERE id = (SELECT id FROM posts WHERE publish_state = ? ORDER BY updated_at LIMIT 1)
		   AND publish_state = ?
		 RETURNING id`,
		PublishPublishing, PublishPending, PublishPending).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, errors.New("领取发布任务失败")
	}
	return id, true, nil
}

// RecoverPublish 把中断的发布任务放回待处理，进程启动时调用。
func (r *Repository) RecoverPublish(ctx context.Context) (int64, error) {
	result, err := r.handle.ExecContext(ctx,
		`UPDATE posts SET publish_state = ?, updated_at = CURRENT_TIMESTAMP WHERE publish_state = ?`,
		PublishPending, PublishPublishing)
	if err != nil {
		return 0, errors.New("恢复发布任务失败")
	}
	affected, _ := result.RowsAffected()
	return affected, nil
}

// MarkPublishFailed 记录发布失败，保留已创建的页面路径供重试复用。
func (r *Repository) MarkPublishFailed(ctx context.Context, postID int64, message string) error {
	if _, err := r.handle.ExecContext(ctx,
		`UPDATE posts SET publish_state = ?, publish_error = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		PublishFailed, message, postID); err != nil {
		return errors.New("更新发布状态失败")
	}
	return nil
}

// PublishStatus 返回发布状态、公开标记与对外链接。
func (r *Repository) PublishStatus(ctx context.Context, postID int64) (string, []string, bool, error) {
	var (
		state  string
		urls   string
		public int
	)
	err := r.handle.QueryRowContext(ctx,
		`SELECT publish_state, telegraph_urls, is_public FROM posts WHERE id = ?`, postID,
	).Scan(&state, &urls, &public)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, false, ErrNotFound
	}
	if err != nil {
		return "", nil, false, errors.New("读取发布状态失败")
	}
	parsed := []string{}
	if urls != "" {
		if err := json.Unmarshal([]byte(urls), &parsed); err != nil {
			parsed = []string{}
		}
	}
	return state, parsed, public == 1, nil
}

// SavePublishPaths 增量保存已创建的页面路径，进程中断后重试可复用。
func (r *Repository) SavePublishPaths(ctx context.Context, postID int64, paths []string) error {
	encoded, err := json.Marshal(paths)
	if err != nil {
		return errors.New("保存页面路径失败")
	}
	if _, err := r.handle.ExecContext(ctx,
		`UPDATE posts SET telegraph_paths = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		string(encoded), postID); err != nil {
		return errors.New("保存页面路径失败")
	}
	return nil
}

// MarkPublished 记录发布完成，写入全部路径与对外链接。
func (r *Repository) MarkPublished(ctx context.Context, postID int64, paths, urls []string) error {
	encodedPaths, err := json.Marshal(paths)
	if err != nil {
		return errors.New("保存发布结果失败")
	}
	encodedURLs, err := json.Marshal(urls)
	if err != nil {
		return errors.New("保存发布结果失败")
	}
	if _, err := r.handle.ExecContext(ctx,
		`UPDATE posts SET publish_state = ?, publish_error = '', telegraph_paths = ?, telegraph_urls = ?,
		 is_public = 1, published_at = COALESCE(NULLIF(published_at, ''), CURRENT_TIMESTAMP),
		 updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		PublishPublished, string(encodedPaths), string(encodedURLs), postID); err != nil {
		return errors.New("保存发布结果失败")
	}
	return nil
}

// UpdateMetadata 更新标题、说明与作者名，不改动发布状态、发布时间与页面路径。
func (r *Repository) UpdateMetadata(ctx context.Context, postID int64, expectedState, title, description, authorName string) error {
	// 写入时按读取到的状态再校验一次：读取后整次发布已完成或已入队都会失败，
	// 避免远端与本地分别落到不同版本。失败帖还要求没有可复用的页面路径。
	result, err := r.handle.ExecContext(ctx,
		`UPDATE posts SET title = ?, description = ?, author_name = ?, updated_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND pending_delete = 0 AND publish_state = ?
		   AND (publish_state IN (?, ?)
		        OR (publish_state = ? AND telegraph_paths IN ('[]', '')))`,
		title, description, authorName, postID, expectedState,
		PublishIdle, PublishPublished, PublishFailed)
	if err != nil {
		return errors.New("更新帖子失败")
	}
	if affected, _ := result.RowsAffected(); affected > 0 {
		return nil
	}

	return r.missingOrBusy(ctx, postID)
}

// PublishInfo 读取发布所需的元数据、存图列表与已保存的页面路径。
func (r *Repository) PublishInfo(ctx context.Context, postID int64) (*PublishInfo, error) {
	info := &PublishInfo{PostID: postID}
	var pathsJSON string
	err := r.handle.QueryRowContext(ctx,
		`SELECT title, description, author_name, publish_state, telegraph_paths,
		        COALESCE(cover_sort_order, 0), publish_path_mode, publish_slug
		 FROM posts WHERE id = ?`, postID,
	).Scan(&info.Title, &info.Description, &info.AuthorName, &info.State, &pathsJSON,
		&info.CoverSortOrder, &info.PathMode, &info.Slug)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, errors.New("读取发布信息失败")
	}
	if pathsJSON != "" {
		if err := json.Unmarshal([]byte(pathsJSON), &info.Paths); err != nil {
			info.Paths = nil
		}
	}
	rows, err := r.handle.QueryContext(ctx,
		`SELECT id, sort_order, short_code, mime_type, file_size, temp_file_path, status, error_message
		 FROM post_images WHERE post_id = ? AND status = ? ORDER BY sort_order`,
		postID, ImageStatusStored)
	if err != nil {
		return nil, errors.New("读取图片列表失败")
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		image := Image{PostID: postID}
		if err := rows.Scan(&image.ID, &image.SortOrder, &image.ShortCode, &image.MimeType,
			&image.FileSize, &image.TempFilePath, &image.Status, &image.ErrorMessage); err != nil {
			return nil, errors.New("读取图片列表失败")
		}
		info.Images = append(info.Images, image)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("读取图片列表失败")
	}
	return info, nil
}

// SetPublic 设置帖子是否对外公开，由发布流程在管理员确认后调用。
func (r *Repository) SetPublic(ctx context.Context, postID int64, public bool) error {
	value := 0
	if public {
		value = 1
	}
	result, err := r.handle.ExecContext(ctx,
		`UPDATE posts SET is_public = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, value, postID)
	if err != nil {
		return errors.New("更新公开标记失败")
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	return nil
}

// SetCoverSelection 更新封面来源序号与缩略图路径；缩略图为空时只改序号。
func (r *Repository) SetCoverSelection(ctx context.Context, id int64, sortOrder int, thumbPath string) error {
	var (
		result sql.Result
		err    error
	)
	if thumbPath == "" {
		result, err = r.handle.ExecContext(ctx,
			`UPDATE posts SET cover_sort_order = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
			sortOrder, id)
	} else {
		result, err = r.handle.ExecContext(ctx,
			`UPDATE posts SET cover_sort_order = ?, cover_thumb_path = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
			sortOrder, thumbPath, id)
	}
	if err != nil {
		return errors.New("更新封面失败")
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	return nil
}

// SetCover 更新帖子封面路径。
func (r *Repository) SetCover(ctx context.Context, id int64, path string) error {
	result, err := r.handle.ExecContext(ctx,
		`UPDATE posts SET cover_thumb_path = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, path, id)
	if err != nil {
		return errors.New("保存封面失败")
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateStatus 更新帖子状态。
func (r *Repository) UpdateStatus(ctx context.Context, id int64, status string) error {
	result, err := r.handle.ExecContext(ctx,
		`UPDATE posts SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, status, id)
	if err != nil {
		return errors.New("更新帖子状态失败")
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	return nil
}

// AttachImage 在短事务内复核帖子状态并写入唯一的 (post_id, sort_order) 记录。
// 返回被替换掉的旧文件路径（与本次路径相同时返回空），由调用方负责删除。
func (r *Repository) AttachImage(ctx context.Context, image Image) (string, error) {
	tx, err := r.handle.BeginTx(ctx, nil)
	if err != nil {
		return "", errors.New("开启图片事务失败")
	}
	defer func() { _ = tx.Rollback() }()

	var status string
	err = tx.QueryRowContext(ctx, `SELECT status FROM posts WHERE id = ?`, image.PostID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", errors.New("读取帖子状态失败")
	}
	if status != StatusUploading {
		return "", ErrNotUploading
	}

	var previous string
	err = tx.QueryRowContext(ctx,
		`SELECT temp_file_path FROM post_images WHERE post_id = ? AND sort_order = ?`,
		image.PostID, image.SortOrder).Scan(&previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", errors.New("读取图片记录失败")
	}

	if image.ShortCode == "" {
		code, codeErr := newShortCode()
		if codeErr != nil {
			return "", codeErr
		}
		image.ShortCode = code
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO post_images (post_id, sort_order, short_code, mime_type, file_size, temp_file_path, status)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(post_id, sort_order) DO UPDATE SET
			mime_type = excluded.mime_type,
			file_size = excluded.file_size,
			temp_file_path = excluded.temp_file_path,
			status = excluded.status,
			error_message = ''`,
		image.PostID, image.SortOrder, image.ShortCode, image.MimeType, image.FileSize, image.TempFilePath, image.Status,
	); err != nil {
		return "", errors.New("保存图片记录失败")
	}

	if err := tx.Commit(); err != nil {
		return "", errors.New("提交图片事务失败")
	}
	if previous == image.TempFilePath {
		previous = ""
	}
	return previous, nil
}

// FreezePublishPath 在首次入队时写入链接生成方式与随机短码，已有取值时不覆盖。
// 冻结后即使设置页改了口径，这次发布仍按入队时的选择执行。
func (r *Repository) FreezePublishPath(ctx context.Context, postID int64, mode, slug string) error {
	if mode == "" {
		return nil
	}
	result, err := r.handle.ExecContext(ctx,
		`UPDATE posts SET publish_path_mode = ?, publish_slug = ?, updated_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND (publish_path_mode IS NULL OR publish_path_mode = '')`,
		mode, slug, postID)
	if err != nil {
		return errors.New("保存链接生成方式失败")
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return nil
	}
	return nil
}

// NewPublishSlug 生成用于 Telegraph 路径的随机短码，只含小写字母与数字。
func NewPublishSlug() (string, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	const length = 10
	buffer := make([]byte, length)
	for index := range buffer {
		value, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", errors.New("生成短码失败")
		}
		buffer[index] = alphabet[value.Int64()]
	}
	return string(buffer), nil
}

// Enqueue 在事务内统计缺图并完成 uploading → queued 的状态转换。
// 缺图时返回缺失序号且不改动状态。
func (r *Repository) Enqueue(ctx context.Context, postID int64) ([]int, error) {
	tx, err := r.handle.BeginTx(ctx, nil)
	if err != nil {
		return nil, errors.New("开启入队事务失败")
	}
	defer func() { _ = tx.Rollback() }()

	var (
		imageCount int
		status     string
	)
	err = tx.QueryRowContext(ctx, `SELECT image_count, status FROM posts WHERE id = ?`, postID).Scan(&imageCount, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, errors.New("读取帖子状态失败")
	}
	if status == StatusQueued {
		return nil, ErrAlreadyQueued
	}
	if status != StatusUploading {
		return nil, ErrNotUploading
	}

	rows, err := tx.QueryContext(ctx,
		`SELECT sort_order FROM post_images WHERE post_id = ? AND status = ?`, postID, ImageStatusUploaded)
	if err != nil {
		return nil, errors.New("统计已上传图片失败")
	}
	uploaded := make(map[int]struct{}, imageCount)
	for rows.Next() {
		var order int
		if err := rows.Scan(&order); err != nil {
			rows.Close()
			return nil, errors.New("统计已上传图片失败")
		}
		uploaded[order] = struct{}{}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, errors.New("统计已上传图片失败")
	}

	var missing []int
	for order := 1; order <= imageCount; order++ {
		if _, ok := uploaded[order]; !ok {
			missing = append(missing, order)
		}
	}
	if len(missing) > 0 {
		return missing, nil
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE posts SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, StatusQueued, postID); err != nil {
		return nil, errors.New("更新帖子状态失败")
	}
	if err := tx.Commit(); err != nil {
		return nil, errors.New("提交入队事务失败")
	}
	return nil, nil
}

// ListImages 返回帖子的全部图片记录，按序号升序。
func (r *Repository) ListImages(ctx context.Context, postID int64) ([]Image, error) {
	rows, err := r.handle.QueryContext(ctx,
		`SELECT id, sort_order, short_code, mime_type, file_size, temp_file_path, status, error_message
		 FROM post_images WHERE post_id = ? ORDER BY sort_order`, postID)
	if err != nil {
		return nil, errors.New("读取图片列表失败")
	}
	defer rows.Close()

	var images []Image
	for rows.Next() {
		image := Image{PostID: postID}
		if err := rows.Scan(&image.ID, &image.SortOrder, &image.ShortCode, &image.MimeType,
			&image.FileSize, &image.TempFilePath, &image.Status, &image.ErrorMessage); err != nil {
			return nil, errors.New("读取图片列表失败")
		}
		images = append(images, image)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("读取图片列表失败")
	}
	return images, nil
}

// MissingOrders 返回 1 到 imageCount 之间尚未上传完成的序号。
func (r *Repository) MissingOrders(ctx context.Context, postID int64, imageCount int) ([]int, error) {
	rows, err := r.handle.QueryContext(ctx,
		`SELECT sort_order FROM post_images WHERE post_id = ? AND status = ?`,
		postID, ImageStatusUploaded)
	if err != nil {
		return nil, errors.New("统计已上传图片失败")
	}
	defer rows.Close()

	uploaded := make(map[int]struct{}, imageCount)
	for rows.Next() {
		var order int
		if err := rows.Scan(&order); err != nil {
			return nil, errors.New("统计已上传图片失败")
		}
		uploaded[order] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("统计已上传图片失败")
	}

	var missing []int
	for order := 1; order <= imageCount; order++ {
		if _, ok := uploaded[order]; !ok {
			missing = append(missing, order)
		}
	}
	return missing, nil
}

// newShortCode 生成 12 位高熵短码，用于隔离远端文件标识。
func newShortCode() (string, error) {
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	const length = 12
	buf := make([]byte, length)
	max := big.NewInt(int64(len(alphabet)))
	for i := 0; i < length; i++ {
		index, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", errors.New("生成短码失败")
		}
		buf[i] = alphabet[index.Int64()]
	}
	return string(buf), nil
}

// RecoverPublishing 把中断在上传中的帖子重置为待处理，返回受影响条数。
func (r *Repository) RecoverPublishing(ctx context.Context) (int64, error) {
	result, err := r.handle.ExecContext(ctx,
		`UPDATE posts SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE status = ?`,
		StatusQueued, StatusPublishing)
	if err != nil {
		return 0, errors.New("恢复中断任务失败")
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, errors.New("恢复中断任务失败")
	}
	return affected, nil
}

// ClaimNext 领取最早的一条待处理帖子并标记为上传中。
func (r *Repository) ClaimNext(ctx context.Context) (int64, bool, error) {
	tx, err := r.handle.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, errors.New("开启领取事务失败")
	}
	defer func() { _ = tx.Rollback() }()

	var id int64
	err = tx.QueryRowContext(ctx,
		`SELECT id FROM posts WHERE status = ? ORDER BY id LIMIT 1`, StatusQueued).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, errors.New("读取待处理帖子失败")
	}

	result, err := tx.ExecContext(ctx,
		`UPDATE posts SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ? AND status = ?`,
		StatusPublishing, id, StatusQueued)
	if err != nil {
		return 0, false, errors.New("领取任务失败")
	}
	affected, err := result.RowsAffected()
	if err != nil || affected == 0 {
		return 0, false, nil
	}
	if err := tx.Commit(); err != nil {
		return 0, false, errors.New("领取任务失败")
	}
	return id, true, nil
}

// CleanupOrphans 删除临时目录中超过保留期且不再被数据库引用的文件。
// 数据库仍引用的文件即使很旧也保留，避免误删待上传内容。
func (r *Repository) CleanupOrphans(ctx context.Context, dataDir string, retention time.Duration, now time.Time) (int, error) {
	rows, err := r.handle.QueryContext(ctx,
		`SELECT temp_file_path FROM post_images WHERE temp_file_path <> ''`)
	if err != nil {
		return 0, errors.New("读取临时文件记录失败")
	}
	referenced := make(map[string]struct{})
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			rows.Close()
			return 0, errors.New("读取临时文件记录失败")
		}
		referenced[filepath.ToSlash(path)] = struct{}{}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, errors.New("读取临时文件记录失败")
	}

	root := filepath.Join(dataDir, TempDir)
	cutoff := now.Add(-retention)
	removed := 0
	walkErr := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, relErr := filepath.Rel(dataDir, path)
		if relErr != nil {
			return nil
		}
		relative = filepath.ToSlash(relative)
		if _, ok := referenced[relative]; ok {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return nil
		}
		if info.ModTime().After(cutoff) {
			return nil
		}
		if removeErr := os.Remove(path); removeErr == nil {
			removed++
		}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, os.ErrNotExist) {
		return removed, errors.New("清理临时文件失败")
	}
	return removed, nil
}

// PendingImages 返回尚未上传到图床的图片，按序号升序。
func (r *Repository) PendingImages(ctx context.Context, postID int64) ([]Image, error) {
	rows, err := r.handle.QueryContext(ctx,
		`SELECT id, sort_order, mime_type, file_size, temp_file_path, status
		 FROM post_images
		 WHERE post_id = ? AND status = ? AND remote_file_id = ''
		 ORDER BY sort_order`, postID, ImageStatusUploaded)
	if err != nil {
		return nil, errors.New("读取待上传图片失败")
	}
	defer rows.Close()

	var images []Image
	for rows.Next() {
		image := Image{PostID: postID}
		if err := rows.Scan(&image.ID, &image.SortOrder, &image.MimeType,
			&image.FileSize, &image.TempFilePath, &image.Status); err != nil {
			return nil, errors.New("读取待上传图片失败")
		}
		images = append(images, image)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("读取待上传图片失败")
	}
	return images, nil
}

// MarkImageStored 记录远端标识并清空本地临时路径。
func (r *Repository) MarkImageStored(ctx context.Context, imageID int64, fileID string, messageID int64, provider string) error {
	if _, err := r.handle.ExecContext(ctx,
		`UPDATE post_images
		 SET remote_file_id = ?, remote_message_id = ?, storage_provider = ?, status = ?,
		     temp_file_path = '', error_message = ''
		 WHERE id = ?`,
		fileID, messageID, provider, ImageStatusStored, imageID,
	); err != nil {
		return errors.New("记录远端标识失败")
	}
	return nil
}

// MarkImageFailed 记录单张图片的失败原因。
func (r *Repository) MarkImageFailed(ctx context.Context, imageID int64, message string) error {
	if _, err := r.handle.ExecContext(ctx,
		`UPDATE post_images SET status = ?, error_message = ? WHERE id = ?`,
		ImageStatusFailed, message, imageID,
	); err != nil {
		return errors.New("记录图片失败状态失败")
	}
	return nil
}

// CountStored 统计已存入图床的图片数量。
func (r *Repository) CountStored(ctx context.Context, postID int64) (int, error) {
	var total int
	if err := r.handle.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM post_images WHERE post_id = ? AND status = ?`,
		postID, ImageStatusStored).Scan(&total); err != nil {
		return 0, errors.New("统计已存图片失败")
	}
	return total, nil
}

// MarkPostFinished 结束帖子的上传流程，写入最终状态与失败原因。
func (r *Repository) MarkPostFinished(ctx context.Context, postID int64, status, message string) error {
	if _, err := r.handle.ExecContext(ctx,
		`UPDATE posts SET status = ?, error_message = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		status, message, postID); err != nil {
		return errors.New("更新帖子状态失败")
	}
	return nil
}

// ResetFailedImages 把失败的图片恢复为待上传，并把帖子重新排入队列。
func (r *Repository) ResetFailedImages(ctx context.Context, postID int64) (int64, error) {
	tx, err := r.handle.BeginTx(ctx, nil)
	if err != nil {
		return 0, errors.New("开启重试事务失败")
	}
	defer func() { _ = tx.Rollback() }()

	var (
		status  string
		pending int
	)
	err = tx.QueryRowContext(ctx, `SELECT status, pending_delete FROM posts WHERE id = ?`, postID).Scan(&status, &pending)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, errors.New("读取帖子状态失败")
	}
	if pending == 1 {
		return 0, ErrPendingDelete
	}
	if status == StatusQueued || status == StatusPublishing {
		return 0, nil
	}

	result, err := tx.ExecContext(ctx,
		`UPDATE post_images SET status = ?, error_message = '' WHERE post_id = ? AND status = ?`,
		ImageStatusUploaded, postID, ImageStatusFailed)
	if err != nil {
		return 0, errors.New("重置失败图片失败")
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, errors.New("重置失败图片失败")
	}
	if affected == 0 {
		return 0, nil
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE posts SET status = ?, error_message = '', updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		StatusQueued, postID); err != nil {
		return 0, errors.New("重新排队失败")
	}
	if err := tx.Commit(); err != nil {
		return 0, errors.New("提交重试事务失败")
	}
	return affected, nil
}
