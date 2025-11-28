package callback

import (
	"context"
	"fmt"
	"time"

	"github.com/go-resty/resty/v2"

	"github.com/fusionn-subs/pkg/logger"
)

type Payload struct {
	ChsSubtitlePath string `json:"chs_subtitle_path"`
	EngSubtitlePath string `json:"eng_subtitle_path"`
	VideoPath       string `json:"video_path"`
}

type Client struct {
	url  string
	http *resty.Client
}

func NewClient(url string, timeout time.Duration, retries int) *Client {
	httpClient := resty.New().
		SetTimeout(timeout).
		SetRetryCount(retries).
		SetRetryWaitTime(500 * time.Millisecond).
		SetRetryMaxWaitTime(5 * time.Second).
		AddRetryCondition(func(r *resty.Response, err error) bool {
			return err != nil || r.StatusCode() >= 500
		})

	return &Client{
		url:  url,
		http: httpClient,
	}
}

func (c *Client) Send(ctx context.Context, payload Payload) error {
	resp, err := c.http.R().
		SetContext(ctx).
		SetHeader("Content-Type", "application/json").
		SetBody(payload).
		Post(c.url)

	if err != nil {
		return fmt.Errorf("send callback: %w", err)
	}

	if resp.StatusCode() >= 300 {
		// Include response body for debugging
		body := resp.String()
		if len(body) > 200 {
			body = body[:200] + "..."
		}
		return fmt.Errorf("callback failed: status %d, body: %s", resp.StatusCode(), body)
	}

	logger.Infof("📤 Callback delivered: %s", payload.ChsSubtitlePath)
	return nil
}
