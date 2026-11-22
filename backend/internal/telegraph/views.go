package telegraph

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"strings"
)

// GetViews 读取一页的累计浏览量。上游不要求访问令牌，这里也不发送。
// Telegraph 按年、月、日、时逐层嵌套返回计数，这里把叶子节点累加成总数。
func (c *Client) GetViews(ctx context.Context, path string) (int, error) {
	path = strings.TrimPrefix(strings.TrimSpace(path), "/")
	if path == "" || strings.ContainsAny(path, "\r\n\t /\\?#") {
		return 0, ErrInvalidInput
	}
	var result struct {
		Views json.RawMessage `json:"views"`
	}
	if err := c.post(ctx, "getViews/"+url.PathEscape(path), url.Values{}, &result); err != nil {
		return 0, err
	}
	return sumViews(result.Views), nil
}

// sumViews 递归累加嵌套计数，结构异常的部分按 0 处理。
func sumViews(raw json.RawMessage) int {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return 0
	}
	var number float64
	if err := json.Unmarshal(raw, &number); err == nil {
		if number <= 0 {
			return 0
		}
		return int(number)
	}
	var nested map[string]json.RawMessage
	if err := json.Unmarshal(raw, &nested); err != nil {
		return 0
	}
	total := 0
	for _, child := range nested {
		total += sumViews(child)
	}
	return total
}
