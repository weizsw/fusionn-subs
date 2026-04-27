package glossary

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

const glossarySystemPrompt = "You extract subtitle glossary entries. Return strict JSON only with an entries array."

type OpenAICompatibleConfig struct {
	BaseURL     string
	Endpoint    string
	APIKey      string
	Model       string
	Temperature float64
}

type OpenAICompatibleClient struct {
	cfg    OpenAICompatibleConfig
	client openai.Client
}

var _ LLMClient = (*OpenAICompatibleClient)(nil)

func NewOpenAICompatibleClient(cfg OpenAICompatibleConfig) *OpenAICompatibleClient {
	if cfg.Endpoint == "" {
		cfg.Endpoint = "/v1/chat/completions"
	}
	client := openai.NewClient(
		option.WithBaseURL(normalizeOpenAIBaseURL(cfg.BaseURL, cfg.Endpoint)),
		option.WithAPIKey(cfg.APIKey),
	)
	return &OpenAICompatibleClient{cfg: cfg, client: client}
}

func (c *OpenAICompatibleClient) GenerateGlossary(ctx context.Context, req GenerateRequest) (GenerateResponse, error) {
	userPrompt, err := buildGlossaryUserPrompt(req)
	if err != nil {
		return GenerateResponse{}, err
	}
	resp, err := c.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model:       openai.ChatModel(c.cfg.Model),
		Temperature: openai.Float(c.cfg.Temperature),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.DeveloperMessage(glossarySystemPrompt),
			openai.UserMessage(userPrompt),
		},
	})
	if err != nil {
		return GenerateResponse{}, fmt.Errorf("call glossary llm: %w", err)
	}
	if len(resp.Choices) == 0 {
		return GenerateResponse{}, fmt.Errorf("glossary llm returned no choices")
	}
	return decodeGenerateResponse(resp.Choices[0].Message.Content)
}

func buildGlossaryUserPrompt(req GenerateRequest) (string, error) {
	payload := struct {
		MediaTitle     string        `json:"media_title"`
		MediaType      string        `json:"media_type"`
		Season         int           `json:"season,omitempty"`
		Episode        int           `json:"episode,omitempty"`
		MediaKey       string        `json:"media_key"`
		TargetLanguage string        `json:"target_language"`
		Candidates     []Candidate   `json:"candidates"`
		Existing       []PromptEntry `json:"existing_entries"`
	}{
		MediaTitle:     req.Job.MediaTitle,
		MediaType:      req.Job.MediaType,
		Season:         req.Job.Season,
		Episode:        req.Job.Episode,
		MediaKey:       req.MediaKey,
		TargetLanguage: req.TargetLanguage,
		Candidates:     req.Candidates,
		Existing:       req.ExistingEntries,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal glossary prompt payload: %w", err)
	}
	return string(b), nil
}

func normalizeOpenAIBaseURL(baseURL, endpoint string) string {
	baseURL = strings.TrimRight(baseURL, "/")
	endpoint = strings.TrimRight(endpoint, "/")
	if strings.HasSuffix(endpoint, "/chat/completions") {
		endpoint = strings.TrimSuffix(endpoint, "/chat/completions")
	}
	if endpoint == "" {
		return baseURL
	}
	return baseURL + endpoint
}

func decodeGenerateResponse(content string) (GenerateResponse, error) {
	var out GenerateResponse
	if err := json.Unmarshal([]byte(content), &out); err != nil {
		return GenerateResponse{}, fmt.Errorf("decode glossary llm response: %w", err)
	}
	if err := out.Validate(); err != nil {
		return GenerateResponse{}, err
	}
	return out, nil
}
