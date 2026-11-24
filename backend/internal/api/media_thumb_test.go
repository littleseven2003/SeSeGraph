package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/littleseven2003/SeSeGraph/backend/internal/media"
	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
)

// thumbBytes 构造一段带 WebP 头的负载，供接口测试使用。
func thumbBytes(size int) []byte {
	data := make([]byte, size)
	copy(data, []byte("RIFF"))
	copy(data[8:], []byte("WEBP"))
	for index := 12; index < size; index++ {
		data[index] = byte(index % 251)
	}
	return data
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
	path := "/api/posts/" + itoa(postID) + "/media/1/thumb?version=" + images[0].MediaVersion
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
