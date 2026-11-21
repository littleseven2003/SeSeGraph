package publisher

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/littleseven2003/SeSeGraph/backend/internal/db"
	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
	"github.com/littleseven2003/SeSeGraph/backend/internal/telegraph"
)

// upstreamCall 记录上游收到的表单与原始地址。
type upstreamCall struct {
	uri  string
	form map[string]string
}

// fakeUpstream 模拟 Telegraph 接口：编辑接口只接受 /editPage/{path}。
type fakeUpstream struct {
	mu      sync.Mutex
	creates []upstreamCall
	edits   []upstreamCall
	server  *httptest.Server
}

func newFakeUpstream(t *testing.T) *fakeUpstream {
	t.Helper()
	fake := &fakeUpstream{}
	fake.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		call := upstreamCall{uri: r.RequestURI, form: map[string]string{}}
		for key := range r.PostForm {
			call.form[key] = r.PostForm.Get(key)
		}

		w.Header().Set("Content-Type", "application/json")
		path := strings.TrimPrefix(r.URL.Path, "/")
		switch {
		case path == "createPage":
			fake.mu.Lock()
			fake.creates = append(fake.creates, call)
			index := len(fake.creates)
			fake.mu.Unlock()
			page := fmt.Sprintf("Page-%d", index)
			writeResult(w, map[string]any{"path": page, "url": "https://telegra.ph/" + page})
		case strings.HasPrefix(path, "editPage/"):
			fake.mu.Lock()
			fake.edits = append(fake.edits, call)
			fake.mu.Unlock()
			page := strings.TrimPrefix(path, "editPage/")
			writeResult(w, map[string]any{"path": page, "url": "https://telegra.ph/" + page})
		case path == "editPage":
			// 与上游一致：缺少页面路径时不接受。
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"ok":false,"error":"PATH_REQUIRED"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"ok":false,"error":"METHOD_NOT_FOUND"}`))
		}
	}))
	t.Cleanup(fake.server.Close)
	return fake
}

func writeResult(w http.ResponseWriter, result map[string]any) {
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
}

// newPublishRepository 建库并造一个待发布的多图帖子。
func newPublishRepository(t *testing.T, images int) *posts.Repository {
	t.Helper()
	handle, err := db.Open(filepath.Join(t.TempDir(), "upstream.db"))
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
		Title: "上游联调", Description: "端到端说明", ImageCount: images, UploadMode: "photo",
	})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	for order := 1; order <= images; order++ {
		if _, err := repo.AttachImage(ctx, posts.Image{
			PostID: postID, SortOrder: order, Status: posts.ImageStatusUploaded,
			MimeType: "image/jpeg", FileSize: 100, TempFilePath: "tmp/y.part",
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
	return repo
}

// TestPublishAgainstUpstreamShapes 用真实客户端打模拟上游，核对发布全链路。
func TestPublishAgainstUpstreamShapes(t *testing.T) {
	repo := newPublishRepository(t, 400)
	upstream := newFakeUpstream(t)
	client := telegraph.New(telegraph.Config{AccessToken: "token", APIBase: upstream.server.URL})

	pub, err := New(Options{
		Repository: repo,
		Pages:      func(context.Context) (PageAPI, error) { return client, nil },
		PublicURL:  "https://images.example.com",
		Logger:     log.New(io.Discard, "", 0),
		Sleep:      func(context.Context, time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatalf("创建发布器失败: %v", err)
	}
	if processed, err := pub.ProcessOnce(context.Background()); err != nil || !processed {
		t.Fatalf("处理结果 processed=%v err=%v", processed, err)
	}

	if len(upstream.creates) < 2 {
		t.Fatalf("创建页数 = %d，期望多页", len(upstream.creates))
	}
	if len(upstream.edits) != len(upstream.creates) {
		t.Fatalf("编辑 %d 次、创建 %d 次，期望一致", len(upstream.edits), len(upstream.creates))
	}

	total := len(upstream.edits)
	for index, call := range upstream.edits {
		if !strings.HasPrefix(call.uri, "/editPage/") {
			t.Errorf("第 %d 次编辑地址 = %q，期望 /editPage/<path>", index+1, call.uri)
		}
		if _, ok := call.form["path"]; ok {
			t.Errorf("第 %d 次编辑把路径放进了表单", index+1)
		}
		content := call.form["content"]
		if want := fmt.Sprintf("第 %d/%d 页", index+1, total); !strings.Contains(content, want) {
			t.Errorf("第 %d 页正文缺少页码 %q", index+1, want)
		}
		hasPrev := strings.Contains(content, "上一页")
		hasNext := strings.Contains(content, "下一页")
		if index == 0 && (hasPrev || !hasNext) {
			t.Errorf("首页导航 prev=%v next=%v，期望只有下一页", hasPrev, hasNext)
		}
		if index == total-1 && (!hasPrev || hasNext) {
			t.Errorf("末页导航 prev=%v next=%v，期望只有上一页", hasPrev, hasNext)
		}
		if index > 0 && index < total-1 && (!hasPrev || !hasNext) {
			t.Errorf("中间页导航 prev=%v next=%v，期望双向", hasPrev, hasNext)
		}
	}

	info, err := repo.PublishInfo(context.Background(), 1)
	if err != nil {
		t.Fatalf("读取发布信息失败: %v", err)
	}
	if info.State != posts.PublishPublished {
		t.Errorf("状态 = %q，期望 published", info.State)
	}
	if len(info.Paths) != total {
		t.Errorf("落库路径 = %d 条，期望 %d 条", len(info.Paths), total)
	}
}
