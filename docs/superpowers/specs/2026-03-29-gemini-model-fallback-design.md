# Gemini Primary/Secondary Model Fallback

**Date:** 2026-03-29
**Status:** Approved

## Problem

The service currently has two translation provider paths (OpenRouter and Gemini) with an optional model auto-selection system that evaluates free OpenRouter models at startup. This complexity is no longer needed. We want to simplify to Gemini-only with a primary/secondary model fallback based on RPD (Requests Per Day) limits.

## Goals

1. Remove the model selection logic from service startup
2. Use Gemini as the sole active translation provider (keep OpenRouter code dormant)
3. Support a primary and secondary Gemini model, each with independent `rate_limit` and `max_batch_size`
4. When the primary model hits its RPD limit (429 error from `gemini-subtrans` script), switch to the secondary model and restart the job
5. Sticky switch: once primary is exhausted, stay on secondary for the rest of the day
6. Reset to primary at midnight Pacific time (aligned with Gemini's quota reset)
7. If both models are exhausted, fail the job immediately

## Constraints

- **Single consumer:** This design assumes exactly one service instance consuming from the Redis queue. The sticky model state lives in-process memory. If multiple replicas are needed in the future, the exhausted state should move to a shared store (e.g. a Redis key with TTL until next Pacific midnight).
- **Config changes require restart.** The hot-reload polling remains for other config values, but changing `primary_model` or `secondary_model` names mid-flight while the sticky state is active could cause confusion. Treat model config changes as requiring a restart.

## Non-Goals

- Removing OpenRouter code (kept dormant for potential future use)
- Tracking request counts ourselves (we react to 429s from the script)
- Supporting more than two models
- Multi-replica shared state for model exhaustion (single consumer assumption)

## Design

### Approach: Model-Aware GeminiTranslator with Built-in Fallback

The `GeminiTranslator` manages two models internally. On 429 detection, it switches the active model and returns a retryable sentinel error. The worker's existing retry loop re-runs the job, which now uses the secondary model. This keeps model switching encapsulated in the translator with minimal worker changes.

### 1. Removals

**From `main.go`:**
- The entire `modelselection` block (~lines 71-115): `Selector`, `Start()`, `OnModelUpdate` callback, `defer modelSelector.Stop()`
- The `modelselection` import

**Kept dormant (files stay, unreachable):**
- `internal/service/modelselection/` — selector, evaluator, prompts
- `internal/service/translator/openrouter.go`
- `internal/client/openrouter/`
- OpenRouter config fields in `config.go`

### 2. Config Changes

**New Gemini config structure:**

```yaml
gemini:
  api_key: ""
  instruction: ""
  primary_model:
    name: "gemini-2.5-flash"
    rate_limit: 8
    max_batch_size: 20
  secondary_model:
    name: "gemini-2.5-pro"
    rate_limit: 5
    max_batch_size: 15
```

**Go structs:**

```go
type GeminiConfig struct {
    APIKey         string            `mapstructure:"api_key"`
    Instruction    string            `mapstructure:"instruction"`
    PrimaryModel   GeminiModelConfig `mapstructure:"primary_model"`
    SecondaryModel GeminiModelConfig `mapstructure:"secondary_model"`
}

type GeminiModelConfig struct {
    Name         string `mapstructure:"name"`
    RateLimit    int    `mapstructure:"rate_limit"`
    MaxBatchSize int    `mapstructure:"max_batch_size"`
}
```

**Validation:**
- `gemini.api_key` required
- `gemini.primary_model.name` required
- `gemini.secondary_model.name` required
- `primary_model.name != secondary_model.name` (distinct models required)
- OpenRouter validation becomes optional (only if `openrouter.api_key` is set)

### 3. GeminiTranslator Redesign

**Structure:**

```go
type GeminiTranslator struct {
    scriptPath     string
    workDir        string
    apiKey         string
    instruction    string
    targetLanguage string
    outputSuffix   string

    mu               sync.RWMutex
    primaryModel     GeminiModelConfig
    secondaryModel   GeminiModelConfig
    activeModel      *GeminiModelConfig
    primaryExhausted bool
}
```

**`Translate()` flow:**

1. Copy `activeModel` config under read lock (do NOT hold lock across exec)
2. Build script args using active model's `name`, `rate_limit`, `max_batch_size`
3. Run `gemini-subtrans.sh` via `executeScript`
4. On success: return output path
5. On error: check captured output for 429 signals
6. On any failure: remove `outputPath` if it exists (partial artifact cleanup)
7. If 429 and on primary: take write lock, set `primaryExhausted = true`, switch `activeModel` to secondary, return `ErrRateLimited`
8. If 429 and on secondary: return `ErrAllModelsExhausted`
9. If non-429 error: return as-is (existing retry handles transient failures)

**Concurrency invariant:** All mutations to `primaryExhausted` and `activeModel` happen under write lock. `ResetToPrimary()` and the 429 transition both take the same write lock. `Translate()` only holds a read lock to copy the model config snapshot, never across the script execution.

**`ResetToPrimary()`:** Takes write lock, sets `primaryExhausted = false`, points `activeModel` back to primary.

### 4. Error Detection

**`executeScript` change:** Return captured stdout+stderr alongside the error so callers can inspect output.

Updated signature with named returns:
```go
func executeScript(cmd *exec.Cmd, outputPath string) (resultPath string, combinedOutput string, err error)
```

When `err != nil`, `resultPath` is empty. `combinedOutput` is always populated (stdout+stderr) regardless of success/failure, so callers can inspect script output.

**429 detection function:**

The detection prioritizes Gemini-specific error signals over generic strings to minimize false positives (e.g. `"429"` could appear in unrelated log lines). The sticky-until-midnight nature of the switch makes false positives costly.

```go
func isRateLimitError(output string) bool {
    lower := strings.ToLower(output)

    // Gemini-specific signals (high confidence)
    geminiIndicators := []string{
        "resource_exhausted",
        "quota exceeded",
    }
    for _, ind := range geminiIndicators {
        if strings.Contains(lower, ind) {
            return true
        }
    }

    // Generic HTTP signals (moderate confidence, but useful as fallback)
    genericIndicators := []string{
        "429 too many requests",
        "429 resource exhausted",
        "rate limit exceeded",
    }
    for _, ind := range genericIndicators {
        if strings.Contains(lower, ind) {
            return true
        }
    }
    return false
}
```

> **Note:** If the `gemini-subtrans` script is ever modified to emit a dedicated marker line (e.g. `RATE_LIMIT_EXCEEDED`), detection should prefer that over substring matching.

**Sentinel errors:**

```go
var (
    ErrRateLimited        = errors.New("model rate limited")
    ErrAllModelsExhausted = errors.New("all models exhausted")
)
```

### 5. Daily Reset

A goroutine inside `GeminiTranslator` sleeps until midnight Pacific, then calls `ResetToPrimary()`:

```go
func (t *GeminiTranslator) startDailyReset(ctx context.Context) {
    go func() {
        for {
            now := time.Now().In(pacificTZ)
            nextMidnight := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, pacificTZ)
            timer := time.NewTimer(time.Until(nextMidnight))
            select {
            case <-ctx.Done():
                timer.Stop()
                return
            case <-timer.C:
                t.ResetToPrimary()
            }
        }
    }()
}
```

Pacific timezone loaded at init time with `time.LoadLocation("America/Los_Angeles")`. If loading fails (missing tzdata in container), the service should fail at startup rather than silently using a wrong offset. The Docker image must include tzdata.

Started from `NewGeminiTranslator`, which now takes a `context.Context` parameter.

> **Operational note:** Gemini's actual quota reset may not align exactly with midnight Pacific in all cases. This is a best-effort alignment. If Google changes their reset window, adjust the reset time accordingly.

### 6. Main.go Changes

**Before (simplified):**
```
load config → redis → NewTranslator(cfg) → modelselection.Start() → wire model updates → worker.Run()
```

**After:**
```
load config → redis → NewTranslator(ctx, cfg) → worker.Run()
```

The `NewTranslator` factory gains a `ctx` parameter:

```go
func NewTranslator(ctx context.Context, cfg *config.Config) (Translator, error)
```

Gemini path is checked first. OpenRouter path remains as dormant fallback.

### 7. Worker Change

Two additions to `processJob` retry loop:

1. Bail immediately if all models exhausted:
```go
if errors.Is(lastErr, translator.ErrAllModelsExhausted) {
    break // no point retrying
}
```

2. Skip the 2s backoff delay when the error is `ErrRateLimited` — the model has already been switched, so retry immediately on the secondary:
```go
if attempt < maxRetries && !errors.Is(err, translator.ErrRateLimited) {
    select {
    case <-time.After(2 * time.Second):
    case <-ctx.Done():
        return ctx.Err()
    }
}
```

3. Use a specific error message when all models are exhausted (not the generic "translation failed after N attempts"):
```go
if errors.Is(lastErr, translator.ErrAllModelsExhausted) {
    return fmt.Errorf("all Gemini models exhausted for today: %w", lastErr)
}
```

## Files Changed

| File | Change |
|------|--------|
| `internal/config/config.go` | New `GeminiModelConfig` struct, update `GeminiConfig`, update validation |
| `config/config.example.yaml` | New Gemini config shape with primary/secondary models |
| `internal/service/translator/gemini.go` | Full rewrite: dual-model, sticky switch, daily reset, 429 detection |
| `internal/service/translator/translator.go` | `executeScript` returns captured output |
| `internal/service/translator/factory.go` | Add `ctx` param, Gemini-first logic |
| `internal/service/translator/errors.go` | New file: sentinel errors |
| `internal/service/worker/worker.go` | Check `ErrAllModelsExhausted` in retry loop |
| `cmd/fusionn-subs/main.go` | Remove modelselection block, pass `ctx` to translator |

## Impact on OpenRouter Translator

`openrouter.go` `executeScript` call also needs updating for the new return signature (add `_` for the output string). No other changes needed — OpenRouter code stays dormant.
