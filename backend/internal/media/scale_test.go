package media

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// TestFullCacheScale 用接近满额的条目数覆盖占用统计、淘汰与写入路径：
// 2 GiB 按 60 KB 一项约为 3.6 万项，这里用同样数量的小文件验证复杂度与正确性。
func TestFullCacheScale(t *testing.T) {
	if testing.Short() {
		t.Skip("满额规模用例在 -short 下跳过")
	}
	const count = 36000
	dir := t.TempDir()
	root := filepath.Join(dir, Dir, "1")
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatalf("创建目录失败: %v", err)
	}
	payload := sampleWebP(64)
	for index := 1; index <= count; index += 1 {
		ver := fmt.Sprintf("%012d", index)
		name := strconv.Itoa(index) + "-s1-" + ver + ".webp"
		if err := os.WriteFile(filepath.Join(root, name), payload, 0o600); err != nil {
			t.Fatalf("写入文件失败: %v", err)
		}
	}
	// 明确拉开访问时间，让淘汰顺序可判定。
	base := time.Now().Add(-time.Duration(count) * time.Second)
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("读取目录失败: %v", err)
	}
	for index, item := range entries {
		stamp := base.Add(time.Duration(index) * time.Second)
		_ = os.Chtimes(filepath.Join(root, item.Name()), stamp, stamp)
	}

	begin := time.Now()
	used, items := Usage(dir)
	usageCost := time.Since(begin)
	if items != count {
		t.Fatalf("条目数 = %d，期望 %d", items, count)
	}
	if used != int64(count*len(payload)) {
		t.Fatalf("占用字节 = %d，期望 %d", used, count*len(payload))
	}

	// 上限设为能容纳 count-100 项：写入前需要淘汰 100 个最久未用的文件。
	limit := int64(count-100) * int64(len(payload))
	begin = time.Now()
	rel, _, err := Store(dir, 1, count+1, "abcdef123456", payload, limit)
	if err != nil {
		t.Fatalf("近满写入失败: %v", err)
	}
	writeCost := time.Since(begin)
	if _, _, err := Open(dir, rel); err != nil {
		t.Fatalf("新文件应已落盘: %v", err)
	}
	used, items = Usage(dir)
	// 淘汰到上限九成并留出余量，条目数应明显低于原数量且仍保留大部分文件。
	if items >= count || items < count/2 {
		t.Fatalf("淘汰后条目数不合理 = %d（原 %d）", items, count)
	}
	if used > limit {
		t.Fatalf("淘汰后占用 %d 超过上限 %d", used, limit)
	}

	t.Logf("满额规模：%d 项，占用统计 %s，近满写入与淘汰 %s", count, usageCost, writeCost)
	// 平方级实现会在这条路径上跑到分钟级；这里给一个宽松但能拦住退化的上界。
	if usageCost > 2*time.Second || writeCost > 5*time.Second {
		t.Fatalf("规模路径过慢：统计 %s，写入 %s", usageCost, writeCost)
	}
}
