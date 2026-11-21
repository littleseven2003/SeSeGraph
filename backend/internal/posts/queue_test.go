package posts

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func mustPost(t *testing.T, repo *Repository, title string) int64 {
	t.Helper()
	id, err := repo.Create(context.Background(), Post{Title: title, ImageCount: 1})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	return id
}

func TestRecoverPublishingResetsQueue(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()

	id := mustPost(t, repo, "中断任务")
	if err := repo.UpdateStatus(ctx, id, StatusPublishing); err != nil {
		t.Fatalf("更新状态失败: %v", err)
	}

	affected, err := repo.RecoverPublishing(ctx)
	if err != nil {
		t.Fatalf("恢复失败: %v", err)
	}
	if affected != 1 {
		t.Errorf("恢复条数 = %d，期望 1", affected)
	}

	stored, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("读取帖子失败: %v", err)
	}
	if stored.Status != StatusQueued {
		t.Errorf("状态 = %q，期望 queued", stored.Status)
	}
}

func TestClaimNextMarksPublishing(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()

	first := mustPost(t, repo, "第一条")
	second := mustPost(t, repo, "第二条")
	for _, id := range []int64{first, second} {
		if err := repo.UpdateStatus(ctx, id, StatusQueued); err != nil {
			t.Fatalf("更新状态失败: %v", err)
		}
	}

	claimed, ok, err := repo.ClaimNext(ctx)
	if err != nil || !ok {
		t.Fatalf("领取失败: ok=%v err=%v", ok, err)
	}
	if claimed != first {
		t.Errorf("领取 = %d，期望按编号取第一条 %d", claimed, first)
	}

	stored, _ := repo.Get(ctx, first)
	if stored.Status != StatusPublishing {
		t.Errorf("领取后状态 = %q，期望 publishing", stored.Status)
	}

	next, ok, err := repo.ClaimNext(ctx)
	if err != nil || !ok || next != second {
		t.Fatalf("第二次领取 = %d ok=%v err=%v", next, ok, err)
	}

	if _, ok, _ := repo.ClaimNext(ctx); ok {
		t.Error("队列为空时不应领取到任务")
	}
}

func TestClaimNextConcurrentReturnsDistinct(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()

	const total = 4
	for i := 0; i < total; i++ {
		id := mustPost(t, repo, "并发任务")
		if err := repo.UpdateStatus(ctx, id, StatusQueued); err != nil {
			t.Fatalf("更新状态失败: %v", err)
		}
	}

	var (
		mu      sync.Mutex
		claimed []int64
		wg      sync.WaitGroup
	)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, ok, err := repo.ClaimNext(ctx)
			if err != nil || !ok {
				return
			}
			mu.Lock()
			claimed = append(claimed, id)
			mu.Unlock()
		}()
	}
	wg.Wait()

	if len(claimed) != total {
		t.Errorf("领取数 = %d，期望 %d", len(claimed), total)
	}
	seen := map[int64]bool{}
	for _, id := range claimed {
		if seen[id] {
			t.Errorf("重复领取 %d", id)
		}
		seen[id] = true
	}
}

func TestCleanupOrphansKeepsReferencedFiles(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	dataDir := t.TempDir()
	now := time.Now()

	postID := mustPost(t, repo, "孤儿清理")
	if _, err := repo.AttachImage(ctx, Image{
		PostID: postID, SortOrder: 1, Status: ImageStatusUploaded,
		TempFilePath: "tmp/1/referenced.part",
	}); err != nil {
		t.Fatalf("保存图片记录失败: %v", err)
	}

	dir := filepath.Join(dataDir, TempDir, "1")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("创建目录失败: %v", err)
	}
	old := now.Add(-48 * time.Hour)
	files := map[string]time.Time{
		"referenced.part": old,
		"orphan-old.part": old,
		"orphan-new.part": now,
	}
	for name, modTime := range files {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatalf("写入文件失败: %v", err)
		}
		if err := os.Chtimes(path, modTime, modTime); err != nil {
			t.Fatalf("设置时间失败: %v", err)
		}
	}

	removed, err := repo.CleanupOrphans(ctx, dataDir, 24*time.Hour, now)
	if err != nil {
		t.Fatalf("清理失败: %v", err)
	}
	if removed != 1 {
		t.Errorf("清理数 = %d，期望 1", removed)
	}

	for name, want := range map[string]bool{"referenced.part": true, "orphan-old.part": false, "orphan-new.part": true} {
		_, statErr := os.Stat(filepath.Join(dir, name))
		exists := statErr == nil
		if exists != want {
			t.Errorf("%s 存在状态 = %v，期望 %v", name, exists, want)
		}
	}
}
