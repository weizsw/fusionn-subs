# Implementation Tasks

## 1. Configuration Changes

- [x] 1.1 Update `Config` struct to replace `GeminiConfig` with `OpenRouterConfig`
- [x] 1.2 Add OpenRouter-specific fields: `api_key` (required), `model` (required), `instruction`, `max_batch_size`, `rate_limit`
- [x] 1.3 Remove `base_url` field (OpenRouter has fixed endpoint)
- [x] 1.4 Update config validation to check OpenRouter required fields (`api_key`, `model`)
- [x] 1.5 Update environment variable prefix from `FUSIONN_SUBS_GEMINI_*` to `FUSIONN_SUBS_OPENROUTER_*`
- [x] 1.6 Update `SafeLogValues()` to mask OpenRouter API key

## 2. Translator Service Abstraction

- [x] 2.1 Create `TranslatorFactory` for provider-based instantiation
- [x] 2.2 Keep existing `GeminiTranslator` (rename to `gemini.go` for clarity)
- [x] 2.3 Create new `OpenRouterTranslator` in `openrouter.go`
- [x] 2.4 Implement factory pattern to select translator based on config
- [x] 2.5 Update main.go to use factory instead of direct instantiation

## 3. OpenRouter Translator Implementation

- [x] 3.1 Create `OpenRouterTranslator` struct with OpenRouter-specific fields
- [x] 3.2 Change script from `gemini-subtrans.sh` to `llm-subtrans.sh`
- [x] 3.3 Update script path to use `LLM_SUBTRANS_SCRIPT_PATH` env var or default to `/opt/llm-subtrans/llm-subtrans.sh`
- [x] 3.4 Update command-line arguments: `--model`, `--apikey`, `-l`, `-o`, `--ratelimit`, `--maxbatchsize`, `--instruction`
- [x] 3.5 Remove `--provider` flag (llm-subtrans defaults to OpenRouter)
- [x] 3.6 Update model format to `provider/model` (e.g., `google/gemini-2.0-flash-exp`)

## 4. Configuration Files

- [x] 4.1 Update `config/config.example.yaml` with OpenRouter section
- [x] 4.2 Keep Gemini section but mark as optional/alternative
- [x] 4.3 Add comments with OpenRouter API key URL and model examples
- [x] 4.4 Document that only one provider section is needed
- [x] 4.5 Set default rate_limit to 10 RPM (conservative, works for all providers)

## 5. Docker & Environment

- [x] 5.1 Add `LLM_SUBTRANS_SCRIPT_PATH=/opt/llm-subtrans/llm-subtrans.sh` to ENV
- [x] 5.2 Keep existing `GEMINI_SCRIPT_PATH` and `GEMINI_WORKDIR` for backward compatibility
- [x] 5.3 **Change installation command**: `printf "2\n\n2\n\n"` → `printf "2\n\n0\n"` in Dockerfile
- [x] 5.4 Verify installation generates `llm-subtrans.sh`
- [x] 5.5 Update docker-compose.yml example with both provider options
- [x] 5.6 Add comments showing how to switch between providers

## 6. Documentation

- [x] 6.1 Update `README.md` with OpenRouter setup instructions
- [x] 6.2 Document provider selection pattern (OpenRouter vs Gemini)
- [x] 6.3 Add section on choosing between providers
- [x] 6.4 Document rate limits: 10 RPM default, tune based on provider plan
- [x] 6.5 Update `openspec/project.md` to reflect multi-provider architecture
- [x] 6.6 Add migration guide for existing Gemini users
- [x] 6.7 Update environment variable table in README with both provider options

## 7. Testing & Validation

- [x] 7.1 Test config loading with OpenRouter parameters
- [x] 7.2 Test config loading with Gemini parameters (backward compatibility)
- [x] 7.3 Test factory pattern selects correct translator based on config
- [x] 7.4 Test config hot-reload with OpenRouter changes
- [x] 7.5 Test config hot-reload with Gemini changes
- [x] 7.6 Verify script invocation with correct OpenRouter flags
- [x] 7.7 Test API key masking in logs for both providers
- [x] 7.8 Validate error handling for OpenRouter-specific errors
- [x] 7.9 Test rate limiting with conservative default (10 RPM)
