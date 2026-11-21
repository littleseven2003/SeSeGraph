package telegraph

import (
	"encoding/json"
	"errors"
)

const (
	// MaxPageContentBytes 是单页 content 的 UTF-8 JSON 字节上限。
	// Telegraph 官方上限 64KB，这里留出安全余量。
	MaxPageContentBytes = 56 << 10
	// NavReserveBytes 为每页的导航节点预留字节数，含完整外链与页码文案。
	NavReserveBytes = 1024
)

// ErrContentTooLarge 表示单个节点在一页内都放不下。
var ErrContentTooLarge = errors.New("Telegraph 内容超过单页上限")

// PageBody 是一页的正文内容与页码信息。
type PageBody struct {
	Index int
	Total int
	Body  []Node
}

// ContentBytes 返回节点数组编码后的字节数，用于阈值判断。
func ContentBytes(content []Node) (int, error) {
	encoded, err := json.Marshal(content)
	if err != nil {
		return 0, ErrInvalidInput
	}
	return len(encoded), nil
}

// nodeBytes 返回单个节点编码后的字节数（不含数组分隔符）。
func nodeBytes(node Node) (int, error) {
	encoded, err := json.Marshal(node)
	if err != nil {
		return 0, ErrInvalidInput
	}
	return len(encoded), nil
}

// Paginate 按字节上限把正文切分为多页，并为每页预留导航空间。
// 页面顺序与正文顺序一致，所有节点恰好出现一次。
func Paginate(body []Node) ([]PageBody, error) {
	if len(body) == 0 {
		return nil, ErrInvalidInput
	}

	budget := MaxPageContentBytes - NavReserveBytes
	// JSON 数组的方括号占 2 字节。
	used := 2
	pages := make([][]Node, 0, 1)
	current := make([]Node, 0, len(body))

	for _, node := range body {
		size, err := nodeBytes(node)
		if err != nil {
			return nil, err
		}
		// 单节点自身就超过预算时，无论当前页是否已有正文都直接拒绝。
		if size+2 > budget {
			return nil, ErrContentTooLarge
		}
		separator := 0
		if len(current) > 0 {
			separator = 1
		}
		if used+separator+size > budget {
			pages = append(pages, current)
			current = make([]Node, 0, len(body))
			used = 2
			separator = 0
		}
		current = append(current, node)
		used += separator + size
	}
	if len(current) > 0 {
		pages = append(pages, current)
	}

	result := make([]PageBody, 0, len(pages))
	for index, nodes := range pages {
		result = append(result, PageBody{Index: index + 1, Total: len(pages), Body: nodes})
	}
	return result, nil
}

// Link 构造站内链接节点。
func Link(href, label string) Node {
	return Node{Tag: "a", Attrs: map[string]string{"href": href}, Children: []any{label}}
}

// NavNodes 生成页面导航节点：首页只有下一页，末页只有上一页，中间双向互链。
func NavNodes(prevURL, nextURL string, index, total int) []Node {
	if total <= 1 {
		return nil
	}
	children := make([]any, 0, 5)
	children = append(children, "第 "+itoa(index)+"/"+itoa(total)+" 页")
	if prevURL != "" {
		children = append(children, " · ", Link(prevURL, "上一页"))
	}
	if nextURL != "" {
		children = append(children, " · ", Link(nextURL, "下一页"))
	}
	if len(children) == 1 {
		return nil
	}
	return []Node{{Tag: "p", Children: children}}
}

// itoa 只处理页码这类小正整数，避免引入额外依赖。
func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := make([]byte, 0, 4)
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}
