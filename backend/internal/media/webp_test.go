package media

import (
	"io/fs"
	"path/filepath"
	"testing"
)

// TestInspectWebPLossless 验证 VP8L 头能被解析出真实宽高。
func TestInspectWebPLossless(t *testing.T) {
	info, err := InspectWebP(sizedWebP(64, 512, 384))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if info.Width != 512 || info.Height != 384 || info.Animated {
		t.Fatalf("解析结果不符: %+v", info)
	}
}

// TestInspectWebPLossy 验证 VP8 关键帧头能被解析出真实宽高。
func TestInspectWebPLossy(t *testing.T) {
	chunk := []byte{
		0x00, 0x00, 0x00, // 帧标签
		0x9d, 0x01, 0x2a, // 关键帧起始码
		0x40, 0x01, // 宽 320
		0xf0, 0x00, // 高 240
	}
	data := make([]byte, 12+8+len(chunk)+1)
	copy(data[0:4], []byte("RIFF"))
	putUint32LE(data[4:8], uint32(len(data)-8))
	copy(data[8:12], []byte("WEBP"))
	copy(data[12:16], []byte("VP8 "))
	putUint32LE(data[16:20], uint32(len(chunk)))
	copy(data[20:], chunk)

	info, err := InspectWebP(data)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if info.Width != 320 || info.Height != 240 {
		t.Fatalf("宽高不符: %d x %d", info.Width, info.Height)
	}
}

// TestInspectWebPRejectsAnimation 验证带 ANIM 块的动态图被识别为动画。
func TestInspectWebPRejectsAnimation(t *testing.T) {
	data := sizedWebP(64, 320, 240)
	// 在图像块前后插一个 ANIM 块，解析应标记为动画。
	anim := make([]byte, 26+len(data)-12)
	copy(anim, data[0:12])
	copy(anim[12:16], []byte("ANIM"))
	putUint32LE(anim[16:20], 6)
	copy(anim[20:], []byte{0, 0, 0, 0, 0, 0})
	copy(anim[26:], data[12:])
	// 修正 RIFF 长度。
	putUint32LE(anim[4:8], uint32(len(anim)-8))

	info, err := InspectWebP(anim)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if !info.Animated {
		t.Fatal("带 ANIM 块应标记为动画")
	}
}

// TestInspectWebPRejectsGarbage 验证错误内容不会误判为合法 WebP。
func TestInspectWebPRejectsGarbage(t *testing.T) {
	if _, err := InspectWebP([]byte("RIFFxxxxWEBP")); err == nil {
		t.Fatal("截断内容应报错")
	}
	if _, err := InspectWebP([]byte("not-a-webp-at-all")); err == nil {
		t.Fatal("非 WebP 应报错")
	}
}

// TestStoreRejectsOversizePixels 验证超过最长边上限的图被拒收。
func TestStoreRejectsOversizePixels(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := Store(dir, 1, 1, "0123456789ab", sizedWebP(2048, 1024, 768), 0); err != ErrInvalidThumb {
		t.Fatalf("超大像素应被拒绝，实际 %v", err)
	}
	if _, _, err := Store(dir, 1, 1, "0123456789ab", sizedWebP(2048, 513, 200), 0); err != ErrInvalidThumb {
		t.Fatalf("超过最长边上限应被拒绝，实际 %v", err)
	}
}

// TestStoreEnforcesCacheLimit 验证正常写入也会执行生效上限，旧图被淘汰。
func TestStoreEnforcesCacheLimit(t *testing.T) {
	dir := t.TempDir()
	first, _, err := Store(dir, 2, 1, "0123456789ab", sampleWebP(200), 0)
	if err != nil {
		t.Fatalf("首次写入失败: %v", err)
	}
	// 上限 300 字节：再写 200 字节必须挤掉最早的一份。
	if _, _, err := Store(dir, 2, 2, "1123456789ab", sampleWebP(200), 300); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	if used, count := Usage(dir); used != 200 || count != 1 {
		t.Fatalf("超过上限后应淘汰旧图，实际 %d 字节 / %d 项", used, count)
	}
	if _, _, err := Open(dir, first); err == nil {
		t.Fatal("最早的一份应被淘汰")
	}
}

// TestStoreReportsCacheFull 验证单张就放不下时返回缓存已满，不落盘。
func TestStoreReportsCacheFull(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := Store(dir, 3, 1, "0123456789ab", sampleWebP(400), 100); err != ErrCacheFull {
		t.Fatalf("放不下时应返回缓存已满，实际 %v", err)
	}
	if used, count := Usage(dir); used != 0 || count != 0 {
		t.Fatalf("拒收后不应留下文件: %d / %d", used, count)
	}
	entries, err := osReadDirAll(dir)
	if err != nil {
		t.Fatalf("读取目录失败: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("不应留下临时文件: %v", entries)
	}
}

// osReadDirAll 递归列出目录下的全部路径，用于确认没有残留临时文件。
func osReadDirAll(root string) ([]string, error) {
	var found []string
	err := filepath.WalkDir(root, func(path string, item fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if item.IsDir() {
			return nil
		}
		found = append(found, path)
		return nil
	})
	return found, err
}
