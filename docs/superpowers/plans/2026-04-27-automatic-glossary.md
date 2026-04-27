# Automatic Glossary Generation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an opt-in automatic glossary preparation step that learns per-media/common terminology, stores it in SQLite, and injects a compact glossary block before subtitle translation.

**Architecture:** The worker orchestrates glossary preparation before calling the translator. Glossary business logic lives in `internal/service/glossary`; SQLite infrastructure lives in `internal/storage/sqlite`; translators only accept per-job extra instruction text and append it to their existing provider instruction.

**Tech Stack:** Go 1.23, Viper, Redis, `llm-subtrans`, `github.com/asticode/go-astisub`, `modernc.org/sqlite`, `github.com/pressly/goose/v3`, `github.com/openai/openai-go/v3`, `google.golang.org/genai`

**Spec:** `docs/superpowers/specs/2026-04-27-automatic-glossary-design.md`

---

## File Structure

| File | Action | Responsibility |
|------|--------|----------------|
| `go.mod`, `go.sum` | Modify | Add pinned dependencies compatible with Go 1.23 |
| `internal/types/job.go` | Modify | Optional media identity fields for glossary keying |
| `internal/types/job_test.go` | Create | Job JSON compatibility tests |
| `internal/config/config.go` | Modify | Glossary config structs, defaults, validation, safe logging |
| `internal/config/config_test.go` | Create | Glossary config validation tests |
| `internal/service/translator/factory.go` | Modify | `translator.Request`, interface update, fallback forwarding |
| `internal/service/translator/gemini.go` | Modify | Use `Request.Job`, append `ExtraInstruction` |
| `internal/service/translator/openrouter.go` | Modify | Use `Request.Job`, append `ExtraInstruction` |
| `internal/service/translator/local_llm.go` | Modify | Use `Request.Job`, append `ExtraInstruction` |
| `internal/service/translator/translator.go` | Modify | Shared instruction merge helper |
| `internal/service/translator/translator_test.go` | Create/modify | Instruction merge tests |
| `internal/service/glossary/types.go` | Create | Domain types and constants |
| `internal/service/glossary/store.go` | Create | Store interface |
| `internal/service/glossary/llm.go` | Create | LLM client interface and response validation |
| `internal/service/glossary/media_key.go` | Create | Media key resolver |
| `internal/service/glossary/media_key_test.go` | Create | Resolver priority tests |
| `internal/service/glossary/prompt_builder.go` | Create | Prompt entry ranking and formatting |
| `internal/service/glossary/prompt_builder_test.go` | Create | Prompt selection tests |
| `internal/service/glossary/extractor.go` | Create | Subtitle parsing/candidate extraction via go-astisub |
| `internal/service/glossary/extractor_test.go` | Create | Extraction and limit tests |
| `internal/storage/sqlite/db.go` | Create | SQLite opener, pragmas, migration runner |
| `internal/storage/sqlite/migrations/00001_glossary.sql` | Create | Glossary schema |
| `internal/storage/sqlite/glossary.go` | Create | SQLite store implementation |
| `internal/storage/sqlite/glossary_test.go` | Create | Migration and repository tests |
| `internal/service/glossary/promoter.go` | Create | Conservative common glossary promotion |
| `internal/service/glossary/promoter_test.go` | Create | Promotion tests |
| `internal/service/glossary/service.go` | Create | Glossary preparation orchestration |
| `internal/service/glossary/service_test.go` | Create | Service failure policy tests |
| `internal/service/glossary/llm_openai.go` | Create | OpenAI-compatible glossary LLM client |
| `internal/service/glossary/llm_openai_test.go` | Create | HTTP client and JSON parsing tests |
| `internal/service/glossary/llm_gemini.go` | Create | Gemini glossary LLM client |
| `internal/service/worker/worker.go` | Modify | Optional glossary service before translation |
| `internal/service/worker/worker_test.go` | Create | Glossary continue/fail policy tests |
| `cmd/fusionn-subs/main.go` | Modify | Open SQLite and wire glossary service |
| `config/config.example.yaml` | Modify | Document glossary config |
| `README.md` | Modify | Document glossary behavior and job identity fields |

---

### Task 1: Add Job Identity And Glossary Config Contracts

**Files:**
- Modify: `internal/types/job.go`
- Create: `internal/types/job_test.go`
- Modify: `internal/config/config.go`
- Create: `internal/config/config_test.go`

- [ ] **Step 1: Write job identity JSON compatibility tests**

Create `internal/types/job_test.go`:

```go
package types

import (
	"encoding/json"
	"testing"
)

func TestJobMessageUnmarshalOptionalGlossaryIdentity(t *testing.T) {
	raw := []byte(`{
		"job_id":"job-1",
		"video_path":"/media/The Capture/S01E01.mkv",
		"subtitle_path":"/media/The Capture/S01E01.eng.srt",
		"media_title":"The Capture",
		"media_type":"series",
		"media_id":"sonarr:42",
		"source_system":"sonarr",
		"external_ids":{"tvdb":"355620","imdb":"tt8201186"},
		"season":1,
		"episode":1
	}`)

	var msg JobMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if msg.ExternalIDs["tvdb"] != "355620" {
		t.Fatalf("tvdb id = %q", msg.ExternalIDs["tvdb"])
	}
	if msg.Season != 1 || msg.Episode != 1 {
		t.Fatalf("season/episode = %d/%d", msg.Season, msg.Episode)
	}
	if err := msg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestJobMessageUnmarshalExistingPayloadStillWorks(t *testing.T) {
	raw := []byte(`{
		"job_id":"job-legacy",
		"video_path":"/media/movie.mkv",
		"subtitle_path":"/media/movie.eng.srt",
		"media_title":"Movie",
		"media_type":"movie"
	}`)

	var msg JobMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := msg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if msg.ExternalIDs != nil {
		t.Fatalf("external ids should be nil for legacy payload")
	}
}
```

- [ ] **Step 2: Run the job identity tests and verify they fail**

Run: `go test ./internal/types -run 'TestJobMessageUnmarshal' -v`

Expected: FAIL because `MediaID`, `SourceSystem`, `ExternalIDs`, `Season`, and `Episode` do not exist on `JobMessage`.

- [ ] **Step 3: Add optional identity fields**

Update `internal/types/job.go`:

```go
type JobMessage struct {
	JobID        string            `json:"job_id"`
	VideoPath    string            `json:"video_path"`
	SubtitlePath string            `json:"subtitle_path"`
	MediaTitle   string            `json:"media_title"`
	MediaType    string            `json:"media_type"`
	MediaID      string            `json:"media_id,omitempty"`
	SourceSystem string            `json:"source_system,omitempty"`
	ExternalIDs  map[string]string `json:"external_ids,omitempty"`
	Season       int               `json:"season,omitempty"`
	Episode      int               `json:"episode,omitempty"`
}
```

- [ ] **Step 4: Run the job identity tests and verify they pass**

Run: `go test ./internal/types -run 'TestJobMessageUnmarshal' -v`

Expected: PASS.

- [ ] **Step 5: Write glossary config validation tests**

Create `internal/config/config_test.go`:

```go
package config

import (
	"strings"
	"testing"
	"time"
)

func validConfigForTest() *Config {
	return &Config{
		Redis: RedisConfig{
			URL:   "redis://localhost:6379",
			Queue: "translate_queue",
		},
		Callback: CallbackConfig{URL: "http://localhost/callback"},
		Gemini: GeminiConfig{
			APIKey: "gemini-key",
			PrimaryModel: GeminiModelConfig{
				Name: "gemini-2.5-flash",
			},
			SecondaryModel: GeminiModelConfig{
				Name: "gemini-2.5-pro",
			},
		},
		Translator: TranslatorConfig{
			TargetLanguage: "Chinese",
			OutputSuffix:   "chs",
		},
	}
}

func TestGlossaryDisabledDoesNotRequireLLMConfig(t *testing.T) {
	cfg := validConfigForTest()
	cfg.Glossary.Enabled = false

	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestGlossaryEnabledAppliesDefaults(t *testing.T) {
	cfg := validConfigForTest()
	cfg.Glossary.Enabled = true
	cfg.Glossary.DBPath = "/tmp/glossary.db"
	cfg.Glossary.LLM.Provider = "openai_compatible"
	cfg.Glossary.LLM.BaseURL = "http://127.0.0.1:8045"
	cfg.Glossary.LLM.Model = "qwen3:8b"

	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}

	if cfg.Glossary.TargetLanguage != "Chinese" {
		t.Fatalf("target language = %q", cfg.Glossary.TargetLanguage)
	}
	if cfg.Glossary.InjectMinConfidence != 0.80 {
		t.Fatalf("inject min confidence = %v", cfg.Glossary.InjectMinConfidence)
	}
	if cfg.Glossary.LLM.Timeout != time.Minute {
		t.Fatalf("timeout = %v", cfg.Glossary.LLM.Timeout)
	}
}

func TestGlossaryEnabledRequiresDBPath(t *testing.T) {
	cfg := validConfigForTest()
	cfg.Glossary.Enabled = true
	cfg.Glossary.LLM.Provider = "openai_compatible"
	cfg.Glossary.LLM.BaseURL = "http://127.0.0.1:8045"
	cfg.Glossary.LLM.Model = "qwen3:8b"

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "glossary.db_path is required") {
		t.Fatalf("error = %v", err)
	}
}

func TestGlossaryEnabledRequiresSupportedProvider(t *testing.T) {
	cfg := validConfigForTest()
	cfg.Glossary.Enabled = true
	cfg.Glossary.DBPath = "/tmp/glossary.db"
	cfg.Glossary.LLM.Provider = "unknown"
	cfg.Glossary.LLM.Model = "qwen3:8b"

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "unsupported glossary.llm.provider") {
		t.Fatalf("error = %v", err)
	}
}
```

- [ ] **Step 6: Run glossary config tests and verify they fail**

Run: `go test ./internal/config -run 'TestGlossary' -v`

Expected: FAIL because `Config.Glossary` and related structs do not exist.

- [ ] **Step 7: Add glossary config structs and defaults**

Modify `internal/config/config.go`:

```go
type Config struct {
	Redis      RedisConfig      `mapstructure:"redis"`
	Callback   CallbackConfig   `mapstructure:"callback"`
	Gemini     GeminiConfig     `mapstructure:"gemini"`
	OpenRouter OpenRouterConfig `mapstructure:"openrouter"`
	LocalLLM   LocalLLMConfig   `mapstructure:"local_llm"`
	Translator TranslatorConfig `mapstructure:"translator"`
	Glossary   GlossaryConfig   `mapstructure:"glossary"`
}

type GlossaryConfig struct {
	Enabled                  bool              `mapstructure:"enabled"`
	DBPath                   string            `mapstructure:"db_path"`
	TargetLanguage           string            `mapstructure:"target_language"`
	MinConfidence            float64           `mapstructure:"min_confidence"`
	InjectMinConfidence      float64           `mapstructure:"inject_min_confidence"`
	MaxPromptEntries         int               `mapstructure:"max_prompt_entries"`
	MaxCandidates            int               `mapstructure:"max_candidates"`
	MaxSnippetsPerCandidate  int               `mapstructure:"max_snippets_per_candidate"`
	MaxSubtitleBytes         int64             `mapstructure:"max_subtitle_bytes"`
	MaxCues                  int               `mapstructure:"max_cues"`
	MaxActiveVariantsPerTerm int               `mapstructure:"max_active_variants_per_term"`
	MaxObservationsPerVariant int              `mapstructure:"max_observations_per_variant"`
	PromoteMinConfidence    float64           `mapstructure:"promote_min_confidence"`
	PromoteMinMediaCount    int               `mapstructure:"promote_min_media_count"`
	LLM                      GlossaryLLMConfig `mapstructure:"llm"`
}

type GlossaryLLMConfig struct {
	Provider    string        `mapstructure:"provider"`
	BaseURL     string        `mapstructure:"base_url"`
	Endpoint    string        `mapstructure:"endpoint"`
	APIKey      string        `mapstructure:"api_key"`
	Model       string        `mapstructure:"model"`
	Timeout     time.Duration `mapstructure:"timeout"`
	Temperature float64       `mapstructure:"temperature"`
}
```

Add helpers:

```go
func (c *Config) applyGlossaryDefaults() {
	g := &c.Glossary
	if !g.Enabled {
		return
	}
	if g.TargetLanguage == "" {
		g.TargetLanguage = c.Translator.TargetLanguage
	}
	if g.MinConfidence == 0 {
		g.MinConfidence = 0.75
	}
	if g.InjectMinConfidence == 0 {
		g.InjectMinConfidence = 0.80
	}
	if g.MaxPromptEntries == 0 {
		g.MaxPromptEntries = 30
	}
	if g.MaxCandidates == 0 {
		g.MaxCandidates = 80
	}
	if g.MaxSnippetsPerCandidate == 0 {
		g.MaxSnippetsPerCandidate = 3
	}
	if g.MaxSubtitleBytes == 0 {
		g.MaxSubtitleBytes = 1 << 20
	}
	if g.MaxCues == 0 {
		g.MaxCues = 3000
	}
	if g.MaxActiveVariantsPerTerm == 0 {
		g.MaxActiveVariantsPerTerm = 3
	}
	if g.MaxObservationsPerVariant == 0 {
		g.MaxObservationsPerVariant = 10
	}
	if g.PromoteMinConfidence == 0 {
		g.PromoteMinConfidence = 0.85
	}
	if g.PromoteMinMediaCount == 0 {
		g.PromoteMinMediaCount = 3
	}
	if g.LLM.Endpoint == "" {
		g.LLM.Endpoint = "/v1/chat/completions"
	}
	if g.LLM.Timeout == 0 {
		g.LLM.Timeout = time.Minute
	}
	if g.LLM.Temperature == 0 {
		g.LLM.Temperature = 0.1
	}
}
```

Call `c.applyGlossaryDefaults()` near the start of `Validate()` after required top-level checks.

- [ ] **Step 8: Add glossary validation**

In `Validate()`, after translator/provider validation:

```go
if c.Glossary.Enabled {
	if strings.TrimSpace(c.Glossary.DBPath) == "" {
		return fmt.Errorf("glossary.db_path is required when glossary is enabled")
	}
	if c.Glossary.TargetLanguage == "" {
		return fmt.Errorf("glossary.target_language is required when glossary is enabled")
	}
	switch c.Glossary.LLM.Provider {
	case "openai_compatible":
		if c.Glossary.LLM.BaseURL == "" {
			return fmt.Errorf("glossary.llm.base_url is required for openai_compatible provider")
		}
	case "gemini":
		if c.Glossary.LLM.APIKey == "" && c.Gemini.APIKey == "" {
			return fmt.Errorf("glossary.llm.api_key or gemini.api_key is required for gemini provider")
		}
	default:
		return fmt.Errorf("unsupported glossary.llm.provider: %q", c.Glossary.LLM.Provider)
	}
	if c.Glossary.LLM.Model == "" {
		return fmt.Errorf("glossary.llm.model is required when glossary is enabled")
	}
}
```

- [ ] **Step 9: Add glossary values to SafeLogValues**

Add entries:

```go
"glossary.enabled":                    c.Glossary.Enabled,
"glossary.db_path":                    c.Glossary.DBPath,
"glossary.target_language":            c.Glossary.TargetLanguage,
"glossary.min_confidence":             c.Glossary.MinConfidence,
"glossary.inject_min_confidence":      c.Glossary.InjectMinConfidence,
"glossary.max_prompt_entries":         c.Glossary.MaxPromptEntries,
"glossary.max_candidates":             c.Glossary.MaxCandidates,
"glossary.llm.provider":               c.Glossary.LLM.Provider,
"glossary.llm.base_url":               c.Glossary.LLM.BaseURL,
"glossary.llm.endpoint":               c.Glossary.LLM.Endpoint,
"glossary.llm.api_key":                util.MaskSecret(c.Glossary.LLM.APIKey),
"glossary.llm.model":                  c.Glossary.LLM.Model,
"glossary.llm.timeout":                c.Glossary.LLM.Timeout.String(),
```

- [ ] **Step 10: Run config tests**

Run: `go test ./internal/config ./internal/types -v`

Expected: PASS.

- [ ] **Step 11: Commit**

```bash
git add internal/types/job.go internal/types/job_test.go internal/config/config.go internal/config/config_test.go
git commit -m "feat(glossary): add config and media identity fields"
```

---

### Task 2: Extend Translator Requests With Per-Job Instructions

**Files:**
- Modify: `internal/service/translator/factory.go`
- Modify: `internal/service/translator/gemini.go`
- Modify: `internal/service/translator/openrouter.go`
- Modify: `internal/service/translator/local_llm.go`
- Modify: `internal/service/translator/translator.go`
- Create/modify: `internal/service/translator/translator_test.go`
- Modify: `internal/service/worker/worker.go`

- [ ] **Step 1: Write instruction merge tests**

Create `internal/service/translator/translator_test.go`:

```go
package translator

import "testing"

func TestCombineInstructions(t *testing.T) {
	tests := []struct {
		name  string
		base  string
		extra string
		want  string
	}{
		{name: "empty", want: ""},
		{name: "base only", base: "Translate naturally.", want: "Translate naturally."},
		{name: "extra only", extra: "Glossary guidance:\n- SO15: keep as SO15", want: "Glossary guidance:\n- SO15: keep as SO15"},
		{name: "both", base: "Translate naturally.", extra: "Glossary guidance:\n- SO15: keep as SO15", want: "Translate naturally.\n\nGlossary guidance:\n- SO15: keep as SO15"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := combineInstructions(tt.base, tt.extra)
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run the instruction test and verify it fails**

Run: `go test ./internal/service/translator -run TestCombineInstructions -v`

Expected: FAIL because `combineInstructions` does not exist.

- [ ] **Step 3: Add request type and merge helper**

Modify `internal/service/translator/factory.go`:

```go
type Request struct {
	Job              types.JobMessage
	ExtraInstruction string
}

type Translator interface {
	Translate(ctx context.Context, req Request) (string, error)
}
```

Modify `internal/service/translator/translator.go`:

```go
func combineInstructions(base, extra string) string {
	base = strings.TrimSpace(base)
	extra = strings.TrimSpace(extra)
	switch {
	case base == "":
		return extra
	case extra == "":
		return base
	default:
		return base + "\n\n" + extra
	}
}
```

- [ ] **Step 4: Update fallback translator forwarding**

In `internal/service/translator/factory.go`, change:

```go
func (f *FallbackTranslator) Translate(ctx context.Context, req Request) (string, error) {
	var lastErr error
	for _, nt := range f.translators {
		out, err := nt.translator.Translate(ctx, req)
		if err == nil {
			return out, nil
		}
		if errors.Is(err, ErrRateLimited) {
			return "", err
		}
		if errors.Is(err, ErrAllModelsExhausted) {
			logger.Warnf("translator provider %s: all models exhausted, trying next provider", nt.name)
			lastErr = err
			continue
		}
		logger.Warnf("translator provider %s failed: %v, trying next provider", nt.name, err)
		lastErr = err
	}
	if lastErr != nil {
		return "", fmt.Errorf("all providers failed, last error: %w", lastErr)
	}
	return "", fmt.Errorf("all providers failed")
}
```

- [ ] **Step 5: Update provider Translate methods**

For `gemini.go`, `openrouter.go`, and `local_llm.go`:

1. Change signature to `Translate(ctx context.Context, req Request) (string, error)`.
2. Add `msg := req.Job` at the start.
3. Replace each instruction check with:

```go
if instruction := combineInstructions(t.instruction, req.ExtraInstruction); instruction != "" {
	args = append(args, "--instruction", instruction)
}
```

For `LocalLLMTranslator`, use the local snapshot:

```go
if instruction := combineInstructions(instruction, req.ExtraInstruction); instruction != "" {
	args = append(args, "--instruction", instruction)
}
```

- [ ] **Step 6: Update worker translation call**

In `internal/service/worker/worker.go`, replace:

```go
chsPath, err = w.translator.Translate(ctx, msg)
```

with:

```go
chsPath, err = w.translator.Translate(ctx, translator.Request{Job: msg})
```

- [ ] **Step 7: Run translator and worker tests/build**

Run: `go test ./internal/service/translator ./internal/service/worker -v`

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/service/translator internal/service/worker/worker.go
git commit -m "feat(translator): support per-job extra instructions"
```

---

### Task 3: Add Glossary Domain Types, Media Keys, And Prompt Builder

**Files:**
- Create: `internal/service/glossary/types.go`
- Create: `internal/service/glossary/store.go`
- Create: `internal/service/glossary/llm.go`
- Create: `internal/service/glossary/media_key.go`
- Create: `internal/service/glossary/media_key_test.go`
- Create: `internal/service/glossary/prompt_builder.go`
- Create: `internal/service/glossary/prompt_builder_test.go`

- [ ] **Step 1: Write media key resolver tests**

Create `internal/service/glossary/media_key_test.go`:

```go
package glossary

import (
	"testing"

	"github.com/fusionn-subs/internal/types"
)

func TestResolveMediaKeyPrefersStableExternalID(t *testing.T) {
	msg := types.JobMessage{
		MediaTitle:  "The Capture",
		MediaType:   "series",
		ExternalIDs: map[string]string{"imdb": "tt8201186", "tvdb": "355620"},
	}

	key := ResolveMediaKey(msg)
	if key.Value != "tvdb:355620" || key.Source != MediaKeySourceExternalID {
		t.Fatalf("key = %#v", key)
	}
}

func TestResolveMediaKeyUsesSourceScopedMediaID(t *testing.T) {
	msg := types.JobMessage{
		MediaTitle:   "The Capture",
		MediaType:    "series",
		MediaID:      "42",
		SourceSystem: "sonarr",
	}

	key := ResolveMediaKey(msg)
	if key.Value != "sonarr:42" || key.Source != MediaKeySourceMediaID {
		t.Fatalf("key = %#v", key)
	}
}

func TestResolveMediaKeyFallsBackToTitle(t *testing.T) {
	msg := types.JobMessage{MediaTitle: "The Capture", MediaType: "Series"}

	key := ResolveMediaKey(msg)
	if key.Value != "title:series:the-capture" || key.Source != MediaKeySourceTitle {
		t.Fatalf("key = %#v", key)
	}
}

func TestResolveMediaKeyFallsBackToPathHash(t *testing.T) {
	msg := types.JobMessage{SubtitlePath: "/media/The Capture/S01E01.eng.srt"}

	key := ResolveMediaKey(msg)
	if key.Source != MediaKeySourcePath || key.Value == "" {
		t.Fatalf("key = %#v", key)
	}
}
```

- [ ] **Step 2: Write prompt builder tests**

Create `internal/service/glossary/prompt_builder_test.go`:

```go
package glossary

import (
	"strings"
	"testing"
	"time"
)

func TestBuildPromptPrefersMediaOverCommon(t *testing.T) {
	now := time.Now()
	entries := []PromptEntry{
		{Scope: ScopeCommon, NormalizedTerm: "so15", DisplayTerm: "SO15", TargetText: "SO15 common", Definition: "Common meaning", Confidence: 0.95, EvidenceCount: 4, Status: StatusActive, Source: SourcePromoted, LastSeenAt: now},
		{Scope: ScopeMedia, MediaKey: "tvdb:355620", NormalizedTerm: "so15", DisplayTerm: "SO15", TargetText: "SO15", Definition: "The Capture usage", TranslationMode: TranslationModePreserve, Confidence: 0.90, EvidenceCount: 2, Status: StatusActive, Source: SourceGenerated, LastSeenAt: now},
	}

	got := BuildPrompt(entries, PromptOptions{
		MediaKey:             "tvdb:355620",
		InjectMinConfidence:  0.80,
		MaxPromptEntries:     10,
	})

	if !strings.Contains(got, `SO15: keep as "SO15"`) {
		t.Fatalf("prompt = %s", got)
	}
	if strings.Contains(got, "Common meaning") {
		t.Fatalf("common entry should not win: %s", got)
	}
}

func TestBuildPromptFiltersLowConfidence(t *testing.T) {
	got := BuildPrompt([]PromptEntry{
		{Scope: ScopeMedia, MediaKey: "m", NormalizedTerm: "x", DisplayTerm: "X", TargetText: "X", Confidence: 0.40, Status: StatusActive},
	}, PromptOptions{MediaKey: "m", InjectMinConfidence: 0.80, MaxPromptEntries: 10})

	if got != "" {
		t.Fatalf("prompt = %q", got)
	}
}
```

- [ ] **Step 3: Run glossary tests and verify they fail**

Run: `go test ./internal/service/glossary -run 'TestResolveMediaKey|TestBuildPrompt' -v`

Expected: FAIL because the glossary package does not exist.

- [ ] **Step 4: Add domain types and interfaces**

Create `internal/service/glossary/types.go` with:

```go
package glossary

import "time"

type Scope string
type VariantStatus string
type VariantSource string
type TranslationMode string
type Category string
type MediaKeySource string

const (
	ScopeMedia  Scope = "media"
	ScopeCommon Scope = "common"

	StatusActive     VariantStatus = "active"
	StatusSuppressed VariantStatus = "suppressed"
	StatusCandidate  VariantStatus = "candidate"

	SourceGenerated VariantSource = "generated"
	SourcePromoted  VariantSource = "promoted"
	SourceCurated   VariantSource = "curated"

	TranslationModeTranslate    TranslationMode = "translate"
	TranslationModePreserve     TranslationMode = "preserve"
	TranslationModeTransliterate TranslationMode = "transliterate"
	TranslationModeContextual   TranslationMode = "contextual"

	CategoryAcronym       Category = "acronym"
	CategoryOrganization  Category = "organization"
	CategoryCharacter     Category = "character"
	CategoryPlace         Category = "place"
	CategoryTechnicalTerm Category = "technical_term"
	CategoryPhrase        Category = "phrase"

	MediaKeySourceExternalID MediaKeySource = "external_id"
	MediaKeySourceMediaID    MediaKeySource = "media_id"
	MediaKeySourceTitle      MediaKeySource = "title"
	MediaKeySourcePath       MediaKeySource = "path"
)

type MediaKey struct {
	Value  string
	Source MediaKeySource
}

type PromptEntry struct {
	Scope           Scope
	MediaKey        string
	NormalizedTerm  string
	DisplayTerm     string
	TargetLanguage  string
	TargetText      string
	Definition      string
	TranslationMode TranslationMode
	Category        Category
	Status          VariantStatus
	Source          VariantSource
	Confidence      float64
	EvidenceCount   int
	LastSeenAt      time.Time
}

type PromptOptions struct {
	MediaKey            string
	InjectMinConfidence float64
	MaxPromptEntries    int
}
```

Create `store.go`:

```go
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
	MinConfidence            float64
	MaxActiveVariantsPerTerm int
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
```

Create `llm.go`:

```go
package glossary

import (
	"context"
	"errors"
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
			return errors.New("entry source_term is required")
		}
		if strings.TrimSpace(entry.NormalizedTerm) == "" {
			return errors.New("entry normalized_term is required")
		}
		if strings.TrimSpace(entry.TargetText) == "" {
			return errors.New("entry target_text is required")
		}
		if entry.Confidence < 0 || entry.Confidence > 1 {
			return errors.New("entry confidence must be between 0 and 1")
		}
		_ = i
	}
	return nil
}
```

- [ ] **Step 5: Add media key resolver**

Create `internal/service/glossary/media_key.go`:

```go
package glossary

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"sort"
	"strings"

	"github.com/fusionn-subs/internal/types"
)

var nonKeyChars = regexp.MustCompile(`[^a-z0-9]+`)

func ResolveMediaKey(msg types.JobMessage) MediaKey {
	for _, key := range []string{"tvdb", "tmdb", "imdb"} {
		if value := strings.TrimSpace(msg.ExternalIDs[key]); value != "" {
			return MediaKey{Value: key + ":" + strings.ToLower(value), Source: MediaKeySourceExternalID}
		}
	}

	keys := make([]string, 0, len(msg.ExternalIDs))
	for key := range msg.ExternalIDs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if key == "sonarr" || key == "radarr" {
			if value := strings.TrimSpace(msg.ExternalIDs[key]); value != "" {
				return MediaKey{Value: key + ":" + strings.ToLower(value), Source: MediaKeySourceExternalID}
			}
		}
	}

	if mediaID := strings.TrimSpace(msg.MediaID); mediaID != "" {
		source := strings.ToLower(strings.TrimSpace(msg.SourceSystem))
		if source == "" {
			source = "media"
		}
		return MediaKey{Value: source + ":" + strings.ToLower(mediaID), Source: MediaKeySourceMediaID}
	}

	if title := normalizeKeyPart(msg.MediaTitle); title != "" {
		mediaType := normalizeKeyPart(msg.MediaType)
		if mediaType == "" {
			mediaType = "unknown"
		}
		return MediaKey{Value: "title:" + mediaType + ":" + title, Source: MediaKeySourceTitle}
	}

	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(msg.SubtitlePath))))
	return MediaKey{Value: "path:" + hex.EncodeToString(sum[:8]), Source: MediaKeySourcePath}
}

func normalizeKeyPart(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = nonKeyChars.ReplaceAllString(value, "-")
	return strings.Trim(value, "-")
}
```

- [ ] **Step 6: Add prompt builder**

Create `internal/service/glossary/prompt_builder.go`:

```go
package glossary

import (
	"fmt"
	"sort"
	"strings"
)

func BuildPrompt(entries []PromptEntry, opts PromptOptions) string {
	if opts.MaxPromptEntries <= 0 {
		return ""
	}

	winners := make(map[string]PromptEntry)
	for _, entry := range entries {
		if entry.Status != StatusActive {
			continue
		}
		if entry.Confidence < opts.InjectMinConfidence && entry.Source != SourceCurated {
			continue
		}
		if entry.Scope == ScopeMedia && entry.MediaKey != opts.MediaKey {
			continue
		}
		current, ok := winners[entry.NormalizedTerm]
		if !ok || promptRank(entry, opts.MediaKey) > promptRank(current, opts.MediaKey) {
			winners[entry.NormalizedTerm] = entry
		}
	}

	selected := make([]PromptEntry, 0, len(winners))
	for _, entry := range winners {
		selected = append(selected, entry)
	}
	sort.SliceStable(selected, func(i, j int) bool {
		return promptRank(selected[i], opts.MediaKey) > promptRank(selected[j], opts.MediaKey)
	})
	if len(selected) > opts.MaxPromptEntries {
		selected = selected[:opts.MaxPromptEntries]
	}
	if len(selected) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("Glossary guidance for this subtitle:")
	for _, entry := range selected {
		b.WriteString("\n- ")
		b.WriteString(entry.DisplayTerm)
		b.WriteString(": ")
		switch entry.TranslationMode {
		case TranslationModePreserve:
			b.WriteString(fmt.Sprintf("keep as %q", entry.TargetText))
		default:
			b.WriteString(fmt.Sprintf("use %q", entry.TargetText))
		}
		if strings.TrimSpace(entry.Definition) != "" {
			b.WriteString("; definition: ")
			b.WriteString(strings.TrimSpace(entry.Definition))
		}
	}
	return b.String()
}

func promptRank(entry PromptEntry, mediaKey string) int {
	score := 0
	if entry.Scope == ScopeMedia && entry.MediaKey == mediaKey {
		score += 1_000_000
	}
	if entry.Source == SourceCurated {
		score += 100_000
	}
	score += int(entry.Confidence * 10_000)
	score += entry.EvidenceCount * 100
	score += int(entry.LastSeenAt.Unix() % 100)
	return score
}
```

- [ ] **Step 7: Run glossary domain tests**

Run: `go test ./internal/service/glossary -run 'TestResolveMediaKey|TestBuildPrompt' -v`

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/service/glossary
git commit -m "feat(glossary): add domain contracts and prompt selection"
```

---

### Task 4: Add Subtitle Candidate Extraction With go-astisub

**Files:**
- Modify: `go.mod`, `go.sum`
- Create: `internal/service/glossary/extractor.go`
- Create: `internal/service/glossary/extractor_test.go`

- [ ] **Step 1: Add subtitle parser dependency**

Run:

```bash
go get github.com/asticode/go-astisub@v0.40.0
go mod tidy
```

Expected: `go.mod` includes `github.com/asticode/go-astisub v0.40.0`.

- [ ] **Step 2: Write extractor tests**

Create `internal/service/glossary/extractor_test.go`:

```go
package glossary

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExtractCandidatesFromSRT(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "episode.srt")
	content := `1
00:00:01,000 --> 00:00:03,000
SO15 asked DCI Carey to review the feed.

2
00:00:04,000 --> 00:00:06,000
SO15 said Counter Terrorism Command was involved.

3
00:00:07,000 --> 00:00:09,000
Carey called SO15 again.
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	candidates, err := ExtractCandidates(path, ExtractOptions{
		MaxSubtitleBytes:        1 << 20,
		MaxCues:                 100,
		MaxCandidates:           20,
		MaxSnippetsPerCandidate: 2,
	})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}

	found := map[string]Candidate{}
	for _, c := range candidates {
		found[c.NormalizedTerm] = c
	}
	if found["so15"].Frequency != 3 {
		t.Fatalf("SO15 frequency = %d", found["so15"].Frequency)
	}
	if len(found["so15"].Snippets) != 2 {
		t.Fatalf("SO15 snippets = %#v", found["so15"].Snippets)
	}
	if _, ok := found["dci"]; !ok {
		t.Fatalf("expected DCI candidate, got %#v", found)
	}
}

func TestExtractCandidatesHonorsSubtitleSizeLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "episode.srt")
	if err := os.WriteFile(path, []byte("1234567890"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	_, err := ExtractCandidates(path, ExtractOptions{MaxSubtitleBytes: 5})
	if err == nil {
		t.Fatal("expected size limit error")
	}
}
```

- [ ] **Step 3: Run extractor tests and verify they fail**

Run: `go test ./internal/service/glossary -run TestExtractCandidates -v`

Expected: FAIL because `ExtractCandidates`, `ExtractOptions`, and `Candidate` do not exist.

- [ ] **Step 4: Add extractor types**

Append to `internal/service/glossary/types.go`:

```go
type Candidate struct {
	Term           string
	NormalizedTerm string
	Frequency      int
	Snippets       []string
}

type ExtractOptions struct {
	MaxSubtitleBytes        int64
	MaxCues                 int
	MaxCandidates           int
	MaxSnippetsPerCandidate int
}
```

- [ ] **Step 5: Implement extractor**

Create `internal/service/glossary/extractor.go`:

```go
package glossary

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	astisub "github.com/asticode/go-astisub"
)

var candidatePattern = regexp.MustCompile(`\b(?:[A-Z]{2,}[0-9]*|[A-Z][a-z]+(?:\s+[A-Z][a-z]+){1,3})\b`)

func ExtractCandidates(path string, opts ExtractOptions) ([]Candidate, error) {
	if opts.MaxSubtitleBytes > 0 {
		stat, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("stat subtitle: %w", err)
		}
		if stat.Size() > opts.MaxSubtitleBytes {
			return nil, fmt.Errorf("subtitle too large: %d bytes > %d", stat.Size(), opts.MaxSubtitleBytes)
		}
	}

	subs, err := astisub.OpenFile(path)
	if err != nil {
		return nil, fmt.Errorf("parse subtitle: %w", err)
	}
	if opts.MaxCues > 0 && len(subs.Items) > opts.MaxCues {
		return nil, fmt.Errorf("too many subtitle cues: %d > %d", len(subs.Items), opts.MaxCues)
	}

	byTerm := make(map[string]*Candidate)
	for _, item := range subs.Items {
		text := strings.TrimSpace(item.String())
		if text == "" {
			continue
		}
		for _, term := range candidatePattern.FindAllString(text, -1) {
			normalized := normalizeTerm(term)
			if normalized == "" {
				continue
			}
			c := byTerm[normalized]
			if c == nil {
				c = &Candidate{Term: term, NormalizedTerm: normalized}
				byTerm[normalized] = c
			}
			c.Frequency++
			if opts.MaxSnippetsPerCandidate <= 0 || len(c.Snippets) < opts.MaxSnippetsPerCandidate {
				c.Snippets = append(c.Snippets, text)
			}
		}
	}

	out := make([]Candidate, 0, len(byTerm))
	for _, c := range byTerm {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Frequency == out[j].Frequency {
			return out[i].NormalizedTerm < out[j].NormalizedTerm
		}
		return out[i].Frequency > out[j].Frequency
	})
	if opts.MaxCandidates > 0 && len(out) > opts.MaxCandidates {
		out = out[:opts.MaxCandidates]
	}
	return out, nil
}

func normalizeTerm(term string) string {
	term = strings.ToLower(strings.TrimSpace(term))
	term = strings.Join(strings.Fields(term), " ")
	return term
}
```

- [ ] **Step 6: Run extractor tests**

Run: `go test ./internal/service/glossary -run TestExtractCandidates -v`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum internal/service/glossary
git commit -m "feat(glossary): extract subtitle terminology candidates"
```

---

### Task 5: Add SQLite Infrastructure And Migrations

**Files:**
- Modify: `go.mod`, `go.sum`
- Create: `internal/storage/sqlite/db.go`
- Create: `internal/storage/sqlite/migrations/00001_glossary.sql`
- Create: `internal/storage/sqlite/db_test.go`

- [ ] **Step 1: Add pinned SQLite and migration dependencies**

Run:

```bash
go get modernc.org/sqlite@v1.38.2 github.com/pressly/goose/v3@v3.24.3
go mod tidy
```

Expected: `go.mod` includes `modernc.org/sqlite v1.38.2` and `github.com/pressly/goose/v3 v3.24.3`.

- [ ] **Step 2: Write DB migration test**

Create `internal/storage/sqlite/db_test.go`:

```go
package sqlite

import (
	"context"
	"path/filepath"
	"testing"
)

func TestOpenMigratesGlossarySchema(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "glossary.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	for _, table := range []string{"glossary_terms", "glossary_variants", "glossary_observations", "glossary_jobs"} {
		var name string
		err := db.QueryRowContext(ctx, `select name from sqlite_master where type = 'table' and name = ?`, table).Scan(&name)
		if err != nil {
			t.Fatalf("table %s missing: %v", table, err)
		}
	}
}
```

- [ ] **Step 3: Run DB test and verify it fails**

Run: `go test ./internal/storage/sqlite -run TestOpenMigratesGlossarySchema -v`

Expected: FAIL because the storage package does not exist.

- [ ] **Step 4: Add migration SQL**

Create `internal/storage/sqlite/migrations/00001_glossary.sql`:

```sql
-- +goose Up
create table if not exists glossary_terms (
    id integer primary key autoincrement,
    scope text not null check (scope in ('media', 'common')),
    media_key text,
    normalized_term text not null,
    display_term text not null,
    created_at datetime not null default current_timestamp,
    updated_at datetime not null default current_timestamp,
    last_seen_at datetime not null default current_timestamp
);

create unique index if not exists idx_glossary_terms_media_unique
    on glossary_terms(scope, media_key, normalized_term)
    where scope = 'media';

create unique index if not exists idx_glossary_terms_common_unique
    on glossary_terms(scope, normalized_term)
    where scope = 'common';

create table if not exists glossary_variants (
    id integer primary key autoincrement,
    term_id integer not null references glossary_terms(id) on delete cascade,
    target_language text not null,
    target_text text not null,
    definition text not null default '',
    translation_mode text not null default 'translate',
    category text not null default 'phrase',
    status text not null check (status in ('active', 'suppressed', 'candidate')),
    source text not null check (source in ('generated', 'promoted', 'curated')),
    confidence real not null,
    evidence_count integer not null default 0,
    created_at datetime not null default current_timestamp,
    updated_at datetime not null default current_timestamp,
    last_seen_at datetime not null default current_timestamp
);

create index if not exists idx_glossary_variants_term_language_status
    on glossary_variants(term_id, target_language, status, confidence);

create index if not exists idx_glossary_variants_language_status_source
    on glossary_variants(target_language, status, source);

create table if not exists glossary_observations (
    id integer primary key autoincrement,
    variant_id integer not null references glossary_variants(id) on delete cascade,
    job_id text not null,
    media_key text not null,
    subtitle_path_hash text not null,
    season integer not null default 0,
    episode integer not null default 0,
    snippet text not null default '',
    confidence real not null,
    created_at datetime not null default current_timestamp
);

create index if not exists idx_glossary_observations_variant_created
    on glossary_observations(variant_id, created_at);

create index if not exists idx_glossary_observations_job
    on glossary_observations(job_id);

create index if not exists idx_glossary_observations_media_subtitle
    on glossary_observations(media_key, subtitle_path_hash);

create table if not exists glossary_jobs (
    job_id text primary key,
    media_key text not null,
    subtitle_path_hash text not null,
    status text not null,
    error text not null default '',
    created_at datetime not null default current_timestamp,
    completed_at datetime
);

-- +goose Down
drop table if exists glossary_jobs;
drop table if exists glossary_observations;
drop table if exists glossary_variants;
drop table if exists glossary_terms;
```

- [ ] **Step 5: Add SQLite opener**

Create `internal/storage/sqlite/db.go`:

```go
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"path/filepath"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

func Open(ctx context.Context, path string) (*sql.DB, error) {
	if path == "" {
		return nil, fmt.Errorf("sqlite path is required")
	}
	if err := ensureParentDir(path); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if err := applyPragmas(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	if err := migrate(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func ensureParentDir(path string) error {
	dir := filepath.Dir(path)
	return os.MkdirAll(dir, 0o755)
}

func applyPragmas(ctx context.Context, db *sql.DB) error {
	pragmas := []string{
		`pragma journal_mode = wal`,
		`pragma busy_timeout = 5000`,
		`pragma foreign_keys = on`,
	}
	for _, stmt := range pragmas {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("apply sqlite pragma %q: %w", stmt, err)
		}
	}
	return nil
}

func migrate(ctx context.Context, db *sql.DB) error {
	goose.SetBaseFS(migrationFS)
	defer goose.SetBaseFS(nil)
	if err := goose.SetDialect("sqlite3"); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}
	if err := goose.UpContext(ctx, db, "migrations"); err != nil {
		return fmt.Errorf("run sqlite migrations: %w", err)
	}
	return nil
}
```

Use this import list in `db.go`:

```go
import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)
```

- [ ] **Step 6: Run DB migration test**

Run: `go test ./internal/storage/sqlite -run TestOpenMigratesGlossarySchema -v`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum internal/storage/sqlite
git commit -m "feat(storage): add sqlite glossary migrations"
```

---

### Task 6: Implement SQLite Glossary Store

**Files:**
- Create: `internal/storage/sqlite/glossary.go`
- Create: `internal/storage/sqlite/glossary_test.go`

- [ ] **Step 1: Write store tests**

Create `internal/storage/sqlite/glossary_test.go`:

```go
package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/fusionn-subs/internal/service/glossary"
	"github.com/fusionn-subs/internal/types"
)

func TestGlossaryStoreUpsertMergesSameTarget(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "glossary.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	store := NewGlossaryStore(db)
	req := glossary.UpsertRequest{
		Job:            types.JobMessage{JobID: "job-1", SubtitlePath: "/tmp/e1.srt"},
		MediaKey:       "tvdb:355620",
		TargetLanguage: "zh-Hans",
		Options: glossary.UpsertOptions{
			MinConfidence:             0.75,
			MaxActiveVariantsPerTerm:  3,
			MaxObservationsPerVariant: 10,
		},
		Entries: []glossary.GeneratedEntry{{
			SourceTerm:      "SO15",
			NormalizedTerm:  "so15",
			TargetLanguage:  "zh-Hans",
			TargetText:      "SO15",
			Definition:      "counter terrorism command",
			TranslationMode: glossary.TranslationModePreserve,
			Category:        glossary.CategoryOrganization,
			Confidence:      0.91,
			Evidence:        []string{"SO15 asked DCI Carey."},
		}},
	}

	first, err := store.UpsertGeneratedEntries(ctx, req)
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	second, err := store.UpsertGeneratedEntries(ctx, req)
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if first.Created != 1 || second.Merged != 1 {
		t.Fatalf("results = %#v %#v", first, second)
	}

	entries, err := store.LoadPromptEntries(ctx, "tvdb:355620", "zh-Hans")
	if err != nil {
		t.Fatalf("load prompt entries: %v", err)
	}
	if len(entries) != 1 || entries[0].EvidenceCount != 2 {
		t.Fatalf("entries = %#v", entries)
	}
}
```

- [ ] **Step 2: Run store test and verify it fails**

Run: `go test ./internal/storage/sqlite -run TestGlossaryStoreUpsertMergesSameTarget -v`

Expected: FAIL because `NewGlossaryStore` does not exist.

- [ ] **Step 3: Implement store constructor and helpers**

Create `internal/storage/sqlite/glossary.go`:

```go
package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/fusionn-subs/internal/service/glossary"
)

type GlossaryStore struct {
	db *sql.DB
}

func NewGlossaryStore(db *sql.DB) *GlossaryStore {
	return &GlossaryStore{db: db}
}

func subtitlePathHash(path string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(path))))
	return hex.EncodeToString(sum[:])
}
```

- [ ] **Step 4: Implement LoadPromptEntries**

Add:

```go
func (s *GlossaryStore) LoadPromptEntries(ctx context.Context, mediaKey, targetLanguage string) ([]glossary.PromptEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
select t.scope, coalesce(t.media_key, ''), t.normalized_term, t.display_term,
       v.target_language, v.target_text, v.definition, v.translation_mode,
       v.category, v.status, v.source, v.confidence, v.evidence_count, v.last_seen_at
from glossary_terms t
join glossary_variants v on v.term_id = t.id
where v.target_language = ?
  and v.status = 'active'
  and (t.scope = 'common' or (t.scope = 'media' and t.media_key = ?))
`, targetLanguage, mediaKey)
	if err != nil {
		return nil, fmt.Errorf("load prompt entries: %w", err)
	}
	defer rows.Close()

	var out []glossary.PromptEntry
	for rows.Next() {
		var e glossary.PromptEntry
		if err := rows.Scan(&e.Scope, &e.MediaKey, &e.NormalizedTerm, &e.DisplayTerm, &e.TargetLanguage, &e.TargetText, &e.Definition, &e.TranslationMode, &e.Category, &e.Status, &e.Source, &e.Confidence, &e.EvidenceCount, &e.LastSeenAt); err != nil {
			return nil, fmt.Errorf("scan prompt entry: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate prompt entries: %w", err)
	}
	return out, nil
}
```

- [ ] **Step 5: Implement UpsertGeneratedEntries with merge**

Add transaction logic:

```go
func (s *GlossaryStore) UpsertGeneratedEntries(ctx context.Context, req glossary.UpsertRequest) (glossary.UpsertResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return glossary.UpsertResult{}, fmt.Errorf("begin glossary upsert: %w", err)
	}
	defer tx.Rollback()

	var result glossary.UpsertResult
	for _, entry := range req.Entries {
		termID, err := upsertTerm(ctx, tx, req.MediaKey, entry)
		if err != nil {
			return result, err
		}
		status := glossary.StatusActive
		if entry.Confidence < req.Options.MinConfidence {
			status = glossary.StatusCandidate
			result.Candidates++
		}

		var variantID int64
		err = tx.QueryRowContext(ctx, `
select id from glossary_variants
where term_id = ? and target_language = ? and lower(target_text) = lower(?)
limit 1`, termID, req.TargetLanguage, entry.TargetText).Scan(&variantID)
		switch {
		case err == nil:
			_, err = tx.ExecContext(ctx, `
update glossary_variants
set confidence = max(confidence, ?),
    evidence_count = evidence_count + 1,
    definition = case when length(definition) >= length(?) then definition else ? end,
    updated_at = current_timestamp,
    last_seen_at = current_timestamp
where id = ?`, entry.Confidence, entry.Definition, entry.Definition, variantID)
			if err != nil {
				return result, fmt.Errorf("merge glossary variant: %w", err)
			}
			result.Merged++
		case err == sql.ErrNoRows:
			res, err := tx.ExecContext(ctx, `
insert into glossary_variants(term_id, target_language, target_text, definition, translation_mode, category, status, source, confidence, evidence_count)
values (?, ?, ?, ?, ?, ?, ?, ?, ?, 1)`, termID, req.TargetLanguage, entry.TargetText, entry.Definition, entry.TranslationMode, entry.Category, status, glossary.SourceGenerated, entry.Confidence)
			if err != nil {
				return result, fmt.Errorf("insert glossary variant: %w", err)
			}
			variantID, _ = res.LastInsertId()
			if status == glossary.StatusActive {
				result.Created++
			}
		default:
			return result, fmt.Errorf("find glossary variant: %w", err)
		}

		if err := insertObservation(ctx, tx, variantID, req, entry); err != nil {
			return result, err
		}
	}

	if err := tx.Commit(); err != nil {
		return result, fmt.Errorf("commit glossary upsert: %w", err)
	}
	return result, nil
}
```

Add helper functions `upsertTerm` and `insertObservation` in the same file using `insert ... on conflict ... do update` for `glossary_terms`.

- [ ] **Step 6: Implement RecordJob and no-op promotion for intermediate compile**

Add:

```go
func (s *GlossaryStore) RecordJob(ctx context.Context, job glossary.JobRecord) error {
	_, err := s.db.ExecContext(ctx, `
insert into glossary_jobs(job_id, media_key, subtitle_path_hash, status, error, completed_at)
values (?, ?, ?, ?, ?, current_timestamp)
on conflict(job_id) do update set
    media_key = excluded.media_key,
    subtitle_path_hash = excluded.subtitle_path_hash,
    status = excluded.status,
    error = excluded.error,
    completed_at = current_timestamp`, job.JobID, job.MediaKey, job.SubtitlePathHash, job.Status, job.Error)
	if err != nil {
		return fmt.Errorf("record glossary job: %w", err)
	}
	return nil
}

func (s *GlossaryStore) PromoteCommonEntries(ctx context.Context, opts glossary.PromotionOptions) (glossary.PromotionResult, error) {
	return glossary.PromotionResult{}, nil
}
```

Promotion is implemented in Task 8; this method intentionally returns no promotions until that task replaces it.

- [ ] **Step 7: Run store tests**

Run: `go test ./internal/storage/sqlite -v`

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/storage/sqlite
git commit -m "feat(glossary): store generated entries in sqlite"
```

---

### Task 7: Implement Glossary LLM Clients

**Files:**
- Create: `internal/service/glossary/llm_openai.go`
- Create: `internal/service/glossary/llm_openai_test.go`
- Create: `internal/service/glossary/llm_gemini.go`
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Add pinned LLM SDK dependencies**

Run:

```bash
go get github.com/openai/openai-go/v3@v3.32.0 google.golang.org/genai@v1.0.0
go mod tidy
```

Expected: both modules are present in `go.mod` or `go.sum` as required by imports.

- [ ] **Step 2: Write OpenAI-compatible client test**

Create `internal/service/glossary/llm_openai_test.go`:

```go
package glossary

import (
	"context"
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
			Term: "SO15", NormalizedTerm: "so15", Frequency: 3, Snippets: []string{"SO15 asked DCI Carey"},
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
		SourceTerm: "X", NormalizedTerm: "x", TargetText: "X", Confidence: 1.5,
	}}}).Validate()
	if err == nil || !strings.Contains(err.Error(), "confidence") {
		t.Fatalf("error = %v", err)
	}
}
```

- [ ] **Step 3: Run OpenAI-compatible tests and verify they fail**

Run: `go test ./internal/service/glossary -run 'TestOpenAICompatible|TestGenerateResponseValidate' -v`

Expected: FAIL because `NewOpenAICompatibleClient` and `OpenAICompatibleConfig` do not exist.

- [ ] **Step 4: Implement prompt construction helper**

In `llm_openai.go`, add a helper that marshals the request into concise JSON:

```go
func buildGlossaryUserPrompt(req GenerateRequest) (string, error) {
	payload := struct {
		MediaTitle     string        `json:"media_title"`
		MediaType      string        `json:"media_type"`
		Season         int           `json:"season,omitempty"`
		Episode        int           `json:"episode,omitempty"`
		MediaKey       string        `json:"media_key"`
		TargetLanguage string        `json:"target_language"`
		Candidates     []Candidate   `json:"candidates"`
		Existing       []PromptEntry `json:"existing_entries"`
	}{
		MediaTitle:     req.Job.MediaTitle,
		MediaType:      req.Job.MediaType,
		Season:         req.Job.Season,
		Episode:        req.Job.Episode,
		MediaKey:       req.MediaKey,
		TargetLanguage: req.TargetLanguage,
		Candidates:     req.Candidates,
		Existing:       req.ExistingEntries,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal glossary prompt payload: %w", err)
	}
	return string(b), nil
}
```

- [ ] **Step 5: Implement OpenAI-compatible client**

Use `openai-go` for the OpenAI-compatible chat-completions endpoint:

```go
import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
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

func NewOpenAICompatibleClient(cfg OpenAICompatibleConfig) *OpenAICompatibleClient {
	if cfg.Endpoint == "" {
		cfg.Endpoint = "/v1/chat/completions"
	}
	client := openai.NewClient(
		option.WithBaseURL(normalizeOpenAIBaseURL(cfg.BaseURL, cfg.Endpoint)),
		option.WithAPIKey(cfg.APIKey),
	)
	return &OpenAICompatibleClient{cfg: cfg, client: client}
}

func normalizeOpenAIBaseURL(baseURL, endpoint string) string {
	baseURL = strings.TrimRight(baseURL, "/")
	endpoint = strings.TrimRight(endpoint, "/")
	if strings.HasSuffix(endpoint, "/chat/completions") {
		endpoint = strings.TrimSuffix(endpoint, "/chat/completions")
	}
	if endpoint == "" {
		return baseURL
	}
	return baseURL + endpoint
}
```

Implement `GenerateGlossary`:

```go
func (c *OpenAICompatibleClient) GenerateGlossary(ctx context.Context, req GenerateRequest) (GenerateResponse, error) {
	userPrompt, err := buildGlossaryUserPrompt(req)
	if err != nil {
		return GenerateResponse{}, err
	}
	resp, err := c.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model:       openai.ChatModel(c.cfg.Model),
		Temperature: openai.Float(c.cfg.Temperature),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.DeveloperMessage("You extract subtitle glossary entries. Return strict JSON only with an entries array."),
			openai.UserMessage(userPrompt),
		},
	})
	if err != nil {
		return GenerateResponse{}, fmt.Errorf("call glossary llm: %w", err)
	}
	if len(resp.Choices) == 0 {
		return GenerateResponse{}, fmt.Errorf("glossary llm returned no choices")
	}
	var out GenerateResponse
	if err := json.Unmarshal([]byte(resp.Choices[0].Message.Content), &out); err != nil {
		return GenerateResponse{}, fmt.Errorf("decode glossary llm response: %w", err)
	}
	if err := out.Validate(); err != nil {
		return GenerateResponse{}, err
	}
	return out, nil
}
```

- [ ] **Step 6: Implement Gemini client**

Create `internal/service/glossary/llm_gemini.go` with:

```go
type GeminiClient struct {
	client *genai.Client
	model  string
}

func NewGeminiClient(ctx context.Context, apiKey, model string) (*GeminiClient, error) {
	c, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("create gemini glossary client: %w", err)
	}
	return &GeminiClient{client: c, model: model}, nil
}
```

Implement `GenerateGlossary` using `client.Models.GenerateContent`, parse `resp.Text()`, unmarshal into `GenerateResponse`, and validate.

- [ ] **Step 7: Run LLM client tests**

Run: `go test ./internal/service/glossary -run 'TestOpenAICompatible|TestGenerateResponseValidate' -v`

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add go.mod go.sum internal/service/glossary/llm_openai.go internal/service/glossary/llm_openai_test.go internal/service/glossary/llm_gemini.go
git commit -m "feat(glossary): add glossary llm clients"
```

---

### Task 8: Add Promotion And Glossary Service Orchestration

**Files:**
- Create: `internal/service/glossary/promoter.go`
- Create: `internal/service/glossary/promoter_test.go`
- Create: `internal/service/glossary/service.go`
- Create: `internal/service/glossary/service_test.go`
- Modify: `internal/storage/sqlite/glossary.go`

- [ ] **Step 1: Write service failure policy tests**

Create `internal/service/glossary/service_test.go`:

```go
package glossary

import (
	"context"
	"errors"
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
func (s fakeStore) RecordJob(context.Context, JobRecord) error { return s.err }

type fakeLLM struct {
	resp GenerateResponse
	err  error
}

func (f fakeLLM) GenerateGlossary(context.Context, GenerateRequest) (GenerateResponse, error) {
	return f.resp, f.err
}

func TestServiceUsesExistingGlossaryWhenLLMFails(t *testing.T) {
	svc := NewService(ServiceConfig{
		Enabled:                 true,
		TargetLanguage:          "zh-Hans",
		InjectMinConfidence:     0.80,
		MaxPromptEntries:        10,
		MaxCandidates:           10,
		MaxSnippetsPerCandidate: 1,
	}, fakeStore{entries: []PromptEntry{{
		Scope: ScopeMedia, MediaKey: "title:series:the-capture", NormalizedTerm: "so15", DisplayTerm: "SO15",
		TargetText: "SO15", TranslationMode: TranslationModePreserve, Status: StatusActive, Source: SourceGenerated, Confidence: 0.9,
	}}}, fakeLLM{err: errors.New("llm down")})

	block, err := svc.Prepare(context.Background(), types.JobMessage{
		JobID: "job-1", MediaTitle: "The Capture", MediaType: "series", SubtitlePath: "/missing.srt",
	})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if block == "" {
		t.Fatal("expected existing glossary block")
	}
}
```

- [ ] **Step 2: Run service tests and verify they fail**

Run: `go test ./internal/service/glossary -run TestServiceUsesExistingGlossaryWhenLLMFails -v`

Expected: FAIL because `Service`, `ServiceConfig`, and `NewService` do not exist.

- [ ] **Step 3: Add service config and constructor**

Create `internal/service/glossary/service.go`:

```go
package glossary

import (
	"context"
	"fmt"

	"github.com/fusionn-subs/internal/types"
	"github.com/fusionn-subs/pkg/logger"
)

type ServiceConfig struct {
	Enabled                  bool
	TargetLanguage           string
	MinConfidence            float64
	InjectMinConfidence      float64
	MaxPromptEntries         int
	MaxCandidates            int
	MaxSnippetsPerCandidate  int
	MaxSubtitleBytes         int64
	MaxCues                  int
	MaxActiveVariantsPerTerm int
	MaxObservationsPerVariant int
	PromoteMinConfidence    float64
	PromoteMinMediaCount    int
}

type Service struct {
	cfg   ServiceConfig
	store Store
	llm   LLMClient
}

func NewService(cfg ServiceConfig, store Store, llm LLMClient) *Service {
	return &Service{cfg: cfg, store: store, llm: llm}
}
```

- [ ] **Step 4: Implement Prepare**

Add:

```go
func (s *Service) Prepare(ctx context.Context, msg types.JobMessage) (string, error) {
	if s == nil || !s.cfg.Enabled {
		return "", nil
	}
	mediaKey := ResolveMediaKey(msg)
	entries, err := s.store.LoadPromptEntries(ctx, mediaKey.Value, s.cfg.TargetLanguage)
	if err != nil {
		return "", fmt.Errorf("load glossary entries: %w", err)
	}

	candidates, extractErr := ExtractCandidates(msg.SubtitlePath, ExtractOptions{
		MaxSubtitleBytes:        s.cfg.MaxSubtitleBytes,
		MaxCues:                 s.cfg.MaxCues,
		MaxCandidates:           s.cfg.MaxCandidates,
		MaxSnippetsPerCandidate: s.cfg.MaxSnippetsPerCandidate,
	})
	if extractErr != nil {
		logger.Warnf("glossary extraction skipped: %v", extractErr)
		return BuildPrompt(entries, PromptOptions{MediaKey: mediaKey.Value, InjectMinConfidence: s.cfg.InjectMinConfidence, MaxPromptEntries: s.cfg.MaxPromptEntries}), nil
	}

	resp, err := s.llm.GenerateGlossary(ctx, GenerateRequest{
		Job:             msg,
		MediaKey:        mediaKey.Value,
		TargetLanguage:  s.cfg.TargetLanguage,
		ExistingEntries: entries,
		Candidates:      candidates,
	})
	if err != nil {
		logger.Warnf("glossary LLM generation skipped: %v", err)
		return BuildPrompt(entries, PromptOptions{MediaKey: mediaKey.Value, InjectMinConfidence: s.cfg.InjectMinConfidence, MaxPromptEntries: s.cfg.MaxPromptEntries}), nil
	}

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
		return "", fmt.Errorf("store glossary entries: %w", err)
	}
	logger.Infof("Glossary entries: created=%d merged=%d candidates=%d suppressed=%d", result.Created, result.Merged, result.Candidates, result.Suppressed)

	if _, err := s.store.PromoteCommonEntries(ctx, PromotionOptions{
		TargetLanguage:        s.cfg.TargetLanguage,
		MinConfidence:         s.cfg.PromoteMinConfidence,
		MinDistinctMediaCount: s.cfg.PromoteMinMediaCount,
	}); err != nil {
		return "", fmt.Errorf("promote glossary entries: %w", err)
	}

	entries, err = s.store.LoadPromptEntries(ctx, mediaKey.Value, s.cfg.TargetLanguage)
	if err != nil {
		return "", fmt.Errorf("reload glossary entries: %w", err)
	}
	return BuildPrompt(entries, PromptOptions{MediaKey: mediaKey.Value, InjectMinConfidence: s.cfg.InjectMinConfidence, MaxPromptEntries: s.cfg.MaxPromptEntries}), nil
}
```

Before each successful return from `Prepare`, call:

```go
if err := s.store.RecordJob(ctx, JobRecord{
	JobID:            msg.JobID,
	MediaKey:         mediaKey.Value,
	SubtitlePathHash: SubtitlePathHash(msg.SubtitlePath),
	Status:           "completed",
}); err != nil {
	return "", fmt.Errorf("record glossary job: %w", err)
}
```

Before returning a fatal store/promotion error, call `RecordJob` with `Status: "failed"` and `Error: err.Error()`.

Add `SubtitlePathHash` to `types.go`:

```go
func SubtitlePathHash(path string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(path))))
	return hex.EncodeToString(sum[:])
}
```

Add `crypto/sha256`, `encoding/hex`, and `strings` imports to `types.go`.

- [ ] **Step 5: Implement SQLite promotion**

Replace the no-op promotion in `internal/storage/sqlite/glossary.go` with SQL that inserts common terms and matching common variants for compatible generated terms:

```go
func (s *GlossaryStore) PromoteCommonEntries(ctx context.Context, opts glossary.PromotionOptions) (glossary.PromotionResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return glossary.PromotionResult{}, fmt.Errorf("begin glossary promotion: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `
select t.normalized_term, min(t.display_term), v.target_text, max(v.definition),
       v.translation_mode, v.category, avg(v.confidence), sum(v.evidence_count)
from glossary_terms t
join glossary_variants v on v.term_id = t.id
where t.scope = 'media'
  and v.target_language = ?
  and v.status = 'active'
  and v.confidence >= ?
group by t.normalized_term, v.target_text, v.translation_mode, v.category
having count(distinct t.media_key) >= ?`, opts.TargetLanguage, opts.MinConfidence, opts.MinDistinctMediaCount)
	if err != nil {
		return glossary.PromotionResult{}, fmt.Errorf("select promotion candidates: %w", err)
	}
	defer rows.Close()

	result := glossary.PromotionResult{}
	for rows.Next() {
		var normalized, display, targetText, definition, mode, category string
		var confidence float64
		var evidenceCount int
		if err := rows.Scan(&normalized, &display, &targetText, &definition, &mode, &category, &confidence, &evidenceCount); err != nil {
			return result, fmt.Errorf("scan promotion candidate: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
insert or ignore into glossary_terms(scope, media_key, normalized_term, display_term)
values ('common', null, ?, ?)`, normalized, display); err != nil {
			return result, fmt.Errorf("insert common term: %w", err)
		}
		var termID int64
		if err := tx.QueryRowContext(ctx, `
select id from glossary_terms where scope = 'common' and normalized_term = ?`, normalized).Scan(&termID); err != nil {
			return result, fmt.Errorf("load common term id: %w", err)
		}
		res, err := tx.ExecContext(ctx, `
insert into glossary_variants(term_id, target_language, target_text, definition, translation_mode, category, status, source, confidence, evidence_count)
select ?, ?, ?, ?, ?, ?, 'active', 'promoted', ?, ?
where not exists (
    select 1 from glossary_variants
    where term_id = ? and target_language = ? and lower(target_text) = lower(?)
)`, termID, opts.TargetLanguage, targetText, definition, mode, category, confidence, evidenceCount, termID, opts.TargetLanguage, targetText)
		if err != nil {
			return result, fmt.Errorf("insert common variant: %w", err)
		}
		affected, _ := res.RowsAffected()
		if affected > 0 {
			result.Promoted++
		} else {
			result.Skipped++
		}
	}
	if err := rows.Err(); err != nil {
		return result, fmt.Errorf("iterate promotion candidates: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return result, fmt.Errorf("commit glossary promotion: %w", err)
	}
	return result, nil
}
```

- [ ] **Step 6: Run service tests**

Run: `go test ./internal/service/glossary ./internal/storage/sqlite -v`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/service/glossary internal/storage/sqlite/glossary.go
git commit -m "feat(glossary): prepare glossary prompts"
```

---

### Task 9: Wire Glossary Into Worker And Main

**Files:**
- Modify: `internal/service/worker/worker.go`
- Create: `internal/service/worker/worker_test.go`
- Modify: `cmd/fusionn-subs/main.go`

- [ ] **Step 1: Add worker tests for glossary failure policy**

Create `internal/service/worker/worker_test.go` with small fakes:

```go
package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/fusionn-subs/internal/client/callback"
	"github.com/fusionn-subs/internal/service/translator"
	"github.com/fusionn-subs/internal/types"
)

type fakeTranslator struct {
	req translator.Request
	err error
}

func (f *fakeTranslator) Translate(ctx context.Context, req translator.Request) (string, error) {
	f.req = req
	return "/tmp/out.chs.srt", f.err
}

type fakeGlossary struct {
	block string
	err   error
}

func (f fakeGlossary) Prepare(ctx context.Context, msg types.JobMessage) (string, error) {
	return f.block, f.err
}

func TestProcessJobPassesGlossaryInstruction(t *testing.T) {
	trans := &fakeTranslator{}
	w := &Worker{
		cfg: Config{MaxTranslationRetries: 1},
		translator: trans,
		glossary: fakeGlossary{block: "Glossary guidance:\n- SO15: keep as \"SO15\""},
		callback: callback.NewClient("http://127.0.0.1/unused", 0, 0, nil),
	}

	err := w.translateJob(context.Background(), types.JobMessage{
		JobID: "job-1", VideoPath: "/tmp/video.mkv", SubtitlePath: "/tmp/in.srt",
	})
	if err != nil {
		t.Fatalf("translate job: %v", err)
	}
	if trans.req.ExtraInstruction == "" {
		t.Fatal("missing glossary instruction")
	}
}

func TestProcessJobFailsOnGlossaryDBError(t *testing.T) {
	w := &Worker{
		cfg: Config{MaxTranslationRetries: 1},
		translator: &fakeTranslator{},
		glossary: fakeGlossary{err: errors.New("sqlite down")},
	}

	err := w.translateJob(context.Background(), types.JobMessage{
		JobID: "job-1", VideoPath: "/tmp/video.mkv", SubtitlePath: "/tmp/in.srt",
	})
	if err == nil {
		t.Fatal("expected glossary error")
	}
}
```

Refactor worker in Step 3 so translation retry logic is testable without callback network I/O.

- [ ] **Step 2: Run worker tests and verify they fail**

Run: `go test ./internal/service/worker -run 'TestProcessJob.*Glossary' -v`

Expected: FAIL because `Worker.glossary`, `translateJob`, and glossary interface do not exist.

- [ ] **Step 3: Add glossary interface and split translation retry logic**

Modify `internal/service/worker/worker.go`:

```go
type GlossaryPreparer interface {
	Prepare(ctx context.Context, msg types.JobMessage) (string, error)
}

type Worker struct {
	redis      *redis.Client
	cfg        Config
	translator translator.Translator
	glossary   GlossaryPreparer
	callback   *callback.Client
}

func New(redisClient *redis.Client, cfg Config, trans translator.Translator, glossary GlossaryPreparer, callbackClient *callback.Client) *Worker {
	return &Worker{
		redis:      redisClient,
		cfg:        cfg,
		translator: trans,
		glossary:   glossary,
		callback:   callbackClient,
	}
}
```

Extract retry logic into:

```go
func (w *Worker) translateJob(ctx context.Context, msg types.JobMessage) (string, error) {
	extraInstruction := ""
	if w.glossary != nil {
		block, err := w.glossary.Prepare(ctx, msg)
		if err != nil {
			return "", fmt.Errorf("prepare glossary: %w", err)
		}
		extraInstruction = block
	}

	var chsPath string
	var lastErr error
	maxRetries := w.cfg.MaxTranslationRetries
	if maxRetries <= 0 {
		maxRetries = 3
	}
	for attempt := 1; attempt <= maxRetries; attempt++ {
		if attempt > 1 {
			logger.Infof("Translation retry %d/%d: job_id=%s", attempt-1, maxRetries-1, msg.JobID)
		}
		var err error
		chsPath, err = w.translator.Translate(ctx, translator.Request{Job: msg, ExtraInstruction: extraInstruction})
		if err == nil {
			return chsPath, nil
		}
		lastErr = err
		logger.Warnf("Translation attempt %d failed: %v", attempt, err)
		if errors.Is(err, translator.ErrAllModelsExhausted) {
			break
		}
		if attempt < maxRetries && !errors.Is(err, translator.ErrRateLimited) {
			select {
			case <-time.After(2 * time.Second):
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
	}
	if errors.Is(lastErr, translator.ErrAllModelsExhausted) {
		return "", fmt.Errorf("all models exhausted: %w", lastErr)
	}
	return "", fmt.Errorf("translation failed after %d attempts: %w", maxRetries, lastErr)
}
```

Then `processJob` calls `translateJob`, sends callback, and logs completion.

- [ ] **Step 4: Wire main without creating an import cycle**

Modify `cmd/fusionn-subs/main.go`:

1. Import glossary service package and SQLite storage package.
2. After context is created and before worker creation, open DB only if `cfg.Glossary.Enabled`.
3. Build `sqlite.NewGlossaryStore(db)`.
4. Build the glossary LLM client from `cfg.Glossary.LLM`.
5. Build `glossary.NewService`.
6. Pass glossary service to `worker.New`.

Sketch:

```go
var glossarySvc worker.GlossaryPreparer
var glossaryDB *sql.DB
if cfg.Glossary.Enabled {
	glossaryDB, err = sqlite.Open(ctx, cfg.Glossary.DBPath)
	if err != nil {
		return fmt.Errorf("glossary sqlite error: %w", err)
	}
	defer glossaryDB.Close()
	store := sqlite.NewGlossaryStore(glossaryDB)
	var glossaryLLM glossary.LLMClient
	switch cfg.Glossary.LLM.Provider {
	case "openai_compatible":
		glossaryLLM = glossary.NewOpenAICompatibleClient(glossary.OpenAICompatibleConfig{
			BaseURL: cfg.Glossary.LLM.BaseURL, Endpoint: cfg.Glossary.LLM.Endpoint,
			APIKey: cfg.Glossary.LLM.APIKey, Model: cfg.Glossary.LLM.Model,
			Temperature: cfg.Glossary.LLM.Temperature,
		})
	case "gemini":
		apiKey := cfg.Glossary.LLM.APIKey
		if apiKey == "" {
			apiKey = cfg.Gemini.APIKey
		}
		glossaryLLM, err = glossary.NewGeminiClient(ctx, apiKey, cfg.Glossary.LLM.Model)
		if err != nil {
			return fmt.Errorf("glossary gemini client error: %w", err)
		}
	default:
		return fmt.Errorf("unsupported glossary llm provider: %s", cfg.Glossary.LLM.Provider)
	}
	glossarySvc = glossary.NewService(glossary.ServiceConfig{
		Enabled: cfg.Glossary.Enabled, TargetLanguage: cfg.Glossary.TargetLanguage,
		MinConfidence: cfg.Glossary.MinConfidence, InjectMinConfidence: cfg.Glossary.InjectMinConfidence,
		MaxPromptEntries: cfg.Glossary.MaxPromptEntries, MaxCandidates: cfg.Glossary.MaxCandidates,
		MaxSnippetsPerCandidate: cfg.Glossary.MaxSnippetsPerCandidate,
		MaxSubtitleBytes: cfg.Glossary.MaxSubtitleBytes, MaxCues: cfg.Glossary.MaxCues,
		MaxActiveVariantsPerTerm: cfg.Glossary.MaxActiveVariantsPerTerm,
		MaxObservationsPerVariant: cfg.Glossary.MaxObservationsPerVariant,
		PromoteMinConfidence: cfg.Glossary.PromoteMinConfidence,
		PromoteMinMediaCount: cfg.Glossary.PromoteMinMediaCount,
	}, store, glossaryLLM)
}
```

Update `worker.New(..., translatorSvc, glossarySvc, callbackClient)`.

- [ ] **Step 5: Run worker/main tests**

Run: `go test ./internal/service/worker ./cmd/fusionn-subs -v`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/service/worker cmd/fusionn-subs/main.go
git commit -m "feat(glossary): wire glossary preparation into worker"
```

---

### Task 10: Update Config Example, README, And Full Verification

**Files:**
- Modify: `config/config.example.yaml`
- Modify: `README.md`

- [ ] **Step 1: Add glossary config example**

In `config/config.example.yaml`, add after `translator`:

```yaml
# -----------------------------------------------------------------------------
# GLOSSARY - Automatic terminology extraction before translation
# -----------------------------------------------------------------------------
glossary:
  enabled: false
  db_path: "/app/data/glossary.db"
  target_language: "zh-Hans"
  min_confidence: 0.75
  inject_min_confidence: 0.80
  max_prompt_entries: 30
  max_candidates: 80
  max_snippets_per_candidate: 3
  max_subtitle_bytes: 1048576
  max_cues: 3000
  max_active_variants_per_term: 3
  max_observations_per_variant: 10
  promote_min_confidence: 0.85
  promote_min_media_count: 3
  llm:
    provider: "openai_compatible"
    base_url: "http://127.0.0.1:8045"
    endpoint: "/v1/chat/completions"
    api_key: ""
    model: "qwen3:8b"
    timeout: 60s
    temperature: 0.1
```

- [ ] **Step 2: Document Redis job identity fields**

In `README.md`, update the job payload example to include optional fields:

```json
{
  "job_id": "job-123",
  "video_path": "/media/Show/S01E01.mkv",
  "subtitle_path": "/media/Show/S01E01.eng.srt",
  "media_title": "The Capture",
  "media_type": "series",
  "source_system": "sonarr",
  "media_id": "42",
  "external_ids": {
    "tvdb": "355620",
    "imdb": "tt8201186"
  },
  "season": 1,
  "episode": 1
}
```

Explain that these fields improve per-series/movie glossary reuse and are optional for backward compatibility.

- [ ] **Step 3: Document glossary failure behavior**

Add a short README section:

```markdown
### Automatic Glossary

When `glossary.enabled` is true, fusionn-subs scans each subtitle locally, extracts terminology candidates, asks the configured glossary LLM for Chinese-oriented glossary entries, stores them in SQLite, and injects a compact glossary block into the translation instruction.

Glossary extraction and LLM failures do not block translation; existing glossary entries may still be injected. SQLite open, migration, corruption, or transaction failures are treated as service/job failures because persistence is not trustworthy.
```

- [ ] **Step 4: Run unit tests**

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 5: Run build**

Run: `make build`

Expected: `fusionn-subs` binary is built successfully.

- [ ] **Step 6: Run Docker build**

Run: `make docker`

Expected: Docker image builds successfully with the pinned CGO-free SQLite driver.

- [ ] **Step 7: Commit**

```bash
git add config/config.example.yaml README.md Dockerfile go.mod go.sum
git commit -m "docs: document automatic glossary configuration"
```

---

## Final Verification

- [ ] Run `go test ./...`
- [ ] Run `make build`
- [ ] Run `make docker`
- [ ] Start with `glossary.enabled=false` and confirm startup behavior matches the previous worker.
- [ ] Start with `glossary.enabled=true` and a temp `db_path`; confirm migrations create the SQLite schema.
- [ ] Run one local Redis job against a sample subtitle containing repeated terms such as `SO15`; confirm glossary rows are inserted and the translator command includes the glossary instruction.
