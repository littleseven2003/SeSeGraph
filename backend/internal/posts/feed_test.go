package posts

import (
	"context"
	"testing"
)

// seedFeedPosts 造四条不同可见性的图集，用于订阅源过滤断言。
func seedFeedPosts(t *testing.T, repo *Repository) map[string]int64 {
	t.Helper()
	ctx := context.Background()
	specs := []struct {
		title   string
		desc    string
		tags    []string
		state   string
		public  int
		disab   int
		urls    string
		publish string
	}{
		{"公开甲", "第一组", []string{"写真", "海边"}, "published", 1, 0, `["https://telegra.ph/Public-A-11-23"]`, "2026-11-22 10:00:00"},
		{"公开乙", "第二组", []string{"风景"}, "published", 1, 0, `["https://telegra.ph/Public-B-11-23"]`, "2026-11-23 09:00:00"},
		{"未公开", "隐藏", []string{"风景"}, "published", 0, 0, `["https://telegra.ph/Hidden-11-23"]`, "2026-11-21 09:00:00"},
		{"已撤回", "撤回", []string{"风景"}, "published", 1, 1, `["https://telegra.ph/Disabled-11-23"]`, "2026-11-20 09:00:00"},
		{"外部链接", "非规范", []string{"风景"}, "published", 1, 0, `["https://example.com/page"]`, "2026-11-19 09:00:00"},
		{"草稿", "未发布", []string{"风景"}, "idle", 1, 0, `["https://telegra.ph/Draft-11-23"]`, "2026-11-18 09:00:00"},
	}
	ids := make(map[string]int64, len(specs))
	for _, spec := range specs {
		id, err := repo.Create(ctx, Post{Title: spec.title, Description: spec.desc, Tags: spec.tags})
		if err != nil {
			t.Fatalf("创建帖子失败: %v", err)
		}
		if _, err := repo.handle.ExecContext(ctx,
			`UPDATE posts SET publish_state = ?, is_public = ?, is_disabled = ?, telegraph_urls = ?, published_at = ?
			  WHERE id = ?`, spec.state, spec.public, spec.disab, spec.urls, spec.publish, id); err != nil {
			t.Fatalf("写入发布状态失败: %v", err)
		}
		ids[spec.title] = id
	}
	return ids
}

func TestListFeedOnlyPublicPublishedWithTelegraphLink(t *testing.T) {
	repo := newTestRepository(t)
	seedFeedPosts(t, repo)

	items, err := repo.ListFeed(context.Background(), 50)
	if err != nil {
		t.Fatalf("读取订阅源失败: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("条数 = %d，期望 2", len(items))
	}
	if items[0].Title != "公开乙" || items[1].Title != "公开甲" {
		t.Errorf("顺序 = %q, %q，期望按发布时间倒序", items[0].Title, items[1].Title)
	}
	if items[0].Link != "https://telegra.ph/Public-B-11-23" {
		t.Errorf("链接 = %q", items[0].Link)
	}
	if len(items[1].Tags) != 2 {
		t.Errorf("标签 = %v，期望两项", items[1].Tags)
	}
	if items[0].PublishedAt != "2026-11-23T09:00:00Z" {
		t.Errorf("时间 = %q，期望 UTC 格式", items[0].PublishedAt)
	}
}

func TestListFeedCapsLimit(t *testing.T) {
	repo := newTestRepository(t)
	seedFeedPosts(t, repo)

	items, err := repo.ListFeed(context.Background(), 1)
	if err != nil {
		t.Fatalf("读取订阅源失败: %v", err)
	}
	if len(items) != 1 || items[0].Title != "公开乙" {
		t.Errorf("限制条数后 = %+v", items)
	}
}

func TestFirstTelegraphLinkRejectsOtherHosts(t *testing.T) {
	cases := map[string]string{
		"https://telegra.ph/Page-11-23":           "https://telegra.ph/Page-11-23",
		"http://telegra.ph/Page-11-23":            "",
		"https://example.com/Page-11-23":          "",
		"https://telegra.ph.evil.test/Page-11-23": "",
	}
	for raw, want := range cases {
		if got := firstTelegraphLink([]string{raw}); got != want {
			t.Errorf("%q → %q，期望 %q", raw, got, want)
		}
	}
}
