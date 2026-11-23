package posts

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strconv"
)

// CoverDir 是封面缩略图相对数据目录的子目录。
const CoverDir = "thumbs"

// SaveCover 把封面缩略图写入数据目录，返回相对数据目录的路径。
// 先写临时文件再改名，避免中断留下半个封面。
func SaveCover(dataDir string, postID int64, data []byte) (string, error) {
	dir := filepath.Join(dataDir, CoverDir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", errors.New("创建封面目录失败")
	}

	name := strconv.FormatInt(postID, 10) + ".webp"
	temp, err := os.CreateTemp(dir, name+".*.part")
	if err != nil {
		return "", errors.New("写入封面失败")
	}
	tempName := temp.Name()
	defer func() {
		_ = os.Remove(tempName)
	}()

	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return "", errors.New("写入封面失败")
	}
	if err := temp.Close(); err != nil {
		return "", errors.New("写入封面失败")
	}
	if err := os.Rename(tempName, filepath.Join(dir, name)); err != nil {
		return "", errors.New("写入封面失败")
	}
	return filepath.ToSlash(filepath.Join(CoverDir, name)), nil
}

// SaveCoverVersioned 写入带随机后缀的封面文件，避免覆盖仍在被读的旧封面。
// 返回相对数据目录的路径；调用方在本地提交成功后自行清理旧文件。
func SaveCoverVersioned(dataDir string, postID int64, data []byte) (string, error) {
	dir := filepath.Join(dataDir, CoverDir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", errors.New("创建封面目录失败")
	}

	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		return "", errors.New("生成封面文件名失败")
	}
	name := strconv.FormatInt(postID, 10) + "-" + hex.EncodeToString(suffix) + ".webp"

	temp, err := os.CreateTemp(dir, name+".*.part")
	if err != nil {
		return "", errors.New("写入封面失败")
	}
	tempName := temp.Name()
	defer func() { _ = os.Remove(tempName) }()

	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return "", errors.New("写入封面失败")
	}
	if err := temp.Close(); err != nil {
		return "", errors.New("写入封面失败")
	}
	if err := os.Rename(tempName, filepath.Join(dir, name)); err != nil {
		return "", errors.New("写入封面失败")
	}
	return filepath.ToSlash(filepath.Join(CoverDir, name)), nil
}

// RemoveCoverFile 删除数据目录下的封面文件，空路径忽略。
func RemoveCoverFile(dataDir, relativePath string) {
	if relativePath == "" {
		return
	}
	_ = os.Remove(filepath.Join(dataDir, filepath.FromSlash(relativePath)))
}
