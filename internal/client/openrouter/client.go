package openrouter

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-resty/resty/v2"

	"github.com/fusionn-subs/pkg/logger"
)

const defaultBaseURL = "https://openrouter.ai/api/v1"

// Client interacts with OpenRouter API.
type Client struct {
	baseURL string
	apiKey  string
	client  *resty.Client
}

// Model represents an OpenRouter model with metadata.
type Model struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ContextLen  int    `json:"context_length"`
	Description string `json:"description"`
	Pricing     struct {
		Prompt     string `json:"prompt"`
		Completion string `json:"completion"`
	} `json:"pricing"`
}

// NewClient creates an OpenRouter API client.
func NewClient(apiKey string) *Client {
	return &Client{
		baseURL: defaultBaseURL,
		apiKey:  apiKey,
		client: resty.New().
			SetTimeout(60*time.Second). // Generous timeout for large model list (~600+ models)
			SetHeader("Authorization", "Bearer "+apiKey).
			SetHeader("Content-Type", "application/json").
			SetHeader("HTTP-Referer", "https://github.com/fusionn-subs"). // For OpenRouter analytics
			SetHeader("X-Title", "fusionn-subs").                         // App identifier
			SetRetryCount(3).                                             // Retry up to 3 times for transient failures
			SetRetryWaitTime(5 * time.Second).                            // Initial wait: 5s
			SetRetryMaxWaitTime(30 * time.Second).                        // Max wait: 30s (exponential backoff)
			AddRetryCondition(func(r *resty.Response, err error) bool {
				// Retry on 429 (rate limit) or 5xx (server errors)
				return r.StatusCode() == 429 || r.StatusCode() >= 500
			}).
			OnAfterResponse(func(c *resty.Client, r *resty.Response) error {
				if r.Request.Attempt > 0 {
					logger.Warnf("⚠️  OpenRouter API retry attempt #%d (status: %d)", r.Request.Attempt, r.StatusCode())
				}
				return nil
			}),
	}
}

// GetFreeModels fetches all free models from OpenRouter.
// Note: OpenRouter API doesn't support server-side filtering or pagination.
// The endpoint returns all models (~600+) in a single response.
// This is acceptable because:
// - Called only at startup + daily (low frequency)
// - Response size ~1-2MB (reasonable for modern systems)
func (c *Client) GetFreeModels() ([]Model, error) {
	var result struct {
		Data []Model `json:"data"`
	}

	resp, err := c.client.R().
		SetResult(&result).
		Get(c.baseURL + "/models")

	if err != nil {
		return nil, fmt.Errorf("fetch models: %w", err)
	}

	if !resp.IsSuccess() {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode(), resp.String())
	}

	// Filter for free models only (let evaluator decide on suitability)
	freeModels := make([]Model, 0, 100)
	for _, model := range result.Data {
		if isFreeModel(model) {
			freeModels = append(freeModels, model)
		}
	}

	return freeModels, nil
}

// isFreeModel checks if a model is free based on OpenRouter conventions:
// 1. Model ID ends with ":free" suffix (e.g., "google/gemini-3-flash:free")
// 2. Both prompt and completion pricing are "0"
// Reference: https://openrouter.ai/models?q=free
func isFreeModel(model Model) bool {
	if strings.HasSuffix(model.ID, ":free") {
		return true
	}
	// Check if both prompt and completion pricing are "0"
	return model.Pricing.Prompt == "0" && model.Pricing.Completion == "0"
}
