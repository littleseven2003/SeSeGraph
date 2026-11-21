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
type pageServer struct {
	mu       sync.Mutex
	method   string
	lastForm map[string]string
	fail     bool
	noPath   bool
	server   *httptest.Server
}

func newPageServer(t *testing.T) *pageServer {
	t.Helper()
	ps := &pageServer{lastForm: map[string]string{}}
	ps.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		ps.mu.Lock()
		ps.method = strings.TrimPrefix(r.URL.Path, "/")
		for key := range r.PostForm {
			ps.lastForm[key] = r.PostForm.Get(key)
		}
		fail, noPath := ps.fail, ps.noPath
		ps.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
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

func TestEditPageSendsPath(t *testing.T) {
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
	if method != "editPage" {
		t.Errorf("调用方法 = %q，期望 editPage", method)
	}
	if form["path"] != "Test-Page-01" {
		t.Errorf("path = %q，期望去掉前导斜杠", form["path"])
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
