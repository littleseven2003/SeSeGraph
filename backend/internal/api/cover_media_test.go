package api

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
	"github.com/littleseven2003/SeSeGraph/backend/internal/storage"
)

// mediaPost 建一个含指定 MIME 媒体、已完成并可选发布的帖子。
func mediaPost(t *testing.T, env *testEnv, mimes []string, publish bool) int64 {
	t.Helper()
	ctx := context.Background()
	postID, err := env.posts.Create(ctx, posts.Post{Title: "媒体封面", ImageCount: len(mimes)})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	for index, mime := range mimes {
		if _, err := env.posts.AttachImage(ctx, posts.Image{
			PostID: postID, SortOrder: index + 1, Status: posts.ImageStatusUploaded, MimeType: mime,
		}); err != nil {
			t.Fatalf("保存媒体失败: %v", err)
		}
	}
	list, err := env.posts.ListImages(ctx, postID)
	if err != nil {
		t.Fatalf("读取媒体失败: %v", err)
	}
	for _, image := range list {
		if err := env.posts.MarkImageStored(ctx, image.ID, fmt.Sprintf("remote-%d", image.SortOrder), 1, "telegram"); err != nil {
			t.Fatalf("标记存图失败: %v", err)
		}
	}
	if err := env.posts.MarkPostFinished(ctx, postID, posts.StatusCompleted, ""); err != nil {
		t.Fatalf("标记完成失败: %v", err)
	}
	if publish {
		if err := env.posts.RequestPublish(ctx, postID); err != nil {
			t.Fatalf("请求发布失败: %v", err)
		}
		if err := env.posts.MarkPublished(ctx, postID,
			[]string{"Page-1"}, []string{"https://telegra.ph/Page-1"}); err != nil {
			t.Fatalf("标记发布失败: %v", err)
		}
	}
	return postID
}

func coverPath(t *testing.T, env *testEnv, postID int64) string {
	t.Helper()
	var path string
	if err := env.handle.QueryRowContext(context.Background(),
		`SELECT cover_thumb_path FROM posts WHERE id = ?`, postID).Scan(&path); err != nil {
		t.Fatalf("读取封面路径失败: %v", err)
	}
	return path
}

// TestChangeCoverUnlistedAndDisabled 验证未公开与已撤回帖子也能换封面并生成新缩略图。
func TestChangeCoverUnlistedAndDisabled(t *testing.T) {
	env, _ := newEditEnv(t)
	cookie := env.setup(t)
	postID := mediaPost(t, env, []string{"image/jpeg", "image/jpeg"}, false)

	rec := env.do(t, http.MethodPut, fmt.Sprintf("/api/posts/%d/cover", postID),
		`{"sort_order":2,"cover_thumbnail":"data:image/webp;base64,bmV3LWNvdmVy"}`, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("未公开帖换封面状态码 = %d，响应: %s", rec.Code, rec.Body.String())
	}
	path := coverPath(t, env, postID)
	if path == "" {
		t.Fatal("未生成封面缩略图")
	}
	if _, err := os.Stat(filepath.Join(env.dataDir, filepath.FromSlash(path))); err != nil {
		t.Fatalf("封面文件不可读: %v", err)
	}

	if _, err := env.handle.ExecContext(context.Background(),
		`UPDATE posts SET is_disabled = 1 WHERE id = ?`, postID); err != nil {
		t.Fatalf("禁用失败: %v", err)
	}
	rec = env.do(t, http.MethodPut, fmt.Sprintf("/api/posts/%d/cover", postID),
		`{"sort_order":1,"cover_thumbnail":"data:image/webp;base64,YW5vdGhlcg=="}`, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("已撤回帖换封面状态码 = %d，响应: %s", rec.Code, rec.Body.String())
	}
	if next := coverPath(t, env, postID); next == path || next == "" {
		t.Errorf("封面路径未更新: %q -> %q", path, next)
	}
}

// TestChangeCoverVideoMovesVideoFirst 验证视频封面在远端仍是 video 节点并排第一。
func TestChangeCoverVideoMovesVideoFirst(t *testing.T) {
	env, pages := newEditEnv(t)
	cookie := env.setup(t)
	postID := mediaPost(t, env, []string{"image/jpeg", "video/mp4"}, true)

	rec := env.do(t, http.MethodPut, fmt.Sprintf("/api/posts/%d/cover", postID),
		`{"sort_order":2,"cover_thumbnail":"data:image/webp;base64,dmlkZW8="}`, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("视频封面状态码 = %d，响应: %s", rec.Code, rec.Body.String())
	}
	_, edits := pages.snapshot()
	if len(edits) != 1 {
		t.Fatalf("远端编辑 %d 次，期望 1 次", len(edits))
	}
	first := edits[0].Content[0]
	if first.Tag != "figure" || len(first.Children) == 0 {
		t.Fatalf("首个节点 = %+v，期望 figure", first)
	}
	content := fmt.Sprint(first.Children[0])
	if !contains(content, "video") {
		t.Errorf("首个媒体不是视频节点: %s", content)
	}
}

func contains(value, want string) bool {
	for index := 0; index+len(want) <= len(value); index++ {
		if value[index:index+len(want)] == want {
			return true
		}
	}
	return false
}

// TestChangeCoverRemoteFailureKeepsOldThumbnail 验证远端失败时旧缩略图仍可读，新文件被清理。
func TestChangeCoverRemoteFailureKeepsOldThumbnail(t *testing.T) {
	env, pages := newEditEnv(t)
	cookie := env.setup(t)
	postID := mediaPost(t, env, []string{"image/jpeg", "image/jpeg"}, true)

	rec := env.do(t, http.MethodPut, fmt.Sprintf("/api/posts/%d/cover", postID),
		`{"sort_order":2,"cover_thumbnail":"data:image/webp;base64,b2xk"}`, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("首次换封面失败: %s", rec.Body.String())
	}
	oldPath := coverPath(t, env, postID)

	pages.mu.Lock()
	pages.fail = true
	pages.mu.Unlock()
	rec = env.do(t, http.MethodPut, fmt.Sprintf("/api/posts/%d/cover", postID),
		`{"sort_order":1,"cover_thumbnail":"data:image/webp;base64,bmV3"}`, cookie)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("远端失败状态码 = %d，期望 502，响应: %s", rec.Code, rec.Body.String())
	}
	if now := coverPath(t, env, postID); now != oldPath {
		t.Errorf("失败后封面路径 = %q，期望保持 %q", now, oldPath)
	}
	if _, err := os.Stat(filepath.Join(env.dataDir, filepath.FromSlash(oldPath))); err != nil {
		t.Errorf("旧封面不可读: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(env.dataDir, posts.CoverDir))
	if err != nil {
		t.Fatalf("读取封面目录失败: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("封面目录文件数 = %d，期望只剩旧封面", len(entries))
	}
}

// TestChangeCoverInterleaved 验证并发换封面串行执行，最终本地与远端一致。
func TestChangeCoverInterleaved(t *testing.T) {
	env, pages := newEditEnv(t)
	cookie := env.setup(t)
	postID := mediaPost(t, env, []string{"image/jpeg", "image/jpeg", "image/jpeg"}, true)

	var wait sync.WaitGroup
	codes := make(chan int, 2)
	for _, order := range []int{1, 3} {
		wait.Add(1)
		go func(order int) {
			defer wait.Done()
			rec := env.do(t, http.MethodPut, fmt.Sprintf("/api/posts/%d/cover", postID),
				fmt.Sprintf(`{"sort_order":%d,"cover_thumbnail":"data:image/webp;base64,dA=="}`, order), cookie)
			codes <- rec.Code
		}(order)
	}
	wait.Wait()
	close(codes)
	for code := range codes {
		if code != http.StatusOK {
			t.Fatalf("并发换封面状态码 = %d，期望 200", code)
		}
	}
	if _, edits := pages.snapshot(); len(edits) != 2 {
		t.Errorf("远端编辑 %d 次，期望每请求一次", len(edits))
	}
	var stored int
	if err := env.handle.QueryRowContext(context.Background(),
		`SELECT COALESCE(cover_sort_order, 0) FROM posts WHERE id = ?`, postID).Scan(&stored); err != nil {
		t.Fatalf("读取封面序号失败: %v", err)
	}
	if stored != 1 && stored != 3 {
		t.Errorf("最终封面序号 = %d，期望 1 或 3", stored)
	}
}

// TestAdminMediaStreamsStoredFile 验证管理员媒体接口受登录保护并流式返回原媒体。
func TestAdminMediaStreamsStoredFile(t *testing.T) {
	provider := &fakeStreamProvider{}
	provider.open = func(context.Context, string) (storage.Stream, error) {
		return storage.Stream{
			Body:        io.NopCloser(strings.NewReader("media-bytes")),
			ContentType: "image/jpeg",
			Size:        11,
		}, nil
	}
	env := newTestEnvWith(t, testSetupToken, func(deps *Deps, _ *sql.DB) {
		deps.Streams = provider
	})
	cookie := env.setup(t)
	postID := mediaPost(t, env, []string{"image/jpeg"}, false)

	rec := env.do(t, http.MethodGet, fmt.Sprintf("/api/posts/%d/media/1", postID), "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("未登录状态码 = %d，期望 401", rec.Code)
	}

	rec = env.do(t, http.MethodGet, fmt.Sprintf("/api/posts/%d/media/1", postID), "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("读取媒体状态码 = %d，响应: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "media-bytes" {
		t.Errorf("媒体正文 = %q", rec.Body.String())
	}
}
