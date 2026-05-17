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

func (r GenerateResponse) ValidateForRequest(req GenerateRequest) error {
	if err := r.Validate(); err != nil {
		return err
	}

	candidates := make(map[string]struct{}, len(req.Candidates))
	for _, candidate := range req.Candidates {
		candidates[candidate.Term+"\x00"+candidate.NormalizedTerm] = struct{}{}
	}

	for i, entry := range r.Entries {
		if strings.TrimSpace(entry.TargetLanguage) == "" {
			return fmt.Errorf("entry %d target_language is required", i)
		}
		if req.TargetLanguage != "" && entry.TargetLanguage != req.TargetLanguage {
			return fmt.Errorf("entry %d target_language %q does not match request target_language %q", i, entry.TargetLanguage, req.TargetLanguage)
		}
		if !validTranslationMode(entry.TranslationMode) {
			return fmt.Errorf("entry %d translation_mode %q is invalid", i, entry.TranslationMode)
		}
		if !validCategory(entry.Category) {
			return fmt.Errorf("entry %d category %q is invalid", i, entry.Category)
		}
		if _, ok := candidates[entry.SourceTerm+"\x00"+entry.NormalizedTerm]; !ok {
			return fmt.Errorf("entry %d source_term and normalized_term must match a request candidate", i)
		}
	}
	return nil
}

func validTranslationMode(mode TranslationMode) bool {
	switch mode {
	case TranslationModeTranslate, TranslationModePreserve, TranslationModeTransliterate, TranslationModeContextual:
		return true
	default:
		return false
	}
}

func validCategory(category Category) bool {
	switch category {
	case CategoryAcronym,
		CategoryOrganization,
		CategoryBrand,
		CategoryProduct,
		CategoryCharacter,
		CategoryPlace,
		CategoryTechnicalTerm,
		CategoryPhrase:
		return true
	default:
		return false
	}
}
