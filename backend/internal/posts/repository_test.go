package posts

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/littleseven2003/SeSeGraph/backend/internal/db"
)

func newTestRepository(t *testing.T) *Repository {
	t.Helper()
	handle, err := db.Open(filepath.Join(t.TempDir(), "sesegraph.db"))
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	if err := db.Migrate(context.Background(), handle); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	return NewRepository(handle)
}

func TestCreateAndGetPost(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()

	id, err := repo.Create(ctx, Post{
		Title:       "测试帖子",
		Description: "说明",
		AuthorName:  "SeSe",
		ImageCount:  2,
		UploadMode:  "document",
	})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}

	stored, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("读取帖子失败: %v", err)
	}
	if stored.Title != "测试帖子" || stored.ImageCount != 2 || stored.UploadMode != "document" {
		t.Errorf("字段不符: %+v", stored)
	}
	if stored.Status != StatusUploading {
		t.Errorf("状态 = %q，期望 %q", stored.Status, StatusUploading)
	}
}

func TestGetMissingPost(t *testing.T) {
	repo := newTestRepository(t)
	if _, err := repo.Get(context.Background(), 999); err == nil {
		t.Error("不存在的帖子应返回错误")
	}
}

func TestSetCoverAndUpdateStatus(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()

	id, err := repo.Create(ctx, Post{Title: "封面帖子", ImageCount: 1})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	if err := repo.SetCover(ctx, id, "thumbs/1.webp"); err != nil {
		t.Fatalf("保存封面路径失败: %v", err)
	}
	if err := repo.UpdateStatus(ctx, id, StatusQueued); err != nil {
		t.Fatalf("更新状态失败: %v", err)
	}

	stored, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("读取帖子失败: %v", err)
	}
	if stored.CoverThumbPath != "thumbs/1.webp" || stored.Status != StatusQueued {
		t.Errorf("更新未生效: %+v", stored)
	}

	if err := repo.UpdateStatus(ctx, 999, StatusQueued); err == nil {
		t.Error("更新不存在的帖子应返回错误")
	}
}

func TestSaveImageReplacesSameOrder(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()

	id, err := repo.Create(ctx, Post{Title: "图片帖子", ImageCount: 2})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}

	if err := repo.SaveImage(ctx, Image{PostID: id, SortOrder: 1, MimeType: "image/jpeg", FileSize: 100, TempFilePath: "tmp/1.part", Status: ImageStatusUploaded}); err != nil {
		t.Fatalf("保存图片失败: %v", err)
	}
	if err := repo.SaveImage(ctx, Image{PostID: id, SortOrder: 1, MimeType: "image/png", FileSize: 250, TempFilePath: "tmp/1.part", Status: ImageStatusUploaded}); err != nil {
		t.Fatalf("重复保存图片失败: %v", err)
	}

	images, err := repo.ListImages(ctx, id)
	if err != nil {
		t.Fatalf("读取图片列表失败: %v", err)
	}
	if len(images) != 1 {
		t.Fatalf("图片记录数 = %d，期望 1（同序号应覆盖）", len(images))
	}
	if images[0].MimeType != "image/png" || images[0].FileSize != 250 {
		t.Errorf("覆盖后字段不符: %+v", images[0])
	}
	if len(images[0].ShortCode) != 12 {
		t.Errorf("短码长度 = %d，期望 12", len(images[0].ShortCode))
	}
}

func TestMissingOrders(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()

	id, err := repo.Create(ctx, Post{Title: "缺图帖子", ImageCount: 3})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}

	missing, err := repo.MissingOrders(ctx, id, 3)
	if err != nil {
		t.Fatalf("统计缺图失败: %v", err)
	}
	if len(missing) != 3 || missing[0] != 1 || missing[2] != 3 {
		t.Errorf("缺图序号 = %v，期望 [1 2 3]", missing)
	}

	for _, order := range []int{1, 3} {
		if err := repo.SaveImage(ctx, Image{PostID: id, SortOrder: order, MimeType: "image/jpeg", Status: ImageStatusUploaded}); err != nil {
			t.Fatalf("保存图片失败: %v", err)
		}
	}
	missing, err = repo.MissingOrders(ctx, id, 3)
	if err != nil {
		t.Fatalf("统计缺图失败: %v", err)
	}
	if len(missing) != 1 || missing[0] != 2 {
		t.Errorf("缺图序号 = %v，期望 [2]", missing)
	}
}
