package api

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
	"github.com/littleseven2003/SeSeGraph/backend/internal/publisher"
	"github.com/littleseven2003/SeSeGraph/backend/internal/telegraph"
)

// fakePageAPI 记录编辑请求并支持注入失败，用于在线编辑的接口测试。
type fakePageAPI struct {
	mu    sync.Mutex
	paths []string
	edits []telegraph.PageRequest
	fail  bool
}

func (f *fakePageAPI) CreatePage(context.Context, telegraph.PageRequest) (telegraph.Page, error) {
	return telegraph.Page{}, telegraph.ErrUpstream
}

func (f *fakePageAPI) EditPage(_ context.Context, path string, request telegraph.PageRequest) (telegraph.Page, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return telegraph.Page{}, telegraph.ErrUpstream
	}
	f.paths = append(f.paths, path)
	f.edits = append(f.edits, request)
	return telegraph.Page{Path: path, URL: "https://telegra.ph/" + path}, nil
}

func (f *fakePageAPI) snapshot() ([]string, []telegraph.PageRequest) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.paths...), append([]telegraph.PageRequest{}, f.edits...)
}

// newEditEnv 装配带编辑器的测试环境，返回接口页面替身。
func newEditEnv(t *testing.T) (*testEnv, *fakePageAPI) {
	t.Helper()
	pages := &fakePageAPI{}
	env := newTestEnvWith(t, testSetupToken, func(deps *Deps, _ *sql.DB) {
		editor, err := publisher.NewEditor(deps.Posts, func(context.Context) (publisher.PageAPI, error) {
			return pages, nil
		}, "https://img.example.com")
		if err != nil {
			t.Fatalf("创建编辑器失败: %v", err)
		}
		deps.Editor = editor
	})
	return env, pages
}

// publishedPost 建一个已完成、已发布且只有一页的帖子。
func publishedPost(t *testing.T, env *testEnv) int64 {
	t.Helper()
	ctx := context.Background()
	postID, err := env.posts.Create(ctx, posts.Post{Title: "旧标题", Description: "旧说明", ImageCount: 1})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	if _, err := env.posts.AttachImage(ctx, posts.Image{
		PostID: postID, SortOrder: 1, Status: posts.ImageStatusUploaded, MimeType: "image/jpeg",
	}); err != nil {
		t.Fatalf("保存图片失败: %v", err)
	}
	images, err := env.posts.ListImages(ctx, postID)
	if err != nil || len(images) == 0 {
		t.Fatalf("读取图片失败: %v", err)
	}
	if err := env.posts.MarkImageStored(ctx, images[0].ID, "remote-1", 1, "telegram"); err != nil {
		t.Fatalf("标记存图失败: %v", err)
	}
	if err := env.posts.MarkPostFinished(ctx, postID, posts.StatusCompleted, ""); err != nil {
		t.Fatalf("标记完成失败: %v", err)
	}
	if err := env.posts.RequestPublish(ctx, postID); err != nil {
		t.Fatalf("请求发布失败: %v", err)
	}
	if err := env.posts.MarkPublished(ctx, postID,
		[]string{"Page-1"}, []string{"https://telegra.ph/Page-1"}); err != nil {
		t.Fatalf("标记发布失败: %v", err)
	}
	return postID
}

func updateBody(title, description, author string) string {
	return `{"title":"` + title + `","description":"` + description + `","author_name":"` + author + `"}`
}

func TestUpdatePostRequiresAuth(t *testing.T) {
	env, _ := newEditEnv(t)
	rec := env.do(t, http.MethodPut, "/api/posts/1", updateBody("新标题", "说明", ""), nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("状态码 = %d，期望 401", rec.Code)
	}
}

func TestUpdatePostValidatesInput(t *testing.T) {
	env, _ := newEditEnv(t)
	cookie := env.setup(t)
	postID := publishedPost(t, env)

	cases := map[string]string{
		"空标题":   updateBody("   ", "说明", ""),
		"说明过长":  updateBody("标题", longRunes(5001), ""),
		"作者名过长": updateBody("标题", "说明", longRunes(101)),
	}
	for name, body := range cases {
		rec := env.do(t, http.MethodPut, "/api/posts/"+formatID(postID), body, cookie)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s 状态码 = %d，期望 400，响应: %s", name, rec.Code, rec.Body.String())
		}
	}
	invalid := env.do(t, http.MethodPut, "/api/posts/abc", updateBody("标题", "说明", ""), cookie)
	if invalid.Code != http.StatusBadRequest {
		t.Errorf("非法编号状态码 = %d，期望 400", invalid.Code)
	}
}

func TestUpdateUnpublishedPostOnlyChangesLocal(t *testing.T) {
	env, pages := newEditEnv(t)
	cookie := env.setup(t)
	postID, err := env.posts.Create(context.Background(), posts.Post{Title: "草稿", ImageCount: 1})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}

	rec := env.do(t, http.MethodPut, "/api/posts/"+formatID(postID), updateBody("草稿标题", "新说明", "SeSe"), cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200，响应: %s", rec.Code, rec.Body.String())
	}
	if paths, _ := pages.snapshot(); len(paths) != 0 {
		t.Errorf("草稿不应调用远端编辑，实际 %v", paths)
	}
	detail, err := env.posts.GalleryDetail(context.Background(), postID)
	if err != nil {
		t.Fatalf("读取详情失败: %v", err)
	}
	if detail.Title != "草稿标题" || detail.Description != "新说明" || detail.AuthorName != "SeSe" {
		t.Errorf("本地元数据 = %+v", detail)
	}
}

func TestUpdatePublishedPostRewritesPagesFirst(t *testing.T) {
	env, pages := newEditEnv(t)
	cookie := env.setup(t)
	postID := publishedPost(t, env)

	rec := env.do(t, http.MethodPut, "/api/posts/"+formatID(postID), updateBody("新标题", "新说明", "作者"), cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200，响应: %s", rec.Code, rec.Body.String())
	}
	paths, edits := pages.snapshot()
	if len(paths) != 1 || paths[0] != "Page-1" {
		t.Errorf("编辑路径 = %v，期望沿用 Page-1", paths)
	}
	if len(edits) != 1 || edits[0].Title != "新标题" || edits[0].AuthorName != "作者" {
		t.Errorf("编辑请求 = %+v", edits)
	}

	detail, err := env.posts.GalleryDetail(context.Background(), postID)
	if err != nil {
		t.Fatalf("读取详情失败: %v", err)
	}
	if detail.Title != "新标题" || detail.Description != "新说明" || detail.AuthorName != "作者" {
		t.Errorf("本地元数据 = %+v", detail)
	}
	if detail.PublishState != posts.PublishPublished || len(detail.Paths) != 1 {
		t.Errorf("发布状态或路径被改动: %+v", detail)
	}
}

func TestUpdatePublishedPostKeepsLocalOnFailure(t *testing.T) {
	env, pages := newEditEnv(t)
	cookie := env.setup(t)
	postID := publishedPost(t, env)
	pages.fail = true

	rec := env.do(t, http.MethodPut, "/api/posts/"+formatID(postID), updateBody("失败标题", "失败说明", ""), cookie)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("状态码 = %d，期望 502，响应: %s", rec.Code, rec.Body.String())
	}
	detail, err := env.posts.GalleryDetail(context.Background(), postID)
	if err != nil {
		t.Fatalf("读取详情失败: %v", err)
	}
	if detail.Title != "旧标题" || detail.Description != "旧说明" {
		t.Errorf("失败时不应改动本地记录: %+v", detail)
	}
}

func TestUpdatePublishedPostRejectsPageCountChange(t *testing.T) {
	env, pages := newEditEnv(t)
	cookie := env.setup(t)
	ctx := context.Background()

	// 用一个原本两页的帖子：缩短说明后只够一页，不能再沿用两条页面路径。
	postID, err := env.posts.Create(ctx, posts.Post{Title: "两页帖子", Description: "初始说明", ImageCount: 300})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	for order := 1; order <= 300; order++ {
		if _, err := env.posts.AttachImage(ctx, posts.Image{
			PostID: postID, SortOrder: order, Status: posts.ImageStatusUploaded, MimeType: "image/jpeg",
		}); err != nil {
			t.Fatalf("保存图片失败: %v", err)
		}
	}
	images, err := env.posts.ListImages(ctx, postID)
	if err != nil || len(images) != 300 {
		t.Fatalf("读取图片失败: %v", err)
	}
	for _, image := range images {
		if err := env.posts.MarkImageStored(ctx, image.ID, "remote-"+formatID(image.ID), 1, "telegram"); err != nil {
			t.Fatalf("标记存图失败: %v", err)
		}
	}
	if err := env.posts.MarkPostFinished(ctx, postID, posts.StatusCompleted, ""); err != nil {
		t.Fatalf("标记完成失败: %v", err)
	}
	if err := env.posts.RequestPublish(ctx, postID); err != nil {
		t.Fatalf("请求发布失败: %v", err)
	}
	if err := env.posts.MarkPublished(ctx, postID,
		[]string{"Page-1", "Page-2"}, []string{"https://telegra.ph/Page-1", "https://telegra.ph/Page-2"}); err != nil {
		t.Fatalf("标记发布失败: %v", err)
	}

	rec := env.do(t, http.MethodPut, "/api/posts/"+formatID(postID), updateBody("新标题", "短说明", ""), cookie)
	if rec.Code != http.StatusConflict {
		t.Fatalf("状态码 = %d，期望 409，响应: %s", rec.Code, rec.Body.String())
	}
	if paths, _ := pages.snapshot(); len(paths) != 0 {
		t.Errorf("分页数量变化时不应发出编辑请求，实际 %v", paths)
	}
	detail, err := env.posts.GalleryDetail(ctx, postID)
	if err != nil {
		t.Fatalf("读取详情失败: %v", err)
	}
	if detail.Title != "两页帖子" {
		t.Errorf("被拒绝的修改不应落库: %+v", detail)
	}
}

func TestUpdatePublishedPostNeedsTelegraph(t *testing.T) {
	env := newTestEnvWith(t, testSetupToken, func(deps *Deps, _ *sql.DB) {
		editor, err := publisher.NewEditor(deps.Posts, func(context.Context) (publisher.PageAPI, error) {
			return nil, telegraph.ErrNotConfigured
		}, "https://img.example.com")
		if err != nil {
			t.Fatalf("创建编辑器失败: %v", err)
		}
		deps.Editor = editor
	})
	cookie := env.setup(t)
	postID := publishedPost(t, env)

	rec := env.do(t, http.MethodPut, "/api/posts/"+formatID(postID), updateBody("新标题", "新说明", ""), cookie)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("状态码 = %d，期望 503，响应: %s", rec.Code, rec.Body.String())
	}
	detail, err := env.posts.GalleryDetail(context.Background(), postID)
	if err != nil {
		t.Fatalf("读取详情失败: %v", err)
	}
	if detail.Title != "旧标题" {
		t.Errorf("未配置时不应改动本地记录: %+v", detail)
	}
}

// TestUpdatePostRejectsBusyPublishStates 验证发布队列中的帖子不会被本地改写。
func TestUpdatePostRejectsBusyPublishStates(t *testing.T) {
	cases := []struct {
		name  string
		state string
		paths string
	}{
		{"待发布", posts.PublishPending, "[]"},
		{"发布中", posts.PublishPublishing, "[]"},
		{"失败但有页面", posts.PublishFailed, `["Page-1"]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, pages := newEditEnv(t)
			cookie := env.setup(t)
			postID := publishedPost(t, env)
			if _, err := env.handle.ExecContext(context.Background(),
				`UPDATE posts SET publish_state = ?, telegraph_paths = ? WHERE id = ?`,
				tc.state, tc.paths, postID); err != nil {
				t.Fatalf("准备状态失败: %v", err)
			}

			rec := env.do(t, http.MethodPut, "/api/posts/"+formatID(postID), updateBody("不应写入", "不应写入", ""), cookie)
			if rec.Code != http.StatusConflict {
				t.Fatalf("状态码 = %d，期望 409，响应: %s", rec.Code, rec.Body.String())
			}
			if paths, _ := pages.snapshot(); len(paths) != 0 {
				t.Errorf("状态不允许时不应调用上游，实际 %v", paths)
			}
			detail, err := env.posts.GalleryDetail(context.Background(), postID)
			if err != nil {
				t.Fatalf("读取详情失败: %v", err)
			}
			if detail.Title != "旧标题" {
				t.Errorf("状态不允许时不应改动本地记录: %+v", detail)
			}
		})
	}
}

// TestUpdateFailedPostWithoutPagesAllowsLocalEdit 验证没有页面路径的失败帖仍可编辑。
func TestUpdateFailedPostWithoutPagesAllowsLocalEdit(t *testing.T) {
	env, pages := newEditEnv(t)
	cookie := env.setup(t)
	postID := publishedPost(t, env)
	if _, err := env.handle.ExecContext(context.Background(),
		`UPDATE posts SET publish_state = ?, telegraph_paths = '[]' WHERE id = ?`,
		posts.PublishFailed, postID); err != nil {
		t.Fatalf("准备状态失败: %v", err)
	}

	rec := env.do(t, http.MethodPut, "/api/posts/"+formatID(postID), updateBody("失败后标题", "失败后说明", ""), cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200，响应: %s", rec.Code, rec.Body.String())
	}
	if paths, _ := pages.snapshot(); len(paths) != 0 {
		t.Errorf("没有页面路径时不应调用上游，实际 %v", paths)
	}
	detail, err := env.posts.GalleryDetail(context.Background(), postID)
	if err != nil {
		t.Fatalf("读取详情失败: %v", err)
	}
	if detail.Title != "失败后标题" {
		t.Errorf("本地记录未更新: %+v", detail)
	}
}

// TestUpdatePublishedPostRequiresHTTPSPublicURL 验证非 HTTPS 公开地址会在调用上游前被拒绝。
func TestUpdatePublishedPostRequiresHTTPSPublicURL(t *testing.T) {
	for _, publicURL := range []string{"http://img.example.com", "https://img.example.com/?x=1"} {
		pages := &fakePageAPI{}
		env := newTestEnvWith(t, testSetupToken, func(deps *Deps, _ *sql.DB) {
			editor, err := publisher.NewEditor(deps.Posts, func(context.Context) (publisher.PageAPI, error) {
				return pages, nil
			}, publicURL)
			if err != nil {
				t.Fatalf("创建编辑器失败: %v", err)
			}
			deps.Editor = editor
		})
		cookie := env.setup(t)
		postID := publishedPost(t, env)

		rec := env.do(t, http.MethodPut, "/api/posts/"+formatID(postID), updateBody("新标题", "新说明", ""), cookie)
		if rec.Code != http.StatusConflict {
			t.Fatalf("公开地址 %q 的状态码 = %d，期望 409，响应: %s", publicURL, rec.Code, rec.Body.String())
		}
		if paths, _ := pages.snapshot(); len(paths) != 0 {
			t.Errorf("公开地址 %q 不应调用上游，实际 %v", publicURL, paths)
		}
		detail, err := env.posts.GalleryDetail(context.Background(), postID)
		if err != nil {
			t.Fatalf("读取详情失败: %v", err)
		}
		if detail.Title != "旧标题" || detail.Description != "旧说明" {
			t.Errorf("公开地址 %q 校验失败时不应改动本地记录: %+v", publicURL, detail)
		}
	}
}

// blockingPageAPI 在编辑时阻塞，用于验证撤回与排队编辑之间的互斥顺序。
type blockingPageAPI struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingPageAPI) CreatePage(context.Context, telegraph.PageRequest) (telegraph.Page, error) {
	return telegraph.Page{}, telegraph.ErrUpstream
}

func (b *blockingPageAPI) EditPage(_ context.Context, path string, _ telegraph.PageRequest) (telegraph.Page, error) {
	b.once.Do(func() { close(b.started) })
	<-b.release
	return telegraph.Page{Path: path, URL: "https://telegra.ph/" + path}, nil
}

// TestUpdateQueuedBehindDeleteReturnsConflict 验证已进入删除流程的编辑返回 409，
// 而不是在撤回之后又把正文写回 Telegraph。
func TestUpdateQueuedBehindDeleteReturnsConflict(t *testing.T) {
	pages := &blockingPageAPI{started: make(chan struct{}), release: make(chan struct{})}
	env := newTestEnvWith(t, testSetupToken, func(deps *Deps, _ *sql.DB) {
		editor, err := publisher.NewEditor(deps.Posts, func(context.Context) (publisher.PageAPI, error) {
			return pages, nil
		}, "https://img.example.com")
		if err != nil {
			t.Fatalf("创建编辑器失败: %v", err)
		}
		deps.Editor = editor
	})
	cookie := env.setup(t)
	postID := publishedPost(t, env)
	path := "/api/posts/" + formatID(postID)

	updateDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		updateDone <- env.doWithToken(t, http.MethodPut, path, updateBody("撤回后的标题", "", ""), env.setupToken, cookie)
	}()
	select {
	case <-pages.started:
	case <-time.After(3 * time.Second):
		t.Fatal("编辑请求未进入远端调用")
	}

	deleteDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { deleteDone <- env.doWithToken(t, http.MethodDelete, path, "", env.setupToken, cookie) }()

	deadline := time.Now().Add(3 * time.Second)
	for {
		pending, err := env.posts.PendingDelete(context.Background(), postID)
		if err == nil && pending {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("删除未标记待删除: pending=%v err=%v", pending, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(pages.release)

	if rec := <-updateDone; rec.Code != http.StatusConflict {
		t.Errorf("待删除时的编辑 = %d，期望 409，响应: %s", rec.Code, rec.Body.String())
	}
	if rec := <-deleteDone; rec.Code != http.StatusNoContent {
		t.Errorf("删除状态 = %d，期望 204，响应: %s", rec.Code, rec.Body.String())
	}
}
