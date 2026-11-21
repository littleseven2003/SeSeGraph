package posts

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
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

func TestAttachImageReplacesSameOrder(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()

	id, err := repo.Create(ctx, Post{Title: "图片帖子", ImageCount: 2})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}

	if _, err := repo.AttachImage(ctx, Image{PostID: id, SortOrder: 1, MimeType: "image/jpeg", FileSize: 100, TempFilePath: "tmp/1/first.part", Status: ImageStatusUploaded}); err != nil {
		t.Fatalf("保存图片失败: %v", err)
	}
	previous, err := repo.AttachImage(ctx, Image{PostID: id, SortOrder: 1, MimeType: "image/png", FileSize: 250, TempFilePath: "tmp/1/second.part", Status: ImageStatusUploaded})
	if err != nil {
		t.Fatalf("重复保存图片失败: %v", err)
	}
	if previous != "tmp/1/first.part" {
		t.Errorf("应返回被替换的旧路径，实际 %q", previous)
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
		if _, err := repo.AttachImage(ctx, Image{PostID: id, SortOrder: order, MimeType: "image/jpeg", Status: ImageStatusUploaded}); err != nil {
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

func TestAttachImageRejectsWhenNotUploading(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()

	id, err := repo.Create(ctx, Post{Title: "已入队", ImageCount: 1})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	if err := repo.UpdateStatus(ctx, id, StatusQueued); err != nil {
		t.Fatalf("更新状态失败: %v", err)
	}

	if _, err := repo.AttachImage(ctx, Image{PostID: id, SortOrder: 1, Status: ImageStatusUploaded}); !errors.Is(err, ErrNotUploading) {
		t.Fatalf("错误 = %v，期望 ErrNotUploading", err)
	}
}

func TestEnqueueMissingAndQueued(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()

	id, err := repo.Create(ctx, Post{Title: "入队测试", ImageCount: 2})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}

	missing, err := repo.Enqueue(ctx, id)
	if err != nil {
		t.Fatalf("缺图统计失败: %v", err)
	}
	if len(missing) != 2 {
		t.Errorf("缺图 = %v，期望 2 项", missing)
	}

	for _, order := range []int{1, 2} {
		if _, err := repo.AttachImage(ctx, Image{PostID: id, SortOrder: order, Status: ImageStatusUploaded, TempFilePath: "tmp/x.part"}); err != nil {
			t.Fatalf("保存图片失败: %v", err)
		}
	}
	if missing, err := repo.Enqueue(ctx, id); err != nil || len(missing) != 0 {
		t.Fatalf("入队失败: missing=%v err=%v", missing, err)
	}
	if _, err := repo.Enqueue(ctx, id); !errors.Is(err, ErrAlreadyQueued) {
		t.Errorf("重复入队应返回 ErrAlreadyQueued，实际 %v", err)
	}
}

func TestAttachAndEnqueueInterleave(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()

	for round := 0; round < 20; round++ {
		id, err := repo.Create(ctx, Post{Title: "交错测试", ImageCount: 1})
		if err != nil {
			t.Fatalf("创建帖子失败: %v", err)
		}

		var (
			wg          sync.WaitGroup
			attachErr   error
			enqueueErr  error
			enqueueMiss []int
		)
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, attachErr = repo.AttachImage(ctx, Image{PostID: id, SortOrder: 1, Status: ImageStatusUploaded, TempFilePath: "tmp/x.part"})
		}()
		go func() {
			defer wg.Done()
			enqueueMiss, enqueueErr = repo.Enqueue(ctx, id)
		}()
		wg.Wait()

		if attachErr != nil && !errors.Is(attachErr, ErrNotUploading) {
			t.Fatalf("第 %d 轮上传错误: %v", round, attachErr)
		}
		if enqueueErr != nil && !errors.Is(enqueueErr, ErrAlreadyQueued) {
			t.Fatalf("第 %d 轮入队错误: %v", round, enqueueErr)
		}

		stored, err := repo.Get(ctx, id)
		if err != nil {
			t.Fatalf("读取帖子失败: %v", err)
		}
		images, err := repo.ListImages(ctx, id)
		if err != nil {
			t.Fatalf("读取图片失败: %v", err)
		}
		if stored.Status == StatusQueued && len(images) != 1 {
			t.Fatalf("第 %d 轮出现已入队但无图片记录", round)
		}
		if stored.Status == StatusQueued && len(enqueueMiss) != 0 {
			t.Fatalf("第 %d 轮已入队却报告缺图 %v", round, enqueueMiss)
		}
	}
}

func TestCreatePostStoresTags(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()

	id, err := repo.Create(ctx, Post{
		Title:      "带标签的帖子",
		ImageCount: 1,
		Tags:       []string{"写真", "海边"},
	})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}

	tags, err := repo.ListTags(ctx, id)
	if err != nil {
		t.Fatalf("读取标签失败: %v", err)
	}
	if len(tags) != 2 || tags[0] != "写真" || tags[1] != "海边" {
		t.Errorf("标签 = %v，期望 [写真 海边]", tags)
	}
}

func TestCreatePostReusesExistingTagRow(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()

	first, err := repo.Create(ctx, Post{Title: "第一篇", ImageCount: 1, Tags: []string{"合集"}})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	second, err := repo.Create(ctx, Post{Title: "第二篇", ImageCount: 1, Tags: []string{"合集"}})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}

	for _, id := range []int64{first, second} {
		tags, err := repo.ListTags(ctx, id)
		if err != nil {
			t.Fatalf("读取标签失败: %v", err)
		}
		if len(tags) != 1 || tags[0] != "合集" {
			t.Errorf("帖子 %d 标签 = %v，期望 [合集]", id, tags)
		}
	}

	var tagRows int
	if err := repo.handle.QueryRowContext(ctx, `SELECT COUNT(*) FROM tags`).Scan(&tagRows); err != nil {
		t.Fatalf("统计标签失败: %v", err)
	}
	if tagRows != 1 {
		t.Errorf("标签表行数 = %d，期望 1（复用已有标签）", tagRows)
	}
}

func TestCreatePostWithoutTags(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()

	id, err := repo.Create(ctx, Post{Title: "无标签", ImageCount: 1})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	tags, err := repo.ListTags(ctx, id)
	if err != nil {
		t.Fatalf("读取标签失败: %v", err)
	}
	if len(tags) != 0 {
		t.Errorf("标签 = %v，期望空列表", tags)
	}
}
