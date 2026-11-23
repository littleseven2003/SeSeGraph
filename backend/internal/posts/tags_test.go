package posts

import (
	"context"
	"testing"
)

// tagNames 读取当前所有标签名，便于断言孤立标签被清理。
func tagNames(t *testing.T, repo *Repository) []string {
	t.Helper()
	counts, err := repo.ListTagCounts(context.Background())
	if err != nil {
		t.Fatalf("读取标签失败: %v", err)
	}
	names := make([]string, 0, len(counts))
	for _, item := range counts {
		names = append(names, item.Name)
	}
	return names
}

func containsTag(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}

// TestUpdateMetadataAndTagsCleansOrphans 验证替换标签只删无关联标签，共享标签保留。
func TestUpdateMetadataAndTagsCleansOrphans(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()

	first, err := repo.Create(ctx, Post{Title: "第一帖", ImageCount: 1, Tags: []string{"共享", "甲"}})
	if err != nil {
		t.Fatalf("创建第一帖失败: %v", err)
	}
	second, err := repo.Create(ctx, Post{Title: "第二帖", ImageCount: 1, Tags: []string{"共享", "乙"}})
	if err != nil {
		t.Fatalf("创建第二帖失败: %v", err)
	}

	tags := []string{"共享"}
	if err := repo.UpdateMetadataAndTags(ctx, first, PublishIdle, "第一帖", "", "", &tags); err != nil {
		t.Fatalf("更新标签失败: %v", err)
	}
	names := tagNames(t, repo)
	if containsTag(names, "甲") {
		t.Errorf("孤立标签未清理: %v", names)
	}
	if !containsTag(names, "共享") || !containsTag(names, "乙") {
		t.Errorf("共享或他人标签被误删: %v", names)
	}

	empty := []string{}
	if err := repo.UpdateMetadataAndTags(ctx, first, PublishIdle, "第一帖", "", "", &empty); err != nil {
		t.Fatalf("清空标签失败: %v", err)
	}
	if err := repo.UpdateMetadataAndTags(ctx, second, PublishIdle, "第二帖", "", "", &empty); err != nil {
		t.Fatalf("清空第二帖标签失败: %v", err)
	}
	if names := tagNames(t, repo); len(names) != 0 {
		t.Errorf("清空后仍残留标签: %v", names)
	}
}

// TestUpdateMetadataKeepsTagsWhenNil 验证省略 tags 时保留原标签。
func TestUpdateMetadataKeepsTagsWhenNil(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	id, err := repo.Create(ctx, Post{Title: "保留标签", ImageCount: 1, Tags: []string{"保留"}})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	if err := repo.UpdateMetadata(ctx, id, PublishIdle, "新标题", "", ""); err != nil {
		t.Fatalf("更新元数据失败: %v", err)
	}
	if tags, err := repo.ListTags(ctx, id); err != nil || len(tags) != 1 || tags[0] != "保留" {
		t.Errorf("标签 = %v / %v，期望保留", tags, err)
	}
}

// TestDeleteCleansOrphanTags 验证永久删除后只清掉不再被引用的标签。
func TestDeleteCleansOrphanTags(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()

	keep, err := repo.Create(ctx, Post{Title: "保留帖", ImageCount: 1, Status: StatusCompleted, Tags: []string{"共享"}})
	if err != nil {
		t.Fatalf("创建保留帖失败: %v", err)
	}
	gone, err := repo.Create(ctx, Post{Title: "待删帖", ImageCount: 1, Status: StatusCompleted, Tags: []string{"共享", "只此一帖"}})
	if err != nil {
		t.Fatalf("创建待删帖失败: %v", err)
	}

	if err := repo.Delete(ctx, gone); err != nil {
		t.Fatalf("删除帖子失败: %v", err)
	}
	names := tagNames(t, repo)
	if containsTag(names, "只此一帖") {
		t.Errorf("删除后孤立标签未清理: %v", names)
	}
	if !containsTag(names, "共享") {
		t.Errorf("共享标签被误删: %v", names)
	}
	if tags, err := repo.ListTags(ctx, keep); err != nil || len(tags) != 1 {
		t.Errorf("保留帖标签 = %v / %v", tags, err)
	}
}
