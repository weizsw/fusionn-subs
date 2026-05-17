package glossary

import (
	"context"
	"fmt"
	"time"

	"github.com/fusionn-subs/internal/types"
	"github.com/fusionn-subs/pkg/logger"
)

type ServiceConfig struct {
	Enabled                   bool
	TargetLanguage            string
	MinConfidence             float64
	InjectMinConfidence       float64
	MaxPromptEntries          int
	MaxCandidates             int
	MaxSnippetsPerCandidate   int
	MaxSubtitleBytes          int64
	MaxCues                   int
	MaxActiveVariantsPerTerm  int
	MaxObservationsPerVariant int
	PromoteMinConfidence      float64
	PromoteMinMediaCount      int
	LLMTimeout                time.Duration
}

type Service struct {
	cfg   ServiceConfig
	store Store
	llm   LLMClient
}

func NewService(cfg ServiceConfig, store Store, llm LLMClient) *Service {
	return &Service{cfg: cfg, store: store, llm: llm}
}

func (s *Service) Prepare(ctx context.Context, msg types.JobMessage) (Payload, error) {
	if s == nil || !s.cfg.Enabled {
		return Payload{}, nil
	}
	if s.store == nil {
		return Payload{}, fmt.Errorf("glossary store is required")
	}

	mediaKey := ResolveMediaKey(msg)
	entries, err := s.store.LoadPromptEntries(ctx, mediaKey.Value, s.cfg.TargetLanguage)
	if err != nil {
		return Payload{}, s.recordFailed(ctx, msg, mediaKey.Value, fmt.Errorf("load glossary entries: %w", err))
	}

	payloadFromEntries := func(entries []PromptEntry) Payload {
		return Payload{
			Terminology: BuildTerminology(entries, PromptOptions{
				MediaKey:            mediaKey.Value,
				InjectMinConfidence: s.cfg.InjectMinConfidence,
				MaxPromptEntries:    s.cfg.MaxPromptEntries,
			}),
			BuildTerminologyMap: true,
		}
	}

	currentPayload := func() Payload {
		return payloadFromEntries(entries)
	}

	candidates, extractErr := ExtractCandidates(msg.SubtitlePath, ExtractOptions{
		MaxSubtitleBytes:        s.cfg.MaxSubtitleBytes,
		MaxCues:                 s.cfg.MaxCues,
		MaxCandidates:           s.cfg.MaxCandidates,
		MaxSnippetsPerCandidate: s.cfg.MaxSnippetsPerCandidate,
	})
	if extractErr != nil {
		logger.Warnf("glossary extraction skipped: %v", extractErr)
		if err := s.recordCompleted(ctx, msg, mediaKey.Value); err != nil {
			return Payload{}, err
		}
		return currentPayload(), nil
	}
	if len(candidates) == 0 {
		logger.Infof("Glossary LLM generation skipped: job_id=%s media_key=%s no candidates found", msg.JobID, mediaKey.Value)
		if err := s.recordCompleted(ctx, msg, mediaKey.Value); err != nil {
			return Payload{}, err
		}
		return currentPayload(), nil
	}

	if s.llm == nil {
		logger.Warn("glossary LLM generation skipped: no client configured")
		if err := s.recordCompleted(ctx, msg, mediaKey.Value); err != nil {
			return Payload{}, err
		}
		return currentPayload(), nil
	}

	llmCtx := ctx
	cancel := func() {}
	if s.cfg.LLMTimeout > 0 {
		llmCtx, cancel = context.WithTimeout(ctx, s.cfg.LLMTimeout)
	}
	defer cancel()

	existingEntries := SelectExistingEntries(entries, PromptOptions{
		MediaKey:         mediaKey.Value,
		MaxPromptEntries: s.cfg.MaxPromptEntries,
	})

	logger.Infof("Glossary LLM generation started: job_id=%s media_key=%s target_language=%s candidates=%d existing_entries=%d", msg.JobID, mediaKey.Value, s.cfg.TargetLanguage, len(candidates), len(existingEntries))
	llmStartedAt := time.Now()
	resp, err := s.llm.GenerateGlossary(llmCtx, GenerateRequest{
		Job:             msg,
		MediaKey:        mediaKey.Value,
		TargetLanguage:  s.cfg.TargetLanguage,
		ExistingEntries: existingEntries,
		Candidates:      candidates,
	})
	llmDuration := time.Since(llmStartedAt).Round(time.Millisecond)
	if err != nil {
		logger.Warnf("glossary LLM generation skipped: job_id=%s media_key=%s candidates=%d duration=%s error=%v", msg.JobID, mediaKey.Value, len(candidates), llmDuration, err)
		if err := s.recordCompleted(ctx, msg, mediaKey.Value); err != nil {
			return Payload{}, err
		}
		return currentPayload(), nil
	}
	logger.Infof("Glossary LLM generation completed: job_id=%s media_key=%s entries=%d duration=%s", msg.JobID, mediaKey.Value, len(resp.Entries), llmDuration)

	result, err := s.store.UpsertGeneratedEntries(ctx, UpsertRequest{
		Job:            msg,
		MediaKey:       mediaKey.Value,
		TargetLanguage: s.cfg.TargetLanguage,
		Entries:        resp.Entries,
		Options: UpsertOptions{
			MinConfidence:             s.cfg.MinConfidence,
			MaxActiveVariantsPerTerm:  s.cfg.MaxActiveVariantsPerTerm,
			MaxObservationsPerVariant: s.cfg.MaxObservationsPerVariant,
		},
	})
	if err != nil {
		return Payload{}, s.recordFailed(ctx, msg, mediaKey.Value, fmt.Errorf("store glossary entries: %w", err))
	}
	logger.Infof("Glossary entries: created=%d merged=%d candidates=%d suppressed=%d", result.Created, result.Merged, result.Candidates, result.Suppressed)

	if _, err := s.store.PromoteCommonEntries(ctx, PromotionOptions{
		TargetLanguage:        s.cfg.TargetLanguage,
		MinConfidence:         s.cfg.PromoteMinConfidence,
		MinDistinctMediaCount: s.cfg.PromoteMinMediaCount,
	}); err != nil {
		return Payload{}, s.recordFailed(ctx, msg, mediaKey.Value, fmt.Errorf("promote glossary entries: %w", err))
	}

	entries, err = s.store.LoadPromptEntries(ctx, mediaKey.Value, s.cfg.TargetLanguage)
	if err != nil {
		return Payload{}, s.recordFailed(ctx, msg, mediaKey.Value, fmt.Errorf("reload glossary entries: %w", err))
	}
	if err := s.recordCompleted(ctx, msg, mediaKey.Value); err != nil {
		return Payload{}, err
	}
	return payloadFromEntries(entries), nil
}

func (s *Service) recordCompleted(ctx context.Context, msg types.JobMessage, mediaKey string) error {
	if err := s.store.RecordJob(ctx, JobRecord{
		JobID:            msg.JobID,
		MediaKey:         mediaKey,
		SubtitlePathHash: SubtitlePathHash(msg.SubtitlePath),
		Status:           JobStatusCompleted,
	}); err != nil {
		return fmt.Errorf("record glossary job: %w", err)
	}
	return nil
}

func (s *Service) recordFailed(ctx context.Context, msg types.JobMessage, mediaKey string, cause error) error {
	recordErr := s.store.RecordJob(ctx, JobRecord{
		JobID:            msg.JobID,
		MediaKey:         mediaKey,
		SubtitlePathHash: SubtitlePathHash(msg.SubtitlePath),
		Status:           JobStatusFailed,
		Error:            cause.Error(),
	})
	if recordErr != nil {
		return fmt.Errorf("%w; record glossary job: %w", cause, recordErr)
	}
	return cause
}
