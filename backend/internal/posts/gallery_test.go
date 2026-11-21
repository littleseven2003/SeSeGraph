package posts

import (
	"context"
	"errors"
	"testing"
)

// seedGallery 建三帖用于列表与筛选断言。
func seedGallery(t *testing.T, repo *Repository) map[string]int64 {
	t.Helper()
	ctx := context.Background()
	specs := []Post{
		{Title: "海边写真", Description: "夏日海滩", Tags: []string{"写真", "海边"}, ImageCount: 2, CoverThumbPath: "thumbs/3.webp"},
		{Title: "街拍记录", Description: "城市漫步", Tags: []string{"写真", "街拍"}, ImageCount: 1, CoverThumbPath: "thumbs/2.webp"},
		{Title: "山间风景", Description: "清晨云雾", Tags: []string{"风景"}, ImageCount: 3},
	}
	ids := make(map[string]int64, len(specs))
	for _, post := range specs {
		id, err := repo.Create(ctx, post)
		if err != nil {
			t.Fatalf("创建帖子失败: %v", err)
		}
		ids[post.Title] = id
	}
	return ids
}

func titlesOf(page *GalleryPage) []string {
	titles := make([]string, 0, len(page.Items))
	for _, item := range page.Items {
		titles = append(titles, item.Title)
	}
	return titles
}

func TestListGalleryReturnsNewestFirst(t *testing.T) {
	repo := newTestRepository(t)
	seedGallery(t, repo)

	page, err := repo.ListGallery(context.Background(), GalleryQuery{})
	if err != nil {
		t.Fatalf("读取画廊失败: %v", err)
	}
	if page.Total != 3 || len(page.Items) != 3 {
		t.Fatalf("总数 = %d，条数 = %d，期望 3", page.Total, len(page.Items))
	}
	if page.Page != 1 || page.PageSize != DefaultGalleryPageSize {
		t.Errorf("分页默认值 = %d/%d", page.Page, page.PageSize)
	}
	want := []string{"山间风景", "街拍记录", "海边写真"}
	for index, title := range want {
		if page.Items[index].Title != title {
			t.Errorf("第 %d 条 = %q，期望 %q", index+1, page.Items[index].Title, title)
		}
	}
	if page.Items[2].CoverThumbPath != "thumbs/3.webp" || page.Items[2].ImageCount != 2 {
		t.Errorf("封面或张数不符: %+v", page.Items[2])
	}
	if len(page.Items[2].Tags) != 2 || page.Items[2].Tags[0] != "写真" {
		t.Errorf("标签不符: %v", page.Items[2].Tags)
	}
}

func TestListGalleryKeywordMatchesTitleDescriptionAndTag(t *testing.T) {
	repo := newTestRepository(t)
	seedGallery(t, repo)
	ctx := context.Background()

	cases := map[string]int{
		"海边":  1,
		"海滩":  1,
		"写真":  2,
		"云雾":  1,
		"不存在": 0,
	}
	for keyword, want := range cases {
		page, err := repo.ListGallery(ctx, GalleryQuery{Keyword: keyword})
		if err != nil {
			t.Fatalf("关键词 %q 查询失败: %v", keyword, err)
		}
		if page.Total != want {
			t.Errorf("关键词 %q 命中 %d，期望 %d", keyword, page.Total, want)
		}
	}
}

func TestListGalleryKeywordEscapesWildcards(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	if _, err := repo.Create(ctx, Post{Title: "进度 100% 记录", ImageCount: 1}); err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	if _, err := repo.Create(ctx, Post{Title: "普通记录", ImageCount: 1}); err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}

	page, err := repo.ListGallery(ctx, GalleryQuery{Keyword: "100%"})
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if page.Total != 1 || page.Items[0].Title != "进度 100% 记录" {
		t.Errorf("通配符未转义，结果 = %v", titlesOf(page))
	}
}

func TestListGalleryRequiresAllTags(t *testing.T) {
	repo := newTestRepository(t)
	seedGallery(t, repo)
	ctx := context.Background()

	both, err := repo.ListGallery(ctx, GalleryQuery{Tags: []string{"写真", "海边"}})
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if both.Total != 1 || both.Items[0].Title != "海边写真" {
		t.Errorf("多标签应为同时具备，结果 = %v", titlesOf(both))
	}

	// 重复标签只按一个处理，不影响结果。
	duplicated, err := repo.ListGallery(ctx, GalleryQuery{Tags: []string{"写真", " 写真 "}})
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if duplicated.Total != 2 {
		t.Errorf("重复标签去重后命中 %d，期望 2", duplicated.Total)
	}

	none, err := repo.ListGallery(ctx, GalleryQuery{Tags: []string{"风景", "写真"}})
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if none.Total != 0 || len(none.Items) != 0 {
		t.Errorf("无交集时应为空，结果 = %v", titlesOf(none))
	}
}

func TestListGalleryPaginates(t *testing.T) {
	repo := newTestRepository(t)
	seedGallery(t, repo)
	ctx := context.Background()

	first, err := repo.ListGallery(ctx, GalleryQuery{Page: 1, PageSize: 2})
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if first.Total != 3 || len(first.Items) != 2 {
		t.Fatalf("第一页 = %d 条，总数 %d", len(first.Items), first.Total)
	}
	second, err := repo.ListGallery(ctx, GalleryQuery{Page: 2, PageSize: 2})
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if len(second.Items) != 1 || second.Items[0].Title != "海边写真" {
		t.Errorf("第二页 = %v", titlesOf(second))
	}
	beyond, err := repo.ListGallery(ctx, GalleryQuery{Page: 9, PageSize: 2})
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if beyond.Total != 3 || len(beyond.Items) != 0 {
		t.Errorf("越界页应为空且保留总数，实际 %+v", beyond)
	}
	if beyond.Items == nil {
		t.Error("空页应返回空数组而非 null")
	}
}

func TestGalleryQueryNormalize(t *testing.T) {
	query := GalleryQuery{Keyword: "  海边 ", Page: 0, PageSize: 500,
		Tags: []string{" 写真 ", "", "写真", "海边"}}.Normalize()
	if query.Keyword != "海边" || query.Page != 1 || query.PageSize != MaxGalleryPageSize {
		t.Errorf("清洗结果 = %+v", query)
	}
	if len(query.Tags) != 2 || query.Tags[0] != "写真" || query.Tags[1] != "海边" {
		t.Errorf("标签清洗 = %v", query.Tags)
	}

	many := make([]string, 0, MaxGalleryTags+5)
	for index := 0; index < MaxGalleryTags+5; index++ {
		many = append(many, "t"+string(rune('a'+index)))
	}
	if got := (GalleryQuery{Tags: many}).Normalize(); len(got.Tags) != MaxGalleryTags {
		t.Errorf("标签上限 = %d，期望 %d", len(got.Tags), MaxGalleryTags)
	}
}

func TestGalleryDetailIncludesImagesAndLinks(t *testing.T) {
	repo := newTestRepository(t)
	ids := seedGallery(t, repo)
	ctx := context.Background()
	postID := ids["海边写真"]

	if _, err := repo.handle.ExecContext(ctx,
		`UPDATE posts SET is_public = 1, publish_state = ?, telegraph_paths = ?, telegraph_urls = ?,
		        cover_thumb_path = ? WHERE id = ?`,
		PublishPublished, `["Page-1","Page-2"]`,
		`["https://telegra.ph/Page-1","https://telegra.ph/Page-2"]`, "thumbs/3.webp", postID); err != nil {
		t.Fatalf("更新发布信息失败: %v", err)
	}
	for order := 1; order <= 2; order++ {
		if _, err := repo.AttachImage(ctx, Image{
			PostID: postID, SortOrder: order, ShortCode: "code0000000" + string(rune('0'+order)),
			MimeType: "image/jpeg", FileSize: int64(order * 100), Status: ImageStatusStored,
		}); err != nil {
			t.Fatalf("保存图片失败: %v", err)
		}
	}

	detail, err := repo.GalleryDetail(ctx, postID)
	if err != nil {
		t.Fatalf("读取详情失败: %v", err)
	}
	if detail.Title != "海边写真" || detail.Description != "夏日海滩" || detail.ImageCount != 2 {
		t.Errorf("详情字段不符: %+v", detail)
	}
	if !detail.IsPublic || detail.PublishState != PublishPublished {
		t.Errorf("发布状态不符: public=%v state=%s", detail.IsPublic, detail.PublishState)
	}
	if len(detail.TelegraphURLs) != 2 || len(detail.Paths) != 2 {
		t.Errorf("分页链接 = %v，路径 = %v", detail.TelegraphURLs, detail.Paths)
	}
	if len(detail.Images) != 2 || detail.Images[0].SortOrder != 1 || detail.Images[1].FileSize != 200 {
		t.Errorf("图片清单不符: %+v", detail.Images)
	}
	if len(detail.Tags) != 2 {
		t.Errorf("标签 = %v", detail.Tags)
	}
}

func TestGalleryDetailMissingPost(t *testing.T) {
	repo := newTestRepository(t)
	if _, err := repo.GalleryDetail(context.Background(), 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("不存在的帖子错误 = %v，期望 ErrNotFound", err)
	}
}

func TestListTagCounts(t *testing.T) {
	repo := newTestRepository(t)
	seedGallery(t, repo)

	counts, err := repo.ListTagCounts(context.Background())
	if err != nil {
		t.Fatalf("读取标签失败: %v", err)
	}
	if len(counts) != 4 {
		t.Fatalf("标签数量 = %d，期望 4", len(counts))
	}
	if counts[0].Name != "写真" || counts[0].Count != 2 {
		t.Errorf("首个标签 = %+v，期望写真 2", counts[0])
	}
	for _, item := range counts[1:] {
		if item.Count != 1 {
			t.Errorf("其余标签计数不符: %+v", item)
		}
	}
}

func TestMarkPublishedKeepsFirstPublishedAt(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	postID, err := repo.Create(ctx, Post{Title: "发布时间", ImageCount: 1})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}

	detail, err := repo.GalleryDetail(ctx, postID)
	if err != nil {
		t.Fatalf("读取详情失败: %v", err)
	}
	if detail.PublishedAt != "" {
		t.Errorf("未发布帖发布时间 = %q，期望为空", detail.PublishedAt)
	}

	if err := repo.MarkPublished(ctx, postID, []string{"Page-1"}, []string{"https://telegra.ph/Page-1"}); err != nil {
		t.Fatalf("标记发布失败: %v", err)
	}
	detail, err = repo.GalleryDetail(ctx, postID)
	if err != nil {
		t.Fatalf("读取详情失败: %v", err)
	}
	if detail.PublishedAt == "" {
		t.Fatal("发布后应写入发布时间")
	}
	first := detail.PublishedAt

	// 固定第一次发布时间，重新发布与开关禁用都不应改写它。
	if _, err := repo.handle.ExecContext(ctx,
		`UPDATE posts SET published_at = '2026-11-01 08:00:00' WHERE id = ?`, postID); err != nil {
		t.Fatalf("固定发布时间失败: %v", err)
	}
	if err := repo.MarkPublished(ctx, postID, []string{"Page-1"}, []string{"https://telegra.ph/Page-1"}); err != nil {
		t.Fatalf("重新发布失败: %v", err)
	}
	if err := repo.SetPublic(ctx, postID, false); err != nil {
		t.Fatalf("关闭公开失败: %v", err)
	}
	if err := repo.SetPublic(ctx, postID, true); err != nil {
		t.Fatalf("重新公开失败: %v", err)
	}

	detail, err = repo.GalleryDetail(ctx, postID)
	if err != nil {
		t.Fatalf("读取详情失败: %v", err)
	}
	if detail.PublishedAt != "2026-11-01T08:00:00Z" {
		t.Errorf("发布时间被改写为 %q，首次为 %q", detail.PublishedAt, first)
	}

	page, err := repo.ListGallery(ctx, GalleryQuery{})
	if err != nil {
		t.Fatalf("读取画廊失败: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].PublishedAt != "2026-11-01T08:00:00Z" {
		t.Errorf("列表发布时间 = %+v", page.Items)
	}
}
