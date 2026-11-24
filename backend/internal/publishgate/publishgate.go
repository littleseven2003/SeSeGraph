// Package publishgate 判断发布前置条件，手动发布与同步收尾共用同一口径。
// 条件不齐时只允许保留已存图状态，公开标记与短码链接都保持关闭。
package publishgate

import (
	"context"
	"errors"

	"github.com/littleseven2003/SeSeGraph/backend/internal/config"
	"github.com/littleseven2003/SeSeGraph/backend/internal/telegraph"
)

var (
	// ErrTelegraph 表示 Telegraph 未配置或客户端暂时取不到账户。
	ErrTelegraph = errors.New("Telegraph 未配置")
	// ErrPublicURL 表示对外地址不是结构完整的 HTTPS 地址。
	ErrPublicURL = errors.New("需要先配置公开 HTTPS 地址")
)

// Check 复核发布条件：Telegraph 可用且对外地址为 HTTPS。
// manager 为 nil 时视为未配置，调用方据此保留未公开状态。
func Check(ctx context.Context, manager *telegraph.Manager, publicURL string) error {
	if manager == nil {
		return ErrTelegraph
	}
	if _, err := manager.Client(ctx); err != nil {
		return ErrTelegraph
	}
	if !config.PublicURLIsHTTPS(publicURL) {
		return ErrPublicURL
	}
	return nil
}
