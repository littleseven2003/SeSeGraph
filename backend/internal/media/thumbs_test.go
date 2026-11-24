package media

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// sampleWebP 构造一个带正确文件头的最小 WebP 字节串，只用于落盘校验。
func sampleWebP(size int) []byte {
	data := make([]byte, size)
	copy(data, []byte("RIFF"))
	copy(data[8:], []byte("WEBP"))
	for index := 12; index < size; index++ {
		data[index] = byte(index % 251)
	}
	return data
}

func TestStoreValidatesHeaderVersionAndSize(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := Store(dir, 1, 1, "0123456789ab", []byte("not-a-webp")); err != ErrInvalidThumb {
		t.Fatalf("非 WebP 内容应被拒绝，实际 %v", err)
	}
	if _, _, err := Store(dir, 1, 1, "短版本", sampleWebP(64)); err != ErrInvalidThumb {
		t.Fatalf("非法版本应被拒绝，实际 %v", err)
	}
	if _, _, err := Store(dir, 1, 0, "0123456789ab", sampleWebP(64)); err != ErrInvalidThumb {
		t.Fatalf("非法序号应被拒绝，实际 %v", err)
	}
	if _, _, err := Store(dir, 1, 1, "0123456789ab", sampleWebP(MaxBytes+1)); err != ErrTooLarge {
		t.Fatalf("超限应被拒绝，实际 %v", err)
	}

	rel, size, err := Store(dir, 7, 3, "0123456789ab", sampleWebP(1024))
	if err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	if rel != "thumbs/media/7/3-s1-0123456789ab.webp" {
		t.Fatalf("路径不符: %s", rel)
	}
	if size != 1024 {
		t.Fatalf("字节数不符: %d", size)
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
		t.Fatalf("文件应已落盘: %v", err)
	}
	// 临时文件不能残留。
	entries, err := os.ReadDir(filepath.Join(dir, Dir, "7"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("目录里应只有一个文件，实际 %d（%v）", len(entries), err)
	}
}

func TestOpenRejectsPathEscape(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := Open(dir, "thumbs/1-abc.webp"); err == nil {
		t.Fatal("越界路径必须打不开")
	}
	if _, _, err := Open(dir, "../../etc/passwd"); err == nil {
		t.Fatal("越界路径必须打不开")
	}
}

func TestRemoveAndRemovePost(t *testing.T) {
	dir := t.TempDir()
	rel, _, err := Store(dir, 5, 2, "0123456789ab", sampleWebP(128))
	if err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	if err := Remove(dir, rel); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); !os.IsNotExist(err) {
		t.Fatalf("文件应已删除: %v", err)
	}
	if err := Remove(dir, ""); err != nil {
		t.Fatalf("空路径应忽略: %v", err)
	}

	if _, _, err := Store(dir, 5, 3, "1123456789ab", sampleWebP(128)); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	if err := RemovePost(dir, 5); err != nil {
		t.Fatalf("删除整帖失败: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, Dir, "5")); !os.IsNotExist(err) {
		t.Fatalf("整帖目录应删除: %v", err)
	}
}

func TestUsageAndEvictByLeastRecentlyUsed(t *testing.T) {
	dir := t.TempDir()
	versions := []string{"0123456789ab", "1123456789ab", "2123456789ab"}
	rels := make([]string, 0, len(versions))
	for index, version := range versions {
		rel, _, err := Store(dir, 9, index+1, version, sampleWebP(100))
		if err != nil {
			t.Fatalf("写入失败: %v", err)
		}
		rels = append(rels, rel)
		// 明确拉开修改时间，避免同一毫秒内无法区分 LRU 顺序。
		stamp := time.Now().Add(-time.Duration(3-index) * time.Minute)
		if err := os.Chtimes(filepath.Join(dir, filepath.FromSlash(rel)), stamp, stamp); err != nil {
			t.Fatalf("调整时间失败: %v", err)
		}
	}

	used, count := Usage(dir)
	if used != 300 || count != 3 {
		t.Fatalf("占用统计不符: %d 字节 / %d 项", used, count)
	}

	// 把最早的一项标记为刚用过，淘汰应跳过它。
	Touch(dir, rels[0])
	removed, freed, err := Evict(dir, 200)
	if err != nil {
		t.Fatalf("淘汰失败: %v", err)
	}
	if removed != 1 || freed != 100 {
		t.Fatalf("应淘汰一项，实际 %d 项 / %d 字节", removed, freed)
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rels[1]))); !os.IsNotExist(err) {
		t.Fatal("第二早的项应被淘汰")
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rels[0]))); err != nil {
		t.Fatal("刚用过的一项应保留")
	}
	if used, count = Usage(dir); used != 200 || count != 2 {
		t.Fatalf("淘汰后统计不符: %d 字节 / %d 项", used, count)
	}

	// 上限为 0 时清空全部。
	removed, _, err = Evict(dir, 0)
	if err != nil || removed != 2 {
		t.Fatalf("上限为 0 应清空，实际 %d 项（%v）", removed, err)
	}
	if used, count = Usage(dir); used != 0 || count != 0 {
		t.Fatalf("清空后应为空: %d / %d", used, count)
	}
}

func TestClearRemovesEverything(t *testing.T) {
	dir := t.TempDir()
	for index := 0; index < 2; index++ {
		if _, _, err := Store(dir, 3, index+1, []string{"0123456789ab", "1123456789ab"}[index], sampleWebP(256)); err != nil {
			t.Fatalf("写入失败: %v", err)
		}
	}
	count, freed, err := Clear(dir)
	if err != nil || count != 2 || freed != 512 {
		t.Fatalf("清理结果不符: %d 项 / %d 字节（%v）", count, freed, err)
	}
	if used, items := Usage(dir); used != 0 || items != 0 {
		t.Fatalf("清理后应为空: %d / %d", used, items)
	}
}

func TestEvictIgnoresForeignFiles(t *testing.T) {
	dir := t.TempDir()
	// 封面缩略图放在 thumbs/ 顶层，不在 media 目录内，淘汰不能误删。
	if err := os.MkdirAll(filepath.Join(dir, "thumbs"), 0o750); err != nil {
		t.Fatalf("准备目录失败: %v", err)
	}
	cover := filepath.Join(dir, "thumbs", "1.webp")
	if err := os.WriteFile(cover, sampleWebP(64), 0o600); err != nil {
		t.Fatalf("写入封面失败: %v", err)
	}
	if used, count := Usage(dir); used != 0 || count != 0 {
		t.Fatalf("media 目录为空时统计应为 0: %d / %d", used, count)
	}
	if _, _, err := Evict(dir, 0); err != nil {
		t.Fatalf("淘汰失败: %v", err)
	}
	if _, err := os.Stat(cover); err != nil {
		t.Fatal("封面文件不能被逐图缓存清理误删")
	}
}

func TestWebPHeader(t *testing.T) {
	if webpHeader([]byte("RIFFxxxxWEBP")) != true {
		t.Fatal("合法头应通过")
	}
	if webpHeader([]byte("RIFFxxxxWEBQ")) {
		t.Fatal("错误头应拒绝")
	}
	if webpHeader(bytes.Repeat([]byte("a"), 8)) {
		t.Fatal("过短内容应拒绝")
	}
}
