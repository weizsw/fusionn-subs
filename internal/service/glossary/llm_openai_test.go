package glossary

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fusionn-subs/internal/types"
)

func TestOpenAICompatibleClientGenerateGlossary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices":[{"message":{"content":"{\"entries\":[{\"source_term\":\"SO15\",\"normalized_term\":\"so15\",\"target_language\":\"zh-Hans\",\"target_text\":\"SO15\",\"definition\":\"London police counter-terrorism unit\",\"translation_mode\":\"preserve\",\"category\":\"organization\",\"confidence\":0.92,\"evidence\":[\"SO15 asked DCI Carey\"]}]}"}}]
		}`))
	}))
	defer server.Close()

	client := NewOpenAICompatibleClient(OpenAICompatibleConfig{
		BaseURL:     server.URL,
		Endpoint:    "/v1/chat/completions",
		APIKey:      "test-key",
		Model:       "test-model",
		Temperature: 0.1,
	})

	resp, err := client.GenerateGlossary(context.Background(), GenerateRequest{
		Job:            types.JobMessage{MediaTitle: "The Capture", MediaType: "series"},
		MediaKey:       "tvdb:355620",
		TargetLanguage: "zh-Hans",
		Candidates: []Candidate{{
			Term:           "SO15",
			NormalizedTerm: "so15",
			Frequency:      3,
			Snippets:       []string{"SO15 asked DCI Carey"},
		}},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(resp.Entries) != 1 || resp.Entries[0].TargetText != "SO15" {
		t.Fatalf("response = %#v", resp)
	}
}

func TestGenerateResponseValidateRejectsInvalidConfidence(t *testing.T) {
	err := (GenerateResponse{Entries: []GeneratedEntry{{
		SourceTerm:     "X",
		NormalizedTerm: "x",
		TargetText:     "X",
		Confidence:     1.5,
	}}}).Validate()
	if err == nil || !strings.Contains(err.Error(), "confidence") {
		t.Fatalf("error = %v", err)
	}
}

func TestGlossaryPromptUsesResponseSchemaAndCandidateFieldNames(t *testing.T) {
	if !strings.Contains(glossarySystemPrompt, "source_term") {
		t.Fatalf("system prompt = %q, missing source_term response schema", glossarySystemPrompt)
	}

	prompt, err := buildGlossaryUserPrompt(GenerateRequest{
		Candidates: []Candidate{{
			Term:           "SO15",
			NormalizedTerm: "so15",
			Frequency:      3,
			Snippets:       []string{"SO15 asked DCI Carey"},
		}},
	})
	if err != nil {
		t.Fatalf("build prompt: %v", err)
	}

	var payload struct {
		Candidates []map[string]any `json:"candidates"`
		Existing   []map[string]any `json:"existing_entries"`
	}
	if err := json.Unmarshal([]byte(prompt), &payload); err != nil {
		t.Fatalf("decode prompt: %v", err)
	}
	if got := payload.Candidates[0]["source_term"]; got != "SO15" {
		t.Fatalf("candidate source_term = %v, want SO15; prompt = %s", got, prompt)
	}
	if got := payload.Candidates[0]["normalized_term"]; got != "so15" {
		t.Fatalf("candidate normalized_term = %v, want so15; prompt = %s", got, prompt)
	}
	if got := payload.Candidates[0]["frequency"]; got != float64(3) {
		t.Fatalf("candidate frequency = %v, want 3; prompt = %s", got, prompt)
	}
	snippets, ok := payload.Candidates[0]["snippets"].([]any)
	if !ok || len(snippets) != 1 || snippets[0] != "SO15 asked DCI Carey" {
		got := payload.Candidates[0]["snippets"]
		t.Fatalf("candidate snippets = %v, want SO15 snippet; prompt = %s", got, prompt)
	}
	if _, ok := payload.Candidates[0]["term"]; ok {
		t.Fatalf("candidate includes legacy term field; prompt = %s", prompt)
	}

	prompt, err = buildGlossaryUserPrompt(GenerateRequest{
		ExistingEntries: []PromptEntry{{
			DisplayTerm:    "SO15",
			NormalizedTerm: "so15",
			TargetText:     "SO15",
		}},
	})
	if err != nil {
		t.Fatalf("build prompt with existing entries: %v", err)
	}
	if err := json.Unmarshal([]byte(prompt), &payload); err != nil {
		t.Fatalf("decode prompt with existing entries: %v", err)
	}
	if got := payload.Existing[0]["source"]; got != "SO15" {
		t.Fatalf("existing source = %v, want SO15; prompt = %s", got, prompt)
	}
	if got := payload.Existing[0]["normalized"]; got != "so15" {
		t.Fatalf("existing normalized = %v, want so15; prompt = %s", got, prompt)
	}
	if got := payload.Existing[0]["target"]; got != "SO15" {
		t.Fatalf("existing target = %v, want SO15; prompt = %s", got, prompt)
	}
	for _, legacyField := range []string{"source_term", "normalized_term", "target_text"} {
		if _, ok := payload.Existing[0][legacyField]; ok {
			t.Fatalf("existing entry includes legacy %s field; prompt = %s", legacyField, prompt)
		}
	}
}

func TestGlossarySystemPromptDescribesSelectiveGeneration(t *testing.T) {
	for _, want := range []string{
		"brand|product",
		"Return entries only for candidates likely to cause consistency problems",
		"Prefer acronyms, organization names, brands, product names",
		"Prefer terms that should be consistently preserved, transliterated, or translated with one fixed rendering",
		"Use translation_mode \"contextual\" only when the term is worth remembering",
		"Skip common real-world places with standard translations, such as New York",
		"Skip ordinary street names or addresses, such as Madison Avenue",
		"Skip malformed phrases such as Have Carbone. If the meaningful proper noun is also present as a candidate",
		"otherwise skip the malformed phrase",
		"Skip common English words only capitalized because they start a sentence",
		"Skip generic speaker labels and caption descriptions",
		"Skip common abbreviations with stable obvious translations, such as OK or TV",
		"Skip phrases whose target translation should vary by sentence",
		"Skip ordinary person or character names that appear only once",
		"Do not invent or guess a fixed target translation",
		"If no candidate clearly needs glossary guidance, return an empty entries array",
	} {
		if !strings.Contains(glossarySystemPrompt, want) {
			t.Fatalf("system prompt missing %q:\n%s", want, glossarySystemPrompt)
		}
	}
}
