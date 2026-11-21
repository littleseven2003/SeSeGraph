package publisher

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
	"github.com/littleseven2003/SeSeGraph/backend/internal/telegraph"
)

// newEditorEnv 发布一帖后返回可用的编辑器。
func newEditorEnv(t *testing.T, images int, description string) (*publishEnv, *Editor) {
	t.Helper()
	env := newPublishEnv(t, images, description)
	if _, err := env.pub.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("初始发布失败: %v", err)
	}
	editor, err := NewEditor(env.repo, func(context.Context) (PageAPI, error) {
		return env.pages, nil
	}, "https://images.example.com")
	if err != nil {
		t.Fatalf("创建编辑器失败: %v", err)
	}
	return env, editor
}

// metadataOf 读取本地记录的标题、说明与作者。
func metadataOf(t *testing.T, env *publishEnv, postID int64) (string, string, string, string) {
	t.Helper()
	detail, err := env.repo.GalleryDetail(context.Background(), postID)
	if err != nil {
		t.Fatalf("读取详情失败: %v", err)
	}
	return detail.Title, detail.Description, detail.AuthorName, detail.PublishedAt
}

func TestEditorUpdatesEveryPageThenMetadata(t *testing.T) {
	env, editor := newEditorEnv(t, 400, "初始说明")
	before := env.publishedInfo(t, 1)
	if len(before.Paths) != 2 {
		t.Fatalf("初始分页 = %d，期望 2", len(before.Paths))
	}
	_, _, _, publishedAt := metadataOf(t, env, 1)
	env.pages.resetCounts()

	err := editor.Update(context.Background(), 1, Metadata{
		Title:       "改过的标题",
		Description: "新的说明",
		AuthorName:  "SeSe",
	})
	if err != nil {
		t.Fatalf("更新失败: %v", err)
	}

	_, edits := env.pages.counts()
	if edits != 2 {
		t.Fatalf("编辑页面 %d 次，期望 2", edits)
	}
	env.pages.mu.Lock()
	editedPaths := append([]string{}, env.pages.editedPaths...)
	edited := append([]telegraph.PageRequest{}, env.pages.edited...)
	env.pages.mu.Unlock()
	if len(editedPaths) != 2 || editedPaths[0] != before.Paths[0] || editedPaths[1] != before.Paths[1] {
		t.Errorf("编辑路径 = %v，期望沿用 %v", editedPaths, before.Paths)
	}
	if edited[0].Title != "改过的标题 (1/2)" || edited[1].Title != "改过的标题 (2/2)" {
		t.Errorf("分页标题 = %q / %q", edited[0].Title, edited[1].Title)
	}
	if edited[0].AuthorName != "SeSe" {
		t.Errorf("作者 = %q，期望 SeSe", edited[0].AuthorName)
	}
	if !containsText(edited[0].Content, "新的说明") || containsText(edited[0].Content, "初始说明") {
		t.Error("首页正文未替换为新说明")
	}
	if !containsText(edited[0].Content, "下一页") || containsText(edited[0].Content, "上一页") {
		t.Error("首页互链应只有下一页")
	}
	if !containsText(edited[1].Content, "上一页") || containsText(edited[1].Content, "下一页") {
		t.Error("末页互链应只有上一页")
	}

	title, description, author, afterPublished := metadataOf(t, env, 1)
	if title != "改过的标题" || description != "新的说明" || author != "SeSe" {
		t.Errorf("本地元数据 = %q/%q/%q", title, description, author)
	}
	if afterPublished != publishedAt {
		t.Errorf("发布时间被改写：%q → %q", publishedAt, afterPublished)
	}
	after := env.publishedInfo(t, 1)
	if after.State != posts.PublishPublished {
		t.Errorf("发布状态 = %q", after.State)
	}
	if len(after.Paths) != 2 || after.Paths[0] != before.Paths[0] || after.Paths[1] != before.Paths[1] {
		t.Errorf("页面路径被改动: %v → %v", before.Paths, after.Paths)
	}
}

func TestEditorKeepsMetadataWhenPageUpdateFails(t *testing.T) {
	env, editor := newEditorEnv(t, 400, "初始说明")
	env.pages.resetCounts()
	env.pages.mu.Lock()
	env.pages.failEditAt = 2
	env.pages.mu.Unlock()

	err := editor.Update(context.Background(), 1, Metadata{
		Title:       "失败标题",
		Description: "失败说明",
		AuthorName:  "",
	})
	if !errors.Is(err, telegraph.ErrUpstream) {
		t.Fatalf("中途失败错误 = %v，期望 ErrUpstream", err)
	}
	title, description, _, _ := metadataOf(t, env, 1)
	if title != "发布测试" || description != "初始说明" {
		t.Errorf("失败后本地记录被改动: %q/%q", title, description)
	}

	// 解除故障后重试：沿用同一批页面路径并成功落库。
	env.pages.mu.Lock()
	env.pages.failEditAt = 0
	env.pages.mu.Unlock()
	if err := editor.Update(context.Background(), 1, Metadata{
		Title:       "重试标题",
		Description: "重试说明",
	}); err != nil {
		t.Fatalf("重试失败: %v", err)
	}
	title, description, _, _ = metadataOf(t, env, 1)
	if title != "重试标题" || description != "重试说明" {
		t.Errorf("重试后本地记录 = %q/%q", title, description)
	}
	info := env.publishedInfo(t, 1)
	if len(info.Paths) != 2 {
		t.Errorf("重试后页面路径 = %v", info.Paths)
	}
}

func TestEditorRejectsPageCountChange(t *testing.T) {
	env, editor := newEditorEnv(t, 1, "初始说明")
	env.pages.resetCounts()

	err := editor.Update(context.Background(), 1, Metadata{
		Title:       "超长说明",
		Description: strings.Repeat("多行说明不会触发单节点上限\n", 4000),
	})
	if !errors.Is(err, ErrPageCountChanged) {
		t.Fatalf("错误 = %v，期望 ErrPageCountChanged", err)
	}
	_, edits := env.pages.counts()
	if edits != 0 {
		t.Errorf("分页数量变化时不应发出编辑请求，实际 %d 次", edits)
	}
	title, _, _, _ := metadataOf(t, env, 1)
	if title != "发布测试" {
		t.Errorf("被拒绝的修改不应落库，实际标题 %q", title)
	}
}

func TestEditorUpdatesUnpublishedPostLocally(t *testing.T) {
	repo := newPublishEnv(t, 0, "").repo
	ctx := context.Background()
	postID, err := repo.Create(ctx, posts.Post{Title: "草稿", Description: "旧说明", ImageCount: 1})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}

	editor, err := NewEditor(repo, func(context.Context) (PageAPI, error) {
		return nil, telegraph.ErrNotConfigured
	}, "https://images.example.com")
	if err != nil {
		t.Fatalf("创建编辑器失败: %v", err)
	}
	if err := editor.Update(ctx, postID, Metadata{
		Title:       "草稿标题",
		Description: "新的说明",
		AuthorName:  "SeSe",
	}); err != nil {
		t.Fatalf("更新草稿失败: %v", err)
	}
	detail, err := repo.GalleryDetail(ctx, postID)
	if err != nil {
		t.Fatalf("读取详情失败: %v", err)
	}
	if detail.Title != "草稿标题" || detail.Description != "新的说明" || detail.AuthorName != "SeSe" {
		t.Errorf("草稿元数据 = %+v", detail)
	}
}

func TestEditorRequiresPublishedURLAndTitle(t *testing.T) {
	env, _ := newEditorEnv(t, 2, "初始说明")
	for _, publicURL := range []string{"  ", "http://images.example.com", "https://images.example.com/?x=1"} {
		editor, err := NewEditor(env.repo, func(context.Context) (PageAPI, error) {
			return env.pages, nil
		}, publicURL)
		if err != nil {
			t.Fatalf("创建编辑器失败: %v", err)
		}
		if err := editor.Update(context.Background(), 1, Metadata{Title: "标题"}); !errors.Is(err, ErrPublicURLInvalid) {
			t.Errorf("公开地址 %q 的错误 = %v，期望 ErrPublicURLInvalid", publicURL, err)
		}
	}

	normal, err := NewEditor(env.repo, func(context.Context) (PageAPI, error) {
		return env.pages, nil
	}, "https://images.example.com")
	if err != nil {
		t.Fatalf("创建编辑器失败: %v", err)
	}
	if err := normal.Update(context.Background(), 1, Metadata{Title: "   "}); !errors.Is(err, telegraph.ErrInvalidInput) {
		t.Errorf("空标题的错误 = %v，期望 ErrInvalidInput", err)
	}
}

// containsText 判断正文节点中是否出现指定文字。
func containsText(nodes []telegraph.Node, want string) bool {
	for _, node := range nodes {
		if nodeHasText(node, want) {
			return true
		}
	}
	return false
}

func nodeHasText(node telegraph.Node, want string) bool {
	for _, child := range node.Children {
		switch value := child.(type) {
		case string:
			if strings.Contains(value, want) {
				return true
			}
		case telegraph.Node:
			if nodeHasText(value, want) {
				return true
			}
		case map[string]string:
			for _, item := range value {
				if strings.Contains(item, want) {
					return true
				}
			}
		}
	}
	return false
}
