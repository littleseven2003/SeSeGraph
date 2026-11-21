package posts

import (
	"context"
	"errors"
	"testing"
)

// TestUpdateMetadataGuardsPublishState 验证本地元数据写入会原子校验发布状态。
func TestUpdateMetadataGuardsPublishState(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	postID, err := repo.Create(ctx, Post{Title: "原始标题", Description: "原始说明", ImageCount: 1})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}

	// 普通草稿可直接更新。
	if err := repo.UpdateMetadata(ctx, postID, PublishIdle, "草稿标题", "草稿说明", "SeSe"); err != nil {
		t.Fatalf("更新草稿失败: %v", err)
	}

	busy := map[string]string{
		"待发布":   `UPDATE posts SET publish_state = 'pending' WHERE id = ?`,
		"发布中":   `UPDATE posts SET publish_state = 'publishing' WHERE id = ?`,
		"失败有页面": `UPDATE posts SET publish_state = 'failed', telegraph_paths = '["Page-1"]' WHERE id = ?`,
	}
	for name, statement := range busy {
		if _, err := repo.handle.ExecContext(ctx, statement, postID); err != nil {
			t.Fatalf("%s 状态准备失败: %v", name, err)
		}
		if err := repo.UpdateMetadata(ctx, postID, stateOf(t, repo, postID), "新标题", "新说明", ""); !errors.Is(err, ErrPublishBusy) {
			t.Errorf("%s 的错误 = %v，期望 ErrPublishBusy", name, err)
		}
		if got, _, _, err := metadataOfPost(t, repo, postID); err != nil || got != "草稿标题" {
			t.Errorf("%s 拒绝更新后标题 = %q，err=%v", name, got, err)
		}
	}

	// 没有页面路径的失败帖视同草稿。
	if _, err := repo.handle.ExecContext(ctx,
		`UPDATE posts SET publish_state = 'failed', telegraph_paths = '[]' WHERE id = ?`, postID); err != nil {
		t.Fatalf("准备失败帖失败: %v", err)
	}
	if err := repo.UpdateMetadata(ctx, postID, PublishFailed, "失败后标题", "失败后说明", ""); err != nil {
		t.Errorf("无页面路径的失败帖应允许更新: %v", err)
	}

	// 已发布帖在远端更新成功后允许写入。
	if _, err := repo.handle.ExecContext(ctx,
		`UPDATE posts SET publish_state = 'published', telegraph_paths = '["Page-1"]' WHERE id = ?`, postID); err != nil {
		t.Fatalf("准备已发布帖失败: %v", err)
	}
	if err := repo.UpdateMetadata(ctx, postID, PublishPublished, "发布后标题", "发布后说明", "SeSe"); err != nil {
		t.Errorf("已发布帖应允许更新: %v", err)
	}

	if err := repo.UpdateMetadata(ctx, 999, PublishIdle, "标题", "说明", ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("不存在的帖子错误 = %v，期望 ErrNotFound", err)
	}
}

// metadataOfPost 读取标题、说明与作者。
func metadataOfPost(t *testing.T, repo *Repository, postID int64) (string, string, string, error) {
	t.Helper()
	detail, err := repo.GalleryDetail(context.Background(), postID)
	if err != nil {
		return "", "", "", err
	}
	return detail.Title, detail.Description, detail.AuthorName, nil
}

// stateOf 读取帖子当前的发布状态。
func stateOf(t *testing.T, repo *Repository, postID int64) string {
	t.Helper()
	info, err := repo.PublishInfo(context.Background(), postID)
	if err != nil {
		t.Fatalf("读取发布信息失败: %v", err)
	}
	return info.State
}

// TestUpdateMetadataRejectsStateChangedAfterRead 验证读取后才完成的发布会挡住草稿写入。
func TestUpdateMetadataRejectsStateChangedAfterRead(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	postID, err := repo.Create(ctx, Post{Title: "读取时的草稿", ImageCount: 1})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}

	// 模拟读取后发布队列完成整次发布：状态变成 published。
	if _, err := repo.handle.ExecContext(ctx,
		`UPDATE posts SET publish_state = 'published', telegraph_paths = '["Page-1"]' WHERE id = ?`, postID); err != nil {
		t.Fatalf("改写状态失败: %v", err)
	}
	err = repo.UpdateMetadata(ctx, postID, PublishIdle, "迟到的标题", "迟到的说明", "")
	if !errors.Is(err, ErrPublishBusy) {
		t.Errorf("错误 = %v，期望 ErrPublishBusy", err)
	}
	title, _, _, err := metadataOfPost(t, repo, postID)
	if err != nil {
		t.Fatalf("读取标题失败: %v", err)
	}
	if title != "读取时的草稿" {
		t.Errorf("竞态下不应写入，实际标题 %q", title)
	}
}
