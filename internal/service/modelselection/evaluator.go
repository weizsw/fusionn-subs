package modelselection

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"regexp"
	"strings"
	"text/template"
	"time"

	"github.com/go-resty/resty/v2"
	"go.uber.org/zap"

	"github.com/fusionn-subs/internal/client/openrouter"
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
					log.Warn(context.Background(), "api_retry", "Gemini API retry",
						zap.Int("attempt_number", r.Request.Attempt),
						zap.Int("status_code", r.StatusCode()),
					)
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
	ctx := context.Background()
	log.Info(ctx, "evaluation_started", "Evaluating models",
		zap.Int("model_count", len(models)),
		zap.String("evaluator_model", e.model),
	)
	log.Debug(ctx, "models_to_evaluate", "Models to evaluate",
		zap.String("models", strings.Join(modelIDs, ",")),
	)

	selectedModel, err := e.callGemini(ctx, prompt)
	if err != nil {
		return "", fmt.Errorf("gemini evaluation failed: %w", err)
	}

	// Validate selected model exists in the list
	selectedModel = strings.TrimSpace(selectedModel)
	for _, m := range models {
		if m.ID == selectedModel {
			log.Info(ctx, "model_selected", "Model selected", zap.String("model", selectedModel))
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
		log.Warn(ctx, "model_not_found", "Model not found in candidate list",
			zap.String("model", selectedModel),
			zap.String("suggestions", strings.Join(suggestions, ",")),
		)
		log.Info(ctx, "model_selected", "Model selected",
			zap.String("model", suggestions[0]),
			zap.String("matched", "closest"),
		)
		return suggestions[0], nil
	}

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
					"text": "You are an AI model evaluation expert. Select the BEST model for English to Chinese subtitle translation from the provided list. Respond with ONLY the model ID, no explanations or reasoning.",
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
		},
		// Note: Google Search is intentionally NOT enabled here.
		// For model selection, we're comparing a known list of models, not researching unknown information.
		// Enabling search causes verbose, research-style responses instead of concise model IDs.
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
		SetHeader("x-goog-api-key", e.apiKey).
		SetBody(requestBody).
		SetResult(&result).
		SetError(&result).
		Post(url)

	if err != nil {
		// The key travels in a header, so a transport error cannot echo it.
		return "", fmt.Errorf("http request failed: %w", err)
	}

	if !resp.IsSuccess() {
		// Provider error bodies are not replayed; the status is the reason.
		return "", fmt.Errorf("API error %d", resp.StatusCode())
	}

	if len(result.Candidates) == 0 || len(result.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("no response from Gemini")
	}

	rawResponse := result.Candidates[0].Content.Parts[0].Text
	log.Debug(ctx, "model_evaluation_response", "Gemini response received",
		zap.Int("response_length", len(rawResponse)),
	)

	// Warn if response is verbose (indicates prompt not followed)
	if len(rawResponse) > 100 {
		log.Warn(ctx, "verbose_response", "Gemini returned verbose response",
			zap.Int("response_length", len(rawResponse)),
		)
	}

	selectedModel := strings.TrimSpace(rawResponse)

	// Robust extraction: Look for model ID pattern in case of verbose response
	// Pattern: provider/model:free OR provider/model (e.g., "google/gemini-2.0-flash-exp:free")
	if strings.Contains(selectedModel, "\n") || strings.Contains(selectedModel, " ") {
		// Response contains explanation - try to extract model ID using regex
		// Look for pattern: word/word OR word/word:free
		re := regexp.MustCompile(`([a-z0-9-]+/[a-z0-9.-]+(?::free)?)`)
		matches := re.FindStringSubmatch(selectedModel)
		if len(matches) > 1 {
			extractedID := matches[1]
			log.Debug(ctx, "model_extracted", "Model ID extracted from verbose response", zap.String("model", extractedID))
			selectedModel = extractedID
		} else {
			// Fallback: take first word/line
			if idx := strings.Index(selectedModel, "\n"); idx != -1 {
				selectedModel = selectedModel[:idx]
			}
			if idx := strings.Index(selectedModel, " "); idx != -1 {
				selectedModel = selectedModel[:idx]
			}
			selectedModel = strings.TrimSpace(selectedModel)
			log.Debug(ctx, "model_extracted", "Model ID extracted from verbose response", zap.String("model", selectedModel))
		}
	}

	return selectedModel, nil
}
