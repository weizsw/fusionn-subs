# Implementation Plan: Local LLM Provider + Provider Selection

**Design spec:** `docs/superpowers/specs/2026-03-31-local-llm-provider-design.md`

## Task Order

Tasks are ordered by dependency. Each task is self-contained and results in compilable code.

---

### Task 1: Add `LocalLLMConfig` and `Providers` to config

**File:** `internal/config/config.go`

**Changes:**

1. Add `LocalLLMConfig` struct after `OpenRouterConfig`:
   ```go
   type LocalLLMConfig struct {
       BaseURL      string `mapstructure:"base_url"`
       APIKey       string `mapstructure:"api_key"`
       Model        string `mapstructure:"model"`
       Endpoint     string `mapstructure:"endpoint"`
       Instruction  string `mapstructure:"instruction"`
       RateLimit    int    `mapstructure:"rate_limit"`
       MaxBatchSize int    `mapstructure:"max_batch_size"`
   }
   ```

2. Add `LocalLLM LocalLLMConfig` field to `Config` struct (after `OpenRouter`):
   ```go
   LocalLLM   LocalLLMConfig   `mapstructure:"local_llm"`
   ```

3. Add `Providers []string` to `TranslatorConfig` (first field):
   ```go
   Providers []string `mapstructure:"providers"`
   ```

4. Add a package-level set of valid provider names for validation:
   ```go
   var validProviders = map[string]bool{
       "gemini":    true,
       "openrouter": true,
       "local_llm": true,
   }
   ```

5. Rewrite `Validate()`:
   - First validate redis, callback (unchanged).
   - If `c.Translator.Providers` is non-empty:
     - Check for unknown names, empty strings, duplicates → error.
     - For each provider in the list, validate its config section:
       - `"gemini"`: require api_key, primary_model.name, secondary_model.name, names different.
       - `"openrouter"`: require api_key; require model unless auto_select_model; existing auto_select_model validation.
       - `"local_llm"`: require base_url.
     - Skip the old monolithic Gemini validation.
   - If `c.Translator.Providers` is empty: keep current validation (Gemini required, OpenRouter optional). This is the backwards-compatible path.

6. Update `SafeLogValues()` — add these entries:
   ```go
   "translator.providers":       c.Translator.Providers,
   "local_llm.base_url":         c.LocalLLM.BaseURL,
   "local_llm.api_key":          util.MaskSecret(c.LocalLLM.APIKey),
   "local_llm.model":            c.LocalLLM.Model,
   "local_llm.endpoint":         c.LocalLLM.Endpoint,
   "local_llm.instruction":      c.LocalLLM.Instruction,
   "local_llm.rate_limit":       c.LocalLLM.RateLimit,
   "local_llm.max_batch_size":   c.LocalLLM.MaxBatchSize,
   ```

**Verification:** `go build ./...` compiles. Existing config.yaml files without `providers` still pass validation.

---

### Task 2: Generalize `ConfigUpdater` interface

**File:** `internal/service/translator/factory.go`

**Changes:**

1. Replace the current `ConfigUpdater` interface:
   ```go
   // Before:
   type ConfigUpdater interface {
       UpdateConfig(cfg config.GeminiConfig)
   }

   // After:
   type ConfigUpdater interface {
       UpdateFromConfig(cfg *config.Config)
   }
   ```

**File:** `internal/service/translator/gemini.go`

2. Rename `UpdateConfig` → `UpdateFromConfig` and change signature:
   ```go
   // Before:
   func (t *GeminiTranslator) UpdateConfig(cfg config.GeminiConfig) {

   // After:
   func (t *GeminiTranslator) UpdateFromConfig(cfg *config.Config) {
       geminiCfg := cfg.Gemini
       // ... same body but using geminiCfg instead of cfg ...
   }
   ```

**File:** `cmd/fusionn-subs/main.go`

3. Update the `OnChange` callback:
   ```go
   // Before:
   if updater, ok := translatorSvc.(translator.ConfigUpdater); ok {
       cfgMgr.OnChange(func(old, new *config.Config) {
           updater.UpdateConfig(new.Gemini)
       })
   }

   // After:
   if updater, ok := translatorSvc.(translator.ConfigUpdater); ok {
       cfgMgr.OnChange(func(old, new *config.Config) {
           updater.UpdateFromConfig(new)
       })
   }
   ```

**Verification:** `go build ./...` compiles. Existing Gemini hot-reload still works.

---

### Task 3: Create `LocalLLMTranslator`

**New file:** `internal/service/translator/local_llm.go`

**Implementation:**

```go
package translator

import (
    "context"
    "fmt"
    "os"
    "os/exec"
    "strconv"
    "strings"
    "sync"

    "github.com/fusionn-subs/internal/config"
    "github.com/fusionn-subs/internal/types"
    "github.com/fusionn-subs/pkg/logger"
)

type LocalLLMTranslator struct {
    scriptPath     string
    workDir        string
    mu             sync.RWMutex
    baseURL        string
    apiKey         string
    model          string
    endpoint       string
    instruction    string
    rateLimit      int
    maxBatchSize   int
    targetLanguage string
    outputSuffix   string
}
```

1. `NewLocalLLMTranslator(cfg config.LocalLLMConfig, targetLang, outputSuffix string) *LocalLLMTranslator`:
   - Reuse `LLM_SUBTRANS_SCRIPT_PATH` / `LLM_SUBTRANS_DIR` env vars (same as OpenRouter).
   - Default `endpoint` to `/v1/chat/completions` if empty.
   - Default `rateLimit` to `10` if zero.

2. `Translate(ctx context.Context, msg types.JobMessage) (string, error)`:
   - `msg.Validate()`, compute `outputPath`.
   - Build args: `<subtitle_path> -o <output> -l <lang> -s <baseURL> -e <endpoint>`
   - If `apiKey` non-empty: add `-k <apiKey>`.
   - If `model` non-empty: add `-m <model>`.
   - If endpoint contains "chat": add `--chat --systemmessages`.
   - Add `--moviename`, `--instruction`, `--ratelimit`, `--maxbatchsize` when set (same pattern as OpenRouter).
   - `exec.CommandContext` with `DefaultGeminiTimeout`.
   - `cmd.Env` = `os.Environ()` + `PYTHONUNBUFFERED=1`.
   - Log with `maskAPIKeyInCommand`.
   - Call `executeScript(cmd, outputPath)`.
   - On error: `os.Remove(outputPath)`, then return error.
   - On success: return resultPath.

3. `UpdateFromConfig(cfg *config.Config)`:
   - Lock mutex.
   - Update `baseURL`, `apiKey`, `model`, `endpoint`, `instruction`, `rateLimit`, `maxBatchSize` from `cfg.LocalLLM`.
   - Default endpoint if empty.
   - Log reload.

**Verification:** `go build ./...` compiles.

---

### Task 4: Add `FallbackTranslator` and update factory

**File:** `internal/service/translator/factory.go`

**Changes:**

1. Add `FallbackTranslator` types:
   ```go
   type namedTranslator struct {
       name       string
       translator Translator
   }

   type FallbackTranslator struct {
       translators []namedTranslator
   }
   ```

2. `FallbackTranslator.Translate(ctx, msg)`:
   - Iterate `translators` in order.
   - Call each `translator.Translate(ctx, msg)`.
   - On success → return immediately.
   - On `ErrRateLimited` → return error directly (do NOT fall through). This preserves Gemini's internal primary→secondary model switch.
   - On `ErrAllModelsExhausted` → log warning, try next.
   - On other errors → log warning, try next.
   - If all fail → return `fmt.Errorf("all providers failed, last error: %w", lastErr)`.

3. `FallbackTranslator.UpdateFromConfig(cfg *config.Config)`:
   - Iterate child translators.
   - Type-assert each to `ConfigUpdater`.
   - Call `UpdateFromConfig(cfg)` on matches.

4. Rewrite `NewTranslator(ctx, cfg)`:
   - If `cfg.Translator.Providers` is non-empty:
     - `buildTranslatorForProvider(ctx, providerName, cfg)` helper that switches on name:
       - `"gemini"` → `NewGeminiTranslator(ctx, cfg.Gemini, targetLang, outputSuffix)`
       - `"openrouter"` → `NewOpenRouterTranslator(cfg.OpenRouter, targetLang, outputSuffix)`
       - `"local_llm"` → `NewLocalLLMTranslator(cfg.LocalLLM, targetLang, outputSuffix)`
     - Collect into `[]namedTranslator`.
     - If len == 1: return the single translator directly.
     - If len > 1: return `&FallbackTranslator{translators: list}`.
   - If `cfg.Translator.Providers` is empty: keep legacy logic (current code).
   - Log which providers are active.

**Verification:** `go build ./...` compiles. Single provider returns unwrapped. Multiple providers wrap in FallbackTranslator.

---

### Task 5: Update `config.example.yaml`

**File:** `config/config.example.yaml`

**Changes:**

1. Update the header comment to mention that providers can now be explicitly selected.

2. Add `local_llm` section (after `gemini`, before `translator`):
   ```yaml
   # ─────────────────────────────────────────────────────────────────────────────
   # LOCAL LLM - OpenAI-compatible local server (e.g., LM Studio, Ollama, vLLM)
   # ─────────────────────────────────────────────────────────────────────────────
   # Uses llm-subtrans Custom Server mode.
   # Only required when "local_llm" appears in translator.providers.
   local_llm:
     base_url: "http://127.0.0.1:8045"         # REQUIRED - Server address
     api_key: ""                                 # API key (optional for local servers)
     model: "gemini-3-flash"                     # Model name to request
     endpoint: "/v1/chat/completions"            # API endpoint (default: /v1/chat/completions)
     instruction: ""                             # Custom translation instruction (optional)
     rate_limit: 10                              # Requests per minute (default: 10)
     max_batch_size: 20                          # Max subtitles per batch (default: 20)
   ```

3. Add `providers` field to `translator` section:
   ```yaml
   translator:
     # providers: ["local_llm", "gemini"]  # Ordered fallback chain (optional)
     # Valid: "gemini", "openrouter", "local_llm". Tries in order; falls back on failure.
     # If omitted, uses legacy behavior: Gemini (required), OpenRouter (optional).
     target_language: "Chinese"
     output_suffix: "chs"
     max_translation_retries: 3
   ```

**Verification:** Example config is valid YAML. Comments explain the feature clearly.

---

### Task 6: Update worker error message for exhausted models

**File:** `internal/service/worker/worker.go`

**Change:** Line 155-156 currently says "All Gemini models exhausted" which is now inaccurate when using FallbackTranslator. Update to be provider-agnostic:

```go
// Before:
logger.Errorf("❌ All Gemini models exhausted for today: job_id=%s", msg.JobID)
return fmt.Errorf("all Gemini models exhausted for today: %w", lastErr)

// After:
logger.Errorf("❌ All models exhausted: job_id=%s", msg.JobID)
return fmt.Errorf("all models exhausted: %w", lastErr)
```

**Verification:** `go build ./...` compiles. Message is accurate for all provider combinations.

---

## Dependency Graph

```
Task 1 (config types + validation)
  ├── Task 2 (ConfigUpdater interface) ── depends on Task 1 for LocalLLMConfig type
  │     └── Task 3 (LocalLLMTranslator) ── depends on Task 1 + 2
  │           └── Task 4 (FallbackTranslator + factory) ── depends on Task 1 + 2 + 3
  ├── Task 5 (config.example.yaml) ── independent, can run in parallel after Task 1
  └── Task 6 (worker error msg) ── independent, can run anytime
```

**Parallelizable:** Tasks 5 and 6 can run in parallel with anything. Tasks 1→2→3→4 are sequential.

## Checklist

- [ ] Task 1: Config types + validation
- [ ] Task 2: Generalize ConfigUpdater
- [ ] Task 3: LocalLLMTranslator
- [ ] Task 4: FallbackTranslator + factory
- [ ] Task 5: config.example.yaml
- [ ] Task 6: Worker error message
- [ ] Final: `go build ./...` passes
- [ ] Final: `golangci-lint run` passes
