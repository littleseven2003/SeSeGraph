package worker

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
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
}

// Worker 串行处理待上传的帖子。
type Worker struct {
	repo        *posts.Repository
	provider    storage.Provider
	dataDir     string
	interval    time.Duration
	maxAttempts int
	logger      *log.Logger
	sleep       func(ctx context.Context, d time.Duration) error
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

	return &Worker{
		repo:        opts.Repository,
		provider:    opts.Provider,
		dataDir:     opts.DataDir,
		interval:    interval,
		maxAttempts: maxAttempts,
		logger:      logger,
		sleep:       sleep,
	}, nil
}

// Recovery 汇报启动恢复的结果。
type Recovery struct {
	Requeued     int64
	RemovedFiles int
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
	if requeued > 0 || removed > 0 {
		w.logger.Printf("上传队列恢复：重新排队 %d 个任务，清理 %d 个临时文件", requeued, removed)
	}
	return Recovery{Requeued: requeued, RemovedFiles: removed}, nil
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

// ProcessOnce 领取并处理一个任务，返回是否领取到任务。
func (w *Worker) ProcessOnce(ctx context.Context) (bool, error) {
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
func (w *Worker) Process(ctx context.Context, postID int64) error {
	if err := ctx.Err(); err != nil {
		return err
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

	for index, image := range images {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := w.uploadImage(ctx, post, image); err != nil {
			w.finish(ctx, postID, posts.StatusFailed, "上传图片失败")
			return errors.New("上传图片失败")
		}
		if index < len(images)-1 {
			if err := w.sleep(ctx, w.interval); err != nil {
				return err
			}
		}
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

	if err := w.repo.MarkPostFinished(ctx, postID, posts.StatusCompleted, ""); err != nil {
		return err
	}
	w.logger.Printf("帖子上传完成：编号 %d，图片 %d 张", postID, stored)
	return nil
}

// uploadImage 上传单张图片。遇到限流按返回的等待时长退避重试，其余错误直接记为失败。
func (w *Worker) uploadImage(ctx context.Context, post *posts.Post, image posts.Image) error {
	path := filepath.Join(w.dataDir, filepath.FromSlash(image.TempFilePath))

	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		file, openErr := os.Open(path)
		if openErr != nil {
			_ = w.repo.MarkImageFailed(ctx, image.ID, "读取临时文件失败")
			return errors.New("读取临时文件失败")
		}
		result, uploadErr := w.provider.Upload(ctx, storage.UploadRequest{
			FileName: filepath.Base(path),
			MIMEType: image.MimeType,
			Size:     image.FileSize,
			Mode:     post.UploadMode,
			Reader:   file,
		})
		_ = file.Close()

		if uploadErr == nil {
			if err := w.repo.MarkImageStored(ctx, image.ID, result.FileID, result.MessageID, w.provider.Name()); err != nil {
				return err
			}
			_ = os.Remove(path)
			return nil
		}

		var limited *storage.RateLimitError
		if errors.As(uploadErr, &limited) && attempt < w.maxAttempts {
			wait := limited.RetryAfter
			if wait <= 0 {
				wait = w.interval
			}
			if err := w.sleep(ctx, wait); err != nil {
				return err
			}
			continue
		}

		_ = w.repo.MarkImageFailed(ctx, image.ID, "上传失败")
		return errors.New("上传失败")
	}
}

func (w *Worker) finish(ctx context.Context, postID int64, status, message string) {
	_ = w.repo.MarkPostFinished(ctx, postID, status, message)
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
