package modelselection

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"strings"
	"text/template"
	"time"

	"github.com/go-resty/resty/v2"

	"github.com/fusionn-subs/internal/client/openrouter"
	"github.com/fusionn-subs/pkg/logger"
)

//go:embed prompts/model_evaluation.tmpl
var evaluationPromptTemplate string

// Evaluator selects the best model from a list of available models.
type Evaluator interface {
	SelectBestModel(models []openrouter.Model) (string, error)
}

// GeminiEvaluator uses Google Gemini to evaluate and select models via REST API.
type GeminiEvaluator struct {
	apiKey  string
	model   string
	baseURL string
	client  *resty.Client
}

// NewGeminiEvaluator creates a Gemini-based model evaluator.
func NewGeminiEvaluator(apiKey, model string) *GeminiEvaluator {
	if model == "" {
		model = "gemini-3-flash-preview"
	}
	return &GeminiEvaluator{
		apiKey:  apiKey,
		model:   model,
		baseURL: "https://generativelanguage.googleapis.com/v1beta/models",
		client: resty.New().
			SetTimeout(3*time.Minute). // Generous timeout for high thinking level + search grounding
			SetHeader("Content-Type", "application/json").
			SetRetryCount(3).                      // Retry up to 3 times for transient failures
			SetRetryWaitTime(5 * time.Second).     // Initial wait: 5s
			SetRetryMaxWaitTime(30 * time.Second). // Max wait: 30s (exponential backoff)
			AddRetryCondition(func(r *resty.Response, err error) bool {
				// Retry on 429 (rate limit) or 5xx (server errors)
				return r.StatusCode() == 429 || r.StatusCode() >= 500
			}).
			OnAfterResponse(func(c *resty.Client, r *resty.Response) error {
				if r.Request.Attempt > 0 {
					logger.Warnf("⚠️  Gemini API retry attempt #%d (status: %d)", r.Request.Attempt, r.StatusCode())
				}
				return nil
			}),
	}
}

// SelectBestModel uses Gemini to evaluate and select the best translation model.
func (e *GeminiEvaluator) SelectBestModel(models []openrouter.Model) (string, error) {
	if len(models) == 0 {
		return "", fmt.Errorf("no models provided")
	}

	prompt, err := e.buildEvaluationPrompt(models)
	if err != nil {
		return "", fmt.Errorf("build prompt: %w", err)
	}

	// Log model list being evaluated
	modelIDs := make([]string, len(models))
	for i, m := range models {
		modelIDs[i] = m.ID
	}
	logger.Infof("🤔 Evaluating %d models with %s", len(models), e.model)
	logger.Infof("📋 Models to evaluate: %v", modelIDs)

	// Log first part of prompt for debugging
	promptPreview := prompt
	logger.Debugf("📝 Evaluation prompt:\n%s", promptPreview)

	ctx := context.Background()
	selectedModel, err := e.callGemini(ctx, prompt)
	if err != nil {
		return "", fmt.Errorf("gemini evaluation failed: %w", err)
	}

	// Validate selected model exists in the list
	selectedModel = strings.TrimSpace(selectedModel)
	for _, m := range models {
		if m.ID == selectedModel {
			logger.Infof("✅ Selected model: %s", selectedModel)
			return selectedModel, nil
		}
	}

	// Model not found - try fuzzy matching for partial matches
	var suggestions []string
	for _, m := range models {
		if strings.Contains(m.ID, selectedModel) || strings.Contains(selectedModel, m.ID) {
			suggestions = append(suggestions, m.ID)
		}
	}

	if len(suggestions) > 0 {
		logger.Warnf("⚠️  Gemini returned invalid model '%s', possible matches: %v", selectedModel, suggestions)
		// Use first suggestion
		logger.Infof("✅ Using closest match: %s", suggestions[0])
		return suggestions[0], nil
	}

	// No matches found at all - list available models for debugging
	availableModels := make([]string, 0, len(models))
	for _, m := range models {
		availableModels = append(availableModels, m.ID)
	}
	logger.Errorf("Available models: %v", availableModels)

	return "", fmt.Errorf("selected model '%s' not found in available models", selectedModel)
}

// buildEvaluationPrompt constructs the evaluation prompt with model metadata using a template.
func (e *GeminiEvaluator) buildEvaluationPrompt(models []openrouter.Model) (string, error) {
	tmpl, err := template.New("evaluation").Parse(evaluationPromptTemplate)
	if err != nil {
		return "", fmt.Errorf("parse template: %w", err)
	}

	data := struct {
		Models []openrouter.Model
	}{
		Models: models,
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute template: %w", err)
	}

	return buf.String(), nil
}

// callGemini sends a prompt to Gemini using REST API with Gemini 3 features.
// Reference: https://ai.google.dev/gemini-api/docs/gemini-3
func (e *GeminiEvaluator) callGemini(ctx context.Context, prompt string) (string, error) {
	url := fmt.Sprintf("%s/%s:generateContent", e.baseURL, e.model)

	// Build request with Gemini 3 features
	requestBody := map[string]any{
		"system_instruction": map[string]any{
			"parts": []map[string]string{
				{
					"text": "You are an AI model evaluation expert specializing in language translation systems. Your task is to select the BEST model for English to Chinese subtitle translation. Use deep reasoning and research capabilities to make an informed decision.",
				},
			},
		},
		"contents": []map[string]any{
			{
				"parts": []map[string]string{
					{"text": prompt},
				},
			},
		},
		"generationConfig": map[string]any{
			"temperature": 0.1,
			// "thinkingConfig": map[string]string{
			// 	"thinkingLevel": "high", // Enable maximum thinking for Gemini 3 Flash
			// },
		},
		"tools": []map[string]any{
			{
				"googleSearch": map[string]any{}, // Enable Google Search grounding
			},
		},
	}

	// Response structure
	var result struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		Error struct {
			Message string `json:"message"`
			Code    int    `json:"code"`
		} `json:"error"`
	}

	resp, err := e.client.R().
		SetContext(ctx).
		SetQueryParam("key", e.apiKey).
		SetBody(requestBody).
		SetResult(&result).
		SetError(&result).
		Post(url)

	if err != nil {
		return "", fmt.Errorf("http request: %w", err)
	}

	if !resp.IsSuccess() {
		if result.Error.Message != "" {
			return "", fmt.Errorf("API error %d: %s", result.Error.Code, result.Error.Message)
		}
		return "", fmt.Errorf("API error %d: %s", resp.StatusCode(), resp.String())
	}

	if len(result.Candidates) == 0 || len(result.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("no response from Gemini")
	}

	rawResponse := result.Candidates[0].Content.Parts[0].Text
	logger.Infof("💬 Gemini raw response: %q", rawResponse)

	selectedModel := strings.TrimSpace(rawResponse)

	// Extract just the model ID if Gemini added extra text
	// Model IDs typically don't have spaces, so take the first word/line
	if idx := strings.Index(selectedModel, "\n"); idx != -1 {
		selectedModel = selectedModel[:idx]
	}
	if idx := strings.Index(selectedModel, " "); idx != -1 {
		selectedModel = selectedModel[:idx]
	}
	selectedModel = strings.TrimSpace(selectedModel)

	if selectedModel != rawResponse {
		logger.Infof("🔧 Extracted model ID: %q (from longer response)", selectedModel)
	}

	return selectedModel, nil
}
