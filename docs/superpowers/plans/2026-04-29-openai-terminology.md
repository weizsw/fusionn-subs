# OpenAI Terminology Integration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a first-class OpenAI translation provider and pass glossary entries to llm-subtrans as structured terminology instead of instruction prose.

**Architecture:** Add `openai` config and translator support beside the existing provider factory. Replace glossary text injection with a small structured payload that carries terminology pairs and an explicit build-map flag through worker and translator request boundaries. Provider translators keep their existing script execution paths and share one helper for llm-subtrans terminology flags.

**Tech Stack:** Go 1.23, Viper config structs, llm-subtrans shell scripts, SQLite-backed glossary service, standard `go test ./...`.

---

## File Structure

- Modify `internal/config/config.go`: add `OpenAIConfig`, validation, defaults, provider constant, safe log values.
- Modify `internal/config/config_test.go`: add OpenAI validation and safe-log tests.
- Modify `internal/service/translator/translator.go`: add `Terminology` fields and shared terminology arg helper.
- Modify `internal/service/translator/translator_test.go`: update fallback request test and add terminology helper tests.
- Create `internal/service/translator/openai.go`: implement `OpenAITranslator`.
- Create `internal/service/translator/openai_test.go`: test OpenAI arg construction without running scripts.
- Modify `internal/service/translator/openrouter.go`, `gemini.go`, `local_llm.go`: append terminology args.
- Modify `internal/service/translator/factory.go`: construct OpenAI provider.
- Modify `internal/service/worker/worker.go`: change glossary interface to return structured payload.
- Modify `internal/service/worker/worker_test.go`: assert terminology payload reaches translator.
- Modify `internal/service/glossary/types.go`: add glossary `Payload` type.
- Modify `internal/service/glossary/prompt_builder.go`: add structured terminology selection while preserving the same ranking rules; remove the old prose builder after service callers move off it.
- Modify `internal/service/glossary/prompt_builder_test.go`: assert structured terminology selection.
- Modify `internal/service/glossary/service.go`: return `glossary.Payload`.
- Modify `cmd/fusionn-subs/main.go`: bridge glossary payload to worker payload.
- Modify `Dockerfile`: generate `gpt-subtrans.sh` and expose `OPENAI_SCRIPT_PATH`.
- Modify `config/config.example.yaml` and `README.md`: document OpenAI and terminology behavior.

### Task 1: Add OpenAI Config Validation

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

- [ ] **Step 1: Write failing OpenAI config tests**

Add these tests near the existing provider/config validation tests in `internal/config/config_test.go`:

```go
func TestOpenAIProviderValidatesConfig(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	cfg := validConfigForTest()
	cfg.Gemini.APIKey = ""
	cfg.Translator.Providers = []string{"openai"}
	cfg.OpenAI = OpenAIConfig{
		APIKey:       "openai-key",
		Model:        "gpt-5-mini",
		RateLimit:    10,
		MaxBatchSize: 20,
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestOpenAIProviderUsesEnvironmentAPIKeyFallback(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "env-openai-key")
	cfg := validConfigForTest()
	cfg.Gemini.APIKey = ""
	cfg.Translator.Providers = []string{"openai"}
	cfg.OpenAI.Model = "gpt-5-mini"

	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestOpenAIProviderRequiresAPIKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	cfg := validConfigForTest()
	cfg.Gemini.APIKey = ""
	cfg.Translator.Providers = []string{"openai"}
	cfg.OpenAI.Model = "gpt-5-mini"

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "openai.api_key or OPENAI_API_KEY is required") {
		t.Fatalf("error = %v", err)
	}
}

func TestOpenAIProviderRequiresModel(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	cfg := validConfigForTest()
	cfg.Gemini.APIKey = ""
	cfg.Translator.Providers = []string{"openai"}
	cfg.OpenAI.APIKey = "openai-key"

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "openai.model is required") {
		t.Fatalf("error = %v", err)
	}
}

func TestOpenAIProviderRejectsHTTPXWithoutAPIBase(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	cfg := validConfigForTest()
	cfg.Gemini.APIKey = ""
	cfg.Translator.Providers = []string{"openai"}
	cfg.OpenAI.APIKey = "openai-key"
	cfg.OpenAI.Model = "gpt-5-mini"
	cfg.OpenAI.UseHTTPX = true

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "openai.use_httpx requires openai.api_base") {
		t.Fatalf("error = %v", err)
	}
}

func TestSafeLogValuesIncludesOpenAIValues(t *testing.T) {
	cfg := validConfigForTest()
	cfg.OpenAI = OpenAIConfig{
		APIKey:       "openai-secret",
		Model:        "gpt-5-mini",
		APIBase:      "https://example.openai.local/v1",
		UseHTTPX:     true,
		Instruction:  "Keep tone natural.",
		RateLimit:    12,
		MaxBatchSize: 18,
		Timeout:      45 * time.Minute,
	}

	values := cfg.SafeLogValues()
	want := map[string]any{
		"openai.api_key":        util.MaskSecret(cfg.OpenAI.APIKey),
		"openai.model":          cfg.OpenAI.Model,
		"openai.api_base":       cfg.OpenAI.APIBase,
		"openai.use_httpx":      cfg.OpenAI.UseHTTPX,
		"openai.instruction":    cfg.OpenAI.Instruction,
		"openai.rate_limit":     cfg.OpenAI.RateLimit,
		"openai.max_batch_size": cfg.OpenAI.MaxBatchSize,
		"openai.timeout":        cfg.OpenAI.Timeout.String(),
	}

	for key, wantValue := range want {
		if got := values[key]; got != wantValue {
			t.Fatalf("%s = %#v, want %#v", key, got, wantValue)
		}
	}
	if values["openai.api_key"] == cfg.OpenAI.APIKey {
		t.Fatalf("openai.api_key was not masked")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config`

Expected: FAIL with undefined `OpenAIConfig` / missing `OpenAI` field / unknown provider errors.

- [ ] **Step 3: Add OpenAI config structs and validation**

In `internal/config/config.go`, add `DefaultOpenAITimeout` and `ProviderOpenAI`:

```go
DefaultOpenAITimeout    = 30 * time.Minute
ProviderOpenAI          = "openai"
```

Add `OpenAI OpenAIConfig` to `Config`:

```go
OpenAI     OpenAIConfig     `mapstructure:"openai"`
```

Add this struct after `OpenRouterConfig`:

```go
type OpenAIConfig struct {
	APIKey       string        `mapstructure:"api_key"`
	Model        string        `mapstructure:"model"`
	APIBase      string        `mapstructure:"api_base"`
	UseHTTPX     bool          `mapstructure:"use_httpx"`
	Instruction  string        `mapstructure:"instruction"`
	RateLimit    int           `mapstructure:"rate_limit"`
	MaxBatchSize int           `mapstructure:"max_batch_size"`
	Timeout      time.Duration `mapstructure:"timeout"`
}
```

Add OpenAI to `validProviders`:

```go
ProviderOpenAI:     true,
```

Add a helper near `validateOpenRouterAutoSelectModel`:

```go
func (c *Config) validateOpenAISection() error {
	if strings.TrimSpace(c.OpenAI.APIKey) == "" && strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) == "" {
		return fmt.Errorf("openai.api_key or OPENAI_API_KEY is required when openai is in translator.providers")
	}
	if strings.TrimSpace(c.OpenAI.Model) == "" {
		return fmt.Errorf("openai.model is required when openai is in translator.providers")
	}
	if c.OpenAI.UseHTTPX && strings.TrimSpace(c.OpenAI.APIBase) == "" {
		return fmt.Errorf("openai.use_httpx requires openai.api_base")
	}
	if c.OpenAI.Timeout == 0 {
		c.OpenAI.Timeout = DefaultOpenAITimeout
	}
	return nil
}
```

In the provider validation switch, add:

```go
case ProviderOpenAI:
	if err := c.validateOpenAISection(); err != nil {
		return err
	}
```

In `SafeLogValues`, add:

```go
"openai.api_key":        util.MaskSecret(c.OpenAI.APIKey),
"openai.model":          c.OpenAI.Model,
"openai.api_base":       c.OpenAI.APIBase,
"openai.use_httpx":      c.OpenAI.UseHTTPX,
"openai.instruction":    c.OpenAI.Instruction,
"openai.rate_limit":     c.OpenAI.RateLimit,
"openai.max_batch_size": c.OpenAI.MaxBatchSize,
"openai.timeout":        c.OpenAI.Timeout.String(),
```

- [ ] **Step 4: Run config tests**

Run: `go test ./internal/config`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): add openai provider settings"
```

### Task 2: Add Structured Terminology Request Plumbing

**Files:**
- Modify: `internal/service/translator/translator.go`
- Modify: `internal/service/translator/translator_test.go`

- [ ] **Step 1: Write failing terminology helper tests**

In `internal/service/translator/translator_test.go`, update `TestFallbackTranslatorForwardsRequestUnchangedToFallbackProvider` so `wantReq` includes:

```go
Terminology: []Terminology{
	{Source: "SO15", Target: "SO15"},
},
BuildTerminologyMap: true,
```

Then add these tests:

```go
func TestAppendTerminologyArgsAddsPairsAndBuildMap(t *testing.T) {
	req := Request{
		Terminology: []Terminology{
			{Source: "SO15", Target: "SO15"},
			{Source: "Major Crimes", Target: "重案组"},
		},
		BuildTerminologyMap: true,
	}

	got := appendTerminologyArgs([]string{"input.srt"}, req)
	want := []string{
		"input.srt",
		"--terminology", "SO15::SO15",
		"--terminology", "Major Crimes::重案组",
		"--build-terminology-map",
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestAppendTerminologyArgsSkipsBlankPairs(t *testing.T) {
	req := Request{
		Terminology: []Terminology{
			{Source: "  ", Target: "SO15"},
			{Source: "Alice", Target: ""},
			{Source: " Bob ", Target: " 鲍勃 "},
		},
	}

	got := appendTerminologyArgs([]string{"input.srt"}, req)
	want := []string{"input.srt", "--terminology", "Bob::鲍勃"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestAppendTerminologyArgsOmitsBuildMapWhenDisabled(t *testing.T) {
	got := appendTerminologyArgs([]string{"input.srt"}, Request{})
	want := []string{"input.srt"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/service/translator`

Expected: FAIL with undefined `Terminology` and `appendTerminologyArgs`.

- [ ] **Step 3: Implement request terminology fields and helper**

In `internal/service/translator/translator.go`, add:

```go
type Terminology struct {
	Source string
	Target string
}
```

Change `Request` to:

```go
type Request struct {
	Job                 types.JobMessage
	ExtraInstruction    string
	Terminology         []Terminology
	BuildTerminologyMap bool
}
```

Add below `combineInstructions`:

```go
func appendTerminologyArgs(args []string, req Request) []string {
	for _, term := range req.Terminology {
		source := strings.TrimSpace(term.Source)
		target := strings.TrimSpace(term.Target)
		if source == "" || target == "" {
			continue
		}
		args = append(args, "--terminology", source+"::"+target)
	}
	if req.BuildTerminologyMap {
		args = append(args, "--build-terminology-map")
	}
	return args
}
```

`translator.go` already imports `strings`, so no import change is needed.

- [ ] **Step 4: Run translator tests**

Run: `go test ./internal/service/translator`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/service/translator/translator.go internal/service/translator/translator_test.go
git commit -m "feat(translator): add terminology request fields"
```

### Task 3: Convert Glossary Prompt Selection to Terminology Selection

**Files:**
- Modify: `internal/service/glossary/types.go`
- Modify: `internal/service/glossary/prompt_builder.go`
- Modify: `internal/service/glossary/prompt_builder_test.go`

- [ ] **Step 1: Write failing glossary terminology tests**

Replace the assertions in `internal/service/glossary/prompt_builder_test.go` with structured terminology expectations:

```go
func TestBuildTerminologyPrefersMediaOverCommon(t *testing.T) {
	now := time.Now()
	entries := []PromptEntry{
		{
			Scope:          ScopeCommon,
			NormalizedTerm: "so15",
			DisplayTerm:    "SO15",
			TargetText:     "SO15 common",
			Definition:     "Common meaning",
			Confidence:     0.95,
			EvidenceCount:  4,
			Status:         StatusActive,
			Source:         SourcePromoted,
			LastSeenAt:     now,
		},
		{
			Scope:           ScopeMedia,
			MediaKey:        "tvdb:355620",
			NormalizedTerm:  "so15",
			DisplayTerm:     "SO15",
			TargetText:      "SO15",
			Definition:      "The Capture usage",
			TranslationMode: TranslationModePreserve,
			Confidence:      0.90,
			EvidenceCount:   2,
			Status:          StatusActive,
			Source:          SourceGenerated,
			LastSeenAt:      now,
		},
	}

	got := BuildTerminology(entries, PromptOptions{
		MediaKey:            "tvdb:355620",
		InjectMinConfidence: 0.80,
		MaxPromptEntries:    10,
	})

	want := []Terminology{{Source: "SO15", Target: "SO15"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("terminology = %#v, want %#v", got, want)
	}
}

func TestBuildTerminologyFiltersLowConfidence(t *testing.T) {
	got := BuildTerminology([]PromptEntry{
		{
			Scope:          ScopeMedia,
			MediaKey:       "m",
			NormalizedTerm: "x",
			DisplayTerm:    "X",
			TargetText:     "X",
			Confidence:     0.40,
			Status:         StatusActive,
		},
	}, PromptOptions{MediaKey: "m", InjectMinConfidence: 0.80, MaxPromptEntries: 10})

	if len(got) != 0 {
		t.Fatalf("terminology = %#v", got)
	}
}

func TestBuildTerminologyFallsBackToNormalizedTerm(t *testing.T) {
	got := BuildTerminology([]PromptEntry{
		{
			Scope:          ScopeCommon,
			NormalizedTerm: "major crimes",
			TargetText:     "重案组",
			Confidence:     0.95,
			Status:         StatusActive,
		},
	}, PromptOptions{MediaKey: "m", InjectMinConfidence: 0.80, MaxPromptEntries: 10})

	want := []Terminology{{Source: "major crimes", Target: "重案组"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("terminology = %#v, want %#v", got, want)
	}
}
```

Update the imports in that test file to:

```go
import (
	"reflect"
	"testing"
	"time"
)
```

- [ ] **Step 2: Run glossary tests to verify they fail**

Run: `go test ./internal/service/glossary`

Expected: FAIL with undefined `BuildTerminology` and `Terminology`.

- [ ] **Step 3: Add glossary payload and terminology type**

In `internal/service/glossary/types.go`, add:

```go
type Terminology struct {
	Source string
	Target string
}

type Payload struct {
	Terminology         []Terminology
	BuildTerminologyMap bool
}
```

- [ ] **Step 4: Add terminology builder beside the existing prompt builder**

In `internal/service/glossary/prompt_builder.go`, add `BuildTerminology` below `BuildPrompt`. Keep `BuildPrompt` for this commit because `service.go` still calls it until Task 4.

```go
func BuildTerminology(entries []PromptEntry, opts PromptOptions) []Terminology {
	if opts.MaxPromptEntries <= 0 {
		return nil
	}

	winners := make(map[string]PromptEntry)
	for _, entry := range entries {
		if !isPromptEligible(entry, opts) {
			continue
		}
		term := strings.TrimSpace(entry.NormalizedTerm)
		current, ok := winners[term]
		if !ok || promptRank(entry, opts.MediaKey) > promptRank(current, opts.MediaKey) {
			winners[term] = entry
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

	terms := make([]Terminology, 0, len(selected))
	for _, entry := range selected {
		source := promptDisplayTerm(entry)
		target := strings.TrimSpace(entry.TargetText)
		if source == "" || target == "" {
			continue
		}
		terms = append(terms, Terminology{Source: source, Target: target})
	}
	return terms
}
```

- [ ] **Step 5: Run glossary tests**

Run: `go test ./internal/service/glossary`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/service/glossary/types.go internal/service/glossary/prompt_builder.go internal/service/glossary/prompt_builder_test.go
git commit -m "feat(glossary): build terminology pairs"
```

### Task 4: Return Glossary Terminology Payload Through Worker

**Files:**
- Modify: `internal/service/glossary/service.go`
- Modify: `internal/service/glossary/prompt_builder.go`
- Modify: `internal/service/worker/worker.go`
- Modify: `internal/service/worker/worker_test.go`
- Modify: `cmd/fusionn-subs/main.go`

- [ ] **Step 1: Write failing worker terminology test**

In `internal/service/worker/worker_test.go`, change `fakeGlossary` to:

```go
type fakeGlossary struct {
	payload GlossaryPayload
	err     error
}

func (f fakeGlossary) Prepare(context.Context, types.JobMessage) (GlossaryPayload, error) {
	return f.payload, f.err
}
```

Replace `TestTranslateJobPassesGlossaryInstruction` with:

```go
func TestTranslateJobPassesGlossaryTerminology(t *testing.T) {
	trans := &fakeTranslator{}
	w := &Worker{
		cfg:        Config{MaxTranslationRetries: 1},
		translator: trans,
		glossary: fakeGlossary{payload: GlossaryPayload{
			Terminology: []translator.Terminology{
				{Source: "SO15", Target: "SO15"},
			},
			BuildTerminologyMap: true,
		}},
	}

	_, err := w.translateJob(context.Background(), types.JobMessage{
		JobID:        "job-1",
		VideoPath:    "/tmp/video.mkv",
		SubtitlePath: "/tmp/in.srt",
	})
	if err != nil {
		t.Fatalf("translate job: %v", err)
	}
	if !reflect.DeepEqual(trans.req.Terminology, []translator.Terminology{{Source: "SO15", Target: "SO15"}}) {
		t.Fatalf("terminology = %#v", trans.req.Terminology)
	}
	if !trans.req.BuildTerminologyMap {
		t.Fatal("missing build terminology map flag")
	}
	if trans.req.ExtraInstruction != "" {
		t.Fatalf("extra instruction = %q, want empty", trans.req.ExtraInstruction)
	}
}
```

Add `reflect` to the imports.

- [ ] **Step 2: Run worker tests to verify they fail**

Run: `go test ./internal/service/worker`

Expected: FAIL because `GlossaryPayload` and the new `Prepare` signature are not implemented.

- [ ] **Step 3: Update worker glossary interface and translation request**

In `internal/service/worker/worker.go`, add:

```go
type GlossaryPayload struct {
	Terminology         []translator.Terminology
	BuildTerminologyMap bool
}
```

Change `GlossaryPreparer` to:

```go
type GlossaryPreparer interface {
	Prepare(ctx context.Context, msg types.JobMessage) (GlossaryPayload, error)
}
```

Change the start of `translateJob` to:

```go
	var glossaryPayload GlossaryPayload
	if w.glossary != nil {
		payload, err := w.glossary.Prepare(ctx, msg)
		if err != nil {
			return "", fmt.Errorf("prepare glossary: %w", err)
		}
		glossaryPayload = payload
	}
```

Change the translator request to:

```go
chsPath, err := w.translator.Translate(ctx, translator.Request{
	Job:                 msg,
	Terminology:         glossaryPayload.Terminology,
	BuildTerminologyMap: glossaryPayload.BuildTerminologyMap,
})
```

- [ ] **Step 4: Update glossary service return values**

In `internal/service/glossary/service.go`, change the signature:

```go
func (s *Service) Prepare(ctx context.Context, msg types.JobMessage) (Payload, error) {
```

Replace all empty returns with `Payload{}, nil` or `Payload{}, err`.

Add this local helper after loading entries:

```go
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
```

Use `payloadFromEntries(entries)` in the extraction-failure and LLM-failure paths instead of `promptFromEntries()`. After reloading entries at the end, return `payloadFromEntries(entries), nil`.

After `service.go` no longer calls `BuildPrompt`, delete the `BuildPrompt` function from `internal/service/glossary/prompt_builder.go` and remove the now-unused `fmt` import. Keep `BuildTerminology`, `isPromptEligible`, `promptDisplayTerm`, and `promptRank`.

- [ ] **Step 5: Bridge glossary payload in main**

In `cmd/fusionn-subs/main.go`, add a wrapper type near `initGlossary`:

```go
type glossaryPreparerAdapter struct {
	service *glossary.Service
}

func (a glossaryPreparerAdapter) Prepare(ctx context.Context, msg types.JobMessage) (worker.GlossaryPayload, error) {
	payload, err := a.service.Prepare(ctx, msg)
	if err != nil {
		return worker.GlossaryPayload{}, err
	}
	terms := make([]translator.Terminology, 0, len(payload.Terminology))
	for _, term := range payload.Terminology {
		terms = append(terms, translator.Terminology{
			Source: term.Source,
			Target: term.Target,
		})
	}
	return worker.GlossaryPayload{
		Terminology:         terms,
		BuildTerminologyMap: payload.BuildTerminologyMap,
	}, nil
}
```

Add `github.com/fusionn-subs/internal/types` to the imports.

In `initGlossary`, assign the service before returning:

```go
svc := glossary.NewService(glossary.ServiceConfig{
	Enabled:                   cfg.Glossary.Enabled,
	TargetLanguage:            cfg.Glossary.TargetLanguage,
	MinConfidence:             cfg.Glossary.MinConfidence,
	InjectMinConfidence:       cfg.Glossary.InjectMinConfidence,
	MaxPromptEntries:          cfg.Glossary.MaxPromptEntries,
	MaxCandidates:             cfg.Glossary.MaxCandidates,
	MaxSnippetsPerCandidate:   cfg.Glossary.MaxSnippetsPerCandidate,
	MaxSubtitleBytes:          cfg.Glossary.MaxSubtitleBytes,
	MaxCues:                   cfg.Glossary.MaxCues,
	MaxActiveVariantsPerTerm:  cfg.Glossary.MaxActiveVariantsPerTerm,
	MaxObservationsPerVariant: cfg.Glossary.MaxObservationsPerVariant,
	PromoteMinConfidence:      cfg.Glossary.PromoteMinConfidence,
	PromoteMinMediaCount:      cfg.Glossary.PromoteMinMediaCount,
	LLMTimeout:                cfg.Glossary.LLM.Timeout,
}, sqlitestore.NewGlossaryStore(db), glossaryLLM)
return glossaryPreparerAdapter{service: svc}, cleanup, nil
```

- [ ] **Step 6: Run worker and glossary tests**

Run: `go test ./internal/service/worker ./internal/service/glossary ./cmd/fusionn-subs`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/service/glossary/service.go internal/service/glossary/prompt_builder.go internal/service/worker/worker.go internal/service/worker/worker_test.go cmd/fusionn-subs/main.go
git commit -m "feat(worker): pass glossary terminology payload"
```

### Task 5: Add OpenAI Translator and Factory Wiring

**Files:**
- Create: `internal/service/translator/openai.go`
- Create: `internal/service/translator/openai_test.go`
- Modify: `internal/service/translator/factory.go`

- [ ] **Step 1: Write failing OpenAI translator tests**

Create `internal/service/translator/openai_test.go`:

```go
package translator

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/fusionn-subs/internal/config"
	"github.com/fusionn-subs/internal/types"
)

func TestOpenAITranslatorBuildArgsIncludesCustomInstance(t *testing.T) {
	t.Setenv("OPENAI_SCRIPT_PATH", "/tmp/gpt-subtrans.sh")
	t.Setenv("LLM_SUBTRANS_DIR", "/tmp/llm-subtrans")
	trans := NewOpenAITranslator(config.OpenAIConfig{
		APIKey:       "openai-key",
		Model:        "gpt-5-mini",
		APIBase:      "https://example.openai.local/v1",
		UseHTTPX:     true,
		Instruction:  "Keep names consistent.",
		RateLimit:    12,
		MaxBatchSize: 18,
		Timeout:      time.Minute,
	}, "Chinese", "chs")

	req := Request{
		Job: types.JobMessage{
			SubtitlePath: "/subs/movie.eng.srt",
			MediaTitle:   "Movie",
		},
		Terminology: []Terminology{
			{Source: "Alice", Target: "爱丽丝"},
		},
		BuildTerminologyMap: true,
	}

	got := trans.buildArgs(req, "/subs/movie.chs.srt")
	want := []string{
		"/subs/movie.eng.srt",
		"-o", "/subs/movie.chs.srt",
		"-l", "Chinese",
		"-k", "openai-key",
		"-m", "gpt-5-mini",
		"-b", "https://example.openai.local/v1",
		"-httpx",
		"--moviename", "Movie",
		"--instruction", "Keep names consistent.",
		"--ratelimit", "12",
		"--maxbatchsize", "18",
		"--terminology", "Alice::爱丽丝",
		"--build-terminology-map",
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestOpenAITranslatorBuildArgsOmitsKeyWhenUsingEnvironmentFallback(t *testing.T) {
	trans := NewOpenAITranslator(config.OpenAIConfig{
		Model: "gpt-5-mini",
	}, "Chinese", "chs")

	req := Request{Job: types.JobMessage{SubtitlePath: "/subs/in.srt"}}
	got := trans.buildArgs(req, "/subs/out.srt")
	want := []string{"/subs/in.srt", "-o", "/subs/out.srt", "-l", "Chinese", "-m", "gpt-5-mini"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestNewTranslatorSupportsOpenAIProvider(t *testing.T) {
	cfg := &config.Config{
		Translator: config.TranslatorConfig{
			Providers:      []string{"openai"},
			TargetLanguage: "Chinese",
			OutputSuffix:   "chs",
		},
		OpenAI: config.OpenAIConfig{
			APIKey: "openai-key",
			Model:  "gpt-5-mini",
		},
	}

	got, err := NewTranslator(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewTranslator() error = %v", err)
	}
	if _, ok := got.(*OpenAITranslator); !ok {
		t.Fatalf("translator = %T, want *OpenAITranslator", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/service/translator`

Expected: FAIL with undefined `NewOpenAITranslator`.

- [ ] **Step 3: Implement OpenAI translator**

Create `internal/service/translator/openai.go`:

```go
package translator

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/fusionn-subs/internal/config"
	"github.com/fusionn-subs/pkg/logger"
)

// OpenAITranslator implements translation using OpenAI via gpt-subtrans.sh.
type OpenAITranslator struct {
	scriptPath     string
	workDir        string
	apiKey         string
	model          string
	apiBase        string
	useHTTPX       bool
	instruction    string
	rateLimit      int
	maxBatchSize   int
	timeout        time.Duration
	targetLanguage string
	outputSuffix   string
}

func NewOpenAITranslator(cfg config.OpenAIConfig, targetLang, outputSuffix string) *OpenAITranslator {
	scriptPath := os.Getenv("OPENAI_SCRIPT_PATH")
	if scriptPath == "" {
		scriptPath = "/opt/llm-subtrans/gpt-subtrans.sh"
	}
	workDir := os.Getenv("LLM_SUBTRANS_DIR")
	if workDir == "" {
		workDir = "/opt/llm-subtrans"
	}
	rateLimit := cfg.RateLimit
	if rateLimit == 0 {
		rateLimit = 10
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = config.DefaultOpenAITimeout
	}
	return &OpenAITranslator{
		scriptPath:     scriptPath,
		workDir:        workDir,
		apiKey:         cfg.APIKey,
		model:          cfg.Model,
		apiBase:        cfg.APIBase,
		useHTTPX:       cfg.UseHTTPX,
		instruction:    cfg.Instruction,
		rateLimit:      rateLimit,
		maxBatchSize:   cfg.MaxBatchSize,
		timeout:        timeout,
		targetLanguage: targetLang,
		outputSuffix:   outputSuffix,
	}
}

func (t *OpenAITranslator) Translate(ctx context.Context, req Request) (string, error) {
	msg := req.Job
	if err := msg.Validate(); err != nil {
		return "", fmt.Errorf("invalid message: %w", err)
	}

	outputPath := msg.OutputPath(t.outputSuffix)
	ctxTimeout, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	args := t.buildArgs(req, outputPath)
	cmd := exec.CommandContext(ctxTimeout, t.scriptPath, args...)
	if t.workDir != "" {
		cmd.Dir = t.workDir
	}

	env := append(os.Environ(), "PYTHONUNBUFFERED=1")
	if strings.TrimSpace(t.apiKey) != "" {
		env = append(env, "OPENAI_API_KEY="+t.apiKey)
	}
	cmd.Env = env

	logger.Infof("🔄 Starting translation (OpenAI/%s): %s → %s", t.model, msg.SubtitlePath, outputPath)
	logger.Debugf("Command: %s", maskAPIKeyInCommand(buildCommandLine(t.scriptPath, args)))

	resultPath, _, err := executeScript(cmd, outputPath)
	if err != nil {
		os.Remove(outputPath)
		return "", err
	}
	return resultPath, nil
}

func (t *OpenAITranslator) buildArgs(req Request, outputPath string) []string {
	msg := req.Job
	args := []string{
		msg.SubtitlePath,
		"-o", outputPath,
		"-l", t.targetLanguage,
	}
	if strings.TrimSpace(t.apiKey) != "" {
		args = append(args, "-k", t.apiKey)
	}
	if strings.TrimSpace(t.model) != "" {
		args = append(args, "-m", t.model)
	}
	if strings.TrimSpace(t.apiBase) != "" {
		args = append(args, "-b", t.apiBase)
		if t.useHTTPX {
			args = append(args, "-httpx")
		}
	}
	if mediaTitle := strings.TrimSpace(msg.MediaTitle); mediaTitle != "" {
		args = append(args, "--moviename", mediaTitle)
	}
	if instruction := combineInstructions(t.instruction, req.ExtraInstruction); instruction != "" {
		args = append(args, "--instruction", instruction)
	}
	if t.rateLimit > 0 {
		args = append(args, "--ratelimit", strconv.Itoa(t.rateLimit))
	}
	if t.maxBatchSize > 0 {
		args = append(args, "--maxbatchsize", strconv.Itoa(t.maxBatchSize))
	}
	return appendTerminologyArgs(args, req)
}
```

- [ ] **Step 4: Wire factory**

In `internal/service/translator/factory.go`, add to the provider switch:

```go
case config.ProviderOpenAI:
	t = NewOpenAITranslator(cfg.OpenAI, targetLang, outputSuffix)
```

Update the final error to:

```go
return nil, fmt.Errorf("no translator configured: set translator.providers or configure gemini/openrouter/openai credentials")
```

- [ ] **Step 5: Run translator tests**

Run: `go test ./internal/service/translator`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/service/translator/openai.go internal/service/translator/openai_test.go internal/service/translator/factory.go
git commit -m "feat(translator): add openai provider"
```

### Task 6: Append Terminology Flags in Existing Translators

**Files:**
- Modify: `internal/service/translator/openrouter.go`
- Modify: `internal/service/translator/gemini.go`
- Modify: `internal/service/translator/local_llm.go`
- Modify: `internal/service/translator/translator_test.go`

- [ ] **Step 1: Write failing existing-provider terminology tests**

Add these focused tests in `internal/service/translator/translator_test.go` after the terminology helper tests:

Add these imports if they are not already present:

```go
	"time"

	"github.com/fusionn-subs/internal/config"
```

```go
func TestOpenRouterBuildArgsIncludesTerminology(t *testing.T) {
	trans := NewOpenRouterTranslator(config.OpenRouterConfig{
		APIKey:       "or-key",
		Model:        "openai/gpt-4o-mini",
		MaxBatchSize: 20,
		RateLimit:    10,
	}, "Chinese", "chs")
	req := Request{
		Job: types.JobMessage{SubtitlePath: "/subs/in.srt"},
		Terminology: []Terminology{{Source: "SO15", Target: "SO15"}},
		BuildTerminologyMap: true,
	}

	got := trans.buildArgs(req, "/subs/out.srt", "openai/gpt-4o-mini")
	if !reflect.DeepEqual(got[len(got)-3:], []string{"--terminology", "SO15::SO15", "--build-terminology-map"}) {
		t.Fatalf("args = %#v", got)
	}
}

func TestGeminiBuildArgsIncludesTerminology(t *testing.T) {
	trans := &GeminiTranslator{
		apiKey:         "gemini-key",
		targetLanguage: "Chinese",
		outputSuffix:   "chs",
		instruction:    "",
	}
	model := config.GeminiModelConfig{Name: "gemini-2.5-flash", RateLimit: 8, MaxBatchSize: 20}
	req := Request{
		Job: types.JobMessage{SubtitlePath: "/subs/in.srt"},
		Terminology: []Terminology{{Source: "SO15", Target: "SO15"}},
		BuildTerminologyMap: true,
	}

	got := trans.buildArgs(req, "/subs/out.srt", model)
	if !reflect.DeepEqual(got[len(got)-3:], []string{"--terminology", "SO15::SO15", "--build-terminology-map"}) {
		t.Fatalf("args = %#v", got)
	}
}

func TestLocalLLMBuildArgsIncludesTerminology(t *testing.T) {
	snapshot := localLLMSnapshot{
		baseURL:        "http://127.0.0.1:8045",
		apiKey:         "",
		model:          "qwen3:8b",
		endpoint:       "/v1/chat/completions",
		instruction:    "",
		rateLimit:      10,
		maxBatchSize:   20,
		timeout:        time.Minute,
		targetLanguage: "Chinese",
	}
	req := Request{
		Job: types.JobMessage{SubtitlePath: "/subs/in.srt"},
		Terminology: []Terminology{{Source: "SO15", Target: "SO15"}},
		BuildTerminologyMap: true,
	}

	got := buildLocalLLMArgs(req, "/subs/out.srt", snapshot)
	if !reflect.DeepEqual(got[len(got)-3:], []string{"--terminology", "SO15::SO15", "--build-terminology-map"}) {
		t.Fatalf("args = %#v", got)
	}
}
```

- [ ] **Step 2: Run translator tests**

Run: `go test ./internal/service/translator`

Expected: FAIL with undefined `buildArgs`, `localLLMSnapshot`, or `buildLocalLLMArgs`.

- [ ] **Step 3: Extract minimal arg builders and append terminology args**

In `openrouter.go`, extract the inline argument construction into:

```go
func (t *OpenRouterTranslator) buildArgs(req Request, outputPath string, currentModel string) []string {
	msg := req.Job
	args := []string{
		msg.SubtitlePath,
		"-o", outputPath,
		"-l", t.targetLanguage,
		"--apikey", t.apiKey,
		"--model", currentModel,
	}
	if mediaTitle := strings.TrimSpace(msg.MediaTitle); mediaTitle != "" {
		args = append(args, "--moviename", mediaTitle)
	}
	if instruction := combineInstructions(t.instruction, req.ExtraInstruction); instruction != "" {
		args = append(args, "--instruction", instruction)
	}
	if t.rateLimit > 0 {
		args = append(args, "--ratelimit", strconv.Itoa(t.rateLimit))
	}
	if t.maxBatchSize > 0 {
		args = append(args, "--maxbatchsize", strconv.Itoa(t.maxBatchSize))
	}
	return appendTerminologyArgs(args, req)
}
```

Then replace the inline args block in `Translate` with:

```go
args := t.buildArgs(req, outputPath, currentModel)
```

In `gemini.go`, extract the inline argument construction into:

```go
func (t *GeminiTranslator) buildArgs(req Request, outputPath string, model config.GeminiModelConfig) []string {
	msg := req.Job
	args := []string{
		msg.SubtitlePath,
		"-o", outputPath,
		"-l", t.targetLanguage,
		"-k", t.apiKey,
	}
	if model.Name != "" {
		args = append(args, "-m", model.Name)
	}
	if mediaTitle := strings.TrimSpace(msg.MediaTitle); mediaTitle != "" {
		args = append(args, "--moviename", mediaTitle)
	}
	if instruction := combineInstructions(t.instruction, req.ExtraInstruction); instruction != "" {
		args = append(args, "--instruction", instruction)
	}
	if model.RateLimit > 0 {
		args = append(args, "--ratelimit", strconv.Itoa(model.RateLimit))
	}
	if model.MaxBatchSize > 0 {
		args = append(args, "--maxbatchsize", strconv.Itoa(model.MaxBatchSize))
	}
	return appendTerminologyArgs(args, req)
}
```

Then replace the inline args block in `Translate` with:

```go
args := t.buildArgs(req, outputPath, model)
```

In `local_llm.go`, add a snapshot type near `LocalLLMTranslator`:

```go
type localLLMSnapshot struct {
	baseURL        string
	apiKey         string
	model          string
	endpoint       string
	instruction    string
	rateLimit      int
	maxBatchSize   int
	timeout        time.Duration
	targetLanguage string
}
```

Add a pure arg builder:

```go
func buildLocalLLMArgs(req Request, outputPath string, snapshot localLLMSnapshot) []string {
	msg := req.Job
	args := []string{
		msg.SubtitlePath,
		"-o", outputPath,
		"-l", snapshot.targetLanguage,
		"-s", snapshot.baseURL,
		"-e", snapshot.endpoint,
	}
	if strings.TrimSpace(snapshot.apiKey) != "" {
		args = append(args, "-k", snapshot.apiKey)
	}
	if strings.TrimSpace(snapshot.model) != "" {
		args = append(args, "-m", snapshot.model)
	}
	if strings.Contains(strings.ToLower(snapshot.endpoint), "chat") {
		args = append(args, "--chat", "--systemmessages")
	}
	if mediaTitle := strings.TrimSpace(msg.MediaTitle); mediaTitle != "" {
		args = append(args, "--moviename", mediaTitle)
	}
	if instruction := combineInstructions(snapshot.instruction, req.ExtraInstruction); instruction != "" {
		args = append(args, "--instruction", instruction)
	}
	if snapshot.rateLimit > 0 {
		args = append(args, "--ratelimit", strconv.Itoa(snapshot.rateLimit))
	}
	if snapshot.maxBatchSize > 0 {
		args = append(args, "--maxbatchsize", strconv.Itoa(snapshot.maxBatchSize))
	}
	return appendTerminologyArgs(args, req)
}
```

In `LocalLLMTranslator.Translate`, replace the inline args block with:

```go
snapshot := localLLMSnapshot{
	baseURL:        baseURL,
	apiKey:         apiKey,
	model:          model,
	endpoint:       endpoint,
	instruction:    instruction,
	rateLimit:      rateLimit,
	maxBatchSize:   maxBatchSize,
	timeout:        timeout,
	targetLanguage: targetLanguage,
}
args := buildLocalLLMArgs(req, outputPath, snapshot)
```

- [ ] **Step 4: Run translator tests**

Run: `go test ./internal/service/translator`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/service/translator/openrouter.go internal/service/translator/gemini.go internal/service/translator/local_llm.go internal/service/translator/translator_test.go
git commit -m "feat(translator): pass terminology to llm-subtrans"
```

### Task 7: Update Docker Runtime

**Files:**
- Modify: `Dockerfile`

- [ ] **Step 1: Update Docker env and install input**

Change the ENV block to include OpenAI:

```dockerfile
ENV LLM_SUBTRANS_DIR=/opt/llm-subtrans \
    LLM_SUBTRANS_SCRIPT_PATH=/opt/llm-subtrans/llm-subtrans.sh \
    OPENAI_SCRIPT_PATH=/opt/llm-subtrans/gpt-subtrans.sh \
    GEMINI_SCRIPT_PATH=/opt/llm-subtrans/gemini-subtrans.sh \
    GEMINI_WORKDIR=/opt/llm-subtrans
```

Change the install input to command-line only plus "all except Bedrock":

```dockerfile
RUN set -e; printf "2\n\na\n\n\n\n\n\n" | ./install.sh
```

This answers:

1. `2` for command-line tools only.
2. empty OpenRouter key.
3. `a` for all non-Bedrock additional providers.
4. empty API keys for Gemini, OpenAI, Claude, DeepSeek, and Mistral prompts.

- [ ] **Step 2: Verify Dockerfile syntax locally**

Run: `docker build --target builder -t fusionn-subs-builder-test .`

Expected: PASS through the Go builder stage.

Run if Docker is available and time allows: `docker build -t fusionn-subs-runtime-test .`

Expected: PASS and generated scripts include `/opt/llm-subtrans/gpt-subtrans.sh`.

- [ ] **Step 3: Commit**

```bash
git add Dockerfile
git commit -m "build: install openai llm-subtrans script"
```

### Task 8: Update Config Example and README

**Files:**
- Modify: `config/config.example.yaml`
- Modify: `README.md`

- [ ] **Step 1: Update config example**

Add an OpenAI section after OpenRouter in `config/config.example.yaml`:

```yaml
# ─────────────────────────────────────────────────────────────────────────────
# OPENAI - Official OpenAI translation provider
# ─────────────────────────────────────────────────────────────────────────────
# Uses llm-subtrans OpenAI mode via gpt-subtrans.sh.
# Only required when "openai" appears in translator.providers.
openai:
  api_key: ""                         # OpenAI API key (or use OPENAI_API_KEY)
  model: "gpt-5-mini"                 # OpenAI model for translation
  api_base: ""                        # Optional custom OpenAI-compatible API base
  use_httpx: false                    # Only valid when api_base is set
  instruction: ""                     # Custom translation instruction (optional)
  rate_limit: 10                      # Requests per minute (default: 10)
  max_batch_size: 20                  # Max subtitles per batch
  timeout: 30m                        # Script timeout (default: 30m)
```

Update the provider comment:

```yaml
# Valid: "gemini", "openrouter", "openai", "local_llm". Tries in order; falls back on failure.
```

- [ ] **Step 2: Update README provider docs**

In `README.md`, update the provider list to include OpenAI:

```markdown
- **OpenAI**: Direct OpenAI API access through llm-subtrans `gpt-subtrans.sh`; supports `api_base` for custom OpenAI instances.
```

Add an OpenAI config snippet after the OpenRouter snippet:

```yaml
translator:
  providers: ["openai", "gemini"]
  target_language: "Chinese"
  output_suffix: "chs"

openai:
  api_key: ""                          # Or set OPENAI_API_KEY
  model: "gpt-5-mini"
  api_base: ""                         # Optional custom instance
  use_httpx: false                     # Requires api_base
  instruction: ""
  max_batch_size: 20
  rate_limit: 10
```

Update the glossary section text to:

```markdown
When `glossary.enabled` is true, fusionn-subs scans each subtitle locally, extracts terminology candidates, asks the configured glossary LLM for Chinese-oriented glossary entries, stores them in SQLite, and passes selected entries to llm-subtrans as repeatable `--terminology SOURCE::TRANSLATION` arguments. It also enables llm-subtrans's `--build-terminology-map` for that job so terms remain consistent across batches.
```

- [ ] **Step 3: Commit**

```bash
git add config/config.example.yaml README.md
git commit -m "docs: document openai provider terminology"
```

### Task 9: Final Verification

**Files:**
- All modified files

- [ ] **Step 1: Run full Go test suite**

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 2: Check formatting**

Run: `gofmt -w internal/config/config.go internal/config/config_test.go internal/service/translator/translator.go internal/service/translator/translator_test.go internal/service/translator/openai.go internal/service/translator/openai_test.go internal/service/translator/openrouter.go internal/service/translator/gemini.go internal/service/translator/local_llm.go internal/service/translator/factory.go internal/service/worker/worker.go internal/service/worker/worker_test.go internal/service/glossary/types.go internal/service/glossary/prompt_builder.go internal/service/glossary/prompt_builder_test.go internal/service/glossary/service.go cmd/fusionn-subs/main.go`

Expected: command exits 0.

- [ ] **Step 3: Re-run full Go test suite after formatting**

Run: `go test ./...`

Expected: PASS.

- [ ] **Step 4: Inspect final diff**

Run: `git diff --stat HEAD`

Expected: only files listed in this plan changed since the previous task commits.

- [ ] **Step 5: Commit final formatting fixes if needed**

If Step 4 shows formatting-only changes:

```bash
git add .
git commit -m "chore: format openai terminology integration"
```

If Step 4 shows no changes, do not create an empty commit.
