// 逐图缩略图的落盘与缓存管理：按帖子/序号/内容版本/规格版本定位，
// 供行内 44px 小图与 224px 放大气泡共用，命中时不再回源原媒体。
package media

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"
)

// 目录与规格常量：改尺寸或质量时提升 SpecVersion，旧文件自然失效。
const (
	// Dir 是逐图缩略图相对数据目录的子目录，和封面缩略图（thumbs/）分开。
	Dir = "thumbs/media"
	// SpecVersion 是当前缩略图规格版本，参与文件名。
	SpecVersion = "s1"
	// MaxBytes 是单张缩略图的硬上限，超过直接拒收。
	MaxBytes = 150_000
	// TargetEdge 是缩略图最长边目标像素。
	TargetEdge = 512
)

// versionPattern 限定内容版本格式，避免把任意字符串拼进路径。
var versionPattern = regexp.MustCompile(`^[0-9a-f]{12}$`)

// ErrInvalidThumb 表示缩略图内容或参数不合法。
var ErrInvalidThumb = errors.New("缩略图不合法")

// ErrTooLarge 表示缩略图超过硬上限。
var ErrTooLarge = errors.New("缩略图超过上限")

// ValidVersion 判断内容版本是否可用作文件名。
func ValidVersion(version string) bool {
	return versionPattern.MatchString(version)
}

// FileName 返回某一项的缩略图文件名：序号-规格版本-内容版本.webp。
func FileName(sortOrder int, version string) string {
	return strconv.Itoa(sortOrder) + "-" + SpecVersion + "-" + version + ".webp"
}

// RelPath 返回缩略图相对数据目录的路径。
func RelPath(postID int64, sortOrder int, version string) string {
	return filepath.ToSlash(filepath.Join(Dir, strconv.FormatInt(postID, 10), FileName(sortOrder, version)))
}

// webpHeader 判断内容是否为 WebP：RIFF....WEBP。
func webpHeader(data []byte) bool {
	return len(data) >= 12 && bytes.Equal(data[0:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP"))
}

// Store 校验后原子写入缩略图，返回相对路径与字节数。
// 先写临时文件再改名，中断不会留下半个文件，也不会覆盖正在被读的旧版本。
func Store(dataDir string, postID int64, sortOrder int, version string, data []byte) (string, int64, error) {
	if !ValidVersion(version) || sortOrder <= 0 || postID <= 0 {
		return "", 0, ErrInvalidThumb
	}
	if len(data) == 0 || !webpHeader(data) {
		return "", 0, ErrInvalidThumb
	}
	if len(data) > MaxBytes {
		return "", 0, ErrTooLarge
	}

	dir := filepath.Join(dataDir, Dir, strconv.FormatInt(postID, 10))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", 0, errors.New("创建缩略图目录失败")
	}
	name := FileName(sortOrder, version)
	temp, err := os.CreateTemp(dir, name+".*.part")
	if err != nil {
		return "", 0, errors.New("写入缩略图失败")
	}
	tempName := temp.Name()
	defer func() { _ = os.Remove(tempName) }()

	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return "", 0, errors.New("写入缩略图失败")
	}
	if err := temp.Close(); err != nil {
		return "", 0, errors.New("写入缩略图失败")
	}
	if err := os.Rename(tempName, filepath.Join(dir, name)); err != nil {
		return "", 0, errors.New("写入缩略图失败")
	}
	return filepath.ToSlash(filepath.Join(Dir, strconv.FormatInt(postID, 10), name)), int64(len(data)), nil
}

// Open 打开已写入的缩略图；路径必须落在缩略图目录内。
func Open(dataDir, rel string) (*os.File, os.FileInfo, error) {
	clean := filepath.Join(dataDir, filepath.Clean(filepath.FromSlash(rel)))
	if !withinRoot(filepath.Join(dataDir, Dir), clean) {
		return nil, nil, os.ErrNotExist
	}
	file, err := os.Open(clean)
	if err != nil {
		return nil, nil, err
	}
	info, statErr := file.Stat()
	if statErr != nil || info.IsDir() {
		_ = file.Close()
		return nil, nil, os.ErrNotExist
	}
	return file, info, nil
}

// withinRoot 判断路径是否位于 root 之下，防止越界读取。
func withinRoot(root, path string) bool {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return false
	}
	return rel != "." && !bytes.HasPrefix([]byte(rel), []byte(".."))
}

// Touch 把访问时间推进到当前时刻，作为 LRU 的最近使用依据。
func Touch(dataDir, rel string) {
	now := time.Now()
	_ = os.Chtimes(filepath.Join(dataDir, filepath.FromSlash(rel)), now, now)
}

// Remove 删除单个缩略图文件，文件不存在时视为成功。
func Remove(dataDir, rel string) error {
	if rel == "" {
		return nil
	}
	clean := filepath.Join(dataDir, filepath.Clean(filepath.FromSlash(rel)))
	if !withinRoot(filepath.Join(dataDir, Dir), clean) {
		return nil
	}
	if err := os.Remove(clean); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// RemovePost 删除某帖的全部缩略图，永久删帖时调用。
func RemovePost(dataDir string, postID int64) error {
	if postID <= 0 {
		return nil
	}
	if err := os.RemoveAll(filepath.Join(dataDir, Dir, strconv.FormatInt(postID, 10))); err != nil {
		return err
	}
	return nil
}

// entry 是扫描目录得到的一条缓存记录。
type entry struct {
	path  string
	size  int64
	mtime time.Time
}

// scan 遍历缩略图目录，返回全部文件按访问时间升序排列。
func scan(dataDir string) ([]entry, error) {
	root := filepath.Join(dataDir, Dir)
	list := make([]entry, 0, 64)
	err := filepath.WalkDir(root, func(path string, item fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if item.IsDir() || filepath.Ext(path) != ".webp" {
			return nil
		}
		info, infoErr := item.Info()
		if infoErr != nil {
			return nil
		}
		list = append(list, entry{path: path, size: info.Size(), mtime: info.ModTime()})
		return nil
	})
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	sortByMtime(list)
	return list, nil
}

// sortByMtime 按访问时间升序，最早使用的排在前面。
func sortByMtime(list []entry) {
	for i := 1; i < len(list); i++ {
		item := list[i]
		j := i - 1
		for j >= 0 && list[j].mtime.After(item.mtime) {
			list[j+1] = list[j]
			j--
		}
		list[j+1] = item
	}
}

// Usage 汇总当前占用的字节数与条目数。
func Usage(dataDir string) (int64, int) {
	list, err := scan(dataDir)
	if err != nil {
		return 0, 0
	}
	var total int64
	for _, item := range list {
		total += item.size
	}
	return total, len(list)
}

// Evict 按最近最少使用淘汰，直到占用不超过 limit；返回删除数与释放字节数。
func Evict(dataDir string, limit int64) (int, int64, error) {
	list, err := scan(dataDir)
	if err != nil {
		return 0, 0, err
	}
	var total int64
	for _, item := range list {
		total += item.size
	}
	if limit < 0 {
		limit = 0
	}
	removed := 0
	var freed int64
	for _, item := range list {
		if total <= limit {
			break
		}
		if err := os.Remove(item.path); err != nil {
			continue
		}
		total -= item.size
		freed += item.size
		removed++
	}
	return removed, freed, nil
}

// Clear 清空整个缩略图目录，返回删除数与释放字节数。
func Clear(dataDir string) (int, int64, error) {
	bytesBefore, count := Usage(dataDir)
	if err := os.RemoveAll(filepath.Join(dataDir, Dir)); err != nil {
		return 0, 0, err
	}
	return count, bytesBefore, nil
}
