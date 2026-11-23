package api

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
)

// doWithHeader 发一个带自定义请求头的匿名请求，用于验证缓存协商。
func (e *testEnv) doWithHeader(t *testing.T, method, path, body, key, value string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:54321"
	req.Header.Set(key, value)
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

// newRSSEnv 构造启用订阅源的测试环境，公开地址为 HTTPS。
func newRSSEnv(t *testing.T) *testEnv {
	t.Helper()
	return newTestEnvWith(t, testSetupToken, func(deps *Deps, _ *sql.DB) {
		deps.PublicURL = "https://gallery.example.com"
		deps.RSSEnabled = true
	})
}

// seedFeedPost 建立一篇已发布图集并写入可见性与 Telegraph 链接。
func seedFeedPost(t *testing.T, env *testEnv, title string, public, disabled bool, publishedAt, link string) int64 {
	t.Helper()
	id, err := env.posts.Create(context.Background(), posts.Post{Title: title, Description: title + " 的说明", Tags: []string{"写真"}})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	publicFlag, disabledFlag := 0, 0
	if public {
		publicFlag = 1
	}
	if disabled {
		disabledFlag = 1
	}
	if _, err := env.handle.Exec(
		`UPDATE posts SET publish_state = 'published', is_public = ?, is_disabled = ?, telegraph_urls = ?, published_at = ? WHERE id = ?`,
		publicFlag, disabledFlag, `["`+link+`"]`, publishedAt, id); err != nil {
		t.Fatalf("写入发布状态失败: %v", err)
	}
	return id
}

func TestRSSDisabledByDefault(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	if rec := env.do(t, http.MethodGet, "/rss.xml", "", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("默认应关闭订阅源，状态码 = %d", rec.Code)
	}
}

func TestRSSRequiresHTTPSPublicURL(t *testing.T) {
	env := newTestEnvWith(t, testSetupToken, func(deps *Deps, _ *sql.DB) {
		deps.PublicURL = "http://gallery.example.com"
		deps.RSSEnabled = true
	})
	if rec := env.do(t, http.MethodGet, "/rss.xml", "", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("非 HTTPS 地址不应提供订阅源，状态码 = %d", rec.Code)
	}
}

func TestRSSServesOnlyPublicItemsAndSupportsCache(t *testing.T) {
	env := newRSSEnv(t)
	seedFeedPost(t, env, "公开图集", true, false, "2026-11-22 10:00:00", "https://telegra.ph/Public-11-23")
	seedFeedPost(t, env, "已撤回图集", true, true, "2026-11-21 10:00:00", "https://telegra.ph/Withdrawn-11-23")
	seedFeedPost(t, env, "未公开图集", false, false, "2026-11-20 10:00:00", "https://telegra.ph/Hidden-11-23")

	rec := env.do(t, http.MethodGet, "/rss.xml", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/rss+xml") {
		t.Errorf("Content-Type = %q", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{"公开图集", "https://telegra.ph/Public-11-23", "<category>写真</category>", "<pubDate>"} {
		if !strings.Contains(body, want) {
			t.Errorf("订阅内容缺少 %q", want)
		}
	}
	for _, banned := range []string{"已撤回图集", "未公开图集", "Withdrawn-11-23"} {
		if strings.Contains(body, banned) {
			t.Errorf("订阅不应包含 %q", banned)
		}
	}

	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("应返回 ETag")
	}
	if cache := rec.Header().Get("Cache-Control"); cache != "public, no-cache" {
		t.Errorf("Cache-Control = %q", cache)
	}

	notModified := env.doWithHeader(t, http.MethodGet, "/rss.xml", "", "If-None-Match", etag)
	if notModified.Code != http.StatusNotModified {
		t.Errorf("命中 ETag 时状态码 = %d，期望 304", notModified.Code)
	}

	head := env.do(t, http.MethodHead, "/rss.xml", "", nil)
	if head.Code != http.StatusOK {
		t.Errorf("HEAD 状态码 = %d，期望 200", head.Code)
	}
	if head.Body.Len() != 0 {
		t.Errorf("HEAD 不应返回正文，实际 %d 字节", head.Body.Len())
	}
}

func TestRSSDropsWithdrawnItem(t *testing.T) {
	env := newRSSEnv(t)
	id := seedFeedPost(t, env, "稍后撤回", true, false, "2026-11-22 10:00:00", "https://telegra.ph/Soon-11-23")

	first := env.do(t, http.MethodGet, "/rss.xml", "", nil)
	if !strings.Contains(first.Body.String(), "稍后撤回") {
		t.Fatal("撤回前应出现在订阅中")
	}

	if _, err := env.handle.Exec(`UPDATE posts SET is_disabled = 1 WHERE id = ?`, id); err != nil {
		t.Fatalf("撤回失败: %v", err)
	}
	second := env.do(t, http.MethodGet, "/rss.xml", "", nil)
	if strings.Contains(second.Body.String(), "稍后撤回") {
		t.Error("撤回后不应再返回该条目")
	}
}

// seedFeedPostFull 建立标题、说明与标签都可自定义的已发布图集。
func seedFeedPostFull(t *testing.T, env *testEnv, title, description string, tags []string, link string) int64 {
	t.Helper()
	id, err := env.posts.Create(context.Background(), posts.Post{Title: title, Description: description, Tags: tags})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	if _, err := env.handle.Exec(
		`UPDATE posts SET publish_state = 'published', is_public = 1, is_disabled = 0, telegraph_urls = ?, published_at = ? WHERE id = ?`,
		`["`+link+`"]`, "2026-11-22 10:00:00", id); err != nil {
		t.Fatalf("写入发布状态失败: %v", err)
	}
	return id
}

func TestRSSSanitizesTitleDescriptionAndTags(t *testing.T) {
	env := newRSSEnv(t)
	const (
		botURL = "https://API.Telegram.ORG/file/bot987654:ZYXwvuTSRqponMLKjihgFEDcba/photo.jpg"
		token  = "123456:ABCdefGHIjklMNOpqrSTUvwxYZ0123456"
	)
	seedFeedPostFull(t, env, "公开图集 "+botURL, "说明 <b>粗体</b> "+botURL+" 结束 "+token,
		[]string{"写真", botURL, token}, "https://telegra.ph/Clean-11-23")

	body := strings.ToLower(env.do(t, http.MethodGet, "/rss.xml", "", nil).Body.String())
	if strings.Contains(body, "api.telegram.org") || strings.Contains(body, "bot987654") {
		t.Error("订阅不应输出 Telegram 文件直链")
	}
	if strings.Contains(body, strings.ToLower(token)) {
		t.Error("订阅不应输出令牌片段")
	}
	if strings.Contains(body, "<b>") {
		t.Error("订阅不应保留 HTML 标签")
	}
	if !strings.Contains(body, "<category>写真</category>") {
		t.Error("正常标签应保留")
	}
}

func TestRSSDropsNonCanonicalTelegraphLink(t *testing.T) {
	env := newRSSEnv(t)
	seedFeedPost(t, env, "带查询参数", true, false, "2026-11-22 10:00:00", "https://telegra.ph/Bad-11-23?token=secret")
	seedFeedPost(t, env, "带用户信息", true, false, "2026-11-21 10:00:00", "https://user@telegra.ph/Bad-11-22")

	body := env.do(t, http.MethodGet, "/rss.xml", "", nil).Body.String()
	for _, banned := range []string{"带查询参数", "带用户信息", "token=secret"} {
		if strings.Contains(body, banned) {
			t.Errorf("非规范 Telegraph 链接不应进入订阅: %q", banned)
		}
	}
	if strings.Contains(body, "<item>") {
		t.Error("没有可公开条目时不应输出 item")
	}
}
