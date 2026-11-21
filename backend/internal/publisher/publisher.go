// Package publisher 把已存图的帖子发布为 Telegraph 页面，支持分页、互链与失败重试。
package publisher

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
	"github.com/littleseven2003/SeSeGraph/backend/internal/telegraph"
)

// 发布队列的轮询间隔与默认页地址前缀。
const (
	idleBackoff      = 2 * time.Second
	defaultPageBase  = "https://telegra.ph/"
	publishErrText   = "发布失败，请稍后重试"
	imagePathPattern = "/i/"
)

// PageAPI 是发布流程需要的页面接口，便于测试替换。
type PageAPI interface {
	CreatePage(ctx context.Context, request telegraph.PageRequest) (telegraph.Page, error)
	EditPage(ctx context.Context, path string, request telegraph.PageRequest) (telegraph.Page, error)
}

// Options 汇总发布器依赖。
type Options struct {
	Repository *posts.Repository
	// Pages 在每次处理任务时获取页面客户端，允许运行期完成建号。
	Pages     func(ctx context.Context) (PageAPI, error)
	PublicURL string
	Logger    *log.Logger
	Sleep     func(ctx context.Context, d time.Duration) error
}

// Publisher 串行处理待发布帖子。
type Publisher struct {
	repo      *posts.Repository
	pages     func(ctx context.Context) (PageAPI, error)
	publicURL string
	logger    *log.Logger
	sleep     func(ctx context.Context, d time.Duration) error
}

// New 创建发布器。
func New(opts Options) (*Publisher, error) {
	if opts.Repository == nil || opts.Pages == nil {
		return nil, errors.New("发布器缺少必要依赖")
	}
	logger := opts.Logger
	if logger == nil {
		logger = log.Default()
	}
	sleep := opts.Sleep
	if sleep == nil {
		sleep = defaultSleep
	}
	return &Publisher{
		repo:      opts.Repository,
		pages:     opts.Pages,
		publicURL: strings.TrimRight(strings.TrimSpace(opts.PublicURL), "/"),
		logger:    logger,
		sleep:     sleep,
	}, nil
}

// Recover 把中断的发布任务放回队列。
func (p *Publisher) Recover(ctx context.Context) (int64, error) {
	return p.repo.RecoverPublish(ctx)
}

// Run 持续处理发布队列，直到上下文结束。
func (p *Publisher) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		processed, err := p.ProcessOnce(ctx)
		if err != nil {
			p.logger.Print("Telegraph 发布任务失败")
		}
		if processed {
			continue
		}
		if err := p.sleep(ctx, idleBackoff); err != nil {
			return
		}
	}
}

// ProcessOnce 处理一条待发布任务，返回是否有任务被处理。
func (p *Publisher) ProcessOnce(ctx context.Context) (bool, error) {
	// 未完成建号时不动队列，任务保持待处理。
	pageAPI, err := p.pages(ctx)
	if err != nil {
		return false, nil
	}

	postID, ok, err := p.repo.ClaimPublishable(ctx)
	if err != nil || !ok {
		return false, err
	}

	if err := p.publish(ctx, postID, pageAPI); err != nil {
		_ = p.repo.MarkPublishFailed(ctx, postID, publishErrText)
		return true, err
	}
	return true, nil
}

// publish 完成单帖发布：创建页面、互链、落库。
func (p *Publisher) publish(ctx context.Context, postID int64, pageAPI PageAPI) error {
	info, err := p.repo.PublishInfo(ctx, postID)
	if err != nil {
		return err
	}
	if len(info.Images) == 0 {
		return errors.New("没有可发布的存图记录")
	}

	body := p.buildBody(info)
	pages, err := telegraph.Paginate(body)
	if err != nil {
		return err
	}

	// 逐页创建并增量落库，进程中断后重试可以复用已建好的页面。
	paths := make([]string, 0, len(pages))
	for index, page := range pages {
		if index < len(info.Paths) && strings.TrimSpace(info.Paths[index]) != "" {
			paths = append(paths, strings.TrimPrefix(info.Paths[index], "/"))
			continue
		}
		created, err := pageAPI.CreatePage(ctx, telegraph.PageRequest{
			Title:      p.pageTitle(info.Title, page),
			AuthorName: info.AuthorName,
			Content:    page.Body,
		})
		if err != nil {
			if len(paths) > 0 {
				_ = p.repo.SavePublishPaths(ctx, postID, paths)
			}
			return err
		}
		paths = append(paths, strings.TrimPrefix(created.Path, "/"))
		if err := p.repo.SavePublishPaths(ctx, postID, paths); err != nil {
			return err
		}
	}

	urls := p.pageURLs(paths)

	// 全部页面就绪后补互链：首页只有下一页、末页只有上一页。
	if len(pages) > 1 {
		for index, page := range pages {
			prevURL, nextURL := "", ""
			if index > 0 {
				prevURL = urls[index-1]
			}
			if index < len(urls)-1 {
				nextURL = urls[index+1]
			}
			content := append(append([]telegraph.Node{}, page.Body...), telegraph.NavNodes(prevURL, nextURL, page.Index, page.Total)...)
			if _, err := telegraph.ContentBytes(content); err != nil {
				return err
			}
			if _, err := pageAPI.EditPage(ctx, paths[index], telegraph.PageRequest{
				Title:      p.pageTitle(info.Title, page),
				AuthorName: info.AuthorName,
				Content:    content,
			}); err != nil {
				return err
			}
		}
	}

	return p.repo.MarkPublished(ctx, postID, paths, urls)
}

// buildBody 生成正文节点：说明段落与逐张图片。
func (p *Publisher) buildBody(info *posts.PublishInfo) []telegraph.Node {
	nodes := make([]telegraph.Node, 0, len(info.Images)+4)
	for _, line := range strings.Split(info.Description, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			nodes = append(nodes, telegraph.Paragraph(line))
		}
	}
	for index, image := range info.Images {
		nodes = append(nodes, telegraph.Node{
			Tag: "figure",
			Children: []any{
				telegraph.Node{Tag: "img", Attrs: map[string]string{
					"src": p.publicURL + imagePathPattern + image.ShortCode,
				}},
				telegraph.Node{Tag: "figcaption", Children: []any{fmt.Sprintf("第 %d 张", index+1)}},
			},
		})
	}
	return nodes
}

// pageTitle 在多页时补上 (i/n) 标记。
func (p *Publisher) pageTitle(title string, page telegraph.PageBody) string {
	if page.Total <= 1 {
		return title
	}
	return fmt.Sprintf("%s (%d/%d)", title, page.Index, page.Total)
}

// pageURLs 把页面路径转换成对外链接。
func (p *Publisher) pageURLs(paths []string) []string {
	urls := make([]string, 0, len(paths))
	for _, path := range paths {
		urls = append(urls, defaultPageBase+strings.TrimPrefix(path, "/"))
	}
	return urls
}

func defaultSleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
