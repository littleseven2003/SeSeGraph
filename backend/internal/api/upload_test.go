package api

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
)

// jpegHeader 是能被识别为 image/jpeg 的最小头部。
var jpegHeader = []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00, 0x01}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

func (e *testEnv) doRaw(t *testing.T, method, path, contentType string, body []byte, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.RemoteAddr = "127.0.0.1:54321"
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

func (e *testEnv) createPost(t *testing.T, cookie *http.Cookie, imageCount int, mode string) int64 {
	t.Helper()
	body := `{"title":"上传测试","image_count":` + strconv.Itoa(imageCount) + `,"upload_mode":"` + mode + `"}`
	rec := e.do(t, http.MethodPost, "/api/posts", body, cookie)
	if rec.Code != http.StatusCreated {
		t.Fatalf("创建帖子失败: %d %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		ID int64 `json:"id"`
	}
	if err := decodeJSON(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	return payload.ID
}

func TestUploadImageRequiresSession(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	rec := env.doRaw(t, http.MethodPost, "/api/posts/1/images?sort=1", "image/jpeg", jpegHeader, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("状态码 = %d，期望 401", rec.Code)
	}
}

func TestUploadImageStoresFile(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)
	postID := env.createPost(t, cookie, 2, "photo")

	content := append(append([]byte{}, jpegHeader...), []byte("image-body")...)
	rec := env.doRaw(t, http.MethodPost, "/api/posts/"+strconv.Itoa(int(postID))+"/images?sort=1", "image/jpeg", content, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200，响应: %s", rec.Code, rec.Body.String())
	}

	var payload struct {
		SortOrder int    `json:"sort_order"`
		FileSize  int64  `json:"file_size"`
		MimeType  string `json:"mime_type"`
		Status    string `json:"status"`
	}
	if err := decodeJSON(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if payload.FileSize != int64(len(content)) || payload.MimeType != "image/jpeg" || payload.Status != posts.ImageStatusUploaded {
		t.Errorf("响应字段不符: %+v", payload)
	}

	images, err := env.posts.ListImages(context.Background(), postID)
	if err != nil {
		t.Fatalf("读取图片记录失败: %v", err)
	}
	if len(images) != 1 || images[0].FileSize != int64(len(content)) {
		t.Fatalf("图片记录不符: %+v", images)
	}

	stored := filepath.Join(env.dataDir, filepath.FromSlash(images[0].TempFilePath))
	written, err := os.ReadFile(stored)
	if err != nil {
		t.Fatalf("落盘文件不存在: %v", err)
	}
	if len(written) != len(content) {
		t.Errorf("落盘大小 = %d，期望 %d", len(written), len(content))
	}
}

func TestUploadImageRejectsOversize(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)
	postID := env.createPost(t, cookie, 1, "photo")

	body := io.MultiReader(
		bytes.NewReader(jpegHeader),
		io.LimitReader(zeroReader{}, posts.MaxPhotoBytes+1-int64(len(jpegHeader))),
	)
	req := httptest.NewRequest(http.MethodPost, "/api/posts/"+strconv.Itoa(int(postID))+"/images?sort=1", body)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Header.Set("Content-Type", "image/jpeg")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	env.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("状态码 = %d，期望 413", rec.Code)
	}
	images, _ := env.posts.ListImages(context.Background(), postID)
	if len(images) != 0 {
		t.Errorf("超限请求不应留下记录: %+v", images)
	}
	entries, err := os.ReadDir(filepath.Join(env.dataDir, "tmp", strconv.Itoa(int(postID))))
	if err == nil && len(entries) != 0 {
		t.Errorf("超限请求不应留下临时文件，实际 %d 个", len(entries))
	}
}

func TestUploadImageRejectsUnknownType(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)
	postID := env.createPost(t, cookie, 1, "photo")

	rec := env.doRaw(t, http.MethodPost, "/api/posts/"+strconv.Itoa(int(postID))+"/images?sort=1", "text/plain", []byte("not an image"), cookie)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("状态码 = %d，期望 415", rec.Code)
	}
}

func TestUploadImageRejectsInvalidSort(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)
	postID := env.createPost(t, cookie, 2, "photo")

	for _, sort := range []string{"0", "3"} {
		rec := env.doRaw(t, http.MethodPost, "/api/posts/"+strconv.Itoa(int(postID))+"/images?sort="+sort, "image/jpeg", jpegHeader, cookie)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("sort=%s 状态码 = %d，期望 400", sort, rec.Code)
		}
	}
}

func TestUploadImageMissingPost(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)

	rec := env.doRaw(t, http.MethodPost, "/api/posts/999/images?sort=1", "image/jpeg", jpegHeader, cookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("状态码 = %d，期望 404", rec.Code)
	}
}

func TestUploadImageReplaceKeepsSingleRecord(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)
	postID := env.createPost(t, cookie, 1, "photo")
	path := "/api/posts/" + strconv.Itoa(int(postID)) + "/images?sort=1"

	first := append(append([]byte{}, jpegHeader...), []byte("first")...)
	if rec := env.doRaw(t, http.MethodPost, path, "image/jpeg", first, cookie); rec.Code != http.StatusOK {
		t.Fatalf("首次上传状态码 = %d", rec.Code)
	}

	second := append(append([]byte{}, jpegHeader...), []byte("second-attempt-body")...)
	if rec := env.doRaw(t, http.MethodPost, path, "image/jpeg", second, cookie); rec.Code != http.StatusOK {
		t.Fatalf("重传状态码 = %d", rec.Code)
	}

	images, err := env.posts.ListImages(context.Background(), postID)
	if err != nil {
		t.Fatalf("读取图片记录失败: %v", err)
	}
	if len(images) != 1 {
		t.Fatalf("图片记录数 = %d，期望 1", len(images))
	}
	if images[0].FileSize != int64(len(second)) {
		t.Errorf("重传后大小 = %d，期望 %d", images[0].FileSize, len(second))
	}
}

func TestConcurrentSameSortUploadKeepsOneRecord(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)
	postID := env.createPost(t, cookie, 1, "photo")
	path := "/api/posts/" + strconv.Itoa(int(postID)) + "/images?sort=1"

	const workers = 8
	var wg sync.WaitGroup
	codes := make(chan int, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			payload := append(append([]byte{}, jpegHeader...), []byte(strconv.Itoa(n))...)
			req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(payload))
			req.RemoteAddr = "127.0.0.1:54321"
			req.Header.Set("Content-Type", "image/jpeg")
			req.AddCookie(cookie)
			rec := httptest.NewRecorder()
			env.router.ServeHTTP(rec, req)
			codes <- rec.Code
		}(i)
	}
	wg.Wait()
	close(codes)

	for code := range codes {
		if code != http.StatusOK {
			t.Errorf("并发上传状态码 = %d，期望 200", code)
		}
	}

	images, err := env.posts.ListImages(context.Background(), postID)
	if err != nil {
		t.Fatalf("读取图片记录失败: %v", err)
	}
	if len(images) != 1 {
		t.Fatalf("图片记录数 = %d，期望 1（唯一约束生效）", len(images))
	}

	entries, err := os.ReadDir(filepath.Join(env.dataDir, "tmp", strconv.Itoa(int(postID))))
	if err != nil {
		t.Fatalf("读取临时目录失败: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("并发覆盖后应只剩 1 个文件，实际 %d 个", len(entries))
	}
	if !strings.HasSuffix(images[0].TempFilePath, entries[0].Name()) {
		t.Errorf("记录应指向保留的文件，记录=%q 文件=%q", images[0].TempFilePath, entries[0].Name())
	}
}

func TestUploadAfterCommitLeavesNoFile(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)
	postID := env.createPost(t, cookie, 1, "photo")
	path := "/api/posts/" + strconv.Itoa(int(postID)) + "/images?sort=1"

	if rec := env.doRaw(t, http.MethodPost, path, "image/jpeg", jpegHeader, cookie); rec.Code != http.StatusOK {
		t.Fatalf("上传状态码 = %d", rec.Code)
	}
	if rec := env.do(t, http.MethodPost, "/api/posts/"+strconv.Itoa(int(postID))+"/commit", "", cookie); rec.Code != http.StatusOK {
		t.Fatalf("提交状态码 = %d", rec.Code)
	}

	rec := env.doRaw(t, http.MethodPost, path, "image/jpeg", append(append([]byte{}, jpegHeader...), 'x'), cookie)
	if rec.Code != http.StatusConflict {
		t.Fatalf("入队后上传状态码 = %d，期望 409", rec.Code)
	}

	entries, err := os.ReadDir(filepath.Join(env.dataDir, "tmp", strconv.Itoa(int(postID))))
	if err != nil {
		t.Fatalf("读取临时目录失败: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("被拒绝的上传不应留下文件，实际 %d 个", len(entries))
	}
}
