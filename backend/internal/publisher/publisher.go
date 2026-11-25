// Package publisher 把已存图的帖子发布为 Telegraph 页面，支持分页、互链与失败重试。
package publisher

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
	"github.com/littleseven2003/SeSeGraph/backend/internal/settings"
	"github.com/littleseven2003/SeSeGraph/backend/internal/telegraph"
)

// 发布队列的轮询间隔与默认页地址前缀。
const (
	idleBackoff      = 2 * time.Second
	defaultPageBase  = "https://telegra.ph/"
	publishErrText   = "发布失败，请稍后重试"
	imagePathPattern = "/i/"
)

// 发布检查点的控制结果：暂停时把任务放回待发布，停止时记录失败。
var (
	errPublishPaused  = errors.New("发布已暂停")
	errPublishStopped = errors.New("任务已停止")
)

// checkControl 在每个页面调用前复核任务控制状态。
func (p *Publisher) checkControl(ctx context.Context, postID int64) error {
	state, _, err := p.repo.Control(ctx, postID)
	if err != nil {
		return nil
	}
	switch state {
	case posts.ControlPaused:
		return errPublishPaused
	case posts.ControlStopped:
		return errPublishStopped
	default:
		return nil
	}
}

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
		switch {
		case errors.Is(err, errPublishPaused), errors.Is(err, errPublishStopped),
			errors.Is(err, posts.ErrPublishStateChanged):
			// 暂停或停止：按当前状态原子交还，恢复早于交还也会回到待发布。
			_ = p.repo.ReleasePublish(ctx, postID)
			return true, nil
		default:
			text := publishErrorText(err)
			_ = p.repo.MarkPublishFailed(ctx, postID, text)
			p.logger.Printf("发布失败：编号 %d，%s", postID, text)
			return true, err
		}
	}
	return true, nil
}

// publishErrorText 把建页失败翻成一句可读说明，不含令牌、原始响应与地址。
func publishErrorText(err error) string {
	if errors.Is(err, telegraph.ErrNotConfigured) {
		return "发布失败：Telegraph 未配置"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "发布失败：请求超时，远端结果未确认"
	}
	var status *telegraph.StatusError
	if errors.As(err, &status) {
		switch {
		case status.Code == 400:
			return "发布失败：页面参数被上游拒绝，上游状态 400"
		case status.Code == 401 || status.Code == 403:
			return "发布失败：Telegraph 令牌或权限有误，上游状态 " + strconv.Itoa(status.Code)
		case status.Code == 429:
			return "发布失败：Telegraph 限流，稍后重试"
		case status.Code >= 500:
			return "发布失败：Telegraph 服务异常，上游状态 " + strconv.Itoa(status.Code)
		}
		return "发布失败：未识别的上游响应，上游状态 " + strconv.Itoa(status.Code)
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "发布失败：请求超时，远端结果未确认"
	}
	return publishErrText
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
	if err := p.checkControl(ctx, postID); err != nil {
		return err
	}

	images := orderForPublish(info.Images, info.CoverSortOrder)
	body := buildBody(p.publicURL, info.Description, images)
	pages, err := telegraph.Paginate(body)
	if err != nil {
		return err
	}

	// 无中文链接模式：用冻结的短码建页固定 path，随后统一改回真实标题。
	// 历史任务没有冻结值，仍按标题生成，行为与升级前一致。
	slug := strings.TrimSpace(info.Slug)
	slugMode := info.PathMode == settings.PathModeSlug && slug != ""

	// 逐页创建并增量落库，进程中断后重试可以复用已建好的页面。
	paths := make([]string, 0, len(pages))
	for index, page := range pages {
		if index < len(info.Paths) && strings.TrimSpace(info.Paths[index]) != "" {
			paths = append(paths, strings.TrimPrefix(info.Paths[index], "/"))
			continue
		}
		if err := p.checkControl(ctx, postID); err != nil {
			return err
		}
		createTitle := pageTitle(info.Title, page)
		if slugMode {
			createTitle = slugPageTitle(slug, page.Index)
		}
		created, err := pageAPI.CreatePage(ctx, telegraph.PageRequest{
			Title:      createTitle,
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

	urls := pageURLs(paths)

	// 全部页面就绪后还原真实标题；多页同时补互链：首页只有下一页、末页只有上一页。
	// 无中文模式下单页也必须编辑一次，否则页面标题会停在建页用的短码。
	if slugMode || len(pages) > 1 {
		for index, page := range pages {
			if err := p.checkControl(ctx, postID); err != nil {
				return err
			}
			prevURL, nextURL := "", ""
			if index > 0 {
				prevURL = urls[index-1]
			}
			if index < len(urls)-1 {
				nextURL = urls[index+1]
			}
			content := append([]telegraph.Node{}, page.Body...)
			if len(pages) > 1 {
				content = append(content, telegraph.NavNodes(prevURL, nextURL, page.Index, page.Total)...)
			}
			if _, err := telegraph.ContentBytes(content); err != nil {
				return err
			}
			if _, err := pageAPI.EditPage(ctx, paths[index], telegraph.PageRequest{
				Title:      pageTitle(info.Title, page),
				AuthorName: info.AuthorName,
				Content:    content,
			}); err != nil {
				return err
			}
		}
	}

	// 收尾前再确认任务未被停止或暂停：停止后不再把发布标记为成功。
	if err := p.checkControl(ctx, postID); err != nil {
		return err
	}

	if err := p.repo.MarkPublished(ctx, postID, paths, urls); err != nil {
		if errors.Is(err, posts.ErrPublishStateChanged) {
			return errPublishStopped
		}
		return err
	}
	return nil
}

// orderForPublish 把封面媒体提到第一项，其余媒体保持原序号顺序。
// 封面序号为空或找不到对应媒体时顺序不变。
func orderForPublish(images []posts.Image, coverSortOrder int) []posts.Image {
	if coverSortOrder <= 0 || len(images) < 2 {
		return images
	}
	index := -1
	for position, image := range images {
		if image.SortOrder == coverSortOrder {
			index = position
			break
		}
	}
	if index <= 0 {
		return images
	}
	ordered := make([]posts.Image, 0, len(images))
	ordered = append(ordered, images[index])
	ordered = append(ordered, images[:index]...)
	ordered = append(ordered, images[index+1:]...)
	return ordered
}

// slugPageTitle 返回无中文模式的建页标题：首页用短码，后续页补页序。
func slugPageTitle(slug string, page int) string {
	if page <= 1 {
		return slug
	}
	return fmt.Sprintf("%s-%d", slug, page)
}

// buildBody 生成正文节点：说明段落与逐张图片。
func buildBody(publicURL, description string, images []posts.Image) []telegraph.Node {
	nodes := make([]telegraph.Node, 0, len(images)+4)
	for _, line := range strings.Split(description, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			nodes = append(nodes, telegraph.Paragraph(line))
		}
	}
	for index, image := range images {
		src := publicURL + imagePathPattern + image.ShortCode
		if image.MimeType == posts.MimeVideoMP4 {
			// 视频用 video 节点内嵌，同时给出可点击的备用链接，
			// 兼容不允许内嵌播放的阅读环境。
			nodes = append(nodes, telegraph.Node{
				Tag: "figure",
				Children: []any{
					telegraph.Node{Tag: "video", Attrs: map[string]string{"src": src}},
					telegraph.Node{Tag: "figcaption", Children: []any{fmt.Sprintf("第 %d 段视频", index+1)}},
					telegraph.Link(src, "打开视频"),
				},
			})
			continue
		}
		nodes = append(nodes, telegraph.Node{
			Tag: "figure",
			Children: []any{
				telegraph.Node{Tag: "img", Attrs: map[string]string{
					"src": src,
				}},
				telegraph.Node{Tag: "figcaption", Children: []any{fmt.Sprintf("第 %d 张", index+1)}},
			},
		})
	}
	return nodes
}

// pageTitle 在多页时补上 (i/n) 标记。
func pageTitle(title string, page telegraph.PageBody) string {
	if page.Total <= 1 {
		return title
	}
	return fmt.Sprintf("%s (%d/%d)", title, page.Index, page.Total)
}

// pageURLs 把页面路径转换成对外链接。
func pageURLs(paths []string) []string {
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
