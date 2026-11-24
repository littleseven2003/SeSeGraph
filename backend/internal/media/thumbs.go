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
	"sort"
	"strconv"
	"strings"
	"sync"
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

// ErrCacheFull 表示缓存上限已经排满，本次写入放不进去。
var ErrCacheFull = errors.New("缩略图缓存已满")

// ErrDiskFull 表示数据盘余量不足，暂停写入新的小图。
var ErrDiskFull = errors.New("磁盘余量不足")

// MinFreeReserve 是写入小图时必须留出的磁盘余量，避免把数据盘写满。
const MinFreeReserve = 64 << 20

// usageTTL 是占用统计的复用时长：离上限还远时不必每次写盘都遍历整个目录。
const usageTTL = 30 * time.Second

// usageState 是某个数据目录的占用缓存，扫描后短时复用，写入成功时增量更新。
type usageState struct {
	total int64
	count int
	at    time.Time
}

// commitMu 让清理与回存的提交阶段互斥：清理期间不会有文件写盘或记录提交，
// 回存期间也不会有目录被整段删除，避免清理结束后留下无引用的文件。
var commitMu sync.Mutex

// LockCommit 锁住清理与回存的提交阶段，返回解锁函数。
func LockCommit() func() {
	commitMu.Lock()
	return commitMu.Unlock
}

var (
	usageMu    sync.Mutex
	usageByDir = map[string]usageState{}
)

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
// 写入前按生效上限腾出空间并确认磁盘余量；先写临时文件再改名，
// 中断不会留下半个文件，也不会覆盖正在被读的旧版本。
func Store(dataDir string, postID int64, sortOrder int, version string, data []byte, limit int64) (string, int64, error) {
	if !ValidVersion(version) || sortOrder <= 0 || postID <= 0 {
		return "", 0, ErrInvalidThumb
	}
	if len(data) == 0 || !webpHeader(data) {
		return "", 0, ErrInvalidThumb
	}
	if len(data) > MaxBytes {
		return "", 0, ErrTooLarge
	}
	// 只收静态位图，且最长边不超过规格值，避免缓存被超大图占满。
	info, err := InspectWebP(data)
	if err != nil || info.Animated || info.Width <= 0 || info.Height <= 0 {
		return "", 0, ErrInvalidThumb
	}
	if info.Width > TargetEdge || info.Height > TargetEdge {
		return "", 0, ErrInvalidThumb
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
	// 内容已经落到临时文件，占用检查放在改名之前，超限时只丢掉这一份。
	if err := reserve(dataDir, int64(len(data)), limit); err != nil {
		return "", 0, err
	}
	if err := os.Rename(tempName, filepath.Join(dir, name)); err != nil {
		return "", 0, errors.New("写入缩略图失败")
	}
	return filepath.ToSlash(filepath.Join(Dir, strconv.FormatInt(postID, 10), name)), int64(len(data)), nil
}

// reserve 在落盘前腾出位置：超出上限时按最近最少使用淘汰旧图，
// 并确认磁盘仍有安全余量。调用方只在本次内容已写入临时文件后调用。
func reserve(dataDir string, incoming, limit int64) error {
	usageMu.Lock()
	defer usageMu.Unlock()

	if state, ok := usageByDir[dataDir]; ok && time.Since(state.at) < usageTTL &&
		(limit <= 0 || state.total+incoming <= limit) && diskHasRoom(dataDir, incoming) {
		state.total += incoming
		state.count++
		usageByDir[dataDir] = state
		return nil
	}

	// 需要核对真实占用：整目录扫描一次，再按最近最少使用淘汰到能容纳本次写入。
	list, err := scan(dataDir)
	if err != nil {
		return errors.New("统计缩略图占用失败")
	}
	var total int64
	for _, item := range list {
		total += item.size
	}
	if limit > 0 && total+incoming > limit {
		// 淘汰到上限的九成，留出余量：满额后不必每次写入都重扫整个目录。
		target := limit - limit/10
		for _, item := range list {
			if total+incoming <= target {
				break
			}
			if err := os.Remove(item.path); err != nil {
				continue
			}
			total -= item.size
		}
		if total+incoming > limit {
			return ErrCacheFull
		}
	}
	if !diskHasRoom(dataDir, incoming) {
		return ErrDiskFull
	}
	// 本轮淘汰后的真实条数以现存文件为准，只把本次新增计入。
	kept := 0
	for _, item := range list {
		if _, statErr := os.Stat(item.path); statErr == nil {
			kept++
		}
	}
	usageByDir[dataDir] = usageState{total: total + incoming, count: kept + 1, at: time.Now()}
	return nil
}

// diskHasRoom 判断数据盘在写入本次内容后是否仍留有余量；探测不到时不阻塞写入。
func diskHasRoom(dataDir string, incoming int64) bool {
	free, ok := diskFree(dataDir)
	if !ok {
		return true
	}
	return free >= incoming+MinFreeReserve
}

// resetUsage 丢弃占用缓存，目录被整体清理或外部改动后重新统计。
func resetUsage(dataDir string) {
	usageMu.Lock()
	delete(usageByDir, dataDir)
	usageMu.Unlock()
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

// RemovePostVersions 删除某帖中内容版本不在保留集合里的缩略图，
// 用于按图集重建：重建期间刚落盘的新版本文件不会被误删。
func RemovePostVersions(dataDir string, postID int64, keep map[string]bool) (int, int64, error) {
	if postID <= 0 {
		return 0, 0, nil
	}
	dir := filepath.Join(dataDir, Dir, strconv.FormatInt(postID, 10))
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			resetUsage(dataDir)
			return 0, 0, nil
		}
		return 0, 0, err
	}
	removed := 0
	var freed int64
	for _, item := range entries {
		if item.IsDir() || filepath.Ext(item.Name()) != ".webp" {
			continue
		}
		if keep[versionFromName(item.Name())] {
			continue
		}
		info, infoErr := item.Info()
		if infoErr != nil {
			continue
		}
		if err := os.Remove(filepath.Join(dir, item.Name())); err != nil {
			continue
		}
		removed++
		freed += info.Size()
	}
	resetUsage(dataDir)
	return removed, freed, nil
}

// versionFromName 从文件名取回内容版本：序号-规格版本-内容版本.webp。
func versionFromName(name string) string {
	base := strings.TrimSuffix(name, ".webp")
	index := strings.LastIndexByte(base, '-')
	if index < 0 || index == len(base)-1 {
		return ""
	}
	return base[index+1:]
}

// RemovePost 删除某帖的全部缩略图，永久删帖或按图集重建时调用。
func RemovePost(dataDir string, postID int64) error {
	if postID <= 0 {
		return nil
	}
	if err := os.RemoveAll(filepath.Join(dataDir, Dir, strconv.FormatInt(postID, 10))); err != nil {
		return err
	}
	resetUsage(dataDir)
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
// 用标准排序：满额缓存可达数万项，插入排序在这种规模下会退化成平方级。
func sortByMtime(list []entry) {
	sort.SliceStable(list, func(i, j int) bool {
		return list[i].mtime.Before(list[j].mtime)
	})
}

// Usage 汇总当前占用的字节数与条目数，同时刷新占用缓存。
// 只统计不排序：查看占用时没必要为几万个文件付出排序开销。
func Usage(dataDir string) (int64, int) {
	var (
		total int64
		count int
	)
	err := filepath.WalkDir(filepath.Join(dataDir, Dir), func(path string, item fs.DirEntry, err error) error {
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
		total += info.Size()
		count++
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return 0, 0
	}
	usageMu.Lock()
	usageByDir[dataDir] = usageState{total: total, count: count, at: time.Now()}
	usageMu.Unlock()
	return total, count
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
	resetUsage(dataDir)
	return removed, freed, nil
}

// Clear 清空整个缩略图目录，返回删除数与释放字节数。
func Clear(dataDir string) (int, int64, error) {
	bytesBefore, count := Usage(dataDir)
	if err := os.RemoveAll(filepath.Join(dataDir, Dir)); err != nil {
		return 0, 0, err
	}
	resetUsage(dataDir)
	return count, bytesBefore, nil
}
