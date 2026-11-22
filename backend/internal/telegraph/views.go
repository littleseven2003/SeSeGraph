package telegraph

import (
	"context"
	"net/url"
	"strings"
)

// GetViews 读取一页的累计浏览量。上游默认只返回一个整数 result.views，
// 缺失、null 与非整数都按上游失败处理，避免把旧缓存覆盖成错误的 0。
// 该接口不要求访问令牌，这里也不发送。
func (c *Client) GetViews(ctx context.Context, path string) (int, error) {
	path = strings.TrimPrefix(strings.TrimSpace(path), "/")
	if path == "" || strings.ContainsAny(path, "\r\n\t /\\?#") {
		return 0, ErrInvalidInput
	}
	var result struct {
		Views *int `json:"views"`
	}
	if err := c.post(ctx, "getViews/"+url.PathEscape(path), url.Values{}, &result); err != nil {
		return 0, err
	}
	if result.Views == nil || *result.Views < 0 {
		return 0, ErrUpstream
	}
	return *result.Views, nil
}
