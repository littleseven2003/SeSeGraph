package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/littleseven2003/SeSeGraph/backend/internal/media"
	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
)

// thumbBytes 构造一张 320x240、总长为 size 的静态 WebP，供接口测试使用。
// 体积超限用例在解析前就被拒绝，此时不要求内容可解析。
func thumbBytes(size int) []byte {
	if size < 34 || (size-34)%2 != 0 {
		data := make([]byte, size)
		copy(data, []byte("RIFF"))
		copy(data[8:], []byte("WEBP"))
		return data
	}
	data := make([]byte, size)
	copy(data[0:4], []byte("RIFF"))
	putUint32LE(data[4:8], uint32(size-8))
	copy(data[8:12], []byte("WEBP"))
	copy(data[12:16], []byte("VP8L"))
	putUint32LE(data[16:20], 5)
	data[20] = 0x2f
	putUint32LE(data[21:25], uint32(320-1)|uint32(240-1)<<14)
	copy(data[26:30], []byte("JUNK"))
	putUint32LE(data[30:34], uint32(size-34))
	return data
}

// putUint32LE 按小端写入 32 位无符号整数。
func putUint32LE(dst []byte, value uint32) {
	dst[0] = byte(value)
	dst[1] = byte(value >> 8)
	dst[2] = byte(value >> 16)
	dst[3] = byte(value >> 24)
}

// detailImages 读取详情里的图片清单。
func detailImages(t *testing.T, env *testEnv, cookie *http.Cookie, postID int64) []galleryImageResponse {
	t.Helper()
	response := env.do(t, http.MethodGet, "/api/posts/"+itoa(postID), "", cookie)
	if response.Code != http.StatusOK {
		t.Fatalf("读取详情失败: %d %s", response.Code, response.Body.String())
	}
	var detail galleryDetailResponse
	if err := json.Unmarshal(response.Body.Bytes(), &detail); err != nil {
		t.Fatalf("解析详情失败: %v", err)
	}
	return detail.Images
}

// itoa 避免为测试引入额外依赖。
func itoa(value int64) string {
	if value == 0 {
		return "0"
	}
	buffer := make([]byte, 0, 20)
	for value > 0 {
		buffer = append([]byte{byte('0' + value%10)}, buffer...)
		value /= 10
	}
	return string(buffer)
}

func TestMediaThumbUploadAndServe(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)
	postID := mediaPost(t, env, []string{"image/jpeg"}, false)

	images := detailImages(t, env, cookie, postID)
	if len(images) != 1 {
		t.Fatalf("图片数 = %d，期望 1", len(images))
	}
	if images[0].ThumbURL != "" {
		t.Fatalf("还没有小图时 thumb_url 应为空，实际 %q", images[0].ThumbURL)
	}
	if len(images[0].MediaVersion) != 12 {
		t.Fatalf("应返回内容版本，实际 %q", images[0].MediaVersion)
	}

	body := thumbBytes(2048)
	// 与前端一致的查询参数名，确认 v 能被正确读取。
	path := "/api/posts/" + itoa(postID) + "/media/1/thumb?v=" + images[0].MediaVersion
	up := env.do(t, http.MethodPut, path, string(body), cookie)
	if up.Code != http.StatusOK {
		t.Fatalf("写入小图失败: %d %s", up.Code, up.Body.String())
	}

	images = detailImages(t, env, cookie, postID)
	thumbURL := images[0].ThumbURL
	if thumbURL == "" {
		t.Fatal("写入后详情应返回 thumb_url")
	}

	served := env.do(t, http.MethodGet, thumbURL, "", cookie)
	if served.Code != http.StatusOK {
		t.Fatalf("读取小图失败: %d", served.Code)
	}
	if got := served.Header().Get("Content-Type"); got != "image/webp" {
		t.Fatalf("Content-Type 不符: %q", got)
	}
	if served.Body.Len() != len(body) {
		t.Fatalf("返回字节数不符: %d", served.Body.Len())
	}

	// 未登录不能读。
	anon := env.do(t, http.MethodGet, thumbURL, "", nil)
	if anon.Code != http.StatusUnauthorized {
		t.Fatalf("未登录应 401，实际 %d", anon.Code)
	}

	// 落盘路径形如 thumbs/media/<post>/<sort>-s1-<version>.webp。
	rel := filepath.Join(media.Dir, itoa(postID), "1-s1-"+images[0].MediaVersion+".webp")
	if _, err := os.Stat(filepath.Join(env.dataDir, rel)); err != nil {
		t.Fatalf("文件应落在 data/thumbs/media: %v", err)
	}
}

func TestMediaThumbRejectsStaleAndInvalid(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)
	postID := mediaPost(t, env, []string{"image/webp"}, false)
	images := detailImages(t, env, cookie, postID)
	version := images[0].MediaVersion

	stale := env.do(t, http.MethodPut, "/api/posts/"+itoa(postID)+"/media/1/thumb?version=000000000000", string(thumbBytes(256)), cookie)
	if stale.Code != http.StatusConflict {
		t.Fatalf("旧版本应 409，实际 %d", stale.Code)
	}

	bad := env.do(t, http.MethodPut, "/api/posts/"+itoa(postID)+"/media/1/thumb?version="+version, "not-a-webp", cookie)
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("非 WebP 应 400，实际 %d", bad.Code)
	}

	tooBig := env.do(t, http.MethodPut, "/api/posts/"+itoa(postID)+"/media/1/thumb?version="+version,
		string(thumbBytes(media.MaxBytes+1)), cookie)
	if tooBig.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("超限应 413，实际 %d", tooBig.Code)
	}

	badVersion := env.do(t, http.MethodPut, "/api/posts/"+itoa(postID)+"/media/1/thumb?version=xyz", string(thumbBytes(256)), cookie)
	if badVersion.Code != http.StatusBadRequest {
		t.Fatalf("版本格式不合法应 400，实际 %d", badVersion.Code)
	}
}

func TestMediaThumbInvalidatedOnReplaceAndDelete(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)
	postID := mediaPost(t, env, []string{"image/jpeg"}, false)

	images := detailImages(t, env, cookie, postID)
	version := images[0].MediaVersion
	up := env.do(t, http.MethodPut, "/api/posts/"+itoa(postID)+"/media/1/thumb?version="+version, string(thumbBytes(512)), cookie)
	if up.Code != http.StatusOK {
		t.Fatalf("写入小图失败: %d", up.Code)
	}
	oldRel := filepath.Join(env.dataDir, media.Dir, itoa(postID), "1-s1-"+version+".webp")
	if _, err := os.Stat(oldRel); err != nil {
		t.Fatalf("文件应存在: %v", err)
	}

	// 覆盖同一序号的上传会换内容版本，旧小图随之失效；先把任务放回上传中。
	if err := env.posts.UpdateStatus(context.Background(), postID, posts.StatusUploading); err != nil {
		t.Fatalf("重置任务状态失败: %v", err)
	}
	replaced, err := env.posts.AttachImage(context.Background(), posts.Image{
		PostID: postID, SortOrder: 1, Status: posts.ImageStatusUploaded, MimeType: "image/jpeg",
	})
	if err != nil {
		t.Fatalf("覆盖上传失败: %v", err)
	}
	if replaced.MediaVersion == version {
		t.Fatal("覆盖上传必须换新的内容版本")
	}
	images = detailImages(t, env, cookie, postID)
	if images[0].ThumbURL != "" {
		t.Fatalf("覆盖后 thumb_url 应为空，实际 %q", images[0].ThumbURL)
	}
	if _, err := os.Stat(oldRel); err != nil {
		t.Fatalf("旧小图文件应还在，等调用方清理: %v", err)
	}

	// 再写一次再删帖，目录应整体消失。
	up = env.do(t, http.MethodPut, "/api/posts/"+itoa(postID)+"/media/1/thumb?version="+replaced.MediaVersion, string(thumbBytes(512)), cookie)
	if up.Code != http.StatusOK {
		t.Fatalf("重新写入小图失败: %d", up.Code)
	}
	if err := env.posts.UpdateStatus(context.Background(), postID, posts.StatusCompleted); err != nil {
		t.Fatalf("结束任务失败: %v", err)
	}
	if resp := env.do(t, http.MethodDelete, "/api/posts/"+itoa(postID), "", cookie); resp.Code != http.StatusNoContent {
		t.Fatalf("删除帖子失败: %d %s", resp.Code, resp.Body.String())
	}
	if _, err := os.Stat(filepath.Join(env.dataDir, media.Dir, itoa(postID))); !os.IsNotExist(err) {
		t.Fatalf("删帖后小图目录应删除: %v", err)
	}
}

// TestMediaThumbResetRotatesVersion 验证按图集重建会清掉文件与记录并换新内容版本，
// 让在途的迟到写入拿旧版本重试时被拒。
func TestMediaThumbResetRotatesVersion(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)
	postID := mediaPost(t, env, []string{"image/jpeg"}, false)

	images := detailImages(t, env, cookie, postID)
	version := images[0].MediaVersion
	if up := env.do(t, http.MethodPut, "/api/posts/"+itoa(postID)+"/media/1/thumb?v="+version, string(thumbBytes(512)), cookie); up.Code != http.StatusOK {
		t.Fatalf("写入小图失败: %d %s", up.Code, up.Body.String())
	}
	if _, err := os.Stat(filepath.Join(env.dataDir, media.Dir, itoa(postID), "1-s1-"+version+".webp")); err != nil {
		t.Fatalf("文件应存在: %v", err)
	}

	reset := env.do(t, http.MethodPost, "/api/posts/"+itoa(postID)+"/media-thumbs/reset", "", cookie)
	if reset.Code != http.StatusOK {
		t.Fatalf("重置失败: %d %s", reset.Code, reset.Body.String())
	}
	var body struct {
		Removed    int   `json:"removed"`
		FreedBytes int64 `json:"freed_bytes"`
	}
	if err := json.Unmarshal(reset.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析结果失败: %v", err)
	}
	if body.Removed != 1 || body.FreedBytes != 512 {
		t.Fatalf("重置结果不符: %+v", body)
	}
	left, readErr := os.ReadDir(filepath.Join(env.dataDir, media.Dir, itoa(postID)))
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatalf("读取目录失败: %v", readErr)
	}
	if len(left) != 0 {
		t.Fatalf("重置后目录应为空，实际 %d 个文件", len(left))
	}

	fresh := detailImages(t, env, cookie, postID)
	if fresh[0].ThumbURL != "" {
		t.Fatalf("重置后 thumb_url 应为空，实际 %q", fresh[0].ThumbURL)
	}
	if fresh[0].MediaVersion == version {
		t.Fatal("重置后应换新内容版本")
	}
	stale := env.do(t, http.MethodPut, "/api/posts/"+itoa(postID)+"/media/1/thumb?v="+version, string(thumbBytes(512)), cookie)
	if stale.Code != http.StatusConflict {
		t.Fatalf("旧版本迟到写入应 409，实际 %d", stale.Code)
	}
}

// TestMediaThumbClearRotatesVersions 验证全局清理会换新内容版本，
// 在途回存拿旧版本重试时被拒，不会把文件重新写回来。
func TestMediaThumbClearRotatesVersions(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)
	postID := mediaPost(t, env, []string{"image/jpeg", "image/jpeg"}, false)

	images := detailImages(t, env, cookie, postID)
	version := images[0].MediaVersion
	if up := env.do(t, http.MethodPut, "/api/posts/"+itoa(postID)+"/media/1/thumb?v="+version, string(thumbBytes(512)), cookie); up.Code != http.StatusOK {
		t.Fatalf("写入小图失败: %d %s", up.Code, up.Body.String())
	}
	if cleared := env.do(t, http.MethodPost, "/api/settings/media-thumbs/clear", "", cookie); cleared.Code != http.StatusOK {
		t.Fatalf("清理失败: %d %s", cleared.Code, cleared.Body.String())
	}
	after := detailImages(t, env, cookie, postID)
	for _, image := range after {
		if image.MediaVersion == version {
			t.Fatalf("序号 %d 的版本没有换新", image.SortOrder)
		}
		if image.ThumbURL != "" {
			t.Fatalf("清理后 thumb_url 应为空，实际 %q", image.ThumbURL)
		}
	}
	late := env.do(t, http.MethodPut, "/api/posts/"+itoa(postID)+"/media/1/thumb?v="+version, string(thumbBytes(512)), cookie)
	if late.Code != http.StatusConflict {
		t.Fatalf("旧版本迟到写入应 409，实际 %d", late.Code)
	}
}

// TestMediaThumbClearInterleavesWithUpload 让清理与回存真正并发执行。
// 结束后不允许出现指向已删文件的记录，也不允许留下无引用的旧版本文件。
func TestMediaThumbClearInterleavesWithUpload(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)
	postID := mediaPost(t, env, []string{"image/jpeg", "image/jpeg"}, false)

	do := func(method, path, body, contentType string) int {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.RemoteAddr = "127.0.0.1:54321"
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		env.router.ServeHTTP(rec, req)
		return rec.Code
	}

	for round := 0; round < 30; round++ {
		images := detailImages(t, env, cookie, postID)
		version := images[0].MediaVersion
		path := "/api/posts/" + itoa(postID) + "/media/1/thumb?v=" + version
		payload := string(thumbBytes(512))
		var wait sync.WaitGroup
		wait.Add(2)
		go func() {
			defer wait.Done()
			do(http.MethodPut, path, payload, "image/webp")
		}()
		go func() {
			defer wait.Done()
			do(http.MethodPost, "/api/settings/media-thumbs/clear", "", "")
		}()
		wait.Wait()

		images = detailImages(t, env, cookie, postID)
		referenced := map[string]bool{}
		for _, image := range images {
			file := "1-s1-" + image.MediaVersion + ".webp"
			if image.ThumbURL == "" {
				continue
			}
			referenced[file] = true
			if _, err := os.Stat(filepath.Join(env.dataDir, media.Dir, itoa(postID), file)); err != nil {
				t.Fatalf("第 %d 轮：记录指向的文件不存在: %v", round, err)
			}
		}
		entries, err := os.ReadDir(filepath.Join(env.dataDir, media.Dir, itoa(postID)))
		if err != nil && !os.IsNotExist(err) {
			t.Fatalf("第 %d 轮：读取目录失败: %v", round, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !referenced[entry.Name()] {
				t.Fatalf("第 %d 轮：留下无引用文件 %s", round, entry.Name())
			}
		}
	}
}

// TestMediaThumbUploadWaitsForClear 验证清理持有提交锁时回存必须排队，
// 清理与回存的提交阶段不会交错。
func TestMediaThumbUploadWaitsForClear(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)
	postID := mediaPost(t, env, []string{"image/jpeg"}, false)
	images := detailImages(t, env, cookie, postID)
	path := "/api/posts/" + itoa(postID) + "/media/1/thumb?v=" + images[0].MediaVersion
	payload := string(thumbBytes(512))

	// 模拟「清理进行中」：先持锁，回存必须等待。
	unlock := media.LockCommit()
	done := make(chan int, 1)
	go func() {
		req := httptest.NewRequest(http.MethodPut, path, strings.NewReader(payload))
		req.RemoteAddr = "127.0.0.1:54321"
		req.Header.Set("Content-Type", "image/webp")
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		env.router.ServeHTTP(rec, req)
		done <- rec.Code
	}()
	select {
	case code := <-done:
		unlock()
		t.Fatalf("回存没有等待清理锁，提前返回 %d", code)
	case <-time.After(150 * time.Millisecond):
	}
	unlock()
	select {
	case code := <-done:
		if code != http.StatusOK {
			t.Fatalf("锁释放后回存应成功，实际 %d", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("锁释放后回存仍未完成")
	}
}
