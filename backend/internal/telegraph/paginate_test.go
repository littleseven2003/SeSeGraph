package telegraph

import (
	"errors"
	"strings"
	"testing"
)

func repeatText(times int) string {
	return strings.Repeat("写真集说明文字，含中文与符号「」；", times)
}

func TestPaginateSinglePage(t *testing.T) {
	body := []Node{Paragraph("短说明"), Paragraph("第二段")}
	pages, err := Paginate(body)
	if err != nil {
		t.Fatalf("分页失败: %v", err)
	}
	if len(pages) != 1 {
		t.Fatalf("页数 = %d，期望 1", len(pages))
	}
	if pages[0].Index != 1 || pages[0].Total != 1 {
		t.Errorf("页码信息 = %+v", pages[0])
	}
	if len(pages[0].Body) != 2 {
		t.Errorf("正文节点数 = %d", len(pages[0].Body))
	}
}

func TestPaginateSplitsByUTF8Bytes(t *testing.T) {
	// 每段约 2KB 中文，40 段明显超过 56KB，必须分页。
	body := make([]Node, 0, 40)
	for index := 0; index < 40; index++ {
		body = append(body, Paragraph(repeatText(40)))
	}
	pages, err := Paginate(body)
	if err != nil {
		t.Fatalf("分页失败: %v", err)
	}
	if len(pages) < 2 {
		t.Fatalf("页数 = %d，期望多页", len(pages))
	}

	total := 0
	for _, page := range pages {
		bytes, err := ContentBytes(page.Body)
		if err != nil {
			t.Fatalf("计算字节失败: %v", err)
		}
		if bytes+NavReserveBytes > MaxPageContentBytes {
			t.Errorf("第 %d 页正文 %d 字节，加上导航超出 %d 上限", page.Index, bytes, MaxPageContentBytes)
		}
		total += len(page.Body)
	}
	if total != len(body) {
		t.Errorf("节点总数 = %d，期望 %d", total, len(body))
	}
	for index, page := range pages {
		if page.Index != index+1 || page.Total != len(pages) {
			t.Errorf("第 %d 页页码信息 = %+v", index+1, page)
		}
	}
}

func TestPaginateAssembledPagesStayUnderLimitWithNav(t *testing.T) {
	body := make([]Node, 0, 30)
	for index := 0; index < 30; index++ {
		body = append(body, Paragraph(repeatText(40)))
	}
	pages, err := Paginate(body)
	if err != nil {
		t.Fatalf("分页失败: %v", err)
	}
	if len(pages) < 2 {
		t.Fatalf("页数 = %d，期望多页", len(pages))
	}

	for index, page := range pages {
		prev, next := "", ""
		if index > 0 {
			prev = "https://telegra.ph/SeSe-Graph-02-01-2026"
		}
		if index < len(pages)-1 {
			next = "https://telegra.ph/SeSe-Graph-02-02-2026"
		}
		nav := NavNodes(prev, next, page.Index, page.Total)
		assembled := append(append([]Node{}, page.Body...), nav...)
		bytes, err := ContentBytes(assembled)
		if err != nil {
			t.Fatalf("计算字节失败: %v", err)
		}
		if bytes > MaxPageContentBytes {
			t.Errorf("第 %d 页含导航共 %d 字节，超过上限 %d", page.Index, bytes, MaxPageContentBytes)
		}
	}
}

func TestPaginateKeepsEscapedCharacters(t *testing.T) {
	text := "引号\"、反斜杠\\、换行\n、表情🎉与中文"
	pages, err := Paginate([]Node{Paragraph(text), Paragraph(text)})
	if err != nil {
		t.Fatalf("分页失败: %v", err)
	}
	if len(pages) != 1 {
		t.Fatalf("页数 = %d，期望 1", len(pages))
	}
	first, ok := pages[0].Body[0].Children[0].(string)
	if !ok || first != text {
		t.Errorf("转义字符被改写: %q", first)
	}
}

func TestPaginateRejectsOversizedNode(t *testing.T) {
	huge := Paragraph(strings.Repeat("长", MaxPageContentBytes))
	if _, err := Paginate([]Node{huge}); !errors.Is(err, ErrContentTooLarge) {
		t.Fatalf("错误 = %v，期望 ErrContentTooLarge", err)
	}
}

func TestPaginateRejectsEmptyBody(t *testing.T) {
	if _, err := Paginate(nil); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("错误 = %v，期望 ErrInvalidInput", err)
	}
}

func TestNavNodesShape(t *testing.T) {
	if nav := NavNodes("", "", 1, 1); nav != nil {
		t.Errorf("单页不应有导航: %+v", nav)
	}
	first := NavNodes("", "https://telegra.ph/b", 1, 3)
	last := NavNodes("https://telegra.ph/a", "", 3, 3)
	if len(first) != 1 || len(last) != 1 {
		t.Fatalf("导航节点数 = %d/%d", len(first), len(last))
	}
	if first[0].Tag != "p" || first[0].Children[0] != "第 1/3 页" {
		t.Errorf("首页导航 = %+v", first[0])
	}
	if !strings.Contains(describeChildren(last[0]), "上一页") {
		t.Errorf("末页导航缺少上一页链接: %+v", last[0])
	}
	if strings.Contains(describeChildren(first[0]), "上一页") {
		t.Errorf("首页不应出现上一页链接")
	}
}

func describeChildren(node Node) string {
	var builder strings.Builder
	for _, child := range node.Children {
		switch value := child.(type) {
		case string:
			builder.WriteString(value)
		case Node:
			builder.WriteString(value.Tag)
			for _, nested := range value.Children {
				if text, ok := nested.(string); ok {
					builder.WriteString(text)
				}
			}
		}
	}
	return builder.String()
}
