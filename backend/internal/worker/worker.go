package worker

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/littleseven2003/SeSeGraph/backend/internal/posts"
	"github.com/littleseven2003/SeSeGraph/backend/internal/storage"
)

const (
	// DefaultInterval 是相邻两张图片之间的默认间隔，用于降低限流概率。
	DefaultInterval = 1500 * time.Millisecond
	// DefaultMaxAttempts 是单张图片遇到限流时的最大尝试次数。
	DefaultMaxAttempts = 3
	// orphanRetention 是孤儿临时文件的保留时长。
	orphanRetention = 24 * time.Hour
	// idleBackoff 是队列为空时的轮询间隔。
	idleBackoff = 2 * time.Second
)

// Options 汇总上传队列所需依赖。
type Options struct {
	Repository  *posts.Repository
	Provider    storage.Provider
	DataDir     string
	Interval    time.Duration
	MaxAttempts int
	Logger      *log.Logger
	Sleep       func(ctx context.Context, d time.Duration) error
	Now         func() time.Time
	// Gate 与图床切换共用：领取任务后持有名额，跨 Bot 切换需要独占同一个协调点。
	Gate *storage.UploadGate
	// PublishReady 判断发布条件是否齐备，与手动发布共用口径；未装配时按未就绪处理。
	PublishReady func(ctx context.Context) error
	// NextProvider 在某个 Bot 被上游判为账号受限后返回池内下一个可写入的提供方。
	// 未装配时保持单 Bot 行为，本张直接失败。
	NextProvider func(ctx context.Context, failed storage.Provider, cause error) (storage.Provider, bool)
}

// Worker 串行处理待上传的帖子。
type Worker struct {
	repo         *posts.Repository
	provider     storage.Provider
	dataDir      string
	interval     time.Duration
	maxAttempts  int
	logger       *log.Logger
	sleep        func(ctx context.Context, d time.Duration) error
	now          func() time.Time
	gate         *storage.UploadGate
	publishReady func(ctx context.Context) error
	nextProvider func(ctx context.Context, failed storage.Provider, cause error) (storage.Provider, bool)

	mu          sync.Mutex
	nextAllowed time.Time
}

// New 创建上传队列。
func New(opts Options) (*Worker, error) {
	if opts.Repository == nil || opts.Provider == nil {
		return nil, errors.New("上传队列缺少必要依赖")
	}
	if opts.DataDir == "" {
		return nil, errors.New("上传队列缺少数据目录")
	}

	interval := opts.Interval
	if interval <= 0 {
		interval = DefaultInterval
	}
	maxAttempts := opts.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = DefaultMaxAttempts
	}
	logger := opts.Logger
	if logger == nil {
		logger = log.Default()
	}
	sleep := opts.Sleep
	if sleep == nil {
		sleep = defaultSleep
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}

	return &Worker{
		repo:         opts.Repository,
		provider:     opts.Provider,
		dataDir:      opts.DataDir,
		interval:     interval,
		maxAttempts:  maxAttempts,
		logger:       logger,
		sleep:        sleep,
		now:          now,
		gate:         opts.Gate,
		publishReady: opts.PublishReady,
		nextProvider: opts.NextProvider,
	}, nil
}

// providerOf 读取当前提供方；与 SetProvider 共用同一把锁，避免热替换时读到半新半旧。
func (w *Worker) providerOf() storage.Provider {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.provider
}

// SetProvider 在配置保存后热替换发送所用的提供方；在途请求继续用自己持有的实例。
func (w *Worker) SetProvider(provider storage.Provider) {
	if provider == nil {
		return
	}
	w.mu.Lock()
	w.provider = provider
	w.mu.Unlock()
}

// Recovery 汇报启动恢复的结果。
type Recovery struct {
	Requeued     int64
	RemovedFiles int
	// AutoEnqueued 是启动时补入发布队列的任务数。
	AutoEnqueued int64
}

// Recover 恢复中断的任务并清理过期的孤儿临时文件。
func (w *Worker) Recover(ctx context.Context) (Recovery, error) {
	requeued, err := w.repo.RecoverPublishing(ctx)
	if err != nil {
		return Recovery{}, err
	}
	removed, err := w.repo.CleanupOrphans(ctx, w.dataDir, orphanRetention, time.Now())
	if err != nil {
		return Recovery{Requeued: requeued}, err
	}
	// 收尾与入队现在同事务，这里兜底旧版本留下的「已同步、未发布」记录。
	auto, err := w.repo.RecoverAutoPublish(ctx, w.checkPublishReady(ctx) == nil, publishNotReadyNote)
	if err != nil {
		return Recovery{Requeued: requeued, RemovedFiles: removed}, err
	}
	if requeued > 0 || removed > 0 || auto > 0 {
		w.logger.Printf("上传队列恢复：重新排队 %d 个任务，清理 %d 个临时文件，补入发布队列 %d 个", requeued, removed, auto)
	}
	return Recovery{Requeued: requeued, RemovedFiles: removed, AutoEnqueued: auto}, nil
}

// Run 持续处理队列，直到上下文结束。
func (w *Worker) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		processed, err := w.ProcessOnce(ctx)
		if err != nil {
			w.logger.Printf("上传任务失败")
		}
		if processed {
			continue
		}
		if err := w.sleep(ctx, idleBackoff); err != nil {
			return
		}
	}
}

// readiness 是提供方的可选接口：未就绪时队列保持等待，不领取任务。
type readiness interface {
	Ready() bool
}

// ProcessOnce 领取并处理一个任务，返回是否领取到任务。
// 图床尚未配置时直接返回，避免把排队中的图片判为失败。
// 领取任务与处理全程持有协调点名额，跨 Bot 切换必须等这一帖结束才能确认图片归属。
func (w *Worker) ProcessOnce(ctx context.Context) (bool, error) {
	provider := w.providerOf()
	if probe, ok := provider.(readiness); ok && !probe.Ready() {
		return false, nil
	}
	release, ok := w.gate.EnterUpload()
	if !ok {
		// 切换正在独占，下一轮再领取，避免把排队中的任务错判为失败。
		return false, nil
	}
	defer release()

	postID, ok, err := w.repo.ClaimNext(ctx)
	if err != nil || !ok {
		return false, err
	}
	if err := w.Process(ctx, postID); err != nil {
		return true, err
	}
	return true, nil
}

// Process 逐张上传帖子中的图片，全部完成后把帖子置为已完成。
// 一帖全程固定使用同一份提供方，避免上传途中配置热切换导致同一帖分散到不同图床。
func (w *Worker) Process(ctx context.Context, postID int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	provider := w.providerOf()
	if fixed, ok := w.fixedProvider(provider); ok {
		provider = fixed
	}
	post, err := w.repo.Get(ctx, postID)
	if err != nil {
		return errors.New("读取帖子失败")
	}

	images, err := w.repo.PendingImages(ctx, postID)
	if err != nil {
		w.finish(ctx, postID, posts.StatusFailed, "读取待上传图片失败")
		return err
	}

	for _, image := range images {
		if err := ctx.Err(); err != nil {
			return err
		}
		// 每张图之前复核控制状态：暂停立即让出任务，停止则不再继续。
		if state, _, stateErr := w.repo.Control(ctx, postID); stateErr == nil && state != posts.ControlRunning {
			if state == posts.ControlPaused {
				return w.repo.ReleaseToQueue(ctx, postID)
			}
			return nil
		}
		used, err := w.uploadImage(ctx, provider, post, image)
		if err != nil {
			message := err.Error()
			if message == "" {
				message = "存图失败，原因未知"
			}
			w.finish(ctx, postID, posts.StatusFailed, message)
			w.logger.Printf("帖子上传失败：编号 %d，%s", postID, message)
			return err
		}
		provider = used
	}

	// 收尾前再看一次控制状态，避免暂停或停止后把任务标成完成。
	if state, _, stateErr := w.repo.Control(ctx, postID); stateErr == nil && state != posts.ControlRunning {
		if state == posts.ControlPaused {
			return w.repo.ReleaseToQueue(ctx, postID)
		}
		return nil
	}

	stored, err := w.repo.CountStored(ctx, postID)
	if err != nil {
		w.finish(ctx, postID, posts.StatusFailed, "统计已存图片失败")
		return err
	}
	if stored < post.ImageCount {
		w.finish(ctx, postID, posts.StatusFailed, "图片数量不足")
		return errors.New("图片数量不足")
	}

	// 发布条件与手动发布共用口径：不齐时只标记完成，公开标记与短码保持关闭。
	readyErr := w.checkPublishReady(ctx)
	enqueued, err := w.repo.FinishSync(ctx, postID, readyErr == nil, publishNotReadyNote)
	if err != nil {
		// 收尾写库失败会让任务停在处理中，这里立刻标失败并留下原因。
		w.finish(ctx, postID, posts.StatusFailed, "同步收尾写库失败，请到任务页重试")
		w.logger.Printf("同步收尾失败：编号 %d，%v", postID, err)
		return err
	}
	if enqueued {
		w.logger.Printf("自动入发布队列：编号 %d", postID)
	} else if readyErr != nil {
		w.logger.Printf("发布条件未就绪，已存图未公开：编号 %d", postID)
	}
	w.logger.Printf("帖子上传完成：编号 %d，图片 %d 张", postID, stored)
	return nil
}

// publishNotReadyNote 是发布条件不齐时写入的提示，明确任务仍处于未公开状态。
const publishNotReadyNote = "发布条件未就绪，已存图未公开，请先配置 Telegraph 与 HTTPS 地址"

// checkPublishReady 调用装配好的发布检查，未装配时按未就绪处理，避免误公开。
func (w *Worker) checkPublishReady(ctx context.Context) error {
	if w.publishReady == nil {
		return errors.New("发布检查未装配")
	}
	return w.publishReady(ctx)
}

// fixedProvider 在提供方支持快照时取一份固定视图，保证整帖使用同一配置。
func (w *Worker) fixedProvider(provider storage.Provider) (storage.Provider, bool) {
	snapshotter, ok := provider.(storage.Snapshotter)
	if !ok {
		return nil, false
	}
	return snapshotter.Snapshot()
}

// uploadImage 上传单张图片。遇到限流按返回的等待时长退避重试；
// 账号被上游限制时换用池内下一个 Bot 重发本张，其余错误直接记为失败。
func (w *Worker) uploadImage(ctx context.Context, provider storage.Provider, post *posts.Post, image posts.Image) (storage.Provider, error) {
	path := filepath.Join(w.dataDir, filepath.FromSlash(image.TempFilePath))
	switched := 0

	for attempt := 1; ; attempt++ {
		if err := w.acquireSlot(ctx); err != nil {
			return provider, err
		}

		file, openErr := os.Open(path)
		if openErr != nil {
			_ = w.repo.MarkImageFailed(ctx, image.ID, "读取临时文件失败")
			return provider, errors.New("读取临时文件失败")
		}
		// 视频不看画质模式，一律走 sendVideo；静态图沿用帖子的画质选择。
		mode := post.UploadMode
		if image.MimeType == posts.MimeVideoMP4 {
			mode = storage.ModeVideo
		}
		result, uploadErr := provider.Upload(ctx, storage.UploadRequest{
			FileName: filepath.Base(path),
			MIMEType: image.MimeType,
			Size:     image.FileSize,
			Mode:     mode,
			Reader:   file,
		})
		_ = file.Close()

		if uploadErr == nil {
			if err := w.recordStored(ctx, provider, image.ID, result); err != nil {
				return provider, err
			}
			_ = os.Remove(path)
			return provider, nil
		}

		var limited *storage.RateLimitError
		if errors.As(uploadErr, &limited) {
			// 限流后把队列整体的下一次发送时间推后，重试耗尽时同样生效。
			wait := limited.RetryAfter
			if wait <= 0 {
				wait = w.interval
			}
			w.holdUntil(w.now().Add(wait))
			if attempt < w.maxAttempts {
				continue
			}
		}

		// 账号被上游限制时换池内下一个 Bot 重发本张，已存的图片不受影响。
		if storage.IsAccountLimited(uploadErr) && w.nextProvider != nil && switched < 2 {
			next, ok := w.nextProvider(ctx, provider, uploadErr)
			if ok && next != nil {
				switched++
				provider = next
				w.logger.Printf("存图换用其他 Bot：帖子 %d 第 %d 张", post.ID, image.SortOrder)
				continue
			}
		}

		// 真实原因写进逐图错误与任务说明，界面据此给出可读解释。
		message := storage.DescribeUpload(uploadErr)
		_ = w.repo.MarkImageFailed(ctx, image.ID, message)
		w.logger.Printf("存图失败：帖子 %d 第 %d 张，%s", post.ID, image.SortOrder, message)
		return provider, errors.New(message)
	}
}

// recordStored 记录存图结果；提供方带身份时把这张图记到实际发送的 Bot 名下。
func (w *Worker) recordStored(ctx context.Context, provider storage.Provider, imageID int64, result storage.UploadResult) error {
	botID, chatID := int64(0), ""
	if origin, ok := provider.(storage.Origin); ok {
		botID, chatID = origin.Origin()
	}
	if botID > 0 {
		return w.repo.MarkImageStoredWithOrigin(ctx, imageID, result.FileID, result.MessageID, provider.Name(), botID, chatID)
	}
	return w.repo.MarkImageStored(ctx, imageID, result.FileID, result.MessageID, provider.Name())
}

// acquireSlot 在每次请求前等待必要的间隔，保证整个队列共享发送节奏。
func (w *Worker) acquireSlot(ctx context.Context) error {
	w.mu.Lock()
	wait := w.nextAllowed.Sub(w.now())
	w.mu.Unlock()

	if wait > 0 {
		if err := w.sleep(ctx, wait); err != nil {
			return err
		}
	}

	w.mu.Lock()
	w.nextAllowed = w.now().Add(w.interval)
	w.mu.Unlock()
	return nil
}

// holdUntil 把队列的下一次发送时间推后到指定时刻。
func (w *Worker) holdUntil(deadline time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if deadline.After(w.nextAllowed) {
		w.nextAllowed = deadline
	}
}

func (w *Worker) finish(ctx context.Context, postID int64, status, message string) {
	if err := w.repo.MarkPostFinished(ctx, postID, status, message); err == nil {
		return
	}
	// 暂停或停止时 finish 的条件更新不命中，失败原因改走无条件落库。
	if status == posts.StatusFailed {
		if err := w.repo.MarkTaskFailed(ctx, postID, message); err != nil {
			w.logger.Printf("记录失败状态失败：编号 %d，%v", postID, err)
		}
	}
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
