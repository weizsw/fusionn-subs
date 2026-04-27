package glossary

import (
	"context"
	"fmt"

	"google.golang.org/genai"
)

type GeminiClient struct {
	client *genai.Client
	model  string
}

var _ LLMClient = (*GeminiClient)(nil)

func NewGeminiClient(ctx context.Context, apiKey, model string) (*GeminiClient, error) {
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("create gemini glossary client: %w", err)
	}
	return &GeminiClient{client: client, model: model}, nil
}

func (c *GeminiClient) GenerateGlossary(ctx context.Context, req GenerateRequest) (GenerateResponse, error) {
	userPrompt, err := buildGlossaryUserPrompt(req)
	if err != nil {
		return GenerateResponse{}, err
	}
	resp, err := c.client.Models.GenerateContent(
		ctx,
		c.model,
		genai.Text(glossarySystemPrompt+"\n\n"+userPrompt),
		nil,
	)
	if err != nil {
		return GenerateResponse{}, fmt.Errorf("call gemini glossary llm: %w", err)
	}
	return decodeGenerateResponse(resp.Text())
}
