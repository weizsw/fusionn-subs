# Gemini Primary/Secondary Model Fallback — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Simplify the translation service to Gemini-only with primary/secondary model fallback on RPD exhaustion.

**Architecture:** The `GeminiTranslator` manages two models with a sticky switch triggered by 429 errors from the `gemini-subtrans` script. The worker's existing retry loop handles re-running failed jobs on the secondary model. A daily reset goroutine flips back to primary at midnight Pacific.

**Tech Stack:** Go 1.23, Viper (config), Redis (queue), exec (shell-out to `gemini-subtrans.sh`)

**Spec:** `docs/superpowers/specs/2026-03-29-gemini-model-fallback-design.md`

---

## File Structure

| File | Action | Responsibility |
|------|--------|----------------|
| `internal/service/translator/errors.go` | Create | Sentinel errors (`ErrRateLimited`, `ErrAllModelsExhausted`) |
| `internal/config/config.go` | Modify | New `GeminiModelConfig` struct, update `GeminiConfig`, validation, `SafeLogValues` |
| `config/config.example.yaml` | Modify | New Gemini config shape with primary/secondary models |
| `internal/service/translator/translator.go` | Modify | `executeScript` returns combined output |
| `internal/service/translator/openrouter.go` | Modify | Update `executeScript` call site for new signature |
| `internal/service/translator/gemini.go` | Rewrite | Dual-model translator with sticky switch, 429 detection, daily reset |
| `internal/service/translator/factory.go` | Modify | Add `ctx` param, Gemini-first selection |
| `internal/service/worker/worker.go` | Modify | Handle `ErrAllModelsExhausted`, skip backoff on `ErrRateLimited` |
| `cmd/fusionn-subs/main.go` | Modify | Remove modelselection block, pass `ctx` to translator |
| `Dockerfile` | Modify | Add `tzdata` package |

---

### Task 1: Create sentinel errors

**Files:**
- Create: `internal/service/translator/errors.go`

- [ ] **Step 1: Create errors.go**

```go
package translator

import "errors"

var (
	ErrRateLimited        = errors.New("model rate limited")
	ErrAllModelsExhausted = errors.New("all models exhausted for today")
)
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./internal/service/translator/`
Expected: Success (no output)

- [ ] **Step 3: Commit**

```bash
git add internal/service/translator/errors.go
git commit -m "feat(translator): add sentinel errors for rate limiting"
```

---

### Task 2: Update config structs and validation

**Files:**
- Modify: `internal/config/config.go:45-51` (GeminiConfig struct)
- Modify: `internal/config/config.go:220-264` (Validate method)
- Modify: `internal/config/config.go:342-366` (SafeLogValues method)

- [ ] **Step 1: Add GeminiModelConfig and update GeminiConfig**

Replace the existing `GeminiConfig` struct (lines 45-51) with:

```go
type GeminiModelConfig struct {
	Name         string `mapstructure:"name"`
	RateLimit    int    `mapstructure:"rate_limit"`
	MaxBatchSize int    `mapstructure:"max_batch_size"`
}

type GeminiConfig struct {
	APIKey         string            `mapstructure:"api_key"`
	Instruction    string            `mapstructure:"instruction"`
	PrimaryModel   GeminiModelConfig `mapstructure:"primary_model"`
	SecondaryModel GeminiModelConfig `mapstructure:"secondary_model"`
}
```

- [ ] **Step 2: Update Validate method**

Replace the existing validation (lines 220-264). The new validation:
- Requires `gemini.api_key` (not "either openrouter or gemini")
- Requires `gemini.primary_model.name` and `gemini.secondary_model.name`
- Requires primary and secondary names to be distinct
- OpenRouter validation only runs if `openrouter.api_key` is set (optional)
- Keep OpenRouter/auto-select rules intact but gated behind `openrouter.api_key` being set

```go
func (c *Config) Validate() error {
	switch {
	case c.Redis.URL == "":
		return fmt.Errorf("redis.url is required")
	case c.Redis.Queue == "":
		return fmt.Errorf("redis.queue is required")
	case c.Callback.URL == "":
		return fmt.Errorf("callback.url is required")
	case c.Gemini.APIKey == "":
		return fmt.Errorf("gemini.api_key is required")
	case c.Gemini.PrimaryModel.Name == "":
		return fmt.Errorf("gemini.primary_model.name is required")
	case c.Gemini.SecondaryModel.Name == "":
		return fmt.Errorf("gemini.secondary_model.name is required")
	case c.Gemini.PrimaryModel.Name == c.Gemini.SecondaryModel.Name:
		return fmt.Errorf("gemini.primary_model.name and gemini.secondary_model.name must be different")
	}

	if c.OpenRouter.APIKey != "" && !c.OpenRouter.AutoSelectModel && c.OpenRouter.Model == "" {
		return fmt.Errorf("openrouter.model is required when openrouter.api_key is set (or enable auto_select_model)")
	}

	if c.OpenRouter.AutoSelectModel {
		if c.OpenRouter.APIKey == "" {
			return fmt.Errorf("openrouter.api_key is required when auto_select_model is enabled")
		}
		if c.OpenRouter.Model == "" {
			return fmt.Errorf("openrouter.model is required when auto_select_model is enabled (used as fallback)")
		}
		if c.OpenRouter.Evaluator.Provider == "" {
			return fmt.Errorf("openrouter.evaluator.provider is required when auto_select_model is enabled")
		}
		if c.OpenRouter.Evaluator.Provider != "gemini" {
			return fmt.Errorf("only 'gemini' is supported as evaluator.provider")
		}
		if c.OpenRouter.Evaluator.GeminiAPIKey == "" && c.Gemini.APIKey == "" {
			return fmt.Errorf("either openrouter.evaluator.gemini_api_key or gemini.api_key is required when auto_select_model is enabled")
		}
		if c.OpenRouter.Evaluator.ScheduleHour < 0 || c.OpenRouter.Evaluator.ScheduleHour > 23 {
			c.OpenRouter.Evaluator.ScheduleHour = 3
		}
		if c.OpenRouter.Evaluator.Model == "" {
			c.OpenRouter.Evaluator.Model = "gemini-3-flash"
		}
	}

	return nil
}
```

- [ ] **Step 3: Update SafeLogValues**

Replace the gemini entries in `SafeLogValues` (lines 348-352) with the new nested structure:

```go
func (c *Config) SafeLogValues() map[string]any {
	return map[string]any{
		"redis.url":                           c.Redis.URL,
		"redis.queue":                         c.Redis.Queue,
		"callback.url":                        c.Callback.URL,
		"gemini.api_key":                      util.MaskSecret(c.Gemini.APIKey),
		"gemini.instruction":                  c.Gemini.Instruction,
		"gemini.primary_model.name":           c.Gemini.PrimaryModel.Name,
		"gemini.primary_model.rate_limit":     c.Gemini.PrimaryModel.RateLimit,
		"gemini.primary_model.max_batch_size": c.Gemini.PrimaryModel.MaxBatchSize,
		"gemini.secondary_model.name":           c.Gemini.SecondaryModel.Name,
		"gemini.secondary_model.rate_limit":     c.Gemini.SecondaryModel.RateLimit,
		"gemini.secondary_model.max_batch_size": c.Gemini.SecondaryModel.MaxBatchSize,
		"openrouter.api_key":                  util.MaskSecret(c.OpenRouter.APIKey),
		"openrouter.model":                    c.OpenRouter.Model,
		"openrouter.instruction":              c.OpenRouter.Instruction,
		"openrouter.max_batch_size":           c.OpenRouter.MaxBatchSize,
		"openrouter.rate_limit":               c.OpenRouter.RateLimit,
		"openrouter.auto_select_model":        c.OpenRouter.AutoSelectModel,
		"openrouter.evaluator.provider":       c.OpenRouter.Evaluator.Provider,
		"openrouter.evaluator.gemini_api_key": util.MaskSecret(c.OpenRouter.Evaluator.GeminiAPIKey),
		"openrouter.evaluator.model":          c.OpenRouter.Evaluator.Model,
		"openrouter.evaluator.schedule_hour":  c.OpenRouter.Evaluator.ScheduleHour,
		"translator.target_lang":              c.Translator.TargetLanguage,
		"translator.suffix":                   c.Translator.OutputSuffix,
	}
}
```

- [ ] **Step 4: Verify config package compiles in isolation**

Run: `go build ./internal/config/`
Expected: Success (config has no dependency on translator)

Note: `go build ./...` will fail until Tasks 4-7 update the translator package. Tasks 2-7 form a logical unit for full-repo compilation.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go
git commit -m "feat(config): add primary/secondary Gemini model config"
```

---

### Task 3: Update config example

**Files:**
- Modify: `config/config.example.yaml`

- [ ] **Step 1: Rewrite the Gemini section**

Replace the commented-out gemini block (lines 59-69) with an active, primary config section. Update the header comment to reflect Gemini as the primary provider. The new gemini section:

```yaml
# ─────────────────────────────────────────────────────────────────────────────
# GEMINI - AI Translation Provider (Primary)
# ─────────────────────────────────────────────────────────────────────────────
# Get API key from: https://aistudio.google.com/apikey
# Uses primary model by default. Automatically falls back to secondary model
# when primary hits its daily rate limit (429). Resets at midnight Pacific.
gemini:
  api_key: ""                         # REQUIRED - Gemini API key
  instruction: ""                     # Custom instruction for translation style (optional)
  primary_model:
    name: "gemini-2.5-flash"          # Primary model (used first)
    rate_limit: 8                     # Requests per minute
    max_batch_size: 20                # Max subtitles per batch
  secondary_model:
    name: "gemini-2.5-pro"            # Fallback model (used when primary is rate-limited)
    rate_limit: 5                     # Requests per minute
    max_batch_size: 15                # Max subtitles per batch
```

Also update the header comment (lines 1-11) to remove "Configure EITHER openrouter OR gemini" and indicate Gemini is the primary provider.

Demote the OpenRouter section header from "Recommended" to "Alternative (dormant)".

- [ ] **Step 2: Commit**

```bash
git add config/config.example.yaml
git commit -m "docs(config): update example config for Gemini primary/secondary models"
```

---

### Task 4: Update executeScript to return captured output

**Files:**
- Modify: `internal/service/translator/translator.go:26-80`

- [ ] **Step 1: Update executeScript signature and return combined output**

Change the function signature from:
```go
func executeScript(cmd *exec.Cmd, outputPath string) (string, error)
```
to:
```go
func executeScript(cmd *exec.Cmd, outputPath string) (resultPath string, combinedOutput string, err error)
```

Key changes inside the function:
- Build `combined := stdoutStr + "\n" + stderrStr` after `cmd.Wait()`
- On pipe errors: return `"", "", fmt.Errorf(...)`
- On start error: return `"", "", fmt.Errorf(...)`
- On `cmd.Wait()` error: return `"", combined, fmt.Errorf(...)` (include combined output even on failure)
- On output file not found: return `"", combined, fmt.Errorf(...)`
- On `detectScriptFailure`: return `"", combined, fmt.Errorf(...)`
- On success: return `outputPath, combined, nil`

Full replacement for the function:

```go
func executeScript(cmd *exec.Cmd, outputPath string) (resultPath string, combinedOutput string, err error) {
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return "", "", fmt.Errorf("stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return "", "", fmt.Errorf("stderr pipe: %w", err)
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	var wg sync.WaitGroup

	wg.Add(2)
	go func() {
		defer wg.Done()
		streamDimmed(stdoutPipe, &stdoutBuf)
	}()
	go func() {
		defer wg.Done()
		streamDimmed(stderrPipe, &stderrBuf)
	}()

	if err := cmd.Start(); err != nil {
		return "", "", fmt.Errorf("start script: %w", err)
	}

	wg.Wait()
	err = cmd.Wait()

	stdoutStr := strings.TrimSpace(stdoutBuf.String())
	stderrStr := strings.TrimSpace(stderrBuf.String())
	combined := stdoutStr + "\n" + stderrStr

	if err != nil {
		logger.Errorf("Translation failed: %v", err)
		if stderrStr != "" {
			logger.Errorf("Script stderr: %s", stderrStr)
		}
		return "", combined, fmt.Errorf("script failed: %w", err)
	}

	if _, statErr := os.Stat(outputPath); statErr != nil {
		logger.Errorf("Output file not found after script completed")
		return "", combined, fmt.Errorf("output not found: %w", statErr)
	}

	if reason, failed := detectScriptFailure(stdoutStr, stderrStr); failed {
		logger.Errorf("Script failure detected: %s", reason)
		return "", combined, fmt.Errorf("script reported failure: %s", reason)
	}

	logger.Infof("✅ Translation completed: %s", outputPath)
	return outputPath, combined, nil
}
```

- [ ] **Step 2: Don't compile yet**

The translator package won't compile until `gemini.go` and `openrouter.go` are updated (Tasks 5-7). Tasks 4-7 form an atomic unit — they'll be committed together.


---

### Task 5: Update OpenRouter translator for new executeScript signature

**Files:**
- Modify: `internal/service/translator/openrouter.go:133`

- [ ] **Step 1: Update the executeScript call**

Change line 133 from:
```go
	return executeScript(cmd, outputPath)
```
to:
```go
	resultPath, _, err := executeScript(cmd, outputPath)
	return resultPath, err
```

- [ ] **Step 2: Don't compile yet — gemini.go and factory.go still need updating (Tasks 6-7)**

---

### Task 6: Rewrite GeminiTranslator

**Files:**
- Rewrite: `internal/service/translator/gemini.go`

This is the biggest change. The file gets a full rewrite.

- [ ] **Step 1: Write the complete new gemini.go**

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
	"time"

	"github.com/fusionn-subs/internal/config"
	"github.com/fusionn-subs/internal/types"
	"github.com/fusionn-subs/pkg/logger"
)

var pacificTZ *time.Location

func init() {
	var err error
	pacificTZ, err = time.LoadLocation("America/Los_Angeles")
	if err != nil {
		panic(fmt.Sprintf("failed to load America/Los_Angeles timezone: %v (ensure tzdata is installed)", err))
	}
}

type GeminiTranslator struct {
	scriptPath     string
	workDir        string
	apiKey         string
	instruction    string
	targetLanguage string
	outputSuffix   string

	mu               sync.RWMutex
	primaryModel     config.GeminiModelConfig
	secondaryModel   config.GeminiModelConfig
	activeModel      *config.GeminiModelConfig
	primaryExhausted bool
}

func NewGeminiTranslator(ctx context.Context, cfg config.GeminiConfig, targetLang, outputSuffix string) *GeminiTranslator {
	scriptPath := os.Getenv("GEMINI_SCRIPT_PATH")
	if scriptPath == "" {
		scriptPath = "/opt/llm-subtrans/gemini-subtrans.sh"
	}
	workDir := os.Getenv("GEMINI_WORKDIR")
	if workDir == "" {
		workDir = "/opt/llm-subtrans"
	}

	t := &GeminiTranslator{
		scriptPath:     scriptPath,
		workDir:        workDir,
		apiKey:         cfg.APIKey,
		instruction:    cfg.Instruction,
		targetLanguage: targetLang,
		outputSuffix:   outputSuffix,
		primaryModel:   cfg.PrimaryModel,
		secondaryModel: cfg.SecondaryModel,
	}
	t.activeModel = &t.primaryModel

	logger.Infof("🤖 Gemini translator: primary=%s, secondary=%s", cfg.PrimaryModel.Name, cfg.SecondaryModel.Name)

	t.startDailyReset(ctx)

	return t
}

func (t *GeminiTranslator) Translate(ctx context.Context, msg types.JobMessage) (string, error) {
	if err := msg.Validate(); err != nil {
		return "", fmt.Errorf("invalid message: %w", err)
	}

	outputPath := msg.OutputPath(t.outputSuffix)

	t.mu.RLock()
	model := *t.activeModel
	isPrimary := !t.primaryExhausted
	t.mu.RUnlock()

	ctxTimeout, cancel := context.WithTimeout(ctx, config.DefaultGeminiTimeout)
	defer cancel()

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

	if t.instruction != "" {
		args = append(args, "--instruction", t.instruction)
	}

	if model.RateLimit > 0 {
		args = append(args, "--ratelimit", strconv.Itoa(model.RateLimit))
	}

	if model.MaxBatchSize > 0 {
		args = append(args, "--maxbatchsize", strconv.Itoa(model.MaxBatchSize))
	}

	cmd := exec.CommandContext(ctxTimeout, t.scriptPath, args...)
	if t.workDir != "" {
		cmd.Dir = t.workDir
	}

	cmd.Env = append(os.Environ(), "GEMINI_API_KEY="+t.apiKey)

	logger.Infof("🔄 Starting translation (Gemini/%s): %s → %s", model.Name, msg.SubtitlePath, outputPath)
	logger.Debugf("Command: %s", maskAPIKeyInCommand(buildCommandLine(t.scriptPath, args)))

	resultPath, combinedOutput, err := executeScript(cmd, outputPath)
	if err != nil {
		os.Remove(outputPath)

		if isRateLimitError(combinedOutput) {
			if isPrimary {
				t.switchToSecondary()
				return "", fmt.Errorf("%w: %s exhausted, switched to %s", ErrRateLimited, model.Name, t.secondaryModel.Name)
			}
			return "", fmt.Errorf("%w: %s also exhausted", ErrAllModelsExhausted, model.Name)
		}

		return "", err
	}

	return resultPath, nil
}

func (t *GeminiTranslator) switchToSecondary() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.primaryExhausted = true
	t.activeModel = &t.secondaryModel
	logger.Infof("⚠️ Primary model rate-limited, switching to secondary: %s", t.secondaryModel.Name)
}

func (t *GeminiTranslator) ResetToPrimary() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.primaryExhausted = false
	t.activeModel = &t.primaryModel
	logger.Infof("🔄 Daily reset: switched back to primary model (%s)", t.primaryModel.Name)
}

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

func isRateLimitError(output string) bool {
	lower := strings.ToLower(output)

	geminiIndicators := []string{
		"resource_exhausted",
		"quota exceeded",
	}
	for _, ind := range geminiIndicators {
		if strings.Contains(lower, ind) {
			return true
		}
	}

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

- [ ] **Step 2: Verify translator package compiles**

Don't compile yet — factory.go still needs updating (Task 7).

---

### Task 7: Update factory

**Files:**
- Modify: `internal/service/translator/factory.go`

- [ ] **Step 1: Rewrite factory.go**

The factory gains a `ctx` parameter and prioritizes Gemini:

```go
package translator

import (
	"context"
	"fmt"

	"github.com/fusionn-subs/internal/config"
	"github.com/fusionn-subs/internal/types"
	"github.com/fusionn-subs/pkg/logger"
)

type Translator interface {
	Translate(ctx context.Context, msg types.JobMessage) (string, error)
}

func NewTranslator(ctx context.Context, cfg *config.Config) (Translator, error) {
	targetLang := cfg.Translator.TargetLanguage
	outputSuffix := cfg.Translator.OutputSuffix

	if cfg.Gemini.APIKey != "" {
		logger.Infof("🤖 Using Gemini translator (primary: %s, secondary: %s)",
			cfg.Gemini.PrimaryModel.Name, cfg.Gemini.SecondaryModel.Name)
		return NewGeminiTranslator(ctx, cfg.Gemini, targetLang, outputSuffix), nil
	}

	if cfg.OpenRouter.APIKey != "" {
		if cfg.OpenRouter.AutoSelectModel {
			logger.Infof("🤖 Using OpenRouter translator (auto-selection enabled)")
		} else {
			logger.Infof("🤖 Using OpenRouter translator (model: %s)", cfg.OpenRouter.Model)
		}
		return NewOpenRouterTranslator(cfg.OpenRouter, targetLang, outputSuffix), nil
	}

	return nil, fmt.Errorf("no translator configured: gemini.api_key is required")
}
```

Note: keep the `types` import for the `Translator` interface.

- [ ] **Step 2: Verify translator package compiles**

Run: `go build ./internal/service/translator/`
Expected: Success (all three files now consistent)

- [ ] **Step 3: Commit tasks 4-7 together**

```bash
git add internal/service/translator/translator.go internal/service/translator/openrouter.go internal/service/translator/gemini.go internal/service/translator/factory.go
git commit -m "feat(translator): rewrite Gemini translator with primary/secondary model fallback"
```

---

### Task 8: Update worker

**Files:**
- Modify: `internal/service/worker/worker.go:113-155`

- [ ] **Step 1: Update processJob retry loop**

Two changes to `processJob`:

1. After the `Translate` call fails, check if the error is `ErrAllModelsExhausted` — break immediately.
2. Before the 2s backoff sleep, skip it if the error is `ErrRateLimited` (retry immediately on secondary).
3. After the retry loop, if `lastErr` wraps `ErrAllModelsExhausted`, return a specific message.

Replace the retry loop body (lines 123-150) and the final error check (lines 152-155):

```go
	for attempt := 1; attempt <= maxRetries; attempt++ {
		if attempt > 1 {
			logger.Infof("⏳ Translation retry %d/%d: job_id=%s", attempt-1, maxRetries-1, msg.JobID)
		}

		var err error
		chsPath, err = w.translator.Translate(ctx, msg)
		if err == nil {
			if attempt > 1 {
				logger.Infof("✅ Translation succeeded on attempt %d", attempt)
			}
			break
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
				return ctx.Err()
			}
		}
	}

	if lastErr != nil {
		if errors.Is(lastErr, translator.ErrAllModelsExhausted) {
			logger.Errorf("❌ All Gemini models exhausted for today: job_id=%s", msg.JobID)
			return fmt.Errorf("all Gemini models exhausted for today: %w", lastErr)
		}
		logger.Errorf("❌ Translation failed after %d attempts: job_id=%s", maxRetries, msg.JobID)
		return fmt.Errorf("translation failed after %d attempts: %w", maxRetries, lastErr)
	}
```

- [ ] **Step 2: Verify worker package compiles**

Run: `go build ./internal/service/worker/`
Expected: Success

- [ ] **Step 3: Commit**

```bash
git add internal/service/worker/worker.go
git commit -m "feat(worker): handle model exhaustion and skip backoff on rate limit switch"
```

---

### Task 9: Simplify main.go

**Files:**
- Modify: `cmd/fusionn-subs/main.go`

- [ ] **Step 1: Remove modelselection import**

Remove line 17:
```go
	"github.com/fusionn-subs/internal/service/modelselection"
```

Also remove the `"time"` import since it's only used in the modelselection block (line 11). Check if `time` is used elsewhere in the file — it's used in `initRedis` (line 181), so keep it.

- [ ] **Step 2: Move signal context setup before translator init**

The translator now needs `ctx` for its daily reset goroutine. Move the signal context setup (currently lines 152-154) to before the translator init. The new flow order:

After Redis init:

```go
	// Setup graceful shutdown (needed before translator init for daily reset goroutine)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
```

- [ ] **Step 3: Update translator init to pass ctx**

Change line 66 from:
```go
	translatorSvc, err := translator.NewTranslator(cfg)
```
to:
```go
	translatorSvc, err := translator.NewTranslator(ctx, cfg)
```

- [ ] **Step 4: Remove entire modelselection block**

Delete lines 71-115 (the `var modelSelector` declaration through the closing brace of the if block).

Also delete lines 156-159 (the `modelSelector.Stop()` defer).

- [ ] **Step 5: Remove the duplicate signal context setup**

The original `ctx, stop := signal.NotifyContext(...)` block (previously at lines 152-154) was moved earlier. Remove the original location.

- [ ] **Step 6: Verify it compiles**

Run: `go build ./cmd/fusionn-subs/`
Expected: Success

- [ ] **Step 7: Commit**

```bash
git add cmd/fusionn-subs/main.go
git commit -m "refactor(main): remove model selection, pass ctx to translator"
```

---

### Task 10: Add tzdata to Dockerfile

**Files:**
- Modify: `Dockerfile:24`

- [ ] **Step 1: Add tzdata to apt-get install**

Change line 24 from:
```dockerfile
RUN apt-get update && apt-get install -y --no-install-recommends git build-essential && rm -rf /var/lib/apt/lists/*
```
to:
```dockerfile
RUN apt-get update && apt-get install -y --no-install-recommends git build-essential tzdata && rm -rf /var/lib/apt/lists/*
```

- [ ] **Step 2: Commit**

```bash
git add Dockerfile
git commit -m "build(docker): add tzdata for Pacific timezone support"
```

---

### Task 11: Full build verification

- [ ] **Step 1: Run full build**

Run: `go build ./...`
Expected: Success with no errors

- [ ] **Step 2: Run go vet**

Run: `go vet ./...`
Expected: No issues

- [ ] **Step 3: Verify no unused imports**

Run: `goimports -l ./internal/... ./cmd/...` (or just check `go build` passed cleanly)

- [ ] **Step 4: Final commit if any formatting needed**

```bash
gofmt -w ./internal/ ./cmd/
git add -A
git diff --cached --quiet || git commit -m "style: format code"
```
