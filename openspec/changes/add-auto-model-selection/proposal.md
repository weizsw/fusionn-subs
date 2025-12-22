# Change: Add Automatic Free Model Selection for OpenRouter

## Why

OpenRouter's free model landscape changes rapidly - models come and go, quality varies, and token counts don't correlate with translation quality. Manual model selection requires constant monitoring and updates. An AI-driven model selector can evaluate available free models and automatically choose the best one for English-to-Chinese subtitle translation.

## What Changes

- Add daily automatic model selection at 3 AM
- Fetch free models from OpenRouter API with metadata
- Use Gemini 3 Flash (gemini-3-flash) to evaluate and select best translation model
- Exclude code-focused models from consideration
- Implement fallback strategy: selected model → last known good → user-configured model
- Re-evaluate on service restart
- Block startup until initial evaluation completes

## Impact

- **NON-BREAKING**: Opt-in via config flag `auto_select_model: true`
- **NEW**: Intelligent model selection without manual intervention
- Affected specs: `model-selection`, `translation-service`
- Affected code:
  - `internal/config/config.go` - Add auto-selection config
  - `internal/service/modelselection/` - New service for model evaluation
  - `internal/service/translator/openrouter.go` - Support dynamic model updates
  - `cmd/fusionn-subs/main.go` - Initialize model selector service
  - `config/config.example.yaml` - Add auto-selection options

## Migration Path

Existing users: No changes needed, manual model selection continues to work.

New users can enable:

```yaml
openrouter:
  auto_select_model: true
  evaluator:
    provider: "gemini"  # Use Gemini for evaluation
    gemini_api_key: ""  # Or reuse from gemini section
  fallback_model: "google/gemini-3-flash:free"
```
