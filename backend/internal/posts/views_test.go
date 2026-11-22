package posts

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// seedViewsPost 建一帖并直接写入发布状态、公开与路径，供浏览量用例复用。
func seedViewsPost(t *testing.T, repo *Repository, title, state string, public bool, paths []string) int64 {
	t.Helper()
	ctx := context.Background()
	id, err := repo.Create(ctx, Post{Title: title, ImageCount: 1})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	if public {
		if err := repo.SetPublic(ctx, id, true); err != nil {
			t.Fatalf("设置公开失败: %v", err)
		}
	}
	raw := "[]"
	if len(paths) > 0 {
		encoded, err := encodePaths(paths)
		if err != nil {
			t.Fatalf("编码路径失败: %v", err)
		}
		raw = encoded
	}
	if _, err := repo.handle.ExecContext(ctx,
		`UPDATE posts SET publish_state = ?, telegraph_paths = ? WHERE id = ?`, state, raw, id); err != nil {
		t.Fatalf("写入发布状态失败: %v", err)
	}
	return id
}

// encodePaths 把路径切片编码成库里的 JSON 文本。
func encodePaths(paths []string) (string, error) {
	encoded, err := json.Marshal(paths)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func TestListViewTargetsOnlyEligibleAndFirstPath(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()

	okID := seedViewsPost(t, repo, "已发布", PublishPublished, true, []string{"First-11-22", "Second-11-22"})
	seedViewsPost(t, repo, "未公开", PublishPublished, false, []string{"Hidden-11-22"})
	seedViewsPost(t, repo, "未发布", PublishIdle, true, []string{"Idle-11-22"})
	seedViewsPost(t, repo, "首篇缺失", PublishPublished, true, []string{"", "Second-11-22"})
	disabledID := seedViewsPost(t, repo, "已撤回", PublishPublished, true, []string{"Hidden-11-23"})
	if _, _, err := repo.SetDisabled(ctx, disabledID, true); err != nil {
		t.Fatalf("设置撤回失败: %v", err)
	}

	targets, err := repo.ListViewTargets(ctx, 0, 10)
	if err != nil {
		t.Fatalf("读取同步目标失败: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("目标条数 = %d，期望 1", len(targets))
	}
	if targets[0].PostID != okID {
		t.Errorf("目标帖子 = %d，期望 %d", targets[0].PostID, okID)
	}
	if targets[0].Path != "First-11-22" {
		t.Errorf("路径 = %q，期望只取首篇", targets[0].Path)
	}

	_, eligible, err := repo.ViewTargetByID(ctx, okID)
	if err != nil || !eligible {
		t.Fatalf("已发布帖应当可统计：eligible=%v err=%v", eligible, err)
	}
	if _, eligible, err := repo.ViewTargetByID(ctx, disabledID); err != nil || eligible {
		t.Fatalf("已撤回帖不应可统计：eligible=%v err=%v", eligible, err)
	}
	if _, _, err := repo.ViewTargetByID(ctx, 99999); err != ErrNotFound {
		t.Fatalf("不存在的帖子错误 = %v，期望 ErrNotFound", err)
	}
}

func TestSaveViewsKeepsZeroAndIgnoresStaleTarget(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	id := seedViewsPost(t, repo, "已发布", PublishPublished, true, []string{"Page-11-22"})

	snapshot, err := repo.ReadViews(ctx, id)
	if err != nil {
		t.Fatalf("读取缓存失败: %v", err)
	}
	if snapshot.Count != 0 || snapshot.UpdatedAt != "" {
		t.Fatalf("初始缓存 = %+v，期望未同步", snapshot)
	}

	// 首次同步到的真实零浏览也要落库，并与时间戳区分于「尚未同步」。
	at := time.Date(2026, 11, 22, 8, 0, 0, 0, time.UTC)
	applied, err := repo.SaveViews(ctx, ViewTarget{PostID: id, Path: "Page-11-22"}, 0, at)
	if err != nil || !applied {
		t.Fatalf("写回零浏览失败：applied=%v err=%v", applied, err)
	}
	snapshot, err = repo.ReadViews(ctx, id)
	if err != nil {
		t.Fatalf("读取缓存失败: %v", err)
	}
	if snapshot.Count != 0 || snapshot.UpdatedAt != "2026-11-22T08:00:00Z" {
		t.Fatalf("零浏览缓存 = %+v", snapshot)
	}

	// 列表与详情都要透出缓存值。
	page, err := repo.ListGallery(ctx, GalleryQuery{})
	if err != nil {
		t.Fatalf("读取画廊失败: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ViewsUpdatedAt != "2026-11-22T08:00:00Z" {
		t.Fatalf("列表浏览量 = %+v", page.Items)
	}
	detail, err := repo.GalleryDetail(ctx, id)
	if err != nil {
		t.Fatalf("读取详情失败: %v", err)
	}
	if detail.ViewsCount != 0 || detail.ViewsUpdatedAt != "2026-11-22T08:00:00Z" {
		t.Fatalf("详情浏览量 = %d / %q", detail.ViewsCount, detail.ViewsUpdatedAt)
	}

	// 改链后旧目标的在途结果不能覆盖新记录。
	applied, err = repo.SaveViews(ctx, ViewTarget{PostID: id, Path: "Other-11-22"}, 42, at.Add(time.Hour))
	if err != nil || applied {
		t.Fatalf("过期目标不应写回：applied=%v err=%v", applied, err)
	}
	snapshot, _ = repo.ReadViews(ctx, id)
	if snapshot.Count != 0 {
		t.Fatalf("过期目标覆盖了缓存：%+v", snapshot)
	}
}

func TestSaveViewsSkipsWithdrawnPost(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	id := seedViewsPost(t, repo, "已发布", PublishPublished, true, []string{"Page-11-23"})
	if _, _, err := repo.SetDisabled(ctx, id, true); err != nil {
		t.Fatalf("设置撤回失败: %v", err)
	}

	applied, err := repo.SaveViews(ctx, ViewTarget{PostID: id, Path: "Page-11-23"}, 9, time.Now())
	if err != nil {
		t.Fatalf("写回失败: %v", err)
	}
	if applied {
		t.Fatal("撤回后不应写回浏览量")
	}
	snapshot, err := repo.ReadViews(ctx, id)
	if err != nil {
		t.Fatalf("读取缓存失败: %v", err)
	}
	if snapshot.UpdatedAt != "" {
		t.Fatalf("撤回帖写入了时间戳：%+v", snapshot)
	}
}

func TestSaveViewsDoesNotTouchUpdatedAt(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	id := seedViewsPost(t, repo, "已发布", PublishPublished, true, []string{"Page-11-24"})

	var before string
	if err := repo.handle.QueryRowContext(ctx,
		`SELECT strftime('%Y-%m-%dT%H:%M:%SZ', updated_at) FROM posts WHERE id = ?`, id).Scan(&before); err != nil {
		t.Fatalf("读取更新时间失败: %v", err)
	}
	if _, err := repo.SaveViews(ctx, ViewTarget{PostID: id, Path: "Page-11-24"}, 12, time.Now()); err != nil {
		t.Fatalf("写回失败: %v", err)
	}
	var after string
	if err := repo.handle.QueryRowContext(ctx,
		`SELECT strftime('%Y-%m-%dT%H:%M:%SZ', updated_at) FROM posts WHERE id = ?`, id).Scan(&after); err != nil {
		t.Fatalf("读取更新时间失败: %v", err)
	}
	if before != after {
		t.Fatalf("同步浏览量改动了编辑时间：%q -> %q", before, after)
	}
}
