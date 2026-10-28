package glossary

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fusionn-subs/internal/types"
	"github.com/fusionn-subs/pkg/logger"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
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

type trackingLLM struct {
	called bool
}

func (f *trackingLLM) GenerateGlossary(context.Context, GenerateRequest) (GenerateResponse, error) {
	f.called = true
	return GenerateResponse{}, nil
}

type capturingLLM struct {
	req    GenerateRequest
	called bool
}

func (f *capturingLLM) GenerateGlossary(_ context.Context, req GenerateRequest) (GenerateResponse, error) {
	f.req = req
	f.called = true
	return GenerateResponse{}, nil
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

	payload, err := svc.Prepare(context.Background(), types.JobMessage{
		JobID:        "job-1",
		MediaTitle:   "The Capture",
		MediaType:    "series",
		SubtitlePath: subtitlePath,
	})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	want := Payload{
		Terminology:         []Terminology{{Source: "SO15", Target: "SO15"}},
		BuildTerminologyMap: true,
	}
	if !reflect.DeepEqual(payload, want) {
		t.Fatalf("payload = %#v, want %#v", payload, want)
	}
}

func TestServiceSkipsLLMWhenExtractionFindsNoCandidates(t *testing.T) {
	subtitlePath := filepath.Join(t.TempDir(), "episode.srt")
	if err := os.WriteFile(subtitlePath, []byte(`1
00:00:01,000 --> 00:00:03,000
thanks for coming over.
`), 0o600); err != nil {
		t.Fatalf("write subtitle: %v", err)
	}

	llm := &trackingLLM{}
	svc := NewService(ServiceConfig{
		Enabled:                 true,
		TargetLanguage:          "zh-Hans",
		MaxPromptEntries:        10,
		MaxCandidates:           10,
		MaxSnippetsPerCandidate: 1,
		MaxSubtitleBytes:        1 << 20,
		MaxCues:                 100,
	}, fakeStore{}, llm)

	_, err := svc.Prepare(context.Background(), types.JobMessage{
		JobID:        "job-no-candidates",
		MediaTitle:   "The Capture",
		MediaType:    "series",
		SubtitlePath: subtitlePath,
	})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if llm.called {
		t.Fatal("expected LLM call to be skipped")
	}
}

func TestServiceCapsAndRanksExistingEntriesForLLM(t *testing.T) {
	subtitlePath := filepath.Join(t.TempDir(), "episode.srt")
	if err := os.WriteFile(subtitlePath, []byte(`1
00:00:01,000 --> 00:00:03,000
SO15 asked DCI Carey.
`), 0o600); err != nil {
		t.Fatalf("write subtitle: %v", err)
	}

	mediaKey := "title:series:the-capture"
	entries := []PromptEntry{
		{
			Scope:          ScopeCommon,
			NormalizedTerm: "echo",
			DisplayTerm:    "Echo",
			TargetText:     "回声",
			Status:         StatusActive,
			Source:         SourceGenerated,
			Confidence:     0.80,
		},
		{
			Scope:          ScopeMedia,
			MediaKey:       mediaKey,
			NormalizedTerm: "alpha",
			DisplayTerm:    "Alpha",
			TargetText:     "阿尔法",
			Status:         StatusActive,
			Source:         SourceGenerated,
			Confidence:     0.50,
		},
		{
			Scope:          ScopeCommon,
			NormalizedTerm: "charlie",
			DisplayTerm:    "Charlie",
			TargetText:     "查理",
			Status:         StatusActive,
			Source:         SourceCurated,
			Confidence:     0.10,
		},
		{
			Scope:          ScopeMedia,
			MediaKey:       mediaKey,
			NormalizedTerm: "bravo",
			DisplayTerm:    "Bravo",
			TargetText:     "布拉沃",
			Status:         StatusActive,
			Source:         SourceGenerated,
			Confidence:     0.90,
		},
		{
			Scope:          ScopeCommon,
			NormalizedTerm: "delta",
			DisplayTerm:    "Delta",
			TargetText:     "德尔塔",
			Status:         StatusActive,
			Source:         SourceGenerated,
			Confidence:     0.99,
		},
		{
			Scope:          ScopeMedia,
			MediaKey:       "title:series:other",
			NormalizedTerm: "wrong-media",
			DisplayTerm:    "Wrong Media",
			TargetText:     "错误媒体",
			Status:         StatusActive,
			Source:         SourceGenerated,
			Confidence:     1.00,
		},
		{
			Scope:          ScopeCommon,
			NormalizedTerm: "inactive",
			DisplayTerm:    "Inactive",
			TargetText:     "停用",
			Status:         StatusCandidate,
			Source:         SourceGenerated,
			Confidence:     1.00,
		},
	}
	for i := range entries {
		entries[i].LastSeenAt = time.Unix(int64(i), 0)
	}

	llm := &capturingLLM{}
	svc := NewService(ServiceConfig{
		Enabled:                 true,
		TargetLanguage:          "zh-Hans",
		MaxPromptEntries:        2,
		MaxCandidates:           10,
		MaxSnippetsPerCandidate: 1,
		MaxSubtitleBytes:        1 << 20,
		MaxCues:                 100,
	}, fakeStore{entries: entries}, llm)

	_, err := svc.Prepare(context.Background(), types.JobMessage{
		JobID:        "job-existing-context",
		MediaTitle:   "The Capture",
		MediaType:    "series",
		SubtitlePath: subtitlePath,
	})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if !llm.called {
		t.Fatal("expected LLM to be called")
	}

	var got []string
	for _, entry := range llm.req.ExistingEntries {
		got = append(got, entry.NormalizedTerm)
	}
	want := []string{"bravo", "alpha", "charlie", "delta"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("existing entries = %#v, want %#v", got, want)
	}
}

func TestServiceLogsGlossaryLLMCallContext(t *testing.T) {
	subtitlePath := filepath.Join(t.TempDir(), "episode.srt")
	if err := os.WriteFile(subtitlePath, []byte(`1
00:00:01,000 --> 00:00:03,000
SO15 asked DCI Carey.
`), 0o600); err != nil {
		t.Fatalf("write subtitle: %v", err)
	}

	core, logs := observer.New(zapcore.DebugLevel)
	previous := logger.Log
	logger.Log = zap.New(core).Sugar()
	t.Cleanup(func() {
		logger.Log = previous
	})

	svc := NewService(ServiceConfig{
		Enabled:                 true,
		TargetLanguage:          "zh-Hans",
		MaxPromptEntries:        10,
		MaxCandidates:           10,
		MaxSnippetsPerCandidate: 1,
		MaxSubtitleBytes:        1 << 20,
		MaxCues:                 100,
	}, fakeStore{}, fakeLLM{resp: GenerateResponse{Entries: []GeneratedEntry{{
		SourceTerm:     "SO15",
		NormalizedTerm: "so15",
		TargetLanguage: "zh-Hans",
		TargetText:     "SO15",
		Confidence:     0.92,
	}}}})

	_, err := svc.Prepare(context.Background(), types.JobMessage{
		JobID:        "job-logs",
		MediaTitle:   "The Capture",
		MediaType:    "series",
		SubtitlePath: subtitlePath,
	})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}

	started := logEntryForEvent(t, logs, "generation_started")
	startedContext := started.ContextMap()
	if startedContext["job_id"] != "job-logs" {
		t.Fatalf("job_id = %v, want job-logs", startedContext["job_id"])
	}
	if startedContext["media_key"] != "title:series:the-capture" {
		t.Fatalf("media_key = %v, want the media key", startedContext["media_key"])
	}
	if startedContext["target_language"] != "zh-Hans" {
		t.Fatalf("target_language = %v, want zh-Hans", startedContext["target_language"])
	}
	if candidates, ok := startedContext["candidates"].(int64); !ok || candidates == 0 {
		t.Fatalf("candidates = %v, want a positive count", startedContext["candidates"])
	}
	if startedContext["existing_entries"] != int64(0) {
		t.Fatalf("existing_entries = %v, want 0", startedContext["existing_entries"])
	}
	if started.Level != zapcore.InfoLevel {
		t.Fatalf("generation_started level = %v, want INFO", started.Level)
	}

	completed := logEntryForEvent(t, logs, "generation_completed")
	completedContext := completed.ContextMap()
	if completedContext["entries"] != int64(1) {
		t.Fatalf("entries = %v, want 1", completedContext["entries"])
	}
	if _, present := completedContext["duration_ms"]; !present {
		t.Fatal("generation_completed is missing duration_ms")
	}
	if completed.Level != zapcore.InfoLevel {
		t.Fatalf("generation_completed level = %v, want INFO", completed.Level)
	}

	// Subtitle text must never reach the log.
	for _, entry := range logs.All() {
		for _, value := range entry.ContextMap() {
			if strings.Contains(fmt.Sprint(value), "DCI Carey") {
				t.Fatalf("log entry leaked subtitle text: %v", entry.ContextMap())
			}
		}
	}
}

func logEntryForEvent(t *testing.T, logs *observer.ObservedLogs, event string) observer.LoggedEntry {
	t.Helper()
	for _, entry := range logs.All() {
		if entry.ContextMap()["event"] == event {
			return entry
		}
	}
	t.Fatalf("no log entry for event %q", event)
	return observer.LoggedEntry{}
}
