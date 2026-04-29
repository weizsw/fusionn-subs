package glossary

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/fusionn-subs/internal/types"
)

type fakeStore struct {
	entries []PromptEntry
	err     error
}

func (s fakeStore) LoadPromptEntries(context.Context, string, string) ([]PromptEntry, error) {
	return s.entries, s.err
}

func (s fakeStore) UpsertGeneratedEntries(context.Context, UpsertRequest) (UpsertResult, error) {
	return UpsertResult{Created: 1}, s.err
}

func (s fakeStore) PromoteCommonEntries(context.Context, PromotionOptions) (PromotionResult, error) {
	return PromotionResult{}, s.err
}

func (s fakeStore) RecordJob(context.Context, JobRecord) error {
	return s.err
}

type fakeLLM struct {
	resp GenerateResponse
	err  error
}

func (f fakeLLM) GenerateGlossary(context.Context, GenerateRequest) (GenerateResponse, error) {
	return f.resp, f.err
}

func TestServiceUsesExistingGlossaryWhenLLMFails(t *testing.T) {
	subtitlePath := filepath.Join(t.TempDir(), "episode.srt")
	if err := os.WriteFile(subtitlePath, []byte(`1
00:00:01,000 --> 00:00:03,000
SO15 asked DCI Carey.
`), 0o600); err != nil {
		t.Fatalf("write subtitle: %v", err)
	}

	svc := NewService(ServiceConfig{
		Enabled:                 true,
		TargetLanguage:          "zh-Hans",
		InjectMinConfidence:     0.80,
		MaxPromptEntries:        10,
		MaxCandidates:           10,
		MaxSnippetsPerCandidate: 1,
		MaxSubtitleBytes:        1 << 20,
		MaxCues:                 100,
	}, fakeStore{entries: []PromptEntry{{
		Scope:           ScopeMedia,
		MediaKey:        "title:series:the-capture",
		NormalizedTerm:  "so15",
		DisplayTerm:     "SO15",
		TargetText:      "SO15",
		TranslationMode: TranslationModePreserve,
		Status:          StatusActive,
		Source:          SourceGenerated,
		Confidence:      0.9,
	}}}, fakeLLM{err: errors.New("llm down")})

	block, err := svc.Prepare(context.Background(), types.JobMessage{
		JobID:        "job-1",
		MediaTitle:   "The Capture",
		MediaType:    "series",
		SubtitlePath: subtitlePath,
	})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if block == "" {
		t.Fatal("expected existing glossary block")
	}
}
