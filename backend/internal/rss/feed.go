package rss

import (
	"encoding/xml"
	"time"
)

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
