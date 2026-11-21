package posts

import (
	"context"
	"errors"
	"testing"
)

// TestSetDisabledIdempotent 验证熔断开关按目标值写入：重复撤回不会反向恢复，且保留公开授权。
func TestSetDisabledIdempotent(t *testing.T) {
	repo := newTestRepository(t)
	ctx := context.Background()
	postID, err := repo.Create(ctx, Post{Title: "撤回测试", ImageCount: 1})
	if err != nil {
		t.Fatalf("创建帖子失败: %v", err)
	}
	if err := repo.SetPublic(ctx, postID, true); err != nil {
		t.Fatalf("设置公开失败: %v", err)
	}

	disabled, public, err := repo.SetDisabled(ctx, postID, true)
	if err != nil {
		t.Fatalf("撤回失败: %v", err)
	}
	if !disabled || !public {
		t.Errorf("首次撤回 = disabled %v public %v，期望禁用且公开", disabled, public)
	}

	// 重复撤回：仍是禁用状态，客户端重试不会把图片重新公开。
	disabled, public, err = repo.SetDisabled(ctx, postID, true)
	if err != nil {
		t.Fatalf("重复撤回失败: %v", err)
	}
	if !disabled || !public {
		t.Errorf("重复撤回 = disabled %v public %v，期望仍禁用且公开", disabled, public)
	}

	disabled, public, err = repo.SetDisabled(ctx, postID, false)
	if err != nil {
		t.Fatalf("恢复失败: %v", err)
	}
	if disabled || !public {
		t.Errorf("恢复 = disabled %v public %v，期望未禁用且公开", disabled, public)
	}

	// 重复恢复同样保持未禁用。
	disabled, public, err = repo.SetDisabled(ctx, postID, false)
	if err != nil {
		t.Fatalf("重复恢复失败: %v", err)
	}
	if disabled || !public {
		t.Errorf("重复恢复 = disabled %v public %v，期望仍未禁用且公开", disabled, public)
	}

	if _, _, err := repo.SetDisabled(ctx, 999, true); !errors.Is(err, ErrNotFound) {
		t.Errorf("不存在帖子错误 = %v，期望 ErrNotFound", err)
	}
}
