package settings

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/littleseven2003/SeSeGraph/backend/internal/db"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	handle, err := db.Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	if err := db.Migrate(context.Background(), handle); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	return NewStore(handle)
}

func TestStoreSetGetOverwriteDelete(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()

	if _, ok, err := store.Get(ctx, KeyTelegraphToken); err != nil || ok {
		t.Fatalf("初始读取 ok=%v err=%v，期望不存在", ok, err)
	}
	if err := store.Set(ctx, KeyTelegraphToken, "first"); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	value, ok, err := store.Get(ctx, KeyTelegraphToken)
	if err != nil || !ok || value != "first" {
		t.Fatalf("读取 = %q ok=%v err=%v", value, ok, err)
	}
	if err := store.Set(ctx, KeyTelegraphToken, "second"); err != nil {
		t.Fatalf("覆盖失败: %v", err)
	}
	if value, _, _ := store.Get(ctx, KeyTelegraphToken); value != "second" {
		t.Errorf("覆盖后 = %q，期望 second", value)
	}
	if err := store.Delete(ctx, KeyTelegraphToken); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if _, ok, _ := store.Get(ctx, KeyTelegraphToken); ok {
		t.Error("删除后仍能读到")
	}
	if err := store.Delete(ctx, KeyTelegraphToken); err != nil {
		t.Errorf("重复删除应成功: %v", err)
	}
}

func TestStoreWithoutHandle(t *testing.T) {
	ctx := context.Background()
	var store *Store
	if _, _, err := store.Get(ctx, "x"); err == nil {
		t.Error("空存储读取应报错")
	}
	if err := store.Set(ctx, "x", "y"); err == nil {
		t.Error("空存储写入应报错")
	}
	if err := NewStore(nil).Set(ctx, "x", "y"); err == nil {
		t.Error("缺少数据库时写入应报错")
	}
}

func TestStoreSnapshotReadsAll(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	if err := store.SetMany(ctx, map[string]string{
		KeyTelegramToken:  "123:token",
		KeyTelegraphToken: "tp-token",
	}); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	values, err := store.Snapshot(ctx)
	if err != nil {
		t.Fatalf("快照失败: %v", err)
	}
	if values[KeyTelegramToken] != "123:token" || values[KeyTelegraphToken] != "tp-token" {
		t.Errorf("快照 = %v，期望包含两项配置", values)
	}
}

func TestStoreReplaceRollsBackOnFailure(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	if err := store.Set(ctx, KeyTelegramToken, "old-token"); err != nil {
		t.Fatalf("准备数据失败: %v", err)
	}

	// 用触发器让第二项写入失败，验证删除与写入整体回滚。
	if _, err := store.handle.ExecContext(ctx,
		`CREATE TRIGGER fail_boom BEFORE INSERT ON settings
		 WHEN NEW.key = 'boom' BEGIN SELECT RAISE(ABORT, 'fail'); END`); err != nil {
		t.Fatalf("创建触发器失败: %v", err)
	}

	err := store.Replace(ctx,
		map[string]string{KeyTelegraphAuthorName: "SeSe", "boom": "x"},
		[]string{KeyTelegramToken})
	if err == nil {
		t.Fatal("写入失败时应返回错误")
	}

	values, err := store.Snapshot(ctx)
	if err != nil {
		t.Fatalf("快照失败: %v", err)
	}
	if values[KeyTelegramToken] != "old-token" {
		t.Errorf("被删除的配置未回滚: %v", values)
	}
	if _, ok := values[KeyTelegraphAuthorName]; ok {
		t.Errorf("失败事务的写入不应落库: %v", values)
	}
	if _, ok := values["boom"]; ok {
		t.Errorf("失败项不应落库: %v", values)
	}
}
