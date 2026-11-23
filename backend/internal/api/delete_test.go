package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"testing"

	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
	"github.com/littleseven2003/SeSeGraph/backend/internal/publisher"
	"github.com/littleseven2003/SeSeGraph/backend/internal/telegraph"
)

// stubPageAPI 记录远端编辑调用，并可模拟撤回失败。
type stubPageAPI struct {
	mu    sync.Mutex
	paths []string
	fail  bool
}

func (s *stubPageAPI) CreatePage(context.Context, telegraph.PageRequest) (telegraph.Page, error) {
	return telegraph.Page{}, nil
}

func (s *stubPageAPI) EditPage(_ context.Context, path string, _ telegraph.PageRequest) (telegraph.Page, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.paths = append(s.paths, path)
	if s.fail {
		return telegraph.Page{}, errors.New("上游失败")
	}
	return telegraph.Page{Path: path, URL: "https://telegra.ph/" + path}, nil
}

func (s *stubPageAPI) calls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.paths...)
}

func (s *stubPageAPI) setFail(fail bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fail = fail
}

// envWithPages 给测试环境装上可观测的远端页面客户端。
func envWithPages(t *testing.T, pages *stubPageAPI) *testEnv {
	t.Helper()
	return newTestEnvWith(t, testSetupToken, func(deps *Deps, _ *sql.DB) {
		deps.Streams = &fakeStreamProvider{}
		editor, err := publisher.NewEditor(deps.Posts,
			func(context.Context) (publisher.PageAPI, error) { return pages, nil },
			"https://gallery.example.com")
		if err != nil {
			t.Fatalf("创建编辑器失败: %v", err)
		}
		deps.Editor = editor
	})
}

// TestDeletePostRemovesShortCode 验证删除后详情与公开短码立即失效。
func TestDeletePostRemovesShortCode(t *testing.T) {
	pages := &stubPageAPI{}
	env := envWithPages(t, pages)
	cookie := env.setup(t)
	postID := publishedPost(t, env)
	code := shortCodeOf(t, env, postID)

	if rec := env.do(t, http.MethodGet, "/i/"+code, "", cookie); rec.Code != http.StatusOK {
		t.Fatalf("删除前短码状态 = %d，期望 200", rec.Code)
	}

	if rec := env.do(t, http.MethodDelete, "/api/posts/"+strconv.FormatInt(postID, 10), "", cookie); rec.Code != http.StatusNoContent {
		t.Fatalf("删除状态 = %d，期望 204，响应: %s", rec.Code, rec.Body.String())
	}
	if rec := env.do(t, http.MethodGet, "/i/"+code, "", cookie); rec.Code != http.StatusNotFound {
		t.Errorf("删除后短码状态 = %d，期望 404", rec.Code)
	}
	if rec := env.do(t, http.MethodGet, "/api/posts/"+strconv.FormatInt(postID, 10), "", cookie); rec.Code != http.StatusNotFound {
		t.Errorf("删除后详情状态 = %d，期望 404", rec.Code)
	}
	if rec := env.do(t, http.MethodDelete, "/api/posts/"+strconv.FormatInt(postID, 10), "", cookie); rec.Code != http.StatusNotFound {
		t.Errorf("重复删除状态 = %d，期望 404", rec.Code)
	}
}

// TestDeletePostRejectsRunningTask 验证上传中的任务不被删除，避免与队列交错。
func TestDeletePostRejectsRunningTask(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)

	for _, status := range []string{posts.StatusUploading, posts.StatusQueued, posts.StatusPublishing} {
		postID, err := env.posts.Create(context.Background(), posts.Post{
			Title: "进行中的任务", ImageCount: 1, UploadMode: "photo", Status: status,
		})
		if err != nil {
			t.Fatalf("创建帖子失败: %v", err)
		}
		rec := env.do(t, http.MethodDelete, "/api/posts/"+strconv.FormatInt(postID, 10), "", cookie)
		if rec.Code != http.StatusConflict {
			t.Errorf("%s 状态删除 = %d，期望 409", status, rec.Code)
		}
		if _, err := env.posts.Get(context.Background(), postID); err != nil {
			t.Errorf("%s 状态删除后记录应保留: %v", status, err)
		}
	}

	// 存图完成但 Telegraph 待发布或发布中同样拒绝，避免发布队列拿着已删记录。
	for _, state := range []string{posts.PublishPending, posts.PublishPublishing} {
		postID, err := env.posts.Create(context.Background(), posts.Post{
			Title: "待发布任务", ImageCount: 1, UploadMode: "photo", Status: posts.StatusCompleted,
		})
		if err != nil {
			t.Fatalf("创建帖子失败: %v", err)
		}
		if _, err := env.handle.Exec(`UPDATE posts SET publish_state = ? WHERE id = ?`, state, postID); err != nil {
			t.Fatalf("更新发布状态失败: %v", err)
		}
		rec := env.do(t, http.MethodDelete, "/api/posts/"+strconv.FormatInt(postID, 10), "", cookie)
		if rec.Code != http.StatusConflict {
			t.Errorf("发布状态 %s 删除 = %d，期望 409", state, rec.Code)
		}
		if _, err := env.posts.Get(context.Background(), postID); err != nil {
			t.Errorf("发布状态 %s 删除后记录应保留: %v", state, err)
		}
	}

	// 任务失败后允许删除。
	postID, err := env.posts.Create(context.Background(), posts.Post{
		Title: "失败任务", ImageCount: 1, UploadMode: "photo", Status: posts.StatusFailed,
	})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	if rec := env.do(t, http.MethodDelete, "/api/posts/"+strconv.FormatInt(postID, 10), "", cookie); rec.Code != http.StatusNoContent {
		t.Errorf("失败任务删除状态 = %d，期望 204", rec.Code)
	}
}

func TestDeletePostWithdrawsRemotePages(t *testing.T) {
	pages := &stubPageAPI{}
	env := envWithPages(t, pages)
	cookie := env.setup(t)
	postID := publishedPost(t, env)
	code := shortCodeOf(t, env, postID)

	if rec := env.do(t, http.MethodDelete, "/api/posts/"+strconv.FormatInt(postID, 10), "", cookie); rec.Code != http.StatusNoContent {
		t.Fatalf("删除状态 = %d，期望 204，响应: %s", rec.Code, rec.Body.String())
	}
	calls := pages.calls()
	if len(calls) != 1 || calls[0] != "Page-1" {
		t.Errorf("远端撤回调用 = %v，期望 Page-1", calls)
	}
	if rec := env.do(t, http.MethodGet, "/i/"+code, "", cookie); rec.Code != http.StatusNotFound {
		t.Errorf("删除后短码状态 = %d，期望 404", rec.Code)
	}
}

func TestDeletePostKeepsRecordWhenWithdrawFails(t *testing.T) {
	pages := &stubPageAPI{}
	pages.setFail(true)
	env := envWithPages(t, pages)
	cookie := env.setup(t)
	postID := publishedPost(t, env)
	code := shortCodeOf(t, env, postID)

	rec := env.do(t, http.MethodDelete, "/api/posts/"+strconv.FormatInt(postID, 10), "", cookie)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("撤回失败状态 = %d，期望 502，响应: %s", rec.Code, rec.Body.String())
	}
	if _, err := env.posts.Get(context.Background(), postID); err != nil {
		t.Fatalf("撤回失败后记录应保留: %v", err)
	}
	pending, err := env.posts.PendingDelete(context.Background(), postID)
	if err != nil || !pending {
		t.Fatalf("撤回失败后应保留待删除标记: pending=%v err=%v", pending, err)
	}
	if rec := env.do(t, http.MethodGet, "/i/"+code, "", cookie); rec.Code != http.StatusNotFound {
		t.Errorf("待删除期间短码状态 = %d，期望 404", rec.Code)
	}

	pages.setFail(false)
	if rec := env.do(t, http.MethodDelete, "/api/posts/"+strconv.FormatInt(postID, 10), "", cookie); rec.Code != http.StatusNoContent {
		t.Fatalf("重试删除状态 = %d，期望 204，响应: %s", rec.Code, rec.Body.String())
	}
	if calls := pages.calls(); len(calls) != 2 {
		t.Errorf("重试应再次撤回远端页面，调用 = %v", calls)
	}
	if _, err := env.posts.Get(context.Background(), postID); !errors.Is(err, posts.ErrNotFound) {
		t.Errorf("重试成功后记录应删除: %v", err)
	}
}

func TestPendingDeleteBlocksRestoreEditAndRepublish(t *testing.T) {
	pages := &stubPageAPI{}
	pages.setFail(true)
	env := envWithPages(t, pages)
	cookie := env.setup(t)
	postID := publishedPost(t, env)
	path := "/api/posts/" + strconv.FormatInt(postID, 10)

	if rec := env.do(t, http.MethodDelete, path, "", cookie); rec.Code != http.StatusBadGateway {
		t.Fatalf("准备待删除状态失败: %d", rec.Code)
	}

	if rec := env.do(t, http.MethodPatch, path+"/toggle", `{"disabled":false}`, cookie); rec.Code != http.StatusConflict {
		t.Errorf("待删除时恢复访问 = %d，期望 409", rec.Code)
	}
	if rec := env.do(t, http.MethodPut, path, `{"title":"新标题","description":"","author_name":""}`, cookie); rec.Code != http.StatusConflict {
		t.Errorf("待删除时编辑 = %d，期望 409", rec.Code)
	}
	if rec := env.do(t, http.MethodPost, path+"/publish", "", cookie); rec.Code != http.StatusConflict {
		t.Errorf("待删除时重新发布 = %d，期望 409", rec.Code)
	}
	if rec := env.do(t, http.MethodPost, path+"/retry", "", cookie); rec.Code != http.StatusConflict {
		t.Errorf("待删除时重试 = %d，期望 409", rec.Code)
	}
}

func TestDeletePostRequiresLogin(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	postID := publishedPost(t, env)
	if rec := env.do(t, http.MethodDelete, "/api/posts/"+strconv.FormatInt(postID, 10), "", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("未登录删除 = %d，期望 401", rec.Code)
	}
}
