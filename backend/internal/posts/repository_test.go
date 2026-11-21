package posts

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
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

// mustStoredImage 建一条已存图记录，返回短码。
func mustStoredImage(t *testing.T, repo *Repository, postID int64, order int) string {
	t.Helper()
	ctx := context.Background()
	if _, err := repo.AttachImage(ctx, Image{
		PostID: postID, SortOrder: order, Status: ImageStatusUploaded,
		MimeType: "image/png", FileSize: 10, TempFilePath: "tmp/x.part",
	}); err != nil {
		t.Fatalf("保存图片记录失败: %v", err)
	}
	images, err := repo.ListImages(ctx, postID)
	if err != nil {
		t.Fatalf("读取图片失败: %v", err)
	}
	for _, image := range images {
		if image.SortOrder != order {
			continue
		}
		if err := repo.MarkImageStored(ctx, image.ID, "remote-file-"+strconv.Itoa(order), 1, "telegram"); err != nil {
			t.Fatalf("标记存图失败: %v", err)
		}
		return image.ShortCode
	}
	t.Fatalf("没有找到序号 %d 的图片", order)
	return ""
}

func TestPublicImageRequiresPublicFlag(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()

	postID, err := repo.Create(ctx, Post{Title: "公开图", ImageCount: 1, UploadMode: "document"})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	shortCode := mustStoredImage(t, repo, postID, 1)
	if err := repo.MarkPostFinished(ctx, postID, StatusCompleted, ""); err != nil {
		t.Fatalf("标记完成失败: %v", err)
	}

	// 存图完成、帖已完成但未标记公开：旧任务升级后依然不可访问。
	if _, err := repo.PublicImage(ctx, shortCode); !errors.Is(err, ErrNotFound) {
		t.Fatalf("未公开帖应返回 ErrNotFound，实际: %v", err)
	}

	if err := repo.SetPublic(ctx, postID, true); err != nil {
		t.Fatalf("设置公开失败: %v", err)
	}
	image, err := repo.PublicImage(ctx, shortCode)
	if err != nil {
		t.Fatalf("公开图片查询失败: %v", err)
	}
	if image.RemoteFileID != "remote-file-1" || image.UploadMode != "document" || image.MIMEType != "image/png" {
		t.Errorf("字段不符: %+v", image)
	}

	if err := repo.SetPublic(ctx, postID, false); err != nil {
		t.Fatalf("取消公开失败: %v", err)
	}
	if _, err := repo.PublicImage(ctx, shortCode); !errors.Is(err, ErrNotFound) {
		t.Fatalf("取消公开后应返回 ErrNotFound，实际: %v", err)
	}
	if err := repo.SetPublic(ctx, 999, true); !errors.Is(err, ErrNotFound) {
		t.Errorf("不存在的帖子应返回 ErrNotFound，实际: %v", err)
	}
}

func TestPublicImageRejectsDisabledPost(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()

	postID, err := repo.Create(ctx, Post{Title: "禁用图", ImageCount: 1})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	shortCode := mustStoredImage(t, repo, postID, 1)
	if err := repo.MarkPostFinished(ctx, postID, StatusCompleted, ""); err != nil {
		t.Fatalf("标记完成失败: %v", err)
	}
	if err := repo.SetPublic(ctx, postID, true); err != nil {
		t.Fatalf("设置公开失败: %v", err)
	}
	if _, err := repo.handle.ExecContext(ctx, `UPDATE posts SET is_disabled = 1 WHERE id = ?`, postID); err != nil {
		t.Fatalf("禁用帖子失败: %v", err)
	}

	if _, err := repo.PublicImage(ctx, shortCode); !errors.Is(err, ErrNotFound) {
		t.Fatalf("禁用帖应返回 ErrNotFound，实际: %v", err)
	}
}

func TestPublicImageRejectsUnstoredAndUnknown(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()

	postID, err := repo.Create(ctx, Post{Title: "未存图", ImageCount: 1})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	if _, err := repo.AttachImage(ctx, Image{
		PostID: postID, SortOrder: 1, Status: ImageStatusUploaded,
		MimeType: "image/jpeg", TempFilePath: "tmp/y.part",
	}); err != nil {
		t.Fatalf("保存图片记录失败: %v", err)
	}
	images, err := repo.ListImages(ctx, postID)
	if err != nil || len(images) != 1 {
		t.Fatalf("读取图片失败: %v", err)
	}
	if err := repo.MarkPostFinished(ctx, postID, StatusCompleted, ""); err != nil {
		t.Fatalf("标记完成失败: %v", err)
	}
	if err := repo.SetPublic(ctx, postID, true); err != nil {
		t.Fatalf("设置公开失败: %v", err)
	}
	if _, err := repo.PublicImage(ctx, images[0].ShortCode); !errors.Is(err, ErrNotFound) {
		t.Fatalf("未存图应返回 ErrNotFound，实际: %v", err)
	}
	if _, err := repo.PublicImage(ctx, "doesnotexist"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("未知短码应返回 ErrNotFound，实际: %v", err)
	}
	if _, err := repo.PublicImage(ctx, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("空短码应返回 ErrNotFound，实际: %v", err)
	}
}

func TestRequestPublishRequiresCompleted(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()

	postID, err := repo.Create(ctx, Post{Title: "待发布", ImageCount: 1})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	if err := repo.RequestPublish(ctx, postID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("未完成帖发布错误 = %v，期望 ErrNotFound", err)
	}

	if err := repo.MarkPostFinished(ctx, postID, StatusCompleted, ""); err != nil {
		t.Fatalf("标记完成失败: %v", err)
	}
	if err := repo.RequestPublish(ctx, postID); err != nil {
		t.Fatalf("发布失败: %v", err)
	}

	info, err := repo.PublishInfo(ctx, postID)
	if err != nil {
		t.Fatalf("读取发布信息失败: %v", err)
	}
	if info.State != PublishPending {
		t.Errorf("状态 = %q，期望 pending", info.State)
	}
	var public int
	if err := repo.handle.QueryRowContext(ctx, `SELECT is_public FROM posts WHERE id = ?`, postID).Scan(&public); err != nil {
		t.Fatalf("读取公开标记失败: %v", err)
	}
	if public != 1 {
		t.Errorf("is_public = %d，期望 1", public)
	}

	if err := repo.RequestPublish(ctx, postID); !errors.Is(err, ErrNotFound) {
		t.Errorf("重复发布错误 = %v，期望 ErrNotFound", err)
	}
}

func TestClaimPublishableAndRecover(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()

	postID, err := repo.Create(ctx, Post{Title: "队列", ImageCount: 1})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	if err := repo.MarkPostFinished(ctx, postID, StatusCompleted, ""); err != nil {
		t.Fatalf("标记完成失败: %v", err)
	}
	if _, ok, err := repo.ClaimPublishable(ctx); err != nil || ok {
		t.Fatalf("空队列不应领取到任务: ok=%v err=%v", ok, err)
	}

	if err := repo.RequestPublish(ctx, postID); err != nil {
		t.Fatalf("发布失败: %v", err)
	}
	claimed, ok, err := repo.ClaimPublishable(ctx)
	if err != nil || !ok || claimed != postID {
		t.Fatalf("领取 = %d ok=%v err=%v", claimed, ok, err)
	}
	if _, ok, _ := repo.ClaimPublishable(ctx); ok {
		t.Error("同一任务不应被重复领取")
	}

	recovered, err := repo.RecoverPublish(ctx)
	if err != nil || recovered != 1 {
		t.Fatalf("恢复数量 = %d err=%v", recovered, err)
	}
	if claimed, ok, _ := repo.ClaimPublishable(ctx); !ok || claimed != postID {
		t.Error("恢复后应能重新领取")
	}
}

func TestSavePublishPathsAndMarkPublished(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()

	postID, err := repo.Create(ctx, Post{Title: "路径", ImageCount: 1})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	if err := repo.SavePublishPaths(ctx, postID, []string{"Path-1"}); err != nil {
		t.Fatalf("保存路径失败: %v", err)
	}
	info, err := repo.PublishInfo(ctx, postID)
	if err != nil {
		t.Fatalf("读取发布信息失败: %v", err)
	}
	if len(info.Paths) != 1 || info.Paths[0] != "Path-1" {
		t.Errorf("路径 = %v", info.Paths)
	}

	if err := repo.MarkPublished(ctx, postID, []string{"Path-1"}, []string{"https://telegra.ph/Path-1"}); err != nil {
		t.Fatalf("标记发布失败: %v", err)
	}
	info, err = repo.PublishInfo(ctx, postID)
	if err != nil {
		t.Fatalf("读取发布信息失败: %v", err)
	}
	if info.State != PublishPublished {
		t.Errorf("状态 = %q，期望 published", info.State)
	}

	var urls string
	if err := repo.handle.QueryRowContext(ctx, `SELECT telegraph_urls FROM posts WHERE id = ?`, postID).Scan(&urls); err != nil {
		t.Fatalf("读取链接失败: %v", err)
	}
	if !strings.Contains(urls, "https://telegra.ph/Path-1") {
		t.Errorf("链接 = %s", urls)
	}

	if err := repo.MarkPublishFailed(ctx, postID, "失败"); err != nil {
		t.Fatalf("标记失败失败: %v", err)
	}
	info, _ = repo.PublishInfo(ctx, postID)
	if info.State != PublishFailed {
		t.Errorf("状态 = %q，期望 failed", info.State)
	}
}
