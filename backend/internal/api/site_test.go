package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
)

func TestSiteReportsRSSURLOnlyWhenEnabled(t *testing.T) {
	disabled := newTestEnv(t, testSetupToken)
	rec := disabled.do(t, http.MethodGet, "/api/site", "", nil)
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是 JSON: %v", err)
	}
	if body["rss_url"] != "" {
		t.Errorf("未启用时 rss_url = %q，期望空串", body["rss_url"])
	}

	enabled := newTestEnvWith(t, testSetupToken, func(deps *Deps, _ *sql.DB) {
		deps.PublicURL = "https://gallery.example.com/gallery"
		deps.RSSEnabled = true
	})
	rec = enabled.do(t, http.MethodGet, "/api/site", "", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是 JSON: %v", err)
	}
	if body["rss_url"] != "https://gallery.example.com/gallery/rss.xml" {
		t.Errorf("子路径部署的订阅地址 = %q", body["rss_url"])
	}
}

func TestIndexDiscoveryFollowsRSSState(t *testing.T) {
	index := fstest.MapFS{"index.html": &fstest.MapFile{
		Data: []byte("<html><head><title>x</title></head><body></body></html>"),
	}}

	enabled := newTestEnvWith(t, testSetupToken, func(deps *Deps, _ *sql.DB) {
		deps.WebFS = index
		deps.PublicURL = "https://gallery.example.com/gallery"
		deps.RSSEnabled = true
	})
	rec := enabled.do(t, http.MethodGet, "/", "", nil)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("入口页状态 = %d", rec.Code)
	}
	if !strings.Contains(body, `type="application/rss+xml"`) {
		t.Error("启用订阅时入口页应带自动发现链接")
	}
	if !strings.Contains(body, "https://gallery.example.com/gallery/rss.xml") {
		t.Errorf("发现链接应使用后端给出的绝对地址: %s", body)
	}

	disabled := newTestEnvWith(t, testSetupToken, func(deps *Deps, _ *sql.DB) {
		deps.WebFS = index
	})
	body = disabled.do(t, http.MethodGet, "/", "", nil).Body.String()
	if strings.Contains(body, `type="application/rss+xml"`) {
		t.Error("关闭订阅时入口页不应宣告订阅地址")
	}
}
