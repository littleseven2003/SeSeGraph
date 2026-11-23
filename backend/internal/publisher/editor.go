package publisher

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/littleseven2003/SeSeGraph/backend/internal/config"
	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
	"github.com/littleseven2003/SeSeGraph/backend/internal/telegraph"
)

// ErrPageCountChanged 表示修改后的内容会改变分页数量，无法沿用既有页面路径。
var ErrPageCountChanged = errors.New("修改后分页数量会变化")

// ErrPublishInProgress 表示帖子正在发布队列中或已有远端页面，暂不可本地改写。
var ErrPublishInProgress = errors.New("帖子正在发布流程中")

// ErrPublicURLInvalid 表示已发布帖缺少可拼接图片的合法 HTTPS 公开地址。
var ErrPublicURLInvalid = errors.New("需要先配置公开 HTTPS 地址")

// WithdrawTitle 是撤回后远端页面统一使用的标题。
const WithdrawTitle = "此图集已撤回"

// Metadata 是帖子可在线编辑的元数据。
type Metadata struct {
	Title       string
	Description string
	AuthorName  string
	// Tags 为空指针时保留原标签，空切片表示清空。
	Tags *[]string
}

// Editor 把已发布帖的标题、说明与作者同步到全部远端页面，
// 只有所有页面更新成功后才写入本地元数据。
type Editor struct {
	repo      *posts.Repository
	pages     func(ctx context.Context) (PageAPI, error)
	publicURL string
	mu        sync.Mutex
}

// NewEditor 创建编辑器。pages 允许运行期完成建号后再取客户端。
func NewEditor(repo *posts.Repository, pages func(ctx context.Context) (PageAPI, error), publicURL string) (*Editor, error) {
	if repo == nil || pages == nil {
		return nil, errors.New("编辑器缺少必要依赖")
	}
	return &Editor{
		repo:      repo,
		pages:     pages,
		publicURL: strings.TrimRight(strings.TrimSpace(publicURL), "/"),
	}, nil
}

// Withdraw 把保存过的远端页面清空为撤回提示，移除图片、作者与互链导航。
// 只做远端替换；本地记录由调用方在全部页面处理成功后删除。
func (e *Editor) Withdraw(ctx context.Context, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	pageAPI, err := e.pages(ctx)
	if err != nil {
		return err
	}
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		if _, err := pageAPI.EditPage(ctx, path, telegraph.PageRequest{
			Title:       WithdrawTitle,
			Content:     []telegraph.Node{telegraph.Paragraph(WithdrawTitle)},
			ClearAuthor: true,
		}); err != nil {
			return err
		}
	}
	return nil
}

// ChangeCover 在同一个编辑临界区里完成远端换序与本地条件提交。
// thumbPath 是新封面缩略图的相对路径；远端全部成功后才写本地，
// 期间若发布已领取或帖子进入删除，本地写入会被拒绝，旧封面保持可读。
func (e *Editor) ChangeCover(ctx context.Context, postID int64, sortOrder int, thumbPath string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	pending, pendingErr := e.repo.PendingDelete(ctx, postID)
	if pendingErr != nil {
		return pendingErr
	}
	if pending {
		return posts.ErrPendingDelete
	}

	if state, _, stateErr := e.repo.Control(ctx, postID); stateErr == nil && state != posts.ControlRunning {
		return posts.ErrControlInvalid
	}
	info, err := e.repo.PublishInfo(ctx, postID)
	if err != nil {
		return err
	}
	switch {
	case info.State == posts.PublishPublished && len(info.Paths) > 0:
		// 已发布：先按新顺序改写全部页面。
	case info.State == posts.PublishIdle,
		info.State == posts.PublishFailed && len(info.Paths) == 0,
		info.State == posts.PublishPublished && len(info.Paths) == 0:
		// 没有远端页面，只需改本地记录。
		return e.repo.SetCoverSelectionGuarded(ctx, postID, sortOrder, thumbPath, info.State)
	default:
		return ErrPublishInProgress
	}
	if !config.PublicURLIsHTTPS(e.publicURL) {
		return ErrPublicURLInvalid
	}

	pageAPI, err := e.pages(ctx)
	if err != nil {
		return err
	}

	pages, err := telegraph.Paginate(buildBody(e.publicURL, info.Description, orderForPublish(info.Images, sortOrder)))
	if err != nil {
		return err
	}
	if len(pages) != len(info.Paths) {
		return ErrPageCountChanged
	}

	urls := pageURLs(info.Paths)
	for index, page := range pages {
		prevURL, nextURL := "", ""
		if index > 0 {
			prevURL = urls[index-1]
		}
		if index < len(urls)-1 {
			nextURL = urls[index+1]
		}
		content := append(append([]telegraph.Node{}, page.Body...), telegraph.NavNodes(prevURL, nextURL, page.Index, page.Total)...)
		if _, err := pageAPI.EditPage(ctx, info.Paths[index], telegraph.PageRequest{
			Title:      pageTitle(info.Title, page),
			AuthorName: info.AuthorName,
			Content:    content,
		}); err != nil {
			return err
		}
	}

	return e.repo.SetCoverSelectionGuarded(ctx, postID, sortOrder, thumbPath, info.State)
}

// Update 更新元数据：未发布的帖子只改本地记录；
// 已发布的帖子按原有页面路径逐页编辑，全部成功后才写入本地。
func (e *Editor) Update(ctx context.Context, postID int64, meta Metadata) error {
	meta.Title = strings.TrimSpace(meta.Title)
	meta.AuthorName = strings.TrimSpace(meta.AuthorName)
	if meta.Title == "" {
		return telegraph.ErrInvalidInput
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	// 互斥之后再看待删除状态：排队的编辑可能在撤回完成后才拿到锁，
	// 此时必须直接失败，绝不能再向 Telegraph 写入页面。
	pending, pendingErr := e.repo.PendingDelete(ctx, postID)
	if pendingErr != nil {
		return pendingErr
	}
	if pending {
		return posts.ErrPendingDelete
	}

	info, err := e.repo.PublishInfo(ctx, postID)
	if err != nil {
		return err
	}
	switch {
	case info.State == posts.PublishPublished && len(info.Paths) > 0:
		// 已发布且有远端页面：走下面的逐页编辑。
	case info.State == posts.PublishIdle,
		info.State == posts.PublishFailed && len(info.Paths) == 0,
		info.State == posts.PublishPublished && len(info.Paths) == 0:
		// 没有可复用的远端页面，只改本地记录。
		return e.repo.UpdateMetadataAndTags(ctx, postID, info.State, meta.Title, meta.Description, meta.AuthorName, meta.Tags)
	default:
		// 待发布、发布中，或已有页面路径的失败帖：发布器可能已读走旧元数据。
		return ErrPublishInProgress
	}
	if !config.PublicURLIsHTTPS(e.publicURL) {
		return ErrPublicURLInvalid
	}

	pageAPI, err := e.pages(ctx)
	if err != nil {
		return err
	}

	// 沿用发布时的正文结构，只替换说明与作者；封面仍前置在首图。
	pages, err := telegraph.Paginate(buildBody(e.publicURL, meta.Description, orderForPublish(info.Images, info.CoverSortOrder)))
	if err != nil {
		return err
	}
	if len(pages) != len(info.Paths) {
		return ErrPageCountChanged
	}

	urls := pageURLs(info.Paths)
	for index, page := range pages {
		prevURL, nextURL := "", ""
		if index > 0 {
			prevURL = urls[index-1]
		}
		if index < len(urls)-1 {
			nextURL = urls[index+1]
		}
		content := append(append([]telegraph.Node{}, page.Body...),
			telegraph.NavNodes(prevURL, nextURL, page.Index, page.Total)...)
		if _, err := pageAPI.EditPage(ctx, info.Paths[index], telegraph.PageRequest{
			Title:      pageTitle(meta.Title, page),
			AuthorName: meta.AuthorName,
			Content:    content,
		}); err != nil {
			return err
		}
	}

	return e.repo.UpdateMetadataAndTags(ctx, postID, info.State, meta.Title, meta.Description, meta.AuthorName, meta.Tags)
}
