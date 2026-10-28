package callback

import (
	"context"
	"fmt"
	"time"

	"github.com/go-resty/resty/v2"
	"go.uber.org/zap"

	"github.com/fusionn-subs/pkg/logger"
)

var log = logger.Component("callback")

type Payload struct {
	JobID           string `json:"job_id"`
	VideoPath       string `json:"video_path"`
	EngSubtitlePath string `json:"eng_subtitle_path"`
	ChsSubtitlePath string `json:"chs_subtitle_path"`
}

type Client struct {
	url                 string
	http                *resty.Client
	maxRetries          int
	retryBackoffSeconds []int
}

func NewClient(url string, timeout time.Duration, maxRetries int, retryBackoffSeconds []int) *Client {
	// Default backoff if not provided
	if len(retryBackoffSeconds) == 0 {
		retryBackoffSeconds = []int{1, 2, 4, 8, 16}
	}

	httpClient := resty.New().
		SetTimeout(timeout)

	return &Client{
		url:                 url,
		http:                httpClient,
		maxRetries:          maxRetries,
		retryBackoffSeconds: retryBackoffSeconds,
	}
}

func (c *Client) Send(ctx context.Context, payload Payload) error {
	ctx = logger.WithJobID(ctx, payload.JobID)

	var lastErr error

	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			// Calculate backoff duration
			backoffIdx := attempt - 1
			if backoffIdx >= len(c.retryBackoffSeconds) {
				backoffIdx = len(c.retryBackoffSeconds) - 1
			}
			backoffDuration := time.Duration(c.retryBackoffSeconds[backoffIdx]) * time.Second

			log.Warn(ctx, "callback_retry", "Callback will retry",
				zap.Int("retry", attempt),
				zap.Int64("retry_after_ms", backoffDuration.Milliseconds()),
				zap.Error(lastErr),
			)

			select {
			case <-time.After(backoffDuration):
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		started := time.Now()
		resp, err := c.http.R().
			SetContext(ctx).
			SetHeader("Content-Type", "application/json").
			SetBody(payload).
			Post(c.url)
		if err != nil {
			lastErr = fmt.Errorf("send callback: %w", err)
			continue
		}

		if resp.StatusCode() >= 300 {
			// The response body is not logged; the status is the failure reason.
			lastErr = fmt.Errorf("callback failed: status %d", resp.StatusCode())

			// Don't retry on 4xx errors (client errors)
			if resp.StatusCode() >= 400 && resp.StatusCode() < 500 {
				return lastErr
			}
			continue
		}

		log.Info(ctx, "callback_delivered", "Callback delivered",
			zap.Int("status_code", resp.StatusCode()),
			logger.DurMs(time.Since(started)),
			zap.Int("attempt_number", attempt+1),
		)
		return nil
	}

	return fmt.Errorf("callback failed after %d attempts: %w", c.maxRetries+1, lastErr)
}
