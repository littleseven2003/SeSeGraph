package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
)

// galleryPost 建一个带标签与封面的帖子，返回编号。
func galleryPost(t *testing.T, env *testEnv, title, cover string, tags ...string) int64 {
	t.Helper()
	id, err := env.posts.Create(context.Background(), posts.Post{
		Title:          title,
		Description:    title + "的说明",
		ImageCount:     1,
		CoverThumbPath: cover,
		Tags:           tags,
	})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	return id
}

// galleryListBody 是列表接口的响应结构。
type galleryListBody struct {
	Items    []galleryItemResponse `json:"items"`
	Total    int                   `json:"total"`
	Page     int                   `json:"page"`
	PageSize int                   `json:"page_size"`
}

func TestGalleryRequiresAuth(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	paths := []string{"/api/posts", "/api/posts/1", "/api/tags", "/api/thumbs/1.webp"}
	for _, target := range paths {
		rec := env.do(t, http.MethodGet, target, "", nil)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s 状态码 = %d，期望 401", target, rec.Code)
		}
	}
}

func TestListPostsFiltersAndPaginates(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)
	galleryPost(t, env, "海边写真", "thumbs/3.webp", "写真", "海边")
	galleryPost(t, env, "街拍记录", "thumbs/2.webp", "写真", "街拍")
	galleryPost(t, env, "山间风景", "", "风景")

	rec := env.do(t, http.MethodGet, "/api/posts?page_size=2", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200，响应: %s", rec.Code, rec.Body.String())
	}
	var body galleryListBody
	if err := decodeJSON(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if body.Total != 3 || len(body.Items) != 2 || body.Page != 1 || body.PageSize != 2 {
		t.Fatalf("列表结果 = %+v", body)
	}
	if body.Items[0].Title != "山间风景" || body.Items[1].Title != "街拍记录" {
		t.Errorf("排序不符: %v", body.Items)
	}
	if body.Items[1].CoverURL != "/api/thumbs/2.webp" {
		t.Errorf("封面地址 = %q", body.Items[1].CoverURL)
	}
	if len(body.Items[1].Tags) != 2 {
		t.Errorf("标签 = %v", body.Items[1].Tags)
	}

	// 关键词命中标签。
	rec = env.do(t, http.MethodGet, "/api/posts?q=%E5%86%99%E7%9C%9F", "", cookie)
	if err := decodeJSON(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if body.Total != 2 {
		t.Errorf("关键词命中 %d，期望 2", body.Total)
	}

	// 多标签同时具备。
	rec = env.do(t, http.MethodGet, "/api/posts?tag=%E5%86%99%E7%9C%9F&tag=%E6%B5%B7%E8%BE%B9", "", cookie)
	if err := decodeJSON(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if body.Total != 1 || body.Items[0].Title != "海边写真" {
		t.Errorf("多标签筛选 = %+v", body.Items)
	}

	// 逗号分隔形式等价。
	rec = env.do(t, http.MethodGet, "/api/posts?tags=%E5%86%99%E7%9C%9F,%E6%B5%B7%E8%BE%B9", "", cookie)
	if err := decodeJSON(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if body.Total != 1 {
		t.Errorf("逗号分隔标签命中 %d，期望 1", body.Total)
	}

	// 第二页只剩一条。
	rec = env.do(t, http.MethodGet, "/api/posts?page=2&page_size=2", "", cookie)
	if err := decodeJSON(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if body.Total != 3 || len(body.Items) != 1 || body.Items[0].Title != "海边写真" {
		t.Errorf("第二页 = %+v", body)
	}
}

func TestListPostsRejectsInvalidParams(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)
	cases := map[string]string{
		"页码为零":   "/api/posts?page=0",
		"页码非数字":  "/api/posts?page=abc",
		"分页过大":   "/api/posts?page_size=101",
		"分页为零":   "/api/posts?page_size=0",
		"标签过多":   "/api/posts?tags=a,b,c,d,e,f,g,h,i,j,k",
		"单个标签过长": "/api/posts?tag=" + longRunes(31),
		"关键词过长":  "/api/posts?q=" + longRunes(101),
	}
	for name, target := range cases {
		rec := env.do(t, http.MethodGet, target, "", cookie)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s 状态码 = %d，期望 400，响应: %s", name, rec.Code, rec.Body.String())
		}
	}
}

// longRunes 生成指定数量的汉字，用于长度上限校验。
func longRunes(count int) string {
	value := make([]rune, count)
	for index := range value {
		value[index] = '图'
	}
	return string(value)
}

func TestPostDetailReturnsImagesAndLinks(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)
	postID := galleryPost(t, env, "详情帖子", "thumbs/9.webp", "写真")
	ctx := context.Background()
	if _, err := env.posts.AttachImage(ctx, posts.Image{
		PostID: postID, SortOrder: 1, ShortCode: "detailcode12",
		MimeType: "image/jpeg", FileSize: 2048, Status: posts.ImageStatusStored,
	}); err != nil {
		t.Fatalf("保存图片失败: %v", err)
	}
	if _, err := env.handle.ExecContext(ctx,
		`UPDATE posts SET is_public = 1, publish_state = ?, telegraph_urls = ? WHERE id = ?`,
		posts.PublishPublished, `["https://telegra.ph/Page-1"]`, postID); err != nil {
		t.Fatalf("更新发布信息失败: %v", err)
	}

	rec := env.do(t, http.MethodGet, "/api/posts/"+formatID(postID), "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200，响应: %s", rec.Code, rec.Body.String())
	}
	var detail galleryDetailResponse
	if err := decodeJSON(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if detail.Title != "详情帖子" || detail.Description != "详情帖子的说明" {
		t.Errorf("详情字段不符: %+v", detail)
	}
	if !detail.Public || detail.PublishState != posts.PublishPublished {
		t.Errorf("发布状态不符: public=%v state=%s", detail.Public, detail.PublishState)
	}
	if len(detail.TelegraphURLs) != 1 || len(detail.Images) != 1 {
		t.Errorf("链接或图片清单不符: %+v", detail)
	}
	if detail.Images[0].ShortCode != "detailcode12" || detail.Images[0].FileSize != 2048 {
		t.Errorf("图片字段不符: %+v", detail.Images[0])
	}
	if detail.CoverURL != "/api/thumbs/9.webp" {
		t.Errorf("封面地址 = %q", detail.CoverURL)
	}

	missing := env.do(t, http.MethodGet, "/api/posts/999", "", cookie)
	if missing.Code != http.StatusNotFound {
		t.Errorf("不存在帖子状态码 = %d，期望 404", missing.Code)
	}
	invalid := env.do(t, http.MethodGet, "/api/posts/abc", "", cookie)
	if invalid.Code != http.StatusBadRequest {
		t.Errorf("非法编号状态码 = %d，期望 400", invalid.Code)
	}
}

func TestGalleryFieldsAlwaysPresent(t *testing.T) {
	env := newTestEnv(t, testSetupToken)
	cookie := env.setup(t)
	galleryPost(t, env, "无封面帖子", "")

	rec := env.do(t, http.MethodGet, "/api/posts", "", cookie)
	var raw struct {
		Items []map[string]any `json:"items"`
	}
	if err := decodeJSON(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	if len(raw.Items) != 1 {
		t.Fatalf("条数 = %d，期望 1", len(raw.Items))
	}
	for _, key := range []string{"cover_url", "cover_thumb_path", "tags", "telegraph_urls"} {
		if _, ok := raw.Items[0][key]; !ok {
			t.Errorf("列表缺少字段 %q", key)
		}
	}

	postID := int64(raw.Items[0]["id"].(float64))
	rec = env.do(t, http.MethodGet, "/api/posts/"+formatID(postID), "", cookie)
	var detail map[string]any
	if err := decodeJSON(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("响应不是合法 JSON: %v", err)
	}
	for _, key := range []string{"images", "telegraph_paths", "telegraph_urls", "tags", "cover_url"} {
		if _, ok := detail[key]; !ok {
			t.Errorf("详情缺少字段 %q", key)
		}
	}
}
