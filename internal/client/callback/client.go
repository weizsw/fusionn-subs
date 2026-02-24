package callback

import (
	"context"
	"fmt"
	"time"

	"github.com/go-resty/resty/v2"

	"github.com/fusionn-subs/pkg/logger"
)

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
	var lastErr error

	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			// Calculate backoff duration
			backoffIdx := attempt - 1
			if backoffIdx >= len(c.retryBackoffSeconds) {
				backoffIdx = len(c.retryBackoffSeconds) - 1
			}
			backoffDuration := time.Duration(c.retryBackoffSeconds[backoffIdx]) * time.Second

			logger.Infof("⏳ Callback retry %d/%d after %v: job_id=%s", attempt, c.maxRetries, backoffDuration, payload.JobID)

			select {
			case <-time.After(backoffDuration):
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		resp, err := c.http.R().
			SetContext(ctx).
			SetHeader("Content-Type", "application/json").
			SetBody(payload).
			Post(c.url)
		if err != nil {
			lastErr = fmt.Errorf("send callback: %w", err)
			logger.Warnf("Callback attempt %d failed: %v", attempt+1, lastErr)
			continue
		}

		if resp.StatusCode() >= 300 {
			body := resp.String()
			if len(body) > 200 {
				body = body[:200] + "..."
			}
			lastErr = fmt.Errorf("callback failed: status %d, body: %s", resp.StatusCode(), body)
			logger.Warnf("Callback attempt %d failed: %v", attempt+1, lastErr)

			// Don't retry on 4xx errors (client errors)
			if resp.StatusCode() >= 400 && resp.StatusCode() < 500 {
				return lastErr
			}
			continue
		}

		logger.Infof("📤 Callback delivered: job_id=%s (attempt %d)", payload.JobID, attempt+1)
		return nil
	}

	logger.Errorf("❌ Callback failed after %d attempts: job_id=%s, error: %v", c.maxRetries+1, payload.JobID, lastErr)
	return fmt.Errorf("callback failed after %d attempts: %w", c.maxRetries+1, lastErr)
}
