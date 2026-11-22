// Package backup 生成 SQLite 热备并打包为 zip，可选经 Bot 发送到频道。
// 热备使用 VACUUM INTO，复制期间数据库仍可读写；打包与发送全程流式处理，不整包读入内存。
package backup

import (
	"archive/zip"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 备份参数默认值。
const (
	// DirName 是数据目录下的备份子目录。
	DirName = "backups"
	// DefaultKeep 是本地保留的备份份数。
	DefaultKeep = 7
	// thumbsDirName 是封面缩略图目录名，与帖子仓储保持一致。
	thumbsDirName = "thumbs"
	// databaseFileName 是主数据库文件名。
	databaseFileName = "sesegraph.db"
	// backupPrefix 是备份文件名前缀。
	backupPrefix = "sesegraph-"
)

// Sender 负责把备份文件发到远端，未配置时只保留本地文件。
type Sender interface {
	SendDocument(ctx context.Context, path, caption string) error
}

// Options 汇总备份所需参数。
type Options struct {
	// DataDir 是数据目录，备份保存在其下的 backups 子目录。
	DataDir string
	// Handle 是主数据库连接，用于 VACUUM INTO。
	Handle *sql.DB
	// IncludeThumbs 决定是否把封面缩略图一起打包。
	IncludeThumbs bool
	// Keep 是保留的备份份数，非正时使用默认值。
	Keep int
	// Sender 为空时只生成本地文件。
	Sender Sender
	// Now 便于测试替换时钟。
	Now func() time.Time
}

// Result 是一次备份的结果。
type Result struct {
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
	Sent      bool      `json:"sent"`
	Includes  []string  `json:"includes"`
}

// ParseDaily 解析只含每日时刻的 cron 表达式（分 时 * * *）。
// 分钟与小时都支持 *，分钟为 * 时按 0 处理。
func ParseDaily(spec string) (hour, minute int, err error) {
	fields := strings.Fields(strings.TrimSpace(spec))
	if len(fields) != 5 {
		return 0, 0, errors.New("备份周期需要 5 段 cron 表达式")
	}
	for index := 2; index < 5; index++ {
		if fields[index] != "*" {
			return 0, 0, errors.New("备份周期只支持每天固定时刻")
		}
	}
	parse := func(raw string, max int) (int, bool) {
		if raw == "*" {
			return 0, true
		}
		value, convErr := strconv.Atoi(raw)
		if convErr != nil || value < 0 || value > max {
			return 0, false
		}
		return value, true
	}
	parsedMinute, ok := parse(fields[0], 59)
	if !ok {
		return 0, 0, errors.New("备份周期的分钟不合法")
	}
	parsedHour, ok := parse(fields[1], 23)
	if !ok {
		return 0, 0, errors.New("备份周期的小时不合法")
	}
	return parsedHour, parsedMinute, nil
}

// DailyText 返回「每天 03:00」这类展示文案。
func DailyText(hour, minute int) string {
	return fmt.Sprintf("每天 %02d:%02d", hour, minute)
}

// ErrBusy 表示已有备份正在进行。
var ErrBusy = errors.New("备份正在进行中")

// Manager 串行执行备份，保证同一时刻只有一份产物在写。
type Manager struct {
	opts Options
	mu   sync.Mutex
	busy bool
}

// New 创建备份管理器。
func New(opts Options) *Manager {
	if opts.Keep <= 0 {
		opts.Keep = DefaultKeep
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Manager{opts: opts}
}

// Dir 返回备份目录。
func (m *Manager) Dir() string {
	return filepath.Join(m.opts.DataDir, DirName)
}

// Start 标记开始一次备份，已有任务时返回 ErrBusy。
func (m *Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.busy {
		return ErrBusy
	}
	m.busy = true
	return nil
}

// finish 结束一次备份。
func (m *Manager) finish() {
	m.mu.Lock()
	m.busy = false
	m.mu.Unlock()
}

// Run 生成一份热备并打包，必要时发送并清理过期文件。
// 结束时自动释放占用；调用方先用 Start 判断是否有备份在进行。
func (m *Manager) Run(ctx context.Context) (Result, error) {
	defer m.finish()

	if m.opts.Handle == nil {
		return Result{}, errors.New("备份缺少数据库连接")
	}
	if strings.TrimSpace(m.opts.DataDir) == "" {
		return Result{}, errors.New("备份缺少数据目录")
	}
	dir := m.Dir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Result{}, errors.New("创建备份目录失败")
	}

	now := m.opts.Now()
	stamp := now.Format("20060102-150405")
	name := backupPrefix + stamp + ".zip"
	target := uniquePath(dir, name)

	// VACUUM INTO 要求目标文件不存在，先占位拿到唯一路径再删除。
	placeholder, err := os.CreateTemp(dir, ".hotcopy-*.db")
	if err != nil {
		return Result{}, errors.New("创建备份快照失败")
	}
	hotCopy := placeholder.Name()
	_ = placeholder.Close()
	_ = os.Remove(hotCopy)

	// 1. 用 VACUUM INTO 生成一致性快照；失败时不留下半份文件。
	if err := vacuumInto(ctx, m.opts.Handle, hotCopy); err != nil {
		_ = os.Remove(hotCopy)
		return Result{}, err
	}
	// 快照只在打包期间使用，打包结束即删除。
	defer func() { _ = os.Remove(hotCopy) }()

	includes, err := m.writeArchive(target, hotCopy)
	if err != nil {
		_ = os.Remove(target)
		return Result{}, err
	}

	info, err := os.Stat(target)
	if err != nil {
		_ = os.Remove(target)
		return Result{}, errors.New("读取备份文件失败")
	}

	result := Result{Name: filepath.Base(target), Size: info.Size(), CreatedAt: now, Includes: includes}
	if m.opts.Sender != nil {
		caption := fmt.Sprintf("SeSeGraph 自动备份 %s（%.1f MB）", stamp, float64(info.Size())/(1<<20))
		if err := m.opts.Sender.SendDocument(ctx, target, caption); err != nil {
			// 发送失败不影响本地备份，调用方据此提示。
			return result, err
		}
		result.Sent = true
	}

	m.prune()
	return result, nil
}

// uniquePath 在同一秒内多次备份时追加序号，避免覆盖已有文件。
func uniquePath(dir, name string) string {
	target := filepath.Join(dir, name)
	if _, err := os.Stat(target); os.IsNotExist(err) {
		return target
	}
	base := strings.TrimSuffix(name, ".zip")
	for suffix := 2; ; suffix++ {
		candidate := filepath.Join(dir, fmt.Sprintf("%s-%d.zip", base, suffix))
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
}

// vacuumInto 在指定路径生成数据库快照。
// 先尝试参数绑定，驱动不支持时退回转义后的字面量。
func vacuumInto(ctx context.Context, handle *sql.DB, path string) error {
	if _, err := handle.ExecContext(ctx, `VACUUM INTO ?`, path); err == nil {
		return nil
	}
	escaped := strings.ReplaceAll(path, "'", "''")
	if _, err := handle.ExecContext(ctx, `VACUUM INTO '`+escaped+`'`); err != nil {
		return errors.New("数据库快照失败")
	}
	return nil
}

// writeArchive 把快照与可选缩略图写入 zip，返回打包的条目类别。
func (m *Manager) writeArchive(target, hotCopy string) ([]string, error) {
	file, err := os.Create(target)
	if err != nil {
		return nil, errors.New("创建备份文件失败")
	}
	writer := zip.NewWriter(file)

	includes := []string{"database"}
	if err := addFile(writer, databaseFileName, hotCopy); err != nil {
		_ = writer.Close()
		_ = file.Close()
		return nil, err
	}

	if m.opts.IncludeThumbs {
		thumbsDir := filepath.Join(m.opts.DataDir, thumbsDirName)
		entries, err := os.ReadDir(thumbsDir)
		if err == nil {
			for _, entry := range entries {
				if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".webp") {
					continue
				}
				source := filepath.Join(thumbsDir, entry.Name())
				if err := addFile(writer, thumbsDirName+"/"+entry.Name(), source); err != nil {
					_ = writer.Close()
					_ = file.Close()
					return nil, err
				}
			}
			includes = append(includes, "thumbs")
		}
	}

	if err := writer.Close(); err != nil {
		_ = file.Close()
		return nil, errors.New("打包备份失败")
	}
	if err := file.Close(); err != nil {
		return nil, errors.New("写入备份失败")
	}
	return includes, nil
}

// addFile 把单个文件流式写入 zip，缓冲区固定 32KB。
func addFile(writer *zip.Writer, name, source string) error {
	file, err := os.Open(source)
	if err != nil {
		return errors.New("读取备份源文件失败")
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil || info.IsDir() {
		return errors.New("读取备份源文件失败")
	}
	header, err := zip.FileInfoHeader(info)
	if err != nil {
		return errors.New("打包备份失败")
	}
	header.Name = name
	header.Method = zip.Deflate

	entry, err := writer.CreateHeader(header)
	if err != nil {
		return errors.New("打包备份失败")
	}
	if _, err := io.Copy(entry, file); err != nil {
		return errors.New("打包备份失败")
	}
	return nil
}

// prune 只保留最近 Keep 份备份。
func (m *Manager) prune() {
	files, err := m.list()
	if err != nil || len(files) <= m.opts.Keep {
		return
	}
	for _, path := range files[:len(files)-m.opts.Keep] {
		_ = os.Remove(path)
	}
}

// list 返回按时间从旧到新排列的备份文件。
func (m *Manager) list() ([]string, error) {
	entries, err := os.ReadDir(m.Dir())
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), backupPrefix) || !strings.HasSuffix(entry.Name(), ".zip") {
			continue
		}
		paths = append(paths, filepath.Join(m.Dir(), entry.Name()))
	}
	sort.Strings(paths)
	return paths, nil
}

// Latest 返回最近一份备份的信息，没有备份时返回 os.ErrNotExist。
func (m *Manager) Latest() (Result, error) {
	paths, err := m.list()
	if err != nil || len(paths) == 0 {
		return Result{}, os.ErrNotExist
	}
	path := paths[len(paths)-1]
	info, err := os.Stat(path)
	if err != nil {
		return Result{}, os.ErrNotExist
	}
	return Result{Name: filepath.Base(path), Size: info.Size(), CreatedAt: info.ModTime()}, nil
}

// LatestPath 返回最近一份备份的完整路径，供下载接口使用。
func (m *Manager) LatestPath() (string, error) {
	paths, err := m.list()
	if err != nil || len(paths) == 0 {
		return "", os.ErrNotExist
	}
	path := paths[len(paths)-1]
	if _, err := os.Stat(path); err != nil {
		return "", os.ErrNotExist
	}
	return path, nil
}
