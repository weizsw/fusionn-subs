package glossary

import (
	"bytes"
	"context"
	"errors"
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

	var buf bytes.Buffer
	encoderConfig := zapcore.EncoderConfig{MessageKey: "msg"}
	core := zapcore.NewCore(
		zapcore.NewConsoleEncoder(encoderConfig),
		zapcore.AddSync(&buf),
		zapcore.InfoLevel,
	)
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

	output := buf.String()
	for _, want := range []string{
		"Glossary LLM generation started:",
		"job_id=job-logs",
		"media_key=title:series:the-capture",
		"target_language=zh-Hans",
		"candidates=",
		"existing_entries=0",
		"Glossary LLM generation completed:",
		"entries=1",
		"duration=",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("log output missing %q:\n%s", want, output)
		}
	}
}
