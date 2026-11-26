package main

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/littleseven2003/SeSeGraph/backend/internal/botpool"
	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
	"github.com/littleseven2003/SeSeGraph/backend/internal/settings"
	"github.com/littleseven2003/SeSeGraph/backend/internal/storage"
)

// takeoverLimit 是后台接管的排队上限，避免大量缺引用图片把内存与上游调用堆起来。
const takeoverLimit = 32

// takeoverTimeout 是单张图接管的上限：转发、删除与登记都在这个时间内完成。
const takeoverTimeout = 30 * time.Second

// takeoverDeps 汇总后台接管需要的外部动作，便于测试替换。
type takeoverDeps struct {
	repo      *posts.Repository
	bots      func(context.Context) ([]botpool.Bot, error)
	effective func(context.Context) (settings.Effective, error)
	provider  func(botpool.Bot, settings.Effective) storage.StreamProvider
	logger    *log.Logger
}

// takeoverQueue 在后台有界地接管缺失引用的老图：公开读图只登记需要接管，
// 同一张图在排队或处理期间只保留一条，避免并发打开时重复转发。
type takeoverQueue struct {
	deps  takeoverDeps
	queue chan *posts.PublicImage

	mu      sync.Mutex
	pending map[int64]bool
}

// newTakeoverQueue 创建后台接管队列。
func newTakeoverQueue(deps takeoverDeps) *takeoverQueue {
	return &takeoverQueue{
		deps:    deps,
		queue:   make(chan *posts.PublicImage, takeoverLimit),
		pending: make(map[int64]bool),
	}
}

// Enqueue 登记一张需要接管的图；同一张图已在排队或处理时不重复入队，
// 队列满时直接丢弃，由下一次读取再触发。
func (q *takeoverQueue) Enqueue(image *posts.PublicImage) {
	if q == nil || image == nil || image.ImageID <= 0 {
		return
	}
	if strings.TrimSpace(image.SourceChatID) == "" || image.SourceMessageID <= 0 {
		return
	}
	q.mu.Lock()
	if q.pending[image.ImageID] {
		q.mu.Unlock()
		return
	}
	q.pending[image.ImageID] = true
	q.mu.Unlock()

	select {
	case q.queue <- image:
	default:
		q.mu.Lock()
		delete(q.pending, image.ImageID)
		q.mu.Unlock()
	}
}

// Start 在后台消费接管任务，直到上下文结束。
func (q *takeoverQueue) Start(ctx context.Context) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case image := <-q.queue:
				q.handle(ctx, image)
			}
		}
	}()
}

// handle 处理一张图并清除排队标记；失败时下次读取仍可再次登记。
func (q *takeoverQueue) handle(ctx context.Context, image *posts.PublicImage) {
	defer func() {
		q.mu.Lock()
		delete(q.pending, image.ImageID)
		q.mu.Unlock()
	}()
	callCtx, cancel := context.WithTimeout(ctx, takeoverTimeout)
	defer cancel()
	if err := q.takeOver(callCtx, image); err != nil && q.deps.logger != nil {
		q.deps.logger.Print("读图接管未完成")
	}
}

// takeOver 选一个池内可写的 Bot 转发原消息，取得属于它的文件编号并登记为引用。
func (q *takeoverQueue) takeOver(ctx context.Context, image *posts.PublicImage) error {
	bots, err := q.deps.bots(ctx)
	if err != nil {
		return err
	}
	effective, err := q.deps.effective(ctx)
	if err != nil {
		return err
	}
	for _, bot := range botpool.Usable(bots) {
		if bot.BotID == image.SourceBotID {
			continue
		}
		provider := q.deps.provider(bot, effective)
		if provider == nil {
			continue
		}
		fileID, takeErr := takeOverImage(ctx, provider, image)
		if takeErr != nil {
			continue
		}
		return q.deps.repo.SetImageFileRef(ctx, image.ImageID, bot.BotID, fileID)
	}
	return nil
}
