package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
	"github.com/littleseven2003/SeSeGraph/backend/internal/views"
)

// viewsFetcher 是固定返回值的上游替身。
type viewsFetcher struct {
	count int
	err   error
	calls int
}

func (f *viewsFetcher) GetViews(context.Context, string) (int, error) {
	f.calls++
	if f.err != nil {
		return 0, f.err
	}
	return f.count, nil
}

// seedPublishedWithPath 建一帖并置为公开、已发布且带首篇路径。
func seedPublishedWithPath(t *testing.T, env *testEnv, title, path string) int64 {
	t.Helper()
	id, err := env.posts.Create(context.Background(), posts.Post{Title: title, ImageCount: 1})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	if err := env.posts.SetPublic(context.Background(), id, true); err != nil {
		t.Fatalf("设置公开失败: %v", err)
	}
	if _, err := env.handle.ExecContext(context.Background(),
		`UPDATE posts SET publish_state = 'published', telegraph_paths = ? WHERE id = ?`,
		`["`+path+`"]`, id); err != nil {
		t.Fatalf("写入发布状态失败: %v", err)
	}
	return id
}

// newViewsService 用给定上游装配浏览量服务。
func newViewsService(t *testing.T, store views.Store, fetcher views.Fetcher) *views.Service {
	t.Helper()
	service, err := views.New(views.Options{
		Store:  store,
		Client: func(context.Context) (views.Fetcher, error) { return fetcher, nil },
		Logger: log.New(io.Discard, "", 0),
		Now:    func() time.Time { return time.Date(2026, 11, 22, 8, 0, 0, 0, time.UTC) },
		Sleep:  func(context.Context, time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatalf("创建浏览量服务失败: %v", err)
	}
	return service
}

// newViewsEnv 装配带浏览量服务的测试环境。
func newViewsEnv(t *testing.T, fetcher *viewsFetcher) *testEnv {
	t.Helper()
	return newTestEnvWith(t, testSetupToken, func(deps *Deps, _ *sql.DB) {
		deps.Views = newViewsService(t, deps.Posts, fetcher)
	})
}

type viewsRefreshBody struct {
	PostID         int64   `json:"post_id"`
	ViewsCount     int     `json:"views_count"`
	ViewsUpdatedAt *string `json:"views_updated_at"`
	Cached         bool    `json:"cached"`
}

func TestRefreshViewsRequiresAuth(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	if rec := env.do(t, http.MethodPost, "/api/posts/1/views/refresh", "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("状态码 = %d，期望 401", rec.Code)
	}
}

func TestRefreshViewsUnavailableWithoutService(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)
	if rec := env.do(t, http.MethodPost, "/api/posts/1/views/refresh", "", cookie); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("状态码 = %d，期望 503", rec.Code)
	}
}

func TestRefreshViewsWritesThenServesCache(t *testing.T) {
	fetcher := &viewsFetcher{count: 1234}
	env := newViewsEnv(t, fetcher)
	cookie := env.setup(t)
	id := seedPublishedWithPath(t, env, "已发布图集", "Page-11-22")
	target := "/api/posts/" + strconv.FormatInt(id, 10) + "/views/refresh"

	rec := env.do(t, http.MethodPost, target, "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200，响应: %s", rec.Code, rec.Body.String())
	}
	var body viewsRefreshBody
	if err := decodeJSON(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if body.ViewsCount != 1234 || body.Cached {
		t.Fatalf("首次刷新结果 = %+v", body)
	}
	if body.ViewsUpdatedAt == nil || *body.ViewsUpdatedAt != "2026-11-22T08:00:00Z" {
		t.Fatalf("同步时间 = %v", body.ViewsUpdatedAt)
	}

	// 冷却期内再次刷新直接返回缓存，不再打上游。
	rec = env.do(t, http.MethodPost, target, "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("冷却期状态码 = %d", rec.Code)
	}
	if err := decodeJSON(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if !body.Cached || body.ViewsCount != 1234 {
		t.Fatalf("冷却期结果 = %+v", body)
	}
	if fetcher.calls != 1 {
		t.Fatalf("上游调用 %d 次，期望 1 次", fetcher.calls)
	}

	// 详情要透出浏览量。
	detail := env.do(t, http.MethodGet, "/api/posts/"+strconv.FormatInt(id, 10), "", cookie)
	var detailBody galleryDetailResponse
	if err := decodeJSON(detail.Body.Bytes(), &detailBody); err != nil {
		t.Fatalf("详情响应不是合法 JSON: %v", err)
	}
	if detailBody.ViewsCount != 1234 || detailBody.ViewsUpdatedAt == nil {
		t.Fatalf("详情浏览量 = %d / %v", detailBody.ViewsCount, detailBody.ViewsUpdatedAt)
	}

	// 列表同样透出浏览量。
	list := env.do(t, http.MethodGet, "/api/posts", "", cookie)
	var listBody galleryListBody
	if err := decodeJSON(list.Body.Bytes(), &listBody); err != nil {
		t.Fatalf("列表响应不是合法 JSON: %v", err)
	}
	if len(listBody.Items) != 1 || listBody.Items[0].ViewsCount != 1234 {
		t.Fatalf("列表浏览量 = %+v", listBody.Items)
	}
}

func TestRefreshViewsNotFoundAndNotApplicable(t *testing.T) {
	env := newViewsEnv(t, &viewsFetcher{count: 1})
	cookie := env.setup(t)

	if rec := env.do(t, http.MethodPost, "/api/posts/9999/views/refresh", "", cookie); rec.Code != http.StatusNotFound {
		t.Fatalf("不存在帖子状态码 = %d，期望 404", rec.Code)
	}

	idle := env.do(t, http.MethodPost, "/api/posts", `{"title":"未发布","image_count":1}`, cookie)
	if idle.Code != http.StatusOK && idle.Code != http.StatusCreated {
		t.Fatalf("建帖失败: %d %s", idle.Code, idle.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := decodeJSON(idle.Body.Bytes(), &created); err != nil {
		t.Fatalf("建帖响应不是合法 JSON: %v", err)
	}
	target := "/api/posts/" + strconv.FormatInt(created.ID, 10) + "/views/refresh"
	if rec := env.do(t, http.MethodPost, target, "", cookie); rec.Code != http.StatusConflict {
		t.Fatalf("未发布帖状态码 = %d，期望 409", rec.Code)
	}
}

func TestRefreshViewsUpstreamFailureKeepsCache(t *testing.T) {
	env := newViewsEnv(t, &viewsFetcher{count: 42})
	cookie := env.setup(t)
	id := seedPublishedWithPath(t, env, "已发布图集", "Page-11-23")
	target := "/api/posts/" + strconv.FormatInt(id, 10) + "/views/refresh"

	if rec := env.do(t, http.MethodPost, target, "", cookie); rec.Code != http.StatusOK {
		t.Fatalf("首次刷新失败: %d %s", rec.Code, rec.Body.String())
	}

	// 新实例（冷却为空）带失败上游：错误只回脱敏文案，缓存保持上一次的值。
	failing := newViewsService(t, env.posts, &viewsFetcher{err: errors.New("上游失败")})
	if _, err := failing.Refresh(context.Background(), id); !errors.Is(err, views.ErrFetch) {
		t.Fatalf("错误 = %v，期望 ErrFetch", err)
	}
	snapshot, err := env.posts.ReadViews(context.Background(), id)
	if err != nil {
		t.Fatalf("读取缓存失败: %v", err)
	}
	if snapshot.Count != 42 {
		t.Fatalf("失败后缓存 = %+v，期望保留 42", snapshot)
	}

	// 接口层同样给 502 与脱敏文案。
	env2 := newTestEnvWith(t, testSetupToken, func(deps *Deps, _ *sql.DB) {
		deps.Views = newViewsService(t, deps.Posts, &viewsFetcher{err: errors.New("上游失败")})
	})
	cookie2 := env2.setup(t)
	id2 := seedPublishedWithPath(t, env2, "已发布图集", "Page-11-24")
	rec := env2.do(t, http.MethodPost, "/api/posts/"+strconv.FormatInt(id2, 10)+"/views/refresh", "", cookie2)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("上游失败状态码 = %d，期望 502", rec.Code)
	}
	var failure map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &failure); err != nil {
		t.Fatalf("失败响应不是合法 JSON: %v", err)
	}
	if failure["error"] == "" || failure["error"] == "上游失败" {
		t.Fatalf("失败文案 = %q，期望脱敏文案", failure["error"])
	}
}
