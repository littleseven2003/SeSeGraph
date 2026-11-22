package posts

import (
	"context"
	"errors"
	"strconv"
	"testing"
)

// TestSetDisabledIdempotent 验证熔断开关按目标值写入：重复撤回不会反向恢复，且保留公开授权。
func TestSetDisabledIdempotent(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	postID, err := repo.Create(ctx, Post{Title: "撤回测试", ImageCount: 1})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	if err := repo.SetPublic(ctx, postID, true); err != nil {
		t.Fatalf("设置公开失败: %v", err)
	}

	disabled, public, err := repo.SetDisabled(ctx, postID, true)
	if err != nil {
		t.Fatalf("撤回失败: %v", err)
	}
	if !disabled || !public {
		t.Errorf("首次撤回 = disabled %v public %v，期望禁用且公开", disabled, public)
	}

	// 重复撤回：仍是禁用状态，客户端重试不会把图片重新公开。
	disabled, public, err = repo.SetDisabled(ctx, postID, true)
	if err != nil {
		t.Fatalf("重复撤回失败: %v", err)
	}
	if !disabled || !public {
		t.Errorf("重复撤回 = disabled %v public %v，期望仍禁用且公开", disabled, public)
	}

	disabled, public, err = repo.SetDisabled(ctx, postID, false)
	if err != nil {
		t.Fatalf("恢复失败: %v", err)
	}
	if disabled || !public {
		t.Errorf("恢复 = disabled %v public %v，期望未禁用且公开", disabled, public)
	}

	// 重复恢复同样保持未禁用。
	disabled, public, err = repo.SetDisabled(ctx, postID, false)
	if err != nil {
		t.Fatalf("重复恢复失败: %v", err)
	}
	if disabled || !public {
		t.Errorf("重复恢复 = disabled %v public %v，期望仍未禁用且公开", disabled, public)
	}

	if _, _, err := repo.SetDisabled(ctx, 999, true); !errors.Is(err, ErrNotFound) {
		t.Errorf("不存在帖子错误 = %v，期望 ErrNotFound", err)
	}
}

// TestCountStoredImages 验证只统计已存入图床的图片，供换 Bot 前判断图库归属。
func TestCountStoredImages(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	postID, err := repo.Create(ctx, Post{Title: "计数测试", ImageCount: 2})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	for sort := 1; sort <= 2; sort++ {
		if _, err := repo.AttachImage(ctx, Image{
			PostID:    postID,
			SortOrder: sort,
			MimeType:  "image/png",
			FileSize:  10,
			Status:    ImageStatusUploaded,
		}); err != nil {
			t.Fatalf("写入图片失败: %v", err)
		}
	}
	if count, err := repo.CountStoredImages(ctx); err != nil || count != 0 {
		t.Fatalf("未存图时计数 = %d err=%v，期望 0", count, err)
	}

	images, err := repo.ListImages(ctx, postID)
	if err != nil || len(images) != 2 {
		t.Fatalf("读取图片失败: %v", err)
	}
	if err := repo.MarkImageStored(ctx, images[0].ID, "file-1", 1, "telegram"); err != nil {
		t.Fatalf("标记存图失败: %v", err)
	}
	if count, err := repo.CountStoredImages(ctx); err != nil || count != 1 {
		t.Errorf("计数 = %d err=%v，期望 1", count, err)
	}
}

// TestSampleStoredFileIDs 取样只返回已存图片的远端标识，按最新优先并受条数限制。
func TestSampleStoredFileIDs(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	postID, err := repo.Create(ctx, Post{Title: "取样测试", ImageCount: 3})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	for sort := 1; sort <= 3; sort++ {
		if _, err := repo.AttachImage(ctx, Image{
			PostID:    postID,
			SortOrder: sort,
			MimeType:  "image/png",
			FileSize:  10,
			Status:    ImageStatusUploaded,
		}); err != nil {
			t.Fatalf("写入图片失败: %v", err)
		}
	}
	if ids, err := repo.SampleStoredFileIDs(ctx, 3); err != nil || len(ids) != 0 {
		t.Fatalf("未存图时取样 = %v err=%v，期望空", ids, err)
	}

	images, err := repo.ListImages(ctx, postID)
	if err != nil || len(images) != 3 {
		t.Fatalf("读取图片失败: %v", err)
	}
	for index, image := range images {
		if err := repo.MarkImageStored(ctx, image.ID, "file-"+strconv.Itoa(index+1), int64(index+1), "telegram"); err != nil {
			t.Fatalf("标记存图失败: %v", err)
		}
	}

	ids, err := repo.SampleStoredFileIDs(ctx, 2)
	if err != nil {
		t.Fatalf("取样失败: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("取样条数 = %d，期望 2", len(ids))
	}
	// 最新优先：sort_order 3 的图片先被取到。
	if ids[0] != "file-3" || ids[1] != "file-2" {
		t.Errorf("取样顺序 = %v，期望最新优先", ids)
	}
}
