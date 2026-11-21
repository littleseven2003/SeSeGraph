package publisher

import (
	"context"
	"fmt"
	"io"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/littleseven2003/SeSeGraph/backend/internal/db"
	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
	"github.com/littleseven2003/SeSeGraph/backend/internal/telegraph"
)

// fakePages 记录页面调用并支持按次数注入失败。
type fakePages struct {
	mu           sync.Mutex
	createCalls  int
	editCalls    int
	failCreateAt int
	created      []telegraph.PageRequest
	edited       []telegraph.PageRequest
	editedPaths  []string
}

func (f *fakePages) CreatePage(_ context.Context, request telegraph.PageRequest) (telegraph.Page, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalls++
	if f.failCreateAt > 0 && f.createCalls == f.failCreateAt {
		return telegraph.Page{}, telegraph.ErrUpstream
	}
	f.created = append(f.created, request)
	path := fmt.Sprintf("Page-%d", f.createCalls)
	return telegraph.Page{Path: path, URL: "https://telegra.ph/" + path}, nil
}

func (f *fakePages) EditPage(_ context.Context, path string, request telegraph.PageRequest) (telegraph.Page, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.editCalls++
	f.edited = append(f.edited, request)
	f.editedPaths = append(f.editedPaths, path)
	return telegraph.Page{Path: path, URL: "https://telegra.ph/" + path}, nil
}

func (f *fakePages) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.createCalls, f.editCalls
}

func (f *fakePages) clearFailure() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failCreateAt = 0
}

// publishEnv 准备数据库、仓储与已完成的帖子。
type publishEnv struct {
	repo  *posts.Repository
	pub   *Publisher
	pages *fakePages
}

func newPublishEnv(t *testing.T, images int, description string) *publishEnv {
	t.Helper()
	handle, err := db.Open(filepath.Join(t.TempDir(), "publish.db"))
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	if err := db.Migrate(context.Background(), handle); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}

	repo := posts.NewRepository(handle)
	ctx := context.Background()
	postID, err := repo.Create(ctx, posts.Post{
		Title: "发布测试", Description: description, ImageCount: images, UploadMode: "photo",
	})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	for order := 1; order <= images; order++ {
		if _, err := repo.AttachImage(ctx, posts.Image{
			PostID: postID, SortOrder: order, Status: posts.ImageStatusUploaded,
			MimeType: "image/jpeg", FileSize: 100, TempFilePath: "tmp/x.part",
		}); err != nil {
			t.Fatalf("保存图片失败: %v", err)
		}
	}
	list, err := repo.ListImages(ctx, postID)
	if err != nil {
		t.Fatalf("读取图片失败: %v", err)
	}
	for _, image := range list {
		if err := repo.MarkImageStored(ctx, image.ID, fmt.Sprintf("remote-%d", image.SortOrder), 1, "telegram"); err != nil {
			t.Fatalf("标记存图失败: %v", err)
		}
	}
	if err := repo.MarkPostFinished(ctx, postID, posts.StatusCompleted, ""); err != nil {
		t.Fatalf("标记完成失败: %v", err)
	}
	if err := repo.RequestPublish(ctx, postID); err != nil {
		t.Fatalf("请求发布失败: %v", err)
	}

	pages := &fakePages{}
	pub, err := New(Options{
		Repository: repo,
		Pages:      pages,
		PublicURL:  "https://images.example.com",
		Logger:     log.New(io.Discard, "", 0),
		Sleep:      func(context.Context, time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatalf("创建发布器失败: %v", err)
	}
	return &publishEnv{repo: repo, pub: pub, pages: pages}
}

func (e *publishEnv) publishedInfo(t *testing.T, postID int64) *posts.PublishInfo {
	t.Helper()
	info, err := e.repo.PublishInfo(context.Background(), postID)
	if err != nil {
		t.Fatalf("读取发布信息失败: %v", err)
	}
	return info
}

func TestPublisherSinglePage(t *testing.T) {
	env := newPublishEnv(t, 2, "第一段说明\n第二段说明")
	processed, err := env.pub.ProcessOnce(context.Background())
	if err != nil || !processed {
		t.Fatalf("处理结果 processed=%v err=%v", processed, err)
	}

	creates, edits := env.pages.counts()
	if creates != 1 || edits != 0 {
		t.Fatalf("创建 %d 次、编辑 %d 次，单页期望 1/0", creates, edits)
	}

	info := env.publishedInfo(t, 1)
	if info.State != posts.PublishPublished {
		t.Errorf("状态 = %q，期望 published", info.State)
	}
	if len(info.Paths) != 1 || len(info.Paths[0]) == 0 {
		t.Errorf("路径 = %v", info.Paths)
	}

	// 正文中的图片链接指向公开网关。
	content, err := telegraph.MarshalContent(env.pages.created[0].Content)
	if err != nil {
		t.Fatalf("序列化内容失败: %v", err)
	}
	if !strings.Contains(string(content), "https://images.example.com/i/") {
		t.Errorf("正文缺少图片网关链接: %s", content)
	}
}

func TestPublisherMultiPageMutualLinks(t *testing.T) {
	env := newPublishEnv(t, 400, "多页测试")
	processed, err := env.pub.ProcessOnce(context.Background())
	if err != nil || !processed {
		t.Fatalf("处理结果 processed=%v err=%v", processed, err)
	}

	creates, edits := env.pages.counts()
	if creates < 2 {
		t.Fatalf("创建页数 = %d，期望多页", creates)
	}
	if edits != creates {
		t.Errorf("编辑次数 = %d，期望与页数 %d 一致", edits, creates)
	}

	for index, content := range env.pages.edited {
		encoded, err := telegraph.MarshalContent(content.Content)
		if err != nil {
			t.Fatalf("序列化失败: %v", err)
		}
		if len(encoded) > telegraph.MaxPageContentBytes {
			t.Errorf("第 %d 页含导航共 %d 字节，超过上限", index+1, len(encoded))
		}
		if content.Title == "多页测试" && creates > 1 {
			t.Errorf("第 %d 页标题缺少分页标记: %q", index+1, content.Title)
		}
	}

	first, _ := telegraph.MarshalContent(env.pages.edited[0].Content)
	last, _ := telegraph.MarshalContent(env.pages.edited[len(env.pages.edited)-1].Content)
	if !strings.Contains(string(first), "下一页") || strings.Contains(string(first), "上一页") {
		t.Errorf("首页导航不符: %s", first)
	}
	if !strings.Contains(string(last), "上一页") || strings.Contains(string(last), "下一页") {
		t.Errorf("末页导航不符: %s", last)
	}
	if len(env.pages.edited) > 2 {
		middle, _ := telegraph.MarshalContent(env.pages.edited[1].Content)
		if !strings.Contains(string(middle), "上一页") || !strings.Contains(string(middle), "下一页") {
			t.Errorf("中间页应双向互链: %s", middle)
		}
	}
}

func TestPublisherRetriesReuseCreatedPages(t *testing.T) {
	env := newPublishEnv(t, 400, "重试测试")
	// 第 2 页创建失败，第一页路径应保留。
	env.pages.mu.Lock()
	env.pages.failCreateAt = 2
	env.pages.mu.Unlock()

	processed, err := env.pub.ProcessOnce(context.Background())
	if err == nil || !processed {
		t.Fatalf("首次发布应失败: processed=%v err=%v", processed, err)
	}
	info := env.publishedInfo(t, 1)
	if info.State != posts.PublishFailed {
		t.Errorf("状态 = %q，期望 failed", info.State)
	}
	if len(info.Paths) != 1 {
		t.Fatalf("失败后应保留 1 条路径，实际 %v", info.Paths)
	}
	firstPath := info.Paths[0]

	// 管理员重新确认发布，任务从 failed 回到队列。
	if err := env.repo.RequestPublish(context.Background(), 1); err != nil {
		t.Fatalf("重新请求发布失败: %v", err)
	}
	env.pages.clearFailure()
	if _, err := env.pub.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("重试发布失败: %v", err)
	}

	info = env.publishedInfo(t, 1)
	if info.State != posts.PublishPublished {
		t.Errorf("重试后状态 = %q，期望 published", info.State)
	}
	if info.Paths[0] != firstPath {
		t.Errorf("重试应复用首条路径: %s -> %s", firstPath, info.Paths[0])
	}

	// 首次 1 成功 + 1 失败 + 重试补齐其余页面；首条路径没有再次创建。
	creates, _ := env.pages.counts()
	if creates != len(info.Paths)+1 {
		t.Errorf("创建调用 %d 次，页数 %d，期望复用 1 条路径", creates, len(info.Paths))
	}
}

func TestPublisherSkipsWhenQueueEmpty(t *testing.T) {
	env := newPublishEnv(t, 1, "")
	if _, err := env.pub.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("首次处理失败: %v", err)
	}
	if err := env.repo.MarkPublished(context.Background(), 1, []string{"x"}, []string{"https://telegra.ph/x"}); err != nil {
		t.Fatalf("标记发布失败: %v", err)
	}
	processed, err := env.pub.ProcessOnce(context.Background())
	if err != nil || processed {
		t.Fatalf("空队列 processed=%v err=%v", processed, err)
	}
}
