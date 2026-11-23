package rss

import (
	"encoding/xml"
	"strings"
	"testing"
	"time"
)

type parsedFeed struct {
	Channel struct {
		Title       string `xml:"title"`
		Link        string `xml:"link"`
		Description string `xml:"description"`
		Items       []struct {
			Title       string   `xml:"title"`
			Link        string   `xml:"link"`
			GUID        string   `xml:"guid"`
			Description string   `xml:"description"`
			PubDate     string   `xml:"pubDate"`
			Categories  []string `xml:"category"`
		} `xml:"item"`
	} `xml:"channel"`
}

func TestMarshalProducesEscapedRSS2(t *testing.T) {
	published := time.Date(2026, 11, 23, 9, 30, 0, 0, time.FixedZone("CST", 8*3600))
	body, err := Feed{
		Title:       "图册 & 记录",
		Link:        "https://gallery.example.com/",
		Description: "Telegraph <更新>",
		Language:    "zh-CN",
		LastBuild:   published,
		Items: []Item{{
			Title:       "海边 & 写真",
			Description: "第一行 <第二行>",
			Link:        "https://telegra.ph/Page-11-23",
			GUID:        "https://telegra.ph/Page-11-23",
			Published:   published,
			Categories:  []string{"写真", "海边"},
		}},
	}.Marshal()
	if err != nil {
		t.Fatalf("生成失败: %v", err)
	}
	if !strings.HasPrefix(string(body), `<?xml version="1.0" encoding="UTF-8"?>`) {
		t.Errorf("缺少 XML 声明: %s", body[:60])
	}

	var parsed parsedFeed
	if err := xml.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if parsed.Channel.Title != "图册 & 记录" || parsed.Channel.Description != "Telegraph <更新>" {
		t.Errorf("频道字段未正确转义: %+v", parsed.Channel)
	}
	if len(parsed.Channel.Items) != 1 {
		t.Fatalf("条目数 = %d", len(parsed.Channel.Items))
	}
	item := parsed.Channel.Items[0]
	if item.Title != "海边 & 写真" || item.Description != "第一行 <第二行>" {
		t.Errorf("条目字段未正确转义: %+v", item)
	}
	if item.PubDate != "Mon, 23 Nov 2026 01:30:00 +0000" {
		t.Errorf("pubDate = %q，期望 UTC", item.PubDate)
	}
	if len(item.Categories) != 2 || item.Categories[0] != "写真" {
		t.Errorf("分类 = %v", item.Categories)
	}
	if item.GUID != "https://telegra.ph/Page-11-23" {
		t.Errorf("guid = %q", item.GUID)
	}
}

func TestMarshalEmptyFeedIsValid(t *testing.T) {
	body, err := Feed{Title: "图册", Link: "https://gallery.example.com/"}.Marshal()
	if err != nil {
		t.Fatalf("生成失败: %v", err)
	}
	var parsed parsedFeed
	if err := xml.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("空订阅应能解析: %v", err)
	}
	if len(parsed.Channel.Items) != 0 {
		t.Errorf("空订阅不应有条目: %d", len(parsed.Channel.Items))
	}
}

func TestSanitizeTextDropsMarkupAndTelegramURLs(t *testing.T) {
	raw := "第一行 <b>粗体</b>\n\n第二行 https://api.telegram.org/file/bot123456:ABCdefGHIjklMNOpqrSTUvwxYZ/photo.jpg 结束"
	got := SanitizeText(raw)
	if strings.Contains(got, "<") || strings.Contains(got, "api.telegram.org") || strings.Contains(got, "bot123456") {
		t.Errorf("清洗后仍含标签或 Telegram 直链: %q", got)
	}
	if !strings.Contains(got, "第一行 粗体") || !strings.Contains(got, "第二行 结束") {
		t.Errorf("清洗结果不符: %q", got)
	}
}
