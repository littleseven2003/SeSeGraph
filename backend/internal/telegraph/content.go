package telegraph

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// Node 是 Telegraph content 数组中的一个节点。
// Children 允许字符串与嵌套节点混排，与 Telegraph 的 JSON 结构一致。
type Node struct {
	Tag      string            `json:"tag,omitempty"`
	Attrs    map[string]string `json:"attrs,omitempty"`
	Children []any             `json:"children,omitempty"`
}

// Paragraph 构造段落节点，文本放在 children 里。
func Paragraph(text string) Node {
	return Node{Tag: "p", Children: []any{text}}
}

// Page 是创建或编辑后的页面信息。
type Page struct {
	Path      string `json:"path"`
	URL       string `json:"url"`
	Title     string `json:"title"`
	AuthorURL string `json:"author_url,omitempty"`
}

// PageRequest 是创建或编辑页面的参数。
type PageRequest struct {
	Title      string
	AuthorName string
	Content    []Node
}

// MarshalContent 返回 content 数组的 UTF-8 JSON 字节，用于体积阈值判断与请求体构造。
func MarshalContent(content []Node) ([]byte, error) {
	encoded, err := json.Marshal(content)
	if err != nil {
		return nil, ErrInvalidInput
	}
	return encoded, nil
}

// normalize 校验标题与内容并补上作者名。
func (c *Client) normalize(request PageRequest) (url.Values, error) {
	title := strings.TrimSpace(request.Title)
	if title == "" {
		return nil, ErrInvalidInput
	}
	if len(request.Content) == 0 {
		return nil, ErrInvalidInput
	}
	encoded, err := MarshalContent(request.Content)
	if err != nil {
		return nil, err
	}
	// 发出请求前复核最终内容（含导航节点）不超过单页上限。
	if len(encoded) > MaxPageContentBytes {
		return nil, ErrContentTooLarge
	}

	author := strings.TrimSpace(request.AuthorName)
	if author == "" {
		author = c.authorName
	}
	if author == "" {
		author = "SeSeGraph"
	}

	form := url.Values{}
	form.Set("access_token", c.token)
	form.Set("title", title)
	form.Set("author_name", author)
	form.Set("content", string(encoded))
	form.Set("return_content", "false")
	return form, nil
}

// CreatePage 创建页面，返回 Telegraph 分配的路径与链接。
func (c *Client) CreatePage(ctx context.Context, request PageRequest) (Page, error) {
	if !c.Configured() {
		return Page{}, ErrNotConfigured
	}
	form, err := c.normalize(request)
	if err != nil {
		return Page{}, err
	}

	var result Page
	if err := c.post(ctx, "createPage", form, &result); err != nil {
		return Page{}, err
	}
	if strings.TrimSpace(result.Path) == "" {
		return Page{}, ErrUpstream
	}
	if strings.TrimSpace(result.URL) == "" {
		result.URL = "https://telegra.ph/" + strings.TrimPrefix(result.Path, "/")
	}
	return result, nil
}

// EditPage 修改已有页面，path 为创建时返回的路径。
func (c *Client) EditPage(ctx context.Context, path string, request PageRequest) (Page, error) {
	if !c.Configured() {
		return Page{}, ErrNotConfigured
	}
	path = strings.TrimPrefix(strings.TrimSpace(path), "/")
	if path == "" {
		return Page{}, ErrInvalidInput
	}
	form, err := c.normalize(request)
	if err != nil {
		return Page{}, err
	}
	form.Set("path", path)

	var result Page
	if err := c.post(ctx, "editPage", form, &result); err != nil {
		return Page{}, err
	}
	if strings.TrimSpace(result.Path) == "" {
		return Page{}, ErrUpstream
	}
	if strings.TrimSpace(result.URL) == "" {
		result.URL = "https://telegra.ph/" + strings.TrimPrefix(result.Path, "/")
	}
	return result, nil
}

// Describe 返回不含令牌的页面说明，便于日志与接口输出。
func (p Page) Describe() string {
	return fmt.Sprintf("页面 %s", strings.TrimPrefix(p.Path, "/"))
}
