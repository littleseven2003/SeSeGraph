package backup

import (
	"archive/zip"
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/littleseven2003/SeSeGraph/backend/internal/db"
)

// testClock 提供可推进的时钟，让备份文件名可预期。
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock() *testClock {
	return &testClock{now: time.Date(2026, 11, 22, 3, 0, 0, 0, time.UTC)}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// fakeSender 记录发送调用。
type fakeSender struct {
	mu    sync.Mutex
	paths []string
	caps  []string
	err   error
}

func (f *fakeSender) SendDocument(_ context.Context, path, caption string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.paths = append(f.paths, path)
	f.caps = append(f.caps, caption)
	return nil
}

// newEnv 准备带结构的数据库、缩略图与备份管理器。
func newEnv(t *testing.T, opts Options) (*Manager, *sql.DB, string, *testClock) {
	t.Helper()
	dataDir := t.TempDir()
	handle, err := db.Open(filepath.Join(dataDir, databaseFileName))
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	if err := db.Migrate(context.Background(), handle); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	if _, err := handle.ExecContext(context.Background(),
		`INSERT INTO settings (key, value) VALUES ('probe', 'backup-ok')`); err != nil {
		t.Fatalf("写入数据失败: %v", err)
	}

	thumbs := filepath.Join(dataDir, thumbsDirName)
	if err := os.MkdirAll(thumbs, 0o750); err != nil {
		t.Fatalf("创建缩略图目录失败: %v", err)
	}
	for _, name := range []string{"1.webp", "2.webp"} {
		if err := os.WriteFile(filepath.Join(thumbs, name), []byte("webp-"+name), 0o600); err != nil {
			t.Fatalf("写入缩略图失败: %v", err)
		}
	}

	clock := newTestClock()
	opts.DataDir = dataDir
	opts.Handle = handle
	opts.Now = clock.Now
	return New(opts), handle, dataDir, clock
}

// archiveEntries 返回 zip 内的条目名与内容大小。
func archiveEntries(t *testing.T, path string) map[string]int {
	t.Helper()
	reader, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("打开备份失败: %v", err)
	}
	defer func() { _ = reader.Close() }()

	entries := make(map[string]int, len(reader.File))
	for _, file := range reader.File {
		entries[file.Name] = int(file.UncompressedSize64)
	}
	return entries
}

func TestRunCreatesArchiveWithDatabase(t *testing.T) {
	manager, handle, dataDir, _ := newEnv(t, Options{})
	if err := manager.Start(); err != nil {
		t.Fatalf("开始备份失败: %v", err)
	}
	result, err := manager.Run(context.Background())
	if err != nil {
		t.Fatalf("备份失败: %v", err)
	}
	if !strings.HasPrefix(result.Name, backupPrefix) || !strings.HasSuffix(result.Name, ".zip") {
		t.Errorf("备份名 = %q，期望带前缀与扩展名", result.Name)
	}
	if result.Size <= 0 || result.Sent {
		t.Errorf("结果 = %+v，期望有大小且未发送", result)
	}
	if len(result.Includes) != 1 || result.Includes[0] != "database" {
		t.Errorf("打包内容 = %v，期望只含数据库", result.Includes)
	}

	entries := archiveEntries(t, filepath.Join(dataDir, DirName, result.Name))
	if _, ok := entries[databaseFileName]; !ok {
		t.Fatalf("压缩包缺少数据库: %v", entries)
	}

	// 解出的数据库应能读出原数据。
	reader, err := zip.OpenReader(filepath.Join(dataDir, DirName, result.Name))
	if err != nil {
		t.Fatalf("打开备份失败: %v", err)
	}
	defer func() { _ = reader.Close() }()
	var extracted string
	for _, file := range reader.File {
		if file.Name != databaseFileName {
			continue
		}
		content, err := file.Open()
		if err != nil {
			t.Fatalf("读取条目失败: %v", err)
		}
		target := filepath.Join(t.TempDir(), databaseFileName)
		output, err := os.Create(target)
		if err != nil {
			t.Fatalf("写入临时文件失败: %v", err)
		}
		if _, err := io.Copy(output, content); err != nil {
			t.Fatalf("导出失败: %v", err)
		}
		_ = output.Close()
		_ = content.Close()
		copyHandle, err := db.Open(target)
		if err != nil {
			t.Fatalf("打开备份数据库失败: %v", err)
		}
		if err := copyHandle.QueryRow(`SELECT value FROM settings WHERE key = 'probe'`).Scan(&extracted); err != nil {
			t.Fatalf("读取备份数据失败: %v", err)
		}
		_ = copyHandle.Close()
	}
	if extracted != "backup-ok" {
		t.Errorf("备份内数据 = %q，期望 backup-ok", extracted)
	}
	_ = handle

	// 临时快照与打包无关的文件都应清理。
	files, err := os.ReadDir(filepath.Join(dataDir, DirName))
	if err != nil {
		t.Fatalf("读取备份目录失败: %v", err)
	}
	for _, file := range files {
		if strings.HasSuffix(file.Name(), ".db") {
			t.Errorf("残留临时快照: %s", file.Name())
		}
	}
}

func TestRunIncludesThumbs(t *testing.T) {
	manager, _, dataDir, _ := newEnv(t, Options{IncludeThumbs: true})
	if err := manager.Start(); err != nil {
		t.Fatalf("开始备份失败: %v", err)
	}
	result, err := manager.Run(context.Background())
	if err != nil {
		t.Fatalf("备份失败: %v", err)
	}
	if len(result.Includes) != 2 || result.Includes[1] != "thumbs" {
		t.Errorf("打包内容 = %v，期望包含缩略图", result.Includes)
	}
	entries := archiveEntries(t, filepath.Join(dataDir, DirName, result.Name))
	for _, name := range []string{"thumbs/1.webp", "thumbs/2.webp"} {
		if _, ok := entries[name]; !ok {
			t.Errorf("压缩包缺少 %s: %v", name, entries)
		}
	}
}

func TestRunSendsArchive(t *testing.T) {
	sender := &fakeSender{}
	manager, _, dataDir, _ := newEnv(t, Options{Sender: sender})
	if err := manager.Start(); err != nil {
		t.Fatalf("开始备份失败: %v", err)
	}
	result, err := manager.Run(context.Background())
	if err != nil {
		t.Fatalf("备份失败: %v", err)
	}
	if !result.Sent {
		t.Error("配置发送后应标记已发送")
	}
	if len(sender.paths) != 1 || sender.paths[0] != filepath.Join(dataDir, DirName, result.Name) {
		t.Errorf("发送路径 = %v，期望备份文件", sender.paths)
	}
	if len(sender.caps) != 1 || !strings.Contains(sender.caps[0], "自动备份") {
		t.Errorf("说明 = %v，期望包含备份说明", sender.caps)
	}
}

func TestRunKeepsLocalFileWhenSendFails(t *testing.T) {
	sender := &fakeSender{err: errors.New("上游不可用")}
	manager, _, dataDir, _ := newEnv(t, Options{Sender: sender})
	if err := manager.Start(); err != nil {
		t.Fatalf("开始备份失败: %v", err)
	}
	result, err := manager.Run(context.Background())
	if err == nil {
		t.Fatal("发送失败时应返回错误")
	}
	if result.Sent {
		t.Error("发送失败不应标记已发送")
	}
	if _, statErr := os.Stat(filepath.Join(dataDir, DirName, result.Name)); statErr != nil {
		t.Errorf("发送失败时本地备份应保留: %v", statErr)
	}
}

func TestRunPrunesOldBackups(t *testing.T) {
	manager, _, dataDir, clock := newEnv(t, Options{Keep: 3})
	for i := 0; i < 5; i++ {
		if err := manager.Start(); err != nil {
			t.Fatalf("开始备份失败: %v", err)
		}
		if _, err := manager.Run(context.Background()); err != nil {
			t.Fatalf("备份失败: %v", err)
		}
		clock.Advance(time.Hour)
	}
	files, err := os.ReadDir(filepath.Join(dataDir, DirName))
	if err != nil {
		t.Fatalf("读取备份目录失败: %v", err)
	}
	if len(files) != 3 {
		t.Errorf("保留份数 = %d，期望 3", len(files))
	}
	latest, err := manager.Latest()
	if err != nil {
		t.Fatalf("读取最新备份失败: %v", err)
	}
	path, err := manager.LatestPath()
	if err != nil || filepath.Base(path) != latest.Name {
		t.Errorf("最新路径 = %q err=%v，期望与 Latest 一致", path, err)
	}
}

func TestStartRejectsConcurrentRun(t *testing.T) {
	manager, _, _, _ := newEnv(t, Options{})
	if err := manager.Start(); err != nil {
		t.Fatalf("首次开始失败: %v", err)
	}
	if err := manager.Start(); !errors.Is(err, ErrBusy) {
		t.Fatalf("错误 = %v，期望 ErrBusy", err)
	}
	if _, err := manager.Run(context.Background()); err != nil {
		t.Fatalf("备份失败: %v", err)
	}
	// Run 结束会释放占用。
	if err := manager.Start(); err != nil {
		t.Errorf("结束后应可再次开始: %v", err)
	}
}

func TestLatestWithoutBackups(t *testing.T) {
	manager, _, _, _ := newEnv(t, Options{})
	if _, err := manager.Latest(); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("错误 = %v，期望 os.ErrNotExist", err)
	}
	if _, err := manager.LatestPath(); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("错误 = %v，期望 os.ErrNotExist", err)
	}
}

// TestParseDaily 验证备份周期只接受每天固定时刻。
func TestParseDaily(t *testing.T) {
	cases := []struct {
		spec   string
		hour   int
		minute int
		ok     bool
	}{
		{spec: "0 3 * * *", hour: 3, minute: 0, ok: true},
		{spec: "30 23 * * *", hour: 23, minute: 30, ok: true},
		{spec: "* 4 * * *", hour: 4, minute: 0, ok: true},
		{spec: "0 3 * * 1", ok: false},
		{spec: "0 24 * * *", ok: false},
		{spec: "60 3 * * *", ok: false},
		{spec: "0 3 * *", ok: false},
		{spec: "x 3 * * *", ok: false},
	}
	for _, test := range cases {
		hour, minute, err := ParseDaily(test.spec)
		if test.ok {
			if err != nil || hour != test.hour || minute != test.minute {
				t.Errorf("%q => %d:%d err=%v，期望 %d:%d", test.spec, hour, minute, err, test.hour, test.minute)
			}
			continue
		}
		if err == nil {
			t.Errorf("%q 应被拒绝", test.spec)
		}
	}
	if text := DailyText(3, 0); text != "每天 03:00" {
		t.Errorf("展示文案 = %q，期望 每天 03:00", text)
	}
}

// inspectingSender 在发送时校验目标文件已完整可读。
type inspectingSender struct {
	t       *testing.T
	checked int
}

func (s *inspectingSender) SendDocument(_ context.Context, path, _ string) error {
	s.t.Helper()
	entries := archiveEntries(s.t, path)
	if _, ok := entries[databaseFileName]; !ok {
		s.t.Errorf("发送时 zip 内缺少数据库条目: %v", entries)
	}
	s.checked++
	return nil
}

// TestRunPublishesCompleteArchive 验证 zip 完整关闭后才进入最终文件名，发送时可直接回读。
func TestRunPublishesCompleteArchive(t *testing.T) {
	sender := &inspectingSender{t: t}
	manager, _, dataDir, _ := newEnv(t, Options{Sender: sender})
	if err := manager.Start(); err != nil {
		t.Fatalf("开始备份失败: %v", err)
	}
	result, err := manager.Run(context.Background())
	if err != nil {
		t.Fatalf("备份失败: %v", err)
	}
	if !result.Sent || sender.checked != 1 {
		t.Fatalf("发送次数 = %d，期望发送一次", sender.checked)
	}

	// 临时文件不应留在备份目录，只有最终产物对下载可见。
	entries, err := os.ReadDir(filepath.Join(dataDir, DirName))
	if err != nil {
		t.Fatalf("读取备份目录失败: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".part-") {
			t.Errorf("残留临时文件 %q", entry.Name())
		}
	}
	latest, err := manager.LatestPath()
	if err != nil {
		t.Fatalf("读取最新备份失败: %v", err)
	}
	if filepath.Base(latest) != result.Name {
		t.Errorf("最新备份 = %q，期望 %q", filepath.Base(latest), result.Name)
	}
}

// TestRunPrunesWhenSendFails 验证连续发送失败时仍只保留最近 Keep 份。
func TestRunPrunesWhenSendFails(t *testing.T) {
	sender := &fakeSender{err: errors.New("发送失败")}
	manager, _, _, clock := newEnv(t, Options{Keep: 2, Sender: sender})

	for index := 0; index < 4; index++ {
		if err := manager.Start(); err != nil {
			t.Fatalf("第 %d 次备份开始失败: %v", index+1, err)
		}
		if _, err := manager.Run(context.Background()); err == nil {
			t.Fatalf("第 %d 次备份应返回发送错误", index+1)
		}
		clock.Advance(time.Second)
	}

	files, err := manager.list()
	if err != nil {
		t.Fatalf("列出备份失败: %v", err)
	}
	if len(files) != 2 {
		t.Errorf("保留份数 = %d，期望 2：%v", len(files), files)
	}
}
