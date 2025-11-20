package worker

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/fusionn-subs/internal/callback"
	"github.com/fusionn-subs/internal/job"
	"github.com/fusionn-subs/internal/translator"
)

type Config struct {
	Queue       string
	PollTimeout time.Duration
}

type Worker struct {
	redis      *redis.Client
	cfg        Config
	translator translator.Translator
	callback   *callback.Client
	logger     *zap.Logger
}

func New(redisClient *redis.Client, cfg Config, translator translator.Translator, callback *callback.Client, logger *zap.Logger) *Worker {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Worker{
		redis:      redisClient,
		cfg:        cfg,
		translator: translator,
		callback:   callback,
		logger:     logger,
	}
}

func (w *Worker) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			if err := w.consumeOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
				w.logger.Error("worker iteration failed", zap.Error(err))
				time.Sleep(time.Second)
			}
		}
	}
}

func (w *Worker) consumeOnce(ctx context.Context) error {
	result, err := w.redis.BRPop(ctx, w.cfg.PollTimeout, w.cfg.Queue).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil
		}
		return err
	}

	if len(result) < 2 {
		return errors.New("redis returned unexpected payload")
	}

	rawMsg := result[1]
	var msg job.Message
	if err := json.Unmarshal([]byte(rawMsg), &msg); err != nil {
		w.logger.Error("failed to parse message", zap.Error(err))
		return nil
	}

	w.logger.Info("message received",
		zap.String("path", msg.Path),
		zap.String("file_name", msg.FileName),
		zap.String("video_path", msg.VideoPath),
		zap.String("provider", msg.Provider),
	)

	chsPath, err := w.translator.Translate(ctx, msg)
	if err != nil {
		w.logger.Error("translation failed", zap.Error(err), zap.String("path", msg.Path))
		return nil
	}

	payload := callback.Payload{
		ChsSubtitlePath: chsPath,
		EngSubtitlePath: msg.Path,
		VideoPath:       msg.VideoPath,
	}

	if err := w.callback.Send(ctx, payload); err != nil {
		w.logger.Error("callback failed", zap.Error(err), zap.String("chs_subtitle_path", chsPath))
		return nil
	}

	return nil
}
