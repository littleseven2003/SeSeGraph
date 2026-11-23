package rss

import (
	"encoding/xml"
	"regexp"
	"strings"
	"time"
)

// htmlTagPattern 匹配说明里的 HTML 标签，订阅只输出纯文本。
var htmlTagPattern = regexp.MustCompile(`<[^>]*>`)

// telegramURLPattern 匹配 Telegram 文件直链，这类地址可能含 Bot 令牌，不能进入订阅。
// 主机名按大小写不敏感识别，并容忍任意子域、端口与查询串。
var telegramURLPattern = regexp.MustCompile(`(?i)https?://(?:[a-z0-9-]+\.)*api\.telegram\.org(?::\d+)?\S*`)

// botTokenPattern 兜底匹配形如 123456:ABC-DEF 的令牌片段。
var botTokenPattern = regexp.MustCompile(`\b\d{6,}:[A-Za-z0-9_-]{30,}\b`)

// SanitizeText 把说明转为订阅可用的纯文本：去掉 HTML 标签、Telegram 文件直链与令牌片段，
// 并压缩多余空白。段落之间的单个空行保留。
func SanitizeText(raw string) string {
	text := htmlTagPattern.ReplaceAllString(raw, "")
	text = telegramURLPattern.ReplaceAllString(text, "")
	text = botTokenPattern.ReplaceAllString(text, "")
	lines := strings.Split(text, "\n")
	cleaned := make([]string, 0, len(lines))
	blank := false
	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")
		if line == "" {
			if blank || len(cleaned) == 0 {
				continue
			}
			blank = true
			cleaned = append(cleaned, "")
			continue
		}
		blank = false
		cleaned = append(cleaned, line)
	}
	for len(cleaned) > 0 && cleaned[len(cleaned)-1] == "" {
		cleaned = cleaned[:len(cleaned)-1]
	}
	return strings.Join(cleaned, "\n")
}

// Item 是订阅源中的一条图集记录。
type Item struct {
	Title       string
	Description string
	Link        string
	GUID        string
	Published   time.Time
	Categories  []string
}

// Feed 是一份 RSS 2.0 订阅源。
type Feed struct {
	Title       string
	Link        string
	Description string
	Language    string
	LastBuild   time.Time
	Items       []Item
}

type rssDocument struct {
	XMLName xml.Name   `xml:"rss"`
	Version string     `xml:"version,attr"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Title         string    `xml:"title"`
	Link          string    `xml:"link"`
	Description   string    `xml:"description"`
	Language      string    `xml:"language,omitempty"`
	LastBuildDate string    `xml:"lastBuildDate,omitempty"`
	Items         []rssItem `xml:"item"`
}

type rssItem struct {
	Title       string   `xml:"title"`
	Link        string   `xml:"link"`
	GUID        rssGUID  `xml:"guid"`
	Description string   `xml:"description"`
	PubDate     string   `xml:"pubDate"`
	Categories  []string `xml:"category"`
}

type rssGUID struct {
	Value       string `xml:",chardata"`
	IsPermaLink string `xml:"isPermaLink,attr"`
}

// Marshal 生成带 XML 声明的 RSS 2.0 文档。
func (f Feed) Marshal() ([]byte, error) {
	doc := rssDocument{
		Version: "2.0",
		Channel: rssChannel{
			Title:       f.Title,
			Link:        f.Link,
			Description: f.Description,
			Language:    f.Language,
		},
	}
	if !f.LastBuild.IsZero() {
		doc.Channel.LastBuildDate = formatTime(f.LastBuild)
	}
	doc.Channel.Items = make([]rssItem, 0, len(f.Items))
	for _, item := range f.Items {
		guid := item.GUID
		if guid == "" {
			guid = item.Link
		}
		doc.Channel.Items = append(doc.Channel.Items, rssItem{
			Title:       item.Title,
			Link:        item.Link,
			GUID:        rssGUID{Value: guid, IsPermaLink: "true"},
			Description: item.Description,
			PubDate:     formatTime(item.Published),
			Categories:  item.Categories,
		})
	}

	body, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	header := []byte(xml.Header)
	return append(header, append(body, '\n')...), nil
}

// formatTime 统一输出 UTC 的 RSS 时间格式。
func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC1123Z)
}
