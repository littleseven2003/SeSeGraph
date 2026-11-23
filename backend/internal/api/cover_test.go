package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
	"github.com/littleseven2003/SeSeGraph/backend/internal/telegraph"
)

// multiImagePost 建一个含多张已存图并已发布的帖子，返回帖子编号与短码表。
func multiImagePost(t *testing.T, env *testEnv, images int, publish bool) (int64, map[int]string) {
	t.Helper()
	ctx := context.Background()
	postID, err := env.posts.Create(ctx, posts.Post{Title: "封面测试", Description: "说明", ImageCount: images})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	codes := make(map[int]string, images)
	for order := 1; order <= images; order++ {
		if _, err := env.posts.AttachImage(ctx, posts.Image{
			PostID: postID, SortOrder: order, Status: posts.ImageStatusUploaded, MimeType: "image/jpeg",
		}); err != nil {
			t.Fatalf("保存图片失败: %v", err)
		}
	}
	list, err := env.posts.ListImages(ctx, postID)
	if err != nil || len(list) != images {
		t.Fatalf("读取图片失败: %v", err)
	}
	for _, image := range list {
		if err := env.posts.MarkImageStored(ctx, image.ID, fmt.Sprintf("remote-%d", image.SortOrder), 1, "telegram"); err != nil {
			t.Fatalf("标记存图失败: %v", err)
		}
		codes[image.SortOrder] = image.ShortCode
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
	return postID, codes
}

// TestChangeCoverPublishedUpdatesRemoteFirst 验证已发布帖换封面先改写远端，再回写本地序号。
func TestChangeCoverPublishedUpdatesRemoteFirst(t *testing.T) {
	env, pages := newEditEnv(t)
	cookie := env.setup(t)
	postID, codes := multiImagePost(t, env, 3, true)

	rec := env.do(t, http.MethodPut, fmt.Sprintf("/api/posts/%d/cover", postID),
		`{"sort_order":3}`, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，响应: %s", rec.Code, rec.Body.String())
	}

	paths, edits := pages.snapshot()
	if len(paths) != 1 || len(edits) != 1 {
		t.Fatalf("远端调用 = %d/%d，期望 1/1", len(paths), len(edits))
	}
	content, err := telegraph.MarshalContent(edits[0].Content)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	text := string(content)
	if strings.Index(text, "/i/"+codes[3]) != strings.Index(text, "/i/") {
		t.Errorf("封面未排到第一张: %s", text)
	}
	if !strings.Contains(text, "第 1 张") {
		t.Errorf("编号未按展示顺序: %s", text)
	}

	detail, err := env.posts.GalleryDetail(context.Background(), postID)
	if err != nil {
		t.Fatalf("读取详情失败: %v", err)
	}
	if detail.CoverSort != 3 {
		t.Errorf("本地封面序号 = %d，期望 3", detail.CoverSort)
	}
}

// TestChangeCoverRemoteFailureKeepsLocal 验证远端失败时本地序号保持旧值，可重试收敛。
func TestChangeCoverRemoteFailureKeepsLocal(t *testing.T) {
	env, pages := newEditEnv(t)
	cookie := env.setup(t)
	postID, _ := multiImagePost(t, env, 3, true)
	pages.mu.Lock()
	pages.fail = true
	pages.mu.Unlock()

	rec := env.do(t, http.MethodPut, fmt.Sprintf("/api/posts/%d/cover", postID),
		`{"sort_order":2}`, cookie)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("状态码 = %d，期望 502，响应: %s", rec.Code, rec.Body.String())
	}
	detail, err := env.posts.GalleryDetail(context.Background(), postID)
	if err != nil {
		t.Fatalf("读取详情失败: %v", err)
	}
	if detail.CoverSort != 0 {
		t.Errorf("失败后本地封面序号 = %d，期望保持 0", detail.CoverSort)
	}

	pages.mu.Lock()
	pages.fail = false
	pages.mu.Unlock()
	rec = env.do(t, http.MethodPut, fmt.Sprintf("/api/posts/%d/cover", postID),
		`{"sort_order":2}`, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("重试状态码 = %d，响应: %s", rec.Code, rec.Body.String())
	}
	detail, err = env.posts.GalleryDetail(context.Background(), postID)
	if err != nil {
		t.Fatalf("读取详情失败: %v", err)
	}
	if detail.CoverSort != 2 {
		t.Errorf("重试后封面序号 = %d，期望 2", detail.CoverSort)
	}
}

// TestChangeCoverRejectsPublishing 验证发布写入期间拒绝换封面。
func TestChangeCoverRejectsPublishing(t *testing.T) {
	env, pages := newEditEnv(t)
	cookie := env.setup(t)
	postID, _ := multiImagePost(t, env, 2, false)
	if _, err := env.handle.ExecContext(context.Background(),
		`UPDATE posts SET publish_state = ? WHERE id = ?`, posts.PublishPublishing, postID); err != nil {
		t.Fatalf("写入发布状态失败: %v", err)
	}

	rec := env.do(t, http.MethodPut, fmt.Sprintf("/api/posts/%d/cover", postID),
		`{"sort_order":2}`, cookie)
	if rec.Code != http.StatusConflict {
		t.Fatalf("状态码 = %d，期望 409，响应: %s", rec.Code, rec.Body.String())
	}
	if paths, _ := pages.snapshot(); len(paths) != 0 {
		t.Errorf("拒绝时不应写远端，实际 %d 次", len(paths))
	}
}

// TestChangeCoverUploadingOnlyStoresLocally 验证上传阶段换封面只写本地。
func TestChangeCoverUploadingOnlyStoresLocally(t *testing.T) {
	env, pages := newEditEnv(t)
	cookie := env.setup(t)
	postID, _ := multiImagePost(t, env, 2, false)

	rec := env.do(t, http.MethodPut, fmt.Sprintf("/api/posts/%d/cover", postID),
		`{"sort_order":1}`, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，响应: %s", rec.Code, rec.Body.String())
	}
	if paths, _ := pages.snapshot(); len(paths) != 0 {
		t.Errorf("未发布帖不应写远端，实际 %d 次", len(paths))
	}
	var stored int
	if err := env.handle.QueryRowContext(context.Background(),
		`SELECT COALESCE(cover_sort_order, 0) FROM posts WHERE id = ?`, postID).Scan(&stored); err != nil {
		t.Fatalf("读取封面序号失败: %v", err)
	}
	if stored != 1 {
		t.Errorf("封面序号 = %d，期望 1", stored)
	}
}

// TestChangeCoverRejectsOutOfRange 验证越界序号被拒绝。
func TestChangeCoverRejectsOutOfRange(t *testing.T) {
	env, _ := newEditEnv(t)
	cookie := env.setup(t)
	postID, _ := multiImagePost(t, env, 2, false)

	rec := env.do(t, http.MethodPut, fmt.Sprintf("/api/posts/%d/cover", postID),
		`{"sort_order":3}`, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("状态码 = %d，期望 400，响应: %s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["error"] == "" {
		t.Errorf("响应缺少错误说明: %s", rec.Body.String())
	}
}
