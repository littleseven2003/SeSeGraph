package publishgate

import (
	"context"
	"errors"
	"testing"

	"github.com/littleseven2003/SeSeGraph/backend/internal/telegraph"
)

// TestCheckRequiresTelegraph 验证未装配账户时按未配置处理，不放到公开地址检查。
func TestCheckRequiresTelegraph(t *testing.T) {
	if err := Check(context.Background(), nil, "https://sese.example.com"); !errors.Is(err, ErrTelegraph) {
		t.Fatalf("err = %v，期望 ErrTelegraph", err)
	}
}

// TestCheckRequiresHTTPS 验证有账户但对外地址不是 HTTPS 时拒绝发布。
func TestCheckRequiresHTTPS(t *testing.T) {
	manager := telegraph.NewManager(telegraph.Config{AccessToken: "token"}, nil)
	ctx := context.Background()
	if err := Check(ctx, manager, "http://sese.example.com"); !errors.Is(err, ErrPublicURL) {
		t.Fatalf("err = %v，期望 ErrPublicURL", err)
	}
	if err := Check(ctx, manager, "https://sese.example.com"); err != nil {
		t.Fatalf("条件齐备时应通过，实际 %v", err)
	}
}
