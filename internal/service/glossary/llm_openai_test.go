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
}

func TestGlossarySystemPromptDescribesSelectiveGeneration(t *testing.T) {
	for _, want := range []string{
		"brand|product",
		"Return entries only for candidates likely to cause consistency problems",
		"Skip common real-world places with standard translations, such as New York",
		"Skip ordinary street names or addresses, such as Madison Avenue",
		"Skip malformed phrases such as Have Carbone",
		"Skip generic speaker labels and caption descriptions",
		"Skip common abbreviations with stable obvious translations, such as OK or TV",
	} {
		if !strings.Contains(glossarySystemPrompt, want) {
			t.Fatalf("system prompt missing %q:\n%s", want, glossarySystemPrompt)
		}
	}
}
