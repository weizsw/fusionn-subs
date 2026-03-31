# Local LLM Provider + Provider Selection Design

**Date:** 2026-03-31
**Status:** Approved

## Problem

fusionn-subs currently supports two translation backends (Gemini and OpenRouter), both requiring external API keys. The selection logic is implicit: Gemini if its key is set, else OpenRouter. There is no way to use a local OpenAI-compatible LLM server, and no way to configure a fallback chain between providers.

## Goals

1. Add a **local LLM** translation provider that shells out to llm-subtrans Custom Server mode.
2. Add an **explicit provider selection** mechanism via config.
3. Support an **ordered fallback chain**: if the first provider fails, try the next.
4. Maintain **backwards compatibility** with existing configs.

## Design

### Config Changes

#### New `translator.providers` field

A YAML list specifying which providers to use and in what order:

```yaml
translator:
  providers: ["local_llm", "gemini"]  # ordered fallback chain
  target_language: "Chinese"
  output_suffix: "chs"
  max_translation_retries: 3
```

Valid provider names: `"gemini"`, `"openrouter"`, `"local_llm"`.

**Backwards compatibility:** If `providers` is empty or unset, the system uses the legacy implicit logic (Gemini if key set, else OpenRouter, else error). Existing configs continue to work without changes.

#### New `local_llm` config section

```yaml
local_llm:
  base_url: "http://127.0.0.1:8045"
  api_key: "sk-0c85c1081474439c91ffcba107229ec0"
  model: "gemini-3-flash"
  endpoint: "/v1/chat/completions"
  instruction: ""
  rate_limit: 10
  max_batch_size: 20
```

Field mapping to llm-subtrans CLI flags:
- `base_url` → `-s` (server address)
- `endpoint` → `-e` (API endpoint)
- `api_key` → `-k` (API key, optional for local servers)
- `model` → `-m` (model name)
- Endpoint containing "chat" → `--chat` flag added automatically
- `--systemmessages` always added when using chat mode (sends instructions as system role)

Defaults: `endpoint` defaults to `/v1/chat/completions`, `rate_limit` to `10`, `max_batch_size` to `20`.

### New Files

#### `internal/service/translator/local_llm.go`

`LocalLLMTranslator` struct, similar pattern to `OpenRouterTranslator`:

- Holds `baseURL`, `apiKey`, `model`, `endpoint`, `instruction`, `rateLimit`, `maxBatchSize`, `scriptPath`, `workDir`.
- `scriptPath` and `workDir` reuse the same env vars as OpenRouter (`LLM_SUBTRANS_SCRIPT_PATH` / `LLM_SUBTRANS_DIR`) since they point to the same llm-subtrans installation.
- `Translate()` builds CLI args for Custom Server mode and calls `executeScript()`.
- Implements `UpdateConfig(cfg config.LocalLLMConfig)` for hot-reload.

CLI command constructed:
```
llm-subtrans.sh <subtitle_path> -o <output> -l <target_lang> \
  -s <base_url> -e <endpoint> -k <api_key> -m <model> \
  --chat --systemmessages \
  [--moviename <title>] [--instruction <inst>] \
  [--ratelimit <n>] [--maxbatchsize <n>]
```

### Modified Files

#### `internal/config/config.go`

1. Add `LocalLLMConfig` struct:
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

2. Add to `Config` struct:
   ```go
   LocalLLM   LocalLLMConfig   `mapstructure:"local_llm"`
   ```

3. Add `Providers []string` to `TranslatorConfig`:
   ```go
   type TranslatorConfig struct {
       Providers             []string `mapstructure:"providers"`
       TargetLanguage        string   `mapstructure:"target_language"`
       OutputSuffix          string   `mapstructure:"output_suffix"`
       MaxTranslationRetries int      `mapstructure:"max_translation_retries"`
   }
   ```

4. Update `Validate()`:
   - If `Providers` is non-empty, only validate config sections for listed providers.
   - `"gemini"` in list → require `gemini.api_key` and model names.
   - `"openrouter"` in list → require `openrouter.api_key` and model.
   - `"local_llm"` in list → require `local_llm.base_url`.
   - If `Providers` is empty, keep current validation (Gemini required).

5. Update `SafeLogValues()` to include `local_llm.*` entries, mask `local_llm.api_key`.

#### `internal/service/translator/factory.go`

1. Add `FallbackTranslator`:
   ```go
   type FallbackTranslator struct {
       translators []namedTranslator
   }

   type namedTranslator struct {
       name       string
       translator Translator
   }
   ```

   `Translate()` iterates through translators in order. On success, returns immediately. On failure, logs a warning and tries the next. If all fail, returns the last error.

2. Update `NewTranslator()`:
   - If `cfg.Translator.Providers` is non-empty, build translators for each listed provider.
   - If only one provider, return it directly (no wrapper).
   - If multiple, wrap in `FallbackTranslator`.
   - If `Providers` is empty, use legacy logic.

3. `FallbackTranslator` implements `ConfigUpdater`-like behavior: on config reload, iterate contained translators and call update methods on those that support it.

#### `internal/service/translator/errors.go`

No changes needed. Existing `ErrRateLimited` and `ErrAllModelsExhausted` are reusable.

#### `cmd/fusionn-subs/main.go`

Update the `OnChange` callback to handle the new `FallbackTranslator` / `LocalLLMTranslator` config updates. The `ConfigUpdater` interface broadens or we add a more general update mechanism in `FallbackTranslator`.

#### `config/config.example.yaml`

Add `translator.providers` field and `local_llm` section with documentation comments.

### Fallback Behavior

The `FallbackTranslator.Translate()` method:

1. Calls provider 1's `Translate()`.
2. If it succeeds, return the result.
3. If it fails, log a warning (`"Provider X failed: <err>, trying next..."`).
4. Call provider 2's `Translate()`.
5. Continue until success or all providers exhausted.
6. If all fail, return `fmt.Errorf("all providers failed, last error: %w", lastErr)`.

The worker's existing retry logic (up to `max_translation_retries`) wraps the entire fallback chain. Each retry goes through the full chain again. `ErrAllModelsExhausted` from Gemini's primary/secondary logic still works within Gemini's translator -- the fallback chain catches it and tries the next provider.

### Hot-Reload

- `LocalLLMTranslator.UpdateConfig(cfg LocalLLMConfig)` updates base_url, api_key, model, endpoint, etc.
- `FallbackTranslator` holds references to its child translators. On config change, `main.go`'s callback iterates and updates each.
- A general `ConfigReloader` interface or type-assertion approach keeps this extensible.

### Error Handling

- Local LLM server unreachable → llm-subtrans script fails → triggers fallback to next provider.
- Rate limit errors from local LLM → same fallback behavior.
- Script errors (Python traceback, etc.) → caught by existing `detectScriptFailure()`.

## Out of Scope

- Direct Go HTTP calls to the OpenAI-compatible API (no Python dependency). Can be added later as a separate provider.
- Auto-selection / health-check based provider switching.
- Multiple local LLM servers.

## Files Changed Summary

| File | Change |
|------|--------|
| `internal/config/config.go` | Add `LocalLLMConfig`, `Providers` field, update validation and logging |
| `internal/service/translator/local_llm.go` | New file: `LocalLLMTranslator` |
| `internal/service/translator/factory.go` | Add `FallbackTranslator`, update `NewTranslator()` |
| `cmd/fusionn-subs/main.go` | Update config reload callback |
| `config/config.example.yaml` | Add `local_llm` section and `providers` field |
