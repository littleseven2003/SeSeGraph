package posts

import (
	"context"
	"testing"
)

// TestMarkImageStoredRecordsOriginAndRef 验证存图时记录来源 Bot 与原频道，
// 并把这次取得的文件引用登记到该 Bot 名下。
func TestMarkImageStoredRecordsOriginAndRef(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()

	postID, err := repo.Create(ctx, Post{Title: "来源记录", ImageCount: 1, UploadMode: "photo"})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	if _, err := repo.AttachImage(ctx, Image{
		PostID: postID, SortOrder: 1, Status: ImageStatusUploaded,
		MimeType: "image/png", FileSize: 10, TempFilePath: "tmp/1.part",
	}); err != nil {
		t.Fatalf("保存图片失败: %v", err)
	}
	images, err := repo.ListImages(ctx, postID)
	if err != nil || len(images) != 1 {
		t.Fatalf("读取图片失败: %v", err)
	}
	image := images[0]

	if err := repo.MarkImageStoredWithOrigin(ctx, image.ID, "file-a", 501, "telegram", 9001, "-100777"); err != nil {
		t.Fatalf("记录存图失败: %v", err)
	}

	var (
		botID  int64
		chatID string
		fileID string
		msgID  int64
	)
	if err := repo.handle.QueryRowContext(ctx,
		`SELECT source_bot_id, source_chat_id, remote_file_id, remote_message_id FROM post_images WHERE id = ?`, image.ID).
		Scan(&botID, &chatID, &fileID, &msgID); err != nil {
		t.Fatalf("读取来源失败: %v", err)
	}
	if botID != 9001 || chatID != "-100777" || fileID != "file-a" || msgID != 501 {
		t.Fatalf("来源记录 = %d/%q/%q/%d，期望 9001/-100777/file-a/501", botID, chatID, fileID, msgID)
	}

	refs, err := repo.ImageFileRefs(ctx, image.ID)
	if err != nil {
		t.Fatalf("读取文件引用失败: %v", err)
	}
	if len(refs) != 1 || refs[0].BotID != 9001 || refs[0].FileID != "file-a" {
		t.Fatalf("文件引用 = %+v，期望 9001/file-a", refs)
	}
}

// TestSetImageFileRefKeepsOneRowPerBot 验证同一图片同一 Bot 只保留一条引用，
// 重复登记时更新为新值，不同 Bot 各自一行。
func TestSetImageFileRefKeepsOneRowPerBot(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()

	postID, err := repo.Create(ctx, Post{Title: "接管引用", ImageCount: 1, UploadMode: "photo"})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	if _, err := repo.AttachImage(ctx, Image{
		PostID: postID, SortOrder: 1, Status: ImageStatusUploaded,
		MimeType: "image/png", FileSize: 10, TempFilePath: "tmp/1.part",
	}); err != nil {
		t.Fatalf("保存图片失败: %v", err)
	}
	images, err := repo.ListImages(ctx, postID)
	if err != nil || len(images) != 1 {
		t.Fatalf("读取图片失败: %v", err)
	}
	image := images[0]

	if err := repo.SetImageFileRef(ctx, image.ID, 9002, "from-b-1"); err != nil {
		t.Fatalf("登记引用失败: %v", err)
	}
	if err := repo.SetImageFileRef(ctx, image.ID, 9002, "from-b-2"); err != nil {
		t.Fatalf("更新引用失败: %v", err)
	}
	if err := repo.SetImageFileRef(ctx, image.ID, 9003, "from-c-1"); err != nil {
		t.Fatalf("登记第二引用失败: %v", err)
	}

	refs, err := repo.ImageFileRefs(ctx, image.ID)
	if err != nil {
		t.Fatalf("读取引用失败: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("引用条数 = %d，期望 2", len(refs))
	}
	if refs[0].BotID != 9002 || refs[0].FileID != "from-b-2" {
		t.Fatalf("Bot 9002 的引用 = %+v，期望更新为 from-b-2", refs[0])
	}
	if refs[1].BotID != 9003 || refs[1].FileID != "from-c-1" {
		t.Fatalf("Bot 9003 的引用 = %+v，期望 from-c-1", refs[1])
	}
}
