package posts

import (
	"context"
	"errors"
	"testing"
)

// TestToggleDisabledPreservesPublic 验证熔断开关只改 is_disabled，并保留原有公开授权。
func TestToggleDisabledPreservesPublic(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	postID, err := repo.Create(ctx, Post{Title: "撤回测试", ImageCount: 1})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	if err := repo.SetPublic(ctx, postID, true); err != nil {
		t.Fatalf("设置公开失败: %v", err)
	}

	disabled, public, err := repo.ToggleDisabled(ctx, postID)
	if err != nil {
		t.Fatalf("撤回失败: %v", err)
	}
	if !disabled || !public {
		t.Errorf("首次切换 = disabled %v public %v，期望禁用且公开", disabled, public)
	}

	disabled, public, err = repo.ToggleDisabled(ctx, postID)
	if err != nil {
		t.Fatalf("恢复失败: %v", err)
	}
	if disabled || !public {
		t.Errorf("再次切换 = disabled %v public %v，期望未禁用且公开", disabled, public)
	}

	if _, _, err := repo.ToggleDisabled(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("不存在帖子错误 = %v，期望 ErrNotFound", err)
	}
}
