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
