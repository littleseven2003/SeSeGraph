package posts

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"math/big"
)

// 帖子状态取值。
const (
	StatusUploading = "uploading"
	StatusQueued    = "queued"
	StatusFailed    = "failed"
)

// 图片状态取值。
const (
	ImageStatusUploaded = "uploaded"
)

// ErrNotFound 表示帖子或图片记录不存在。
var ErrNotFound = errors.New("帖子不存在")

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
	result, err := r.handle.ExecContext(ctx,
		`INSERT INTO posts (title, description, author_name, cover_thumb_path, image_count, upload_mode, status)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		post.Title, post.Description, post.AuthorName, post.CoverThumbPath, post.ImageCount, post.UploadMode, post.Status,
	)
	if err != nil {
		return 0, errors.New("保存帖子失败")
	}
	return result.LastInsertId()
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

// SaveImage 写入或覆盖某张图片的记录。同一帖子同一序号重复上传时只更新记录。
func (r *Repository) SaveImage(ctx context.Context, image Image) error {
	var existingID int64
	err := r.handle.QueryRowContext(ctx,
		`SELECT id FROM post_images WHERE post_id = ? AND sort_order = ?`, image.PostID, image.SortOrder,
	).Scan(&existingID)
	switch {
	case err == nil:
		if _, err := r.handle.ExecContext(ctx,
			`UPDATE post_images SET mime_type = ?, file_size = ?, temp_file_path = ?, status = ?, error_message = ''
			 WHERE id = ?`,
			image.MimeType, image.FileSize, image.TempFilePath, image.Status, existingID,
		); err != nil {
			return errors.New("更新图片记录失败")
		}
		return nil
	case errors.Is(err, sql.ErrNoRows):
		code, codeErr := newShortCode()
		if codeErr != nil {
			return codeErr
		}
		if _, err := r.handle.ExecContext(ctx,
			`INSERT INTO post_images (post_id, sort_order, short_code, mime_type, file_size, temp_file_path, status)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			image.PostID, image.SortOrder, code, image.MimeType, image.FileSize, image.TempFilePath, image.Status,
		); err != nil {
			return errors.New("保存图片记录失败")
		}
		return nil
	default:
		return errors.New("读取图片记录失败")
	}
}

// ListImages 返回帖子的全部图片记录，按序号升序。
func (r *Repository) ListImages(ctx context.Context, postID int64) ([]Image, error) {
	rows, err := r.handle.QueryContext(ctx,
		`SELECT id, sort_order, short_code, mime_type, file_size, temp_file_path, status
		 FROM post_images WHERE post_id = ? ORDER BY sort_order`, postID)
	if err != nil {
		return nil, errors.New("读取图片列表失败")
	}
	defer rows.Close()

	var images []Image
	for rows.Next() {
		image := Image{PostID: postID}
		if err := rows.Scan(&image.ID, &image.SortOrder, &image.ShortCode, &image.MimeType,
			&image.FileSize, &image.TempFilePath, &image.Status); err != nil {
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
