package posts

import (
	"context"
	"crypto/rand"
	"database/sql"
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

	var status string
	err = tx.QueryRowContext(ctx, `SELECT status FROM posts WHERE id = ?`, postID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, errors.New("读取帖子状态失败")
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
