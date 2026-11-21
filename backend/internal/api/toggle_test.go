package api

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"testing"

	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
)

// newToggleEnv 装配带假流式提供方的环境，便于验证源站 200/404 切换。
func newToggleEnv(t *testing.T) (*testEnv, *fakeStreamProvider) {
	t.Helper()
	streams := &fakeStreamProvider{}
	env := newTestEnvWith(t, testSetupToken, func(deps *Deps, _ *sql.DB) {
		deps.Streams = streams
	})
	return env, streams
}

// toggleBody 生成显式目标状态的请求体。
func toggleBody(disabled bool) string {
	if disabled {
		return `{"disabled":true}`
	}
	return `{"disabled":false}`
}

// shortCodeOf 返回帖子的首张图片短码。
func shortCodeOf(t *testing.T, env *testEnv, postID int64) string {
	t.Helper()
	images, err := env.posts.ListImages(context.Background(), postID)
	if err != nil || len(images) == 0 {
		t.Fatalf("读取图片失败: %v", err)
	}
	return images[0].ShortCode
}

// publicFlag 读取帖子的公开标记。
func publicFlag(t *testing.T, env *testEnv, postID int64) bool {
	t.Helper()
	detail, err := env.posts.GalleryDetail(context.Background(), postID)
	if err != nil {
		t.Fatalf("读取详情失败: %v", err)
	}
	return detail.IsPublic
}

// toggleResponse 是撤回接口的响应体。
type toggleResponse struct {
	ID       int64 `json:"id"`
	Disabled bool  `json:"disabled"`
	Public   bool  `json:"public"`
}

func TestTogglePostRequiresAuth(t *testing.T) {
	env, _ := newToggleEnv(t)
	rec := env.do(t, http.MethodPatch, "/api/posts/1/toggle", toggleBody(true), nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("状态码 = %d，期望 401", rec.Code)
	}
}

func TestTogglePostWithdrawsAndRestoresShortCode(t *testing.T) {
	env, streams := newToggleEnv(t)
	cookie := env.setup(t)
	postID := publishedPost(t, env)
	code := shortCodeOf(t, env, postID)

	rec := env.do(t, http.MethodGet, "/i/"+code, "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("撤回前短码状态 = %d，期望 200", rec.Code)
	}
	callsBefore := streams.callCount()

	// 撤回：源站立即 404，且不再请求上游。
	rec = env.do(t, http.MethodPatch, "/api/posts/"+formatID(postID)+"/toggle", toggleBody(true), cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("撤回状态码 = %d，期望 200，响应: %s", rec.Code, rec.Body.String())
	}
	var body toggleResponse
	if err := decodeJSON(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if !body.Disabled || !body.Public {
		t.Errorf("撤回响应 = %+v，期望 disabled 且保留公开", body)
	}
	if publicFlag(t, env, postID) != true {
		t.Error("撤回不应改动公开标记")
	}

	for i := 0; i < 3; i++ {
		rec = env.do(t, http.MethodGet, "/i/"+code, "", nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("撤回后短码状态 = %d，期望 404", rec.Code)
		}
	}
	if got := streams.callCount(); got != callsBefore {
		t.Errorf("撤回期间上游调用从 %d 变为 %d，期望不变", callsBefore, got)
	}

	// 恢复：沿用原有公开授权，短码重新可取。
	rec = env.do(t, http.MethodPatch, "/api/posts/"+formatID(postID)+"/toggle", toggleBody(false), cookie)
	if err := decodeJSON(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if body.Disabled || !body.Public {
		t.Errorf("恢复响应 = %+v，期望未禁用且公开", body)
	}
	rec = env.do(t, http.MethodGet, "/i/"+code, "", nil)
	if rec.Code != http.StatusOK {
		t.Errorf("恢复后短码状态 = %d，期望 200", rec.Code)
	}
	if got := streams.callCount(); got != callsBefore+1 {
		t.Errorf("恢复后上游调用 = %d，期望 %d", got, callsBefore+1)
	}
}

// TestTogglePostRepeatedWithdrawKeepsBlocked 覆盖「首次撤回已写库但响应丢失」的重试：
// 客户端重发撤回后仍为禁用，短码继续 404，且不产生任何上游请求。
func TestTogglePostRepeatedWithdrawKeepsBlocked(t *testing.T) {
	env, streams := newToggleEnv(t)
	cookie := env.setup(t)
	postID := publishedPost(t, env)
	code := shortCodeOf(t, env, postID)

	if rec := env.do(t, http.MethodGet, "/i/"+code, "", nil); rec.Code != http.StatusOK {
		t.Fatalf("撤回前短码状态 = %d，期望 200", rec.Code)
	}
	callsBefore := streams.callCount()

	for attempt := 0; attempt < 2; attempt++ {
		rec := env.do(t, http.MethodPatch, "/api/posts/"+formatID(postID)+"/toggle", toggleBody(true), cookie)
		if rec.Code != http.StatusOK {
			t.Fatalf("第 %d 次撤回状态码 = %d，响应: %s", attempt+1, rec.Code, rec.Body.String())
		}
		var body toggleResponse
		if err := decodeJSON(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("响应不是合法 JSON: %v", err)
		}
		if !body.Disabled {
			t.Errorf("第 %d 次撤回后 disabled = false，期望仍禁用", attempt+1)
		}
		if !body.Public {
			t.Errorf("第 %d 次撤回后公开标记丢失", attempt+1)
		}
	}

	for i := 0; i < 3; i++ {
		if rec := env.do(t, http.MethodGet, "/i/"+code, "", nil); rec.Code != http.StatusNotFound {
			t.Errorf("重复撤回后短码状态 = %d，期望 404", rec.Code)
		}
	}
	if got := streams.callCount(); got != callsBefore {
		t.Errorf("重复撤回期间上游调用从 %d 变为 %d，期望不变", callsBefore, got)
	}
}

// TestTogglePostRequiresTargetState 确认缺少目标状态时拒绝请求，避免被当成切换或恢复。
func TestTogglePostRequiresTargetState(t *testing.T) {
	env, _ := newToggleEnv(t)
	cookie := env.setup(t)
	postID := publishedPost(t, env)
	path := "/api/posts/" + formatID(postID) + "/toggle"

	for _, body := range []string{"", "{}", `{"disabled":null}`} {
		rec := env.do(t, http.MethodPatch, path, body, cookie)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("请求体 %q 状态码 = %d，期望 400", body, rec.Code)
		}
	}
	if publicFlag(t, env, postID) != true {
		t.Error("非法请求不应改动公开标记")
	}
}

// TestTogglePostRejectsOversizedBody 确认撤回请求体走统一限量中间件，超出上限直接 413。
func TestTogglePostRejectsOversizedBody(t *testing.T) {
	env, _ := newToggleEnv(t)
	cookie := env.setup(t)
	postID := publishedPost(t, env)

	padded := `{"disabled":true,"pad":"` + strings.Repeat("a", maxToggleBodyBytes) + `"}`
	rec := env.do(t, http.MethodPatch, "/api/posts/"+formatID(postID)+"/toggle", padded, cookie)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("超限请求体状态码 = %d，期望 413，响应: %s", rec.Code, rec.Body.String())
	}

	if publicFlag(t, env, postID) != true {
		t.Error("超限请求不应改动公开标记")
	}
	listRec := env.do(t, http.MethodGet, "/api/posts", "", cookie)
	var page galleryListBody
	if err := decodeJSON(listRec.Body.Bytes(), &page); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].Disabled {
		t.Errorf("超限请求改动了撤回状态: %+v", page.Items)
	}
}

func TestTogglePostMissingAndInvalid(t *testing.T) {
	env, _ := newToggleEnv(t)
	cookie := env.setup(t)

	missing := env.do(t, http.MethodPatch, "/api/posts/999/toggle", toggleBody(true), cookie)
	if missing.Code != http.StatusNotFound {
		t.Errorf("不存在帖子状态码 = %d，期望 404", missing.Code)
	}
	invalid := env.do(t, http.MethodPatch, "/api/posts/abc/toggle", toggleBody(true), cookie)
	if invalid.Code != http.StatusBadRequest {
		t.Errorf("非法编号状态码 = %d，期望 400", invalid.Code)
	}
}

func TestTogglePostListShowsDisabled(t *testing.T) {
	env, _ := newToggleEnv(t)
	cookie := env.setup(t)
	postID := publishedPost(t, env)

	rec := env.do(t, http.MethodPatch, "/api/posts/"+formatID(postID)+"/toggle", toggleBody(true), cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("撤回失败: %d", rec.Code)
	}

	rec = env.do(t, http.MethodGet, "/api/posts", "", cookie)
	var page galleryListBody
	if err := decodeJSON(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if len(page.Items) != 1 || !page.Items[0].Disabled {
		t.Errorf("列表未反映撤回状态: %+v", page.Items)
	}
	detailRec := env.do(t, http.MethodGet, "/api/posts/"+formatID(postID), "", cookie)
	var detail galleryDetailResponse
	if err := decodeJSON(detailRec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if !detail.Disabled || !detail.Public {
		t.Errorf("详情 = disabled %v public %v，期望已撤回但仍公开", detail.Disabled, detail.Public)
	}
}

func TestTogglePostKeepsPublishState(t *testing.T) {
	env, _ := newToggleEnv(t)
	cookie := env.setup(t)
	postID := publishedPost(t, env)
	before, err := env.posts.GalleryDetail(context.Background(), postID)
	if err != nil {
		t.Fatalf("读取详情失败: %v", err)
	}

	env.do(t, http.MethodPatch, "/api/posts/"+formatID(postID)+"/toggle", toggleBody(true), cookie)
	after, err := env.posts.GalleryDetail(context.Background(), postID)
	if err != nil {
		t.Fatalf("读取详情失败: %v", err)
	}
	if after.PublishState != posts.PublishPublished || after.PublishedAt != before.PublishedAt {
		t.Errorf("撤回改动了发布状态: %+v", after)
	}
	if len(after.Paths) != len(before.Paths) || len(after.TelegraphURLs) != len(before.TelegraphURLs) {
		t.Errorf("撤回改动了页面路径或分享链接: %+v", after)
	}
}
