package telegraph

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// pageServer 模拟 createPage / editPage。
// 编辑接口与上游一致，只接受 /editPage/{path} 形式，路径放在表单里会被判为不合法。
type pageServer struct {
	mu       sync.Mutex
	method   string
	lastForm map[string]string
	fail     bool
	noPath   bool
	badShape bool
	rawURI   string
	server   *httptest.Server
}

func newPageServer(t *testing.T) *pageServer {
	t.Helper()
	ps := &pageServer{lastForm: map[string]string{}}
	ps.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		callPath := strings.TrimPrefix(r.URL.Path, "/")
		// 编辑接口必须在 URL 上带页面路径，否则视为请求形态错误。
		badShape := callPath == "editPage"
		ps.mu.Lock()
		ps.method = callPath
		ps.rawURI = r.RequestURI
		if badShape {
			ps.badShape = true
		}
		for key := range r.PostForm {
			ps.lastForm[key] = r.PostForm.Get(key)
		}
		fail, noPath := ps.fail, ps.noPath
		ps.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if badShape {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"ok":false,"error":"PATH_REQUIRED"}`))
			return
		}
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"ok":false,"error":"TOKEN_LEAK"}`))
			return
		}
		path := "Test-Page-01"
		if noPath {
			path = ""
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"result": map[string]any{
				"path": path,
				"url":  "https://telegra.ph/" + path,
			},
		})
	}))
	t.Cleanup(ps.server.Close)
	return ps
}

// requestURI 返回原始请求地址，用于核对路径转义。
func (p *pageServer) requestURI() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.rawURI
}

func (p *pageServer) requestForm() (string, map[string]string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	copied := make(map[string]string, len(p.lastForm))
	for key, value := range p.lastForm {
		copied[key] = value
	}
	return p.method, copied
}

func TestCreatePageSendsContentAndReturnsURL(t *testing.T) {
	server := newPageServer(t)
	client := New(Config{AccessToken: "secret-access-token", AuthorName: "SeSe", APIBase: server.server.URL})

	content := []Node{
		Paragraph("第一段说明"),
		{Tag: "figure", Children: []any{
			Node{Tag: "img", Attrs: map[string]string{"src": "https://example.com/i/abc12345"}},
			Node{Tag: "figcaption", Children: []any{"第一张"}},
		}},
	}
	page, err := client.CreatePage(context.Background(), PageRequest{Title: "测试帖子", Content: content})
	if err != nil {
		t.Fatalf("创建页面失败: %v", err)
	}
	if page.Path != "Test-Page-01" || page.URL != "https://telegra.ph/Test-Page-01" {
		t.Errorf("页面信息 = %+v", page)
	}

	method, form := server.requestForm()
	if method != "createPage" {
		t.Errorf("调用方法 = %q，期望 createPage", method)
	}
	if form["title"] != "测试帖子" || form["author_name"] != "SeSe" {
		t.Errorf("表单字段不符: %v", form)
	}
	if form["access_token"] != "secret-access-token" {
		t.Errorf("访问令牌未随请求发送")
	}

	var decoded []Node
	if err := json.Unmarshal([]byte(form["content"]), &decoded); err != nil {
		t.Fatalf("content 不是合法 JSON: %v", err)
	}
	if len(decoded) != 2 || decoded[0].Tag != "p" || decoded[1].Tag != "figure" {
		t.Errorf("content 结构不符: %+v", decoded)
	}
}

func TestEditPageSendsPathInURL(t *testing.T) {
	server := newPageServer(t)
	client := New(Config{AccessToken: "secret-access-token", APIBase: server.server.URL})

	_, err := client.EditPage(context.Background(), "/Test-Page-01", PageRequest{
		Title:   "改过的标题",
		Content: []Node{Paragraph("新说明")},
	})
	if err != nil {
		t.Fatalf("编辑页面失败: %v", err)
	}
	method, form := server.requestForm()
	if method != "editPage/Test-Page-01" {
		t.Errorf("调用路径 = %q，期望 editPage/Test-Page-01", method)
	}
	if _, ok := form["path"]; ok {
		t.Errorf("路径不应放在表单里，实际表单: %v", form)
	}
	if form["title"] != "改过的标题" {
		t.Errorf("表单缺少标题: %v", form)
	}
	if server.badShape {
		t.Error("不应退回不带路径的 editPage 请求")
	}
}

func TestEditPageEscapesPath(t *testing.T) {
	server := newPageServer(t)
	client := New(Config{AccessToken: "secret-access-token", APIBase: server.server.URL})

	if _, err := client.EditPage(context.Background(), "/Page%2F1", PageRequest{
		Title:   "转义检查",
		Content: []Node{Paragraph("x")},
	}); err != nil {
		t.Fatalf("编辑页面失败: %v", err)
	}
	if uri := server.requestURI(); uri != "/editPage/Page%252F1" {
		t.Errorf("原始请求地址 = %q，期望转义百分号", uri)
	}
	if server.badShape {
		t.Error("不应退回不带路径的 editPage 请求")
	}
}

func TestEditPageRejectsInvalidPath(t *testing.T) {
	server := newPageServer(t)
	client := New(Config{AccessToken: "secret-access-token", APIBase: server.server.URL})

	for _, path := range []string{"", "   ", "/", "Page 1", "Page\n1"} {
		if _, err := client.EditPage(context.Background(), path, PageRequest{
			Title:   "非法路径",
			Content: []Node{Paragraph("x")},
		}); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("路径 %q 的错误 = %v，期望 ErrInvalidInput", path, err)
		}
	}
	if uri := server.requestURI(); uri != "" {
		t.Errorf("非法路径不应发出请求，实际 %q", uri)
	}
}

func TestContentRequiresTokenAndFields(t *testing.T) {
	client := New(Config{})
	if _, err := client.CreatePage(context.Background(), PageRequest{Title: "t", Content: []Node{Paragraph("x")}}); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("缺少令牌时错误 = %v，期望 ErrNotConfigured", err)
	}

	configured := New(Config{AccessToken: "token"})
	if _, err := configured.CreatePage(context.Background(), PageRequest{Title: "  ", Content: []Node{Paragraph("x")}}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("空标题时错误 = %v，期望 ErrInvalidInput", err)
	}
	if _, err := configured.CreatePage(context.Background(), PageRequest{Title: "标题"}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("空内容时错误 = %v，期望 ErrInvalidInput", err)
	}
	if _, err := configured.EditPage(context.Background(), "", PageRequest{Title: "标题", Content: []Node{Paragraph("x")}}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("空路径时错误 = %v，期望 ErrInvalidInput", err)
	}
}

func TestCreatePageUpstreamFailures(t *testing.T) {
	server := newPageServer(t)
	server.fail = true
	client := New(Config{AccessToken: "secret-access-token", APIBase: server.server.URL})

	_, err := client.CreatePage(context.Background(), PageRequest{Title: "标题", Content: []Node{Paragraph("x")}})
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("错误类别 = %v，期望 ErrUpstream", err)
	}
	for _, secret := range []string{"TOKEN_LEAK", "secret-access-token", server.server.URL} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("错误信息泄露了 %q: %v", secret, err)
		}
	}

	server.fail = false
	server.noPath = true
	if _, err := client.CreatePage(context.Background(), PageRequest{Title: "标题", Content: []Node{Paragraph("x")}}); !errors.Is(err, ErrUpstream) {
		t.Errorf("缺少 path 时错误 = %v，期望 ErrUpstream", err)
	}
}

func TestDefaultAuthorFallback(t *testing.T) {
	server := newPageServer(t)
	client := New(Config{AccessToken: "token", APIBase: server.server.URL})
	if _, err := client.CreatePage(context.Background(), PageRequest{Title: "标题", Content: []Node{Paragraph("x")}}); err != nil {
		t.Fatalf("创建页面失败: %v", err)
	}
	_, form := server.requestForm()
	if form["author_name"] != "SeSeGraph" {
		t.Errorf("author_name = %q，期望默认值", form["author_name"])
	}
}

func TestCreatePageRejectsOversizedContentBeforeRequest(t *testing.T) {
	server := newPageServer(t)
	client := New(Config{AccessToken: "secret-access-token", APIBase: server.server.URL})

	huge := []Node{Paragraph(strings.Repeat("长", MaxPageContentBytes))}
	_, err := client.CreatePage(context.Background(), PageRequest{Title: "超大内容", Content: huge})
	if !errors.Is(err, ErrContentTooLarge) {
		t.Fatalf("错误 = %v，期望 ErrContentTooLarge", err)
	}
	if method, _ := server.requestForm(); method != "" {
		t.Errorf("超限内容不应发出请求，实际调用了 %q", method)
	}

	_, err = client.EditPage(context.Background(), "Test-Page-01", PageRequest{Title: "超大内容", Content: huge})
	if !errors.Is(err, ErrContentTooLarge) {
		t.Fatalf("编辑时错误 = %v，期望 ErrContentTooLarge", err)
	}
	if method, _ := server.requestForm(); method != "" {
		t.Errorf("编辑超限内容不应发出请求，实际调用了 %q", method)
	}
}
