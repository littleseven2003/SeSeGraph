package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
	"github.com/littleseven2003/SeSeGraph/backend/internal/settings"
	"github.com/littleseven2003/SeSeGraph/backend/internal/telegraph"
)

// newPublishEnv 构建带 Telegraph 与公开地址的测试环境。
func newPublishEnv(t *testing.T, publicURL string, withTelegraph bool) *testEnv {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":     true,
			"result": map[string]any{"path": "Test-Page-01", "url": "https://telegra.ph/Test-Page-01"},
		})
	}))
	t.Cleanup(upstream.Close)

	return newTestEnvWith(t, testSetupToken, func(deps *Deps, handle *sql.DB) {
		deps.PublicURL = publicURL
		if !withTelegraph {
			return
		}
		store := settings.NewStore(handle)
		manager := telegraph.NewManager(telegraph.Config{APIBase: upstream.URL}, store)
		deps.Telegraph = manager
		// 预置令牌，模拟已完成建号。
		if err := store.Set(context.Background(), settings.KeyTelegraphToken, "publish-token"); err != nil {
			t.Fatalf("预置令牌失败: %v", err)
		}
	})
}

// completedPost 建一个图片已存图的已完成帖子。
func completedPost(t *testing.T, env *testEnv) int64 {
	t.Helper()
	ctx := context.Background()
	postID, err := env.posts.Create(ctx, posts.Post{Title: "待发布图集", ImageCount: 1, UploadMode: "photo"})
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
	return postID
}

func publishStateOf(t *testing.T, env *testEnv, postID int64) (string, int) {
	t.Helper()
	var state string
	var public int
	if err := env.handle.QueryRow(
		`SELECT publish_state, is_public FROM posts WHERE id = ?`, postID).Scan(&state, &public); err != nil {
		t.Fatalf("读取发布状态失败: %v", err)
	}
	return state, public
}

func TestPublishRequiresTelegraphAndHTTPS(t *testing.T) {
	env := newPublishEnv(t, "http://example.com", true)
	cookie := env.setup(t)
	postID := completedPost(t, env)
	path := "/api/posts/" + formatID(postID) + "/publish"

	rec := env.do(t, http.MethodPost, path, "", cookie)
	if rec.Code != http.StatusConflict {
		t.Fatalf("非 HTTPS 状态码 = %d，期望 409，响应: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "HTTPS") {
		t.Errorf("错误文案未说明 HTTPS: %s", rec.Body.String())
	}
	if state, public := publishStateOf(t, env, postID); state != posts.PublishIdle || public != 0 {
		t.Errorf("校验失败时不应改变状态: state=%s public=%d", state, public)
	}

	withoutTelegraph := newPublishEnv(t, "https://example.com", false)
	cookie2 := withoutTelegraph.setup(t)
	postID2 := completedPost(t, withoutTelegraph)
	rec = withoutTelegraph.do(t, http.MethodPost, "/api/posts/"+formatID(postID2)+"/publish", "", cookie2)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("未配置 Telegraph 状态码 = %d，期望 503", rec.Code)
	}
}

// TestPublishRejectsMalformedPublicURL 校验公开地址结构不合法时拒绝发布，
// 图片保持私有，短码仍然 404。
func TestPublishRejectsMalformedPublicURL(t *testing.T) {
	cases := map[string]string{
		"带查询串": "https://img.example.com/?a=1",
		"空查询串": "https://img.example.com/?",
		"带片段":  "https://img.example.com/#top",
		"内嵌凭据": "https://user@img.example.com",
	}
	for name, publicURL := range cases {
		t.Run(name, func(t *testing.T) {
			env := newPublishEnv(t, publicURL, true)
			cookie := env.setup(t)
			postID := completedPost(t, env)

			rec := env.do(t, http.MethodPost, "/api/posts/"+formatID(postID)+"/publish", "", cookie)
			if rec.Code != http.StatusConflict {
				t.Fatalf("状态码 = %d，期望 409，响应: %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "HTTPS") {
				t.Errorf("错误文案未说明地址要求: %s", rec.Body.String())
			}
			if state, public := publishStateOf(t, env, postID); state != posts.PublishIdle || public != 0 {
				t.Errorf("校验失败时不应开启公开: state=%s public=%d", state, public)
			}

			ctx := context.Background()
			images, err := env.posts.ListImages(ctx, postID)
			if err != nil || len(images) == 0 {
				t.Fatalf("读取图片失败: %v", err)
			}
			if _, err := env.posts.PublicImage(ctx, images[0].ShortCode); !errors.Is(err, posts.ErrNotFound) {
				t.Errorf("图片仍应私有，实际错误 = %v", err)
			}
			gate := env.do(t, http.MethodGet, "/i/"+images[0].ShortCode, "", nil)
			if gate.Code != http.StatusNotFound {
				t.Errorf("短码状态码 = %d，期望 404", gate.Code)
			}
		})
	}
}

func TestPublishAcceptsPublicURLWithBasePath(t *testing.T) {
	env := newPublishEnv(t, "https://img.example.com/base/", true)
	cookie := env.setup(t)
	postID := completedPost(t, env)

	rec := env.do(t, http.MethodPost, "/api/posts/"+formatID(postID)+"/publish", "", cookie)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("状态码 = %d，期望 202，响应: %s", rec.Code, rec.Body.String())
	}
	if state, public := publishStateOf(t, env, postID); state != posts.PublishPending || public != 1 {
		t.Errorf("状态 = %s public=%d，期望 pending 且公开", state, public)
	}
}

func TestPublishRejectsUnfinishedPost(t *testing.T) {
	env := newPublishEnv(t, "https://example.com", true)
	cookie := env.setup(t)
	postID, err := env.posts.Create(context.Background(), posts.Post{Title: "未完成", ImageCount: 1})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}

	rec := env.do(t, http.MethodPost, "/api/posts/"+formatID(postID)+"/publish", "", cookie)
	if rec.Code != http.StatusConflict {
		t.Fatalf("状态码 = %d，期望 409，响应: %s", rec.Code, rec.Body.String())
	}
	if state, public := publishStateOf(t, env, postID); state != posts.PublishIdle || public != 0 {
		t.Errorf("未完成帖不应被公开: state=%s public=%d", state, public)
	}
}

func TestPublishMarksPendingAndPublic(t *testing.T) {
	env := newPublishEnv(t, "https://example.com", true)
	cookie := env.setup(t)
	postID := completedPost(t, env)

	rec := env.do(t, http.MethodPost, "/api/posts/"+formatID(postID)+"/publish", "", cookie)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("状态码 = %d，期望 202，响应: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		ID           int64  `json:"id"`
		PublishState string `json:"publish_state"`
		Public       bool   `json:"public"`
	}
	if err := decodeJSON(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if body.PublishState != posts.PublishPending || !body.Public {
		t.Errorf("响应 = %+v", body)
	}

	state, public := publishStateOf(t, env, postID)
	if state != posts.PublishPending || public != 1 {
		t.Errorf("库内状态 = %s public=%d，期望 pending 且公开", state, public)
	}

	// 重复调用在发布未完成前视为冲突，避免重复入队。
	again := env.do(t, http.MethodPost, "/api/posts/"+formatID(postID)+"/publish", "", cookie)
	if again.Code != http.StatusConflict {
		t.Errorf("重复发布状态码 = %d，期望 409", again.Code)
	}
}

func TestPublishUnknownPost(t *testing.T) {
	env := newPublishEnv(t, "https://example.com", true)
	cookie := env.setup(t)
	rec := env.do(t, http.MethodPost, "/api/posts/999/publish", "", cookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("状态码 = %d，期望 404", rec.Code)
	}
}

// formatID 把帖子编号转成路径片段。
func formatID(id int64) string {
	return strconv.FormatInt(id, 10)
}

func TestProgressExposesPublishState(t *testing.T) {
	env := newPublishEnv(t, "https://example.com", true)
	cookie := env.setup(t)
	postID := completedPost(t, env)

	rec := env.do(t, http.MethodGet, "/api/posts/"+formatID(postID)+"/progress", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", rec.Code)
	}
	var before struct {
		PublishState string   `json:"publish_state"`
		Public       bool     `json:"public"`
		URLs         []string `json:"telegraph_urls"`
	}
	if err := decodeJSON(rec.Body.Bytes(), &before); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if before.PublishState != posts.PublishIdle || before.Public || len(before.URLs) != 0 {
		t.Errorf("发布前状态 = %+v", before)
	}

	if err := env.posts.MarkPublished(context.Background(), postID,
		[]string{"Page-1"}, []string{"https://telegra.ph/Page-1"}); err != nil {
		t.Fatalf("标记发布失败: %v", err)
	}

	rec = env.do(t, http.MethodGet, "/api/posts/"+formatID(postID)+"/progress", "", cookie)
	var after struct {
		PublishState string   `json:"publish_state"`
		Public       bool     `json:"public"`
		URLs         []string `json:"telegraph_urls"`
	}
	if err := decodeJSON(rec.Body.Bytes(), &after); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if after.PublishState != posts.PublishPublished || !after.Public {
		t.Errorf("发布后状态 = %+v", after)
	}
	if len(after.URLs) != 1 || after.URLs[0] != "https://telegra.ph/Page-1" {
		t.Errorf("分享链接 = %v", after.URLs)
	}
}
