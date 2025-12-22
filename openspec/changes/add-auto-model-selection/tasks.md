# Implementation Tasks

## 1. Configuration Changes

- [x] 1.1 Add `auto_select_model` boolean flag to OpenRouterConfig
- [x] 1.2 Add `EvaluatorConfig` struct with provider, API key, schedule fields
- [x] 1.3 Add `fallback_model` string to OpenRouterConfig
- [x] 1.4 Update config validation to check evaluator config when auto-selection enabled
- [x] 1.5 Update `SafeLogValues()` to include evaluator settings

## 2. OpenRouter API Client

- [x] 2.1 Create `internal/client/openrouter/` package
- [x] 2.2 Implement `GetFreeModels()` to fetch models with `:free` suffix
- [x] 2.3 Parse model metadata (name, context length, description)
- [x] 2.4 Filter out code-focused models (contains "code", "coder", "coding" in name/description)
- [x] 2.5 Add error handling and retry logic

## 3. Model Evaluator Service

- [x] 3.1 Create `internal/service/modelselection/` package
- [x] 3.2 Implement `Evaluator` interface with `SelectBestModel()` method
- [x] 3.3 Create `GeminiEvaluator` implementation
- [x] 3.4 Build evaluation prompt: "Given these free models with metadata, which is best for English to Chinese subtitle translation? Output only the model name."
- [x] 3.5 Parse LLM response to extract model name
- [x] 3.6 Validate selected model exists in fetched list

## 4. Model Selector Service

- [x] 4.1 Create `ModelSelector` service in `modelselection/` package
- [x] 4.2 Implement startup evaluation (blocking)
- [x] 4.3 Implement daily scheduler (3 AM UTC)
- [x] 4.4 Store selected model in-memory with timestamp
- [x] 4.5 Implement fallback chain: selected → last-known → fallback_model → error
- [x] 4.6 Add thread-safe access to current model

## 5. Integration with Translator

- [x] 5.1 Modify `OpenRouterTranslator` to accept model updates
- [x] 5.2 Add `UpdateModel(newModel string)` method to OpenRouterTranslator
- [x] 5.3 Register model selector as config change subscriber
- [x] 5.4 Log model changes with emoji prefix
- [x] 5.5 Handle mid-translation model updates gracefully

## 6. Main Application Integration

- [x] 6.1 Initialize ModelSelector service when auto_select_model enabled
- [x] 6.2 Wait for initial model selection before starting worker
- [x] 6.3 Add startup logging for model selection status
- [x] 6.4 Handle evaluator initialization failures
- [x] 6.5 Add graceful shutdown for scheduler

## 7. Configuration Files

- [x] 7.1 Update `config/config.example.yaml` with auto-selection example
- [x] 7.2 Document evaluator providers (gemini recommended)
- [x] 7.3 Add comments explaining fallback strategy
- [x] 7.4 Document schedule options

## 8. Documentation

- [x] 8.1 Update README with auto-selection feature
- [x] 8.2 Add troubleshooting section for model selection
- [x] 8.3 Document evaluator API key requirements
- [x] 8.4 Update `openspec/project.md` with model selection architecture
- [x] 8.5 Add examples of evaluation prompts

## 9. Testing & Validation

- [ ] 9.1 Test OpenRouter API client with mock responses
- [ ] 9.2 Test model filtering (exclude code models)
- [ ] 9.3 Test evaluator with various model lists
- [ ] 9.4 Test fallback chain logic
- [ ] 9.5 Test scheduler triggers correctly
- [ ] 9.6 Test startup blocking behavior
- [ ] 9.7 Verify model updates don't break in-flight translations

