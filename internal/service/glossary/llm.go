package glossary

import (
	"context"
	"fmt"
	"strings"

	"github.com/fusionn-subs/internal/types"
)

type LLMClient interface {
	GenerateGlossary(ctx context.Context, req GenerateRequest) (GenerateResponse, error)
}

type GenerateRequest struct {
	Job             types.JobMessage
	MediaKey        string
	TargetLanguage  string
	ExistingEntries []PromptEntry
	Candidates      []Candidate
}

type GenerateResponse struct {
	Entries []GeneratedEntry `json:"entries"`
}

type GeneratedEntry struct {
	SourceTerm      string          `json:"source_term"`
	NormalizedTerm  string          `json:"normalized_term"`
	TargetLanguage  string          `json:"target_language"`
	TargetText      string          `json:"target_text"`
	Definition      string          `json:"definition"`
	TranslationMode TranslationMode `json:"translation_mode"`
	Category        Category        `json:"category"`
	Confidence      float64         `json:"confidence"`
	Evidence        []string        `json:"evidence"`
}

func (r GenerateResponse) Validate() error {
	for i, entry := range r.Entries {
		if strings.TrimSpace(entry.SourceTerm) == "" {
			return fmt.Errorf("entry %d source_term is required", i)
		}
		if strings.TrimSpace(entry.NormalizedTerm) == "" {
			return fmt.Errorf("entry %d normalized_term is required", i)
		}
		if strings.TrimSpace(entry.TargetText) == "" {
			return fmt.Errorf("entry %d target_text is required", i)
		}
		if entry.Confidence < 0 || entry.Confidence > 1 {
			return fmt.Errorf("entry %d confidence must be between 0 and 1", i)
		}
	}
	return nil
}
