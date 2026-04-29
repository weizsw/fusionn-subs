package glossary

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

const (
	defaultOpenAIChatCompletionsEndpoint = "/v1/chat/completions"
	openAIChatCompletionsEndpointSuffix  = "/chat/completions"
	glossarySystemPrompt                 = `You extract subtitle glossary entries from candidates. Return strict JSON only with this shape:
{"entries":[{"source_term":"...","normalized_term":"...","target_language":"...","target_text":"...","definition":"...","translation_mode":"translate|preserve|transliterate|contextual","category":"acronym|organization|brand|product|character|place|technical_term|phrase","confidence":0.0,"evidence":["..."]}]}
Rules:
- Return entries only for candidates likely to cause consistency problems across subtitle lines or episodes.
- Prefer acronyms, organization names, brands, product names, fictional/media-specific places, groups, abilities, artifacts, titles, and technical terms.
- Prefer terms that should be consistently preserved, transliterated, or translated with one fixed rendering.
- Use translation_mode "contextual" only when the term is worth remembering but should not be injected as one fixed SOURCE::TRANSLATION mapping.
- Skip ordinary person or character names that appear only once unless snippets show special in-media meaning, alias/title usage, ambiguity, or recurring consistency risk.
- Skip common real-world places with standard translations, such as New York, unless snippets show special media-specific meaning.
- Skip ordinary street names or addresses, such as Madison Avenue, unless used as a recurring concept or organization.
- Skip malformed phrases such as Have Carbone. If the meaningful proper noun is also present as a candidate, return that candidate instead; otherwise skip the malformed phrase.
- Skip common English words only capitalized because they start a sentence.
- Skip generic speaker labels and caption descriptions, such as MAN, WOMAN, Door Opens, and Phone Ringing.
- Skip common abbreviations with stable obvious translations, such as OK or TV, unless they are media-specific.
- Skip phrases whose target translation should vary by sentence.
- Do not invent or guess a fixed target translation. If the snippets do not support a reliable fixed translation, skip the candidate or use translation_mode "contextual" only when worth remembering.
- If no candidate clearly needs glossary guidance, return an empty entries array.
- source_term must copy a candidates[].source_term exactly.
- normalized_term must copy the matching candidates[].normalized_term exactly.
- target_text must be non-empty.`
)

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
		cfg.Endpoint = defaultOpenAIChatCompletionsEndpoint
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
		Model:       c.cfg.Model,
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
	type promptCandidate struct {
		SourceTerm     string   `json:"source_term"`
		NormalizedTerm string   `json:"normalized_term"`
		Frequency      int      `json:"frequency"`
		Snippets       []string `json:"snippets,omitempty"`
	}
	type promptEntry struct {
		Scope           Scope           `json:"scope"`
		MediaKey        string          `json:"media_key,omitempty"`
		SourceTerm      string          `json:"source"`
		NormalizedTerm  string          `json:"normalized"`
		TargetLanguage  string          `json:"target_language"`
		TargetText      string          `json:"target"`
		Definition      string          `json:"definition,omitempty"`
		TranslationMode TranslationMode `json:"translation_mode"`
		Category        Category        `json:"category"`
		Confidence      float64         `json:"confidence"`
		EvidenceCount   int             `json:"evidence_count"`
	}
	candidates := make([]promptCandidate, 0, len(req.Candidates))
	for _, candidate := range req.Candidates {
		candidates = append(candidates, promptCandidate{
			SourceTerm:     candidate.Term,
			NormalizedTerm: candidate.NormalizedTerm,
			Frequency:      candidate.Frequency,
			Snippets:       candidate.Snippets,
		})
	}
	existing := make([]promptEntry, 0, len(req.ExistingEntries))
	for _, entry := range req.ExistingEntries {
		existing = append(existing, promptEntry{
			Scope:           entry.Scope,
			MediaKey:        entry.MediaKey,
			SourceTerm:      promptDisplayTerm(entry),
			NormalizedTerm:  strings.TrimSpace(entry.NormalizedTerm),
			TargetLanguage:  entry.TargetLanguage,
			TargetText:      entry.TargetText,
			Definition:      entry.Definition,
			TranslationMode: entry.TranslationMode,
			Category:        entry.Category,
			Confidence:      entry.Confidence,
			EvidenceCount:   entry.EvidenceCount,
		})
	}
	payload := struct {
		MediaTitle     string            `json:"media_title"`
		MediaType      string            `json:"media_type"`
		Season         int               `json:"season,omitempty"`
		Episode        int               `json:"episode,omitempty"`
		MediaKey       string            `json:"media_key"`
		TargetLanguage string            `json:"target_language"`
		Candidates     []promptCandidate `json:"candidates"`
		Existing       []promptEntry     `json:"existing_entries"`
	}{
		MediaTitle:     req.Job.MediaTitle,
		MediaType:      req.Job.MediaType,
		Season:         req.Job.Season,
		Episode:        req.Job.Episode,
		MediaKey:       req.MediaKey,
		TargetLanguage: req.TargetLanguage,
		Candidates:     candidates,
		Existing:       existing,
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
	endpoint = strings.TrimSuffix(endpoint, openAIChatCompletionsEndpointSuffix)
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
