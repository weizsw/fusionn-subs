package callback

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/hashicorp/go-retryablehttp"
	"go.uber.org/zap"
)

type Payload struct {
	ChsSubtitlePath string `json:"chs_subtitle_path"`
	EngSubtitlePath string `json:"eng_subtitle_path"`
	VideoPath       string `json:"video_path"`
}

type Client struct {
	url    string
	http   *retryablehttp.Client
	logger *zap.Logger
}

func NewClient(url string, timeout time.Duration, retries int, logger *zap.Logger) *Client {
	if logger == nil {
		logger = zap.NewNop()
	}
	httpClient := retryablehttp.NewClient()
	httpClient.Logger = nil
	httpClient.RetryMax = retries
	httpClient.RetryWaitMin = 500 * time.Millisecond
	httpClient.RetryWaitMax = 5 * time.Second
	httpClient.HTTPClient.Timeout = timeout

	return &Client{
		url:    url,
		http:   httpClient,
		logger: logger,
	}
}

func (c *Client) Send(ctx context.Context, payload Payload) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal callback payload: %w", err)
	}

	req, err := retryablehttp.NewRequest(http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create callback request: %w", err)
	}

	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("send callback: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("callback failed with status %d", resp.StatusCode)
	}

	c.logger.Info("callback delivered",
		zap.String("url", c.url),
		zap.String("chs_subtitle_path", payload.ChsSubtitlePath),
	)

	return nil
}
