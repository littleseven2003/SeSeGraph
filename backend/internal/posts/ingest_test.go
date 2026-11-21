package posts

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestSaveImageStreamWritesFile(t *testing.T) {
	dataDir := t.TempDir()
	content := []byte("fake-image-bytes")

	saved, err := SaveImageStream(dataDir, 7, 2, bytes.NewReader(content), 1024)
	if err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	if saved.FileSize != int64(len(content)) {
		t.Errorf("大小 = %d，期望 %d", saved.FileSize, len(content))
	}
	if saved.Path != "tmp/7/2.bin" {
		t.Errorf("路径 = %q，期望 tmp/7/2.bin", saved.Path)
	}

	full := filepath.Join(dataDir, filepath.FromSlash(saved.Path))
	written, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("读取落盘文件失败: %v", err)
	}
	if !bytes.Equal(written, content) {
		t.Error("落盘内容与输入不一致")
	}

	entries, _ := os.ReadDir(filepath.Join(dataDir, TempDir, "7"))
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".part") {
			t.Errorf("不应残留临时文件: %s", entry.Name())
		}
	}
}

func TestSaveImageStreamRejectsOversize(t *testing.T) {
	dataDir := t.TempDir()
	body := strings.NewReader(strings.Repeat("x", 17))

	if _, err := SaveImageStream(dataDir, 1, 1, body, 16); !errors.Is(err, ErrImageTooLarge) {
		t.Fatalf("错误 = %v，期望 ErrImageTooLarge", err)
	}
	assertNoLeftovers(t, dataDir, 1)
}

type failingReader struct{ read int }

func (f *failingReader) Read(p []byte) (int, error) {
	if f.read > 0 {
		return 0, errors.New("连接中断")
	}
	f.read = copy(p, []byte("partial"))
	return f.read, nil
}

func TestSaveImageStreamCleansUpOnInterrupt(t *testing.T) {
	dataDir := t.TempDir()
	if _, err := SaveImageStream(dataDir, 3, 1, &failingReader{}, 1024); !errors.Is(err, ErrWriteInterrupted) {
		t.Fatalf("错误 = %v，期望 ErrWriteInterrupted", err)
	}
	assertNoLeftovers(t, dataDir, 3)
}

func assertNoLeftovers(t *testing.T, dataDir string, postID int64) {
	t.Helper()
	dir := filepath.Join(dataDir, TempDir, strconv.FormatInt(postID, 10))
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return
		}
		t.Fatalf("读取临时目录失败: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("临时目录应清空，仍有 %d 个文件", len(entries))
	}
}

func TestLimitForMode(t *testing.T) {
	if got := LimitForMode("document"); got != MaxDocumentBytes {
		t.Errorf("document 上限 = %d", got)
	}
	if got := LimitForMode("photo"); got != MaxPhotoBytes {
		t.Errorf("photo 上限 = %d", got)
	}
	if got := LimitForMode(""); got != MaxPhotoBytes {
		t.Errorf("默认上限 = %d", got)
	}
}
