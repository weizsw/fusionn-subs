package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/fusionn-subs/internal/client/callback"
	"github.com/fusionn-subs/internal/service/translator"
	"github.com/fusionn-subs/internal/types"
	"github.com/fusionn-subs/pkg/logger"
)

const (
	// Backoff settings for Redis connection errors
	initialBackoff = time.Second
	maxBackoff     = 30 * time.Second
	backoffFactor  = 2
)

type Config struct {
	Queue                 string
	PollTimeout           time.Duration
	MaxTranslationRetries int
}

type GlossaryPreparer interface {
	Prepare(ctx context.Context, msg types.JobMessage) (string, error)
}

type Worker struct {
	redis      *redis.Client
	cfg        Config
	translator translator.Translator
	glossary   GlossaryPreparer
	callback   *callback.Client
}

func New(redisClient *redis.Client, cfg Config, trans translator.Translator, glossary GlossaryPreparer, callbackClient *callback.Client) *Worker {
	return &Worker{
		redis:      redisClient,
		cfg:        cfg,
		translator: trans,
		glossary:   glossary,
		callback:   callbackClient,
	}
}

func (w *Worker) Run(ctx context.Context) error {
	backoff := initialBackoff

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			err := w.processNext(ctx)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return err
				}

				// Exponential backoff for connection errors
				logger.Errorf("Worker error: %v (retry in %v)", err, backoff)
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(backoff):
				}

				backoff = min(backoff*backoffFactor, maxBackoff)
			} else {
				// Reset backoff on success
				backoff = initialBackoff
			}
		}
	}
}

func (w *Worker) processNext(ctx context.Context) error {
	result, err := w.redis.BRPop(ctx, w.cfg.PollTimeout, w.cfg.Queue).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil // Timeout, no message - this is normal
		}
		return err // Connection error - will trigger backoff
	}

	if len(result) < 2 {
		logger.Warn("Redis returned unexpected payload format")
		return nil
	}

	rawMsg := result[1]
	var msg types.JobMessage
	if err := json.Unmarshal([]byte(rawMsg), &msg); err != nil {
		logger.Errorf("Failed to parse message (dropping): %v", err)
		return nil // Bad message, don't retry
	}

	logger.Infof("📥 Message received: %s (%s) [job: %s]", msg.MediaTitle, msg.MediaType, msg.JobID)

	// Process the job
	if err := w.processJob(ctx, msg); err != nil {
		logger.Errorf("❌ Job failed for %s: %v", msg.SubtitlePath, err)
		// Note: Message is already consumed. Consider implementing:
		// - Dead letter queue for failed jobs
		// - Retry with LPUSH back to queue
		return nil
	}

	return nil
}

func (w *Worker) processJob(ctx context.Context, msg types.JobMessage) error {
	chsPath, err := w.translateJob(ctx, msg)
	if err != nil {
		return err
	}

	payload := callback.Payload{
		JobID:           msg.JobID,
		VideoPath:       msg.VideoPath,
		EngSubtitlePath: msg.SubtitlePath,
		ChsSubtitlePath: chsPath,
	}

	if err := w.callback.Send(ctx, payload); err != nil {
		return err
	}

	logger.Infof("✅ Completed: %s", chsPath)
	return nil
}

func (w *Worker) translateJob(ctx context.Context, msg types.JobMessage) (string, error) {
	extraInstruction := ""
	if w.glossary != nil {
		block, err := w.glossary.Prepare(ctx, msg)
		if err != nil {
			return "", fmt.Errorf("prepare glossary: %w", err)
		}
		extraInstruction = block
	}

	var lastErr error
	maxRetries := w.cfg.MaxTranslationRetries
	if maxRetries <= 0 {
		maxRetries = 3 // Default
	}

	for attempt := 1; attempt <= maxRetries; attempt++ {
		if attempt > 1 {
			logger.Infof("⏳ Translation retry %d/%d: job_id=%s", attempt-1, maxRetries-1, msg.JobID)
		}

		var err error
		chsPath, err := w.translator.Translate(ctx, translator.Request{
			Job:              msg,
			ExtraInstruction: extraInstruction,
		})
		if err == nil {
			if attempt > 1 {
				logger.Infof("✅ Translation succeeded on attempt %d", attempt)
			}
			return chsPath, nil
		}

		lastErr = err
		logger.Warnf("Translation attempt %d failed: %v", attempt, err)

		if errors.Is(err, translator.ErrAllModelsExhausted) {
			break
		}

		if attempt < maxRetries && !errors.Is(err, translator.ErrRateLimited) {
			select {
			case <-time.After(2 * time.Second):
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
	}

	if errors.Is(lastErr, translator.ErrAllModelsExhausted) {
		logger.Errorf("❌ All models exhausted: job_id=%s", msg.JobID)
		return "", fmt.Errorf("all models exhausted: %w", lastErr)
	}
	logger.Errorf("❌ Translation failed after %d attempts: job_id=%s", maxRetries, msg.JobID)
	return "", fmt.Errorf("translation failed after %d attempts: %w", maxRetries, lastErr)
}
