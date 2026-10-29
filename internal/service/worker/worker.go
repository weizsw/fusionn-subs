package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/fusionn-subs/internal/client/callback"
	"github.com/fusionn-subs/internal/service/translator"
	"github.com/fusionn-subs/internal/types"
	"github.com/fusionn-subs/pkg/logger"
)

var log = logger.Component("worker")

const (
	// Backoff settings for Redis connection errors
	initialBackoff = time.Second
	maxBackoff     = 30 * time.Second
	backoffFactor  = 2

	// Delay between translation attempts when the queue config does not set one
	defaultTranslationRetryDelay = 2 * time.Second

	// deadLetterSuffix names the list, next to the queue, that keeps messages
	// whose job panicked, for inspection and replay.
	deadLetterSuffix = ":dead"

	// deadLetterTimeout bounds the dead-letter write, which must not depend on
	// the job's own cancellation.
	deadLetterTimeout = 5 * time.Second
)

type Config struct {
	Queue                 string
	PollTimeout           time.Duration
	MaxTranslationRetries int
	TranslationRetryDelay time.Duration
}

type GlossaryPayload struct {
	Terminology         []translator.Terminology
	BuildTerminologyMap bool
}

type GlossaryPreparer interface {
	Prepare(ctx context.Context, msg types.JobMessage) (GlossaryPayload, error)
}

// queueStore is the part of the Redis client the worker uses.
type queueStore interface {
	BRPop(ctx context.Context, timeout time.Duration, keys ...string) *redis.StringSliceCmd
	LPush(ctx context.Context, key string, values ...interface{}) *redis.IntCmd
}

type Worker struct {
	redis      queueStore
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
				log.Warn(ctx, "queue_unavailable", "Queue unavailable",
					zap.Int64("retry_after_ms", backoff.Milliseconds()),
					zap.Error(err),
				)
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
		log.Warn(ctx, "queue_payload_invalid", "Unexpected queue payload")
		return nil
	}

	rawMsg := result[1]
	var msg types.JobMessage
	if err := json.Unmarshal([]byte(rawMsg), &msg); err != nil {
		log.Error(ctx, "job_dropped", "Dropping unparseable job message", zap.Error(err))
		return nil // Bad message, don't retry
	}

	if w.handleMessage(ctx, msg) {
		w.deadLetter(logger.WithJobID(ctx, msg.JobID), rawMsg)
	}
	return nil
}

// handleMessage runs one job and records its single boundary failure event. It
// reports whether the job panicked, so the caller can keep the message.
func (w *Worker) handleMessage(ctx context.Context, msg types.JobMessage) (panicked bool) {
	ctx = logger.WithJobID(ctx, msg.JobID)

	log.Info(ctx, "job_received", "Job received",
		zap.String("media_type", msg.MediaType),
		zap.String("subtitle_path", msg.SubtitlePath),
	)

	started := time.Now()
	if err := w.processJob(ctx, msg); err != nil {
		log.Error(ctx, "job_failed", "Job failed",
			zap.String("stage", jobStage(err)),
			logger.DurMs(time.Since(started)),
			zap.Error(err),
			logger.PanicStack(err),
		)
		// Note: Message is already consumed. Only a panicked job is
		// dead-lettered; other failures are not retried or kept.
		var pe *logger.PanicError
		return errors.As(err, &pe)
	}
	return false
}

// deadLetter keeps the original message of a panicked job next to the queue,
// so it can be inspected and replayed after a fix instead of being lost.
func (w *Worker) deadLetter(ctx context.Context, raw string) {
	key := w.cfg.Queue + deadLetterSuffix
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), deadLetterTimeout)
	defer cancel()
	if err := w.redis.LPush(writeCtx, key, raw).Err(); err != nil {
		log.Error(ctx, "dead_letter_failed", "Message could not be dead-lettered",
			zap.String("queue", key), zap.Error(err))
		return
	}
	log.Warn(ctx, "job_dead_lettered", "Message kept for replay", zap.String("queue", key))
}

func (w *Worker) processJob(ctx context.Context, msg types.JobMessage) (err error) {
	// A panic fails this job like any other error instead of ending the
	// process, which would also lose the consumed message.
	stage := "translation"
	defer func() {
		if r := recover(); r != nil {
			err = &stageError{stage: stage, err: logger.Recovered(r)}
		}
	}()

	chsPath, err := w.translateJob(ctx, msg)
	if err != nil {
		return &stageError{stage: "translation", err: err}
	}

	payload := callback.Payload{
		JobID:           msg.JobID,
		VideoPath:       msg.VideoPath,
		EngSubtitlePath: msg.SubtitlePath,
		ChsSubtitlePath: chsPath,
	}

	stage = "callback"
	if err := w.callback.Send(ctx, payload); err != nil {
		return &stageError{stage: "callback", err: err}
	}

	log.Info(ctx, "job_completed", "Job completed", zap.String("chs_subtitle_path", chsPath))
	return nil
}

func (w *Worker) translateJob(ctx context.Context, msg types.JobMessage) (string, error) {
	glossaryPayload := GlossaryPayload{}
	if w.glossary != nil {
		payload, err := w.glossary.Prepare(ctx, msg)
		if err != nil {
			return "", fmt.Errorf("prepare glossary: %w", err)
		}
		glossaryPayload = payload
	}

	var lastErr error
	maxRetries := w.cfg.MaxTranslationRetries
	if maxRetries <= 0 {
		maxRetries = 3 // Default
	}
	retryDelay := w.cfg.TranslationRetryDelay
	if retryDelay <= 0 {
		retryDelay = defaultTranslationRetryDelay
	}

	for attempt := 1; attempt <= maxRetries; attempt++ {
		var err error
		chsPath, err := w.translator.Translate(ctx, translator.Request{
			Job:                 msg,
			Terminology:         glossaryPayload.Terminology,
			BuildTerminologyMap: glossaryPayload.BuildTerminologyMap,
		})
		if err == nil {
			if attempt > 1 {
				log.Info(ctx, "translation_recovered", "Translation recovered after retry",
					zap.Int("attempt_number", attempt),
				)
			}
			return chsPath, nil
		}

		lastErr = err

		if errors.Is(err, translator.ErrAllModelsExhausted) {
			break
		}
		if attempt == maxRetries {
			break
		}

		attrs := []zap.Field{
			zap.Int("retry", attempt),
			zap.Error(err),
		}
		if errors.Is(err, translator.ErrRateLimited) {
			log.Warn(ctx, "translation_retry", "Translation will retry", attrs...)
			continue
		}
		attrs = append(attrs, zap.Int64("retry_after_ms", retryDelay.Milliseconds()))
		log.Warn(ctx, "translation_retry", "Translation will retry", attrs...)

		select {
		case <-time.After(retryDelay):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}

	if errors.Is(lastErr, translator.ErrAllModelsExhausted) {
		return "", fmt.Errorf("all models exhausted: %w", lastErr)
	}
	return "", fmt.Errorf("translation failed after %d attempts: %w", maxRetries, lastErr)
}

// stageError records which pipeline stage failed so the worker boundary can
// report the stage while logging the single failure record.
type stageError struct {
	stage string
	err   error
}

func (e *stageError) Error() string { return e.err.Error() }

func (e *stageError) Unwrap() error { return e.err }

func jobStage(err error) string {
	var se *stageError
	if errors.As(err, &se) {
		return se.stage
	}
	return "unknown"
}
