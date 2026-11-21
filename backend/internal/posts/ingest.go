package posts

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
)

const (
	// TempDir 是等待上传到图床的临时文件目录。
	TempDir = "tmp"
	// MaxPhotoBytes 是 photo 模式下单张图片的上限。
	MaxPhotoBytes = 10 << 20
	// MaxDocumentBytes 是 document 模式下单张原图的上限。
	MaxDocumentBytes = 20 << 20
)

var (
	// ErrImageTooLarge 表示请求体超过当前画质模式允许的单张大小。
	ErrImageTooLarge = errors.New("图片超过大小限制")
	// ErrWriteInterrupted 表示写入过程中断，半成品已清理。
	ErrWriteInterrupted = errors.New("写入图片失败")
)

// LimitForMode 返回指定画质模式的单张图片上限。
func LimitForMode(mode string) int64 {
	if mode == "document" {
		return MaxDocumentBytes
	}
	return MaxPhotoBytes
}

// SavedImage 描述一次落盘结果。
type SavedImage struct {
	Path     string
	FileSize int64
}

// SaveImageStream 把请求体流式写入临时目录，返回相对数据目录的路径。
// 写入发生在数据库事务之外；超过上限或中途出错时删除半成品，不留下残余文件。
func SaveImageStream(dataDir string, postID int64, sortOrder int, body io.Reader, limit int64) (SavedImage, error) {
	dir := filepath.Join(dataDir, TempDir, strconv.FormatInt(postID, 10))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return SavedImage{}, ErrWriteInterrupted
	}

	temp, err := os.CreateTemp(dir, fmt.Sprintf("%d-*.part", sortOrder))
	if err != nil {
		return SavedImage{}, ErrWriteInterrupted
	}
	tempName := temp.Name()
	cleanup := func() {
		_ = temp.Close()
		_ = os.Remove(tempName)
	}

	written, copyErr := io.Copy(temp, io.LimitReader(body, limit+1))
	if copyErr != nil {
		cleanup()
		return SavedImage{}, ErrWriteInterrupted
	}
	if written > limit {
		cleanup()
		return SavedImage{}, ErrImageTooLarge
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempName)
		return SavedImage{}, ErrWriteInterrupted
	}

	finalName := filepath.Join(dir, fmt.Sprintf("%d.bin", sortOrder))
	if err := os.Rename(tempName, finalName); err != nil {
		_ = os.Remove(tempName)
		return SavedImage{}, ErrWriteInterrupted
	}

	return SavedImage{
		Path:     filepath.ToSlash(filepath.Join(TempDir, strconv.FormatInt(postID, 10), fmt.Sprintf("%d.bin", sortOrder))),
		FileSize: written,
	}, nil
}
