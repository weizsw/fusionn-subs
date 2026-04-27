package glossary

import (
	"context"

	"github.com/fusionn-subs/internal/types"
)

type Store interface {
	LoadPromptEntries(ctx context.Context, mediaKey, targetLanguage string) ([]PromptEntry, error)
	UpsertGeneratedEntries(ctx context.Context, req UpsertRequest) (UpsertResult, error)
	PromoteCommonEntries(ctx context.Context, opts PromotionOptions) (PromotionResult, error)
	RecordJob(ctx context.Context, job JobRecord) error
}

type UpsertRequest struct {
	Job            types.JobMessage
	MediaKey       string
	TargetLanguage string
	Entries        []GeneratedEntry
	Options        UpsertOptions
}

type UpsertOptions struct {
	MinConfidence             float64
	MaxActiveVariantsPerTerm  int
	MaxObservationsPerVariant int
}

type UpsertResult struct {
	Created    int
	Merged     int
	Suppressed int
	Candidates int
}

type PromotionOptions struct {
	TargetLanguage        string
	MinConfidence         float64
	MinDistinctMediaCount int
}

type PromotionResult struct {
	Promoted int
	Skipped  int
}

type JobRecord struct {
	JobID            string
	MediaKey         string
	SubtitlePathHash string
	Status           string
	Error            string
}
