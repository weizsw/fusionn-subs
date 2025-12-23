# Change: Add OpenRouter Support with Multi-Provider Architecture

## Why

OpenRouter provides access to multiple LLM providers through a unified API, offering better flexibility, potentially lower costs, and access to 100+ models from various providers (OpenAI, Anthropic, Google, Meta, etc.). Adding OpenRouter while maintaining Gemini support provides users with choice and flexibility.

## What Changes

- Add OpenRouter configuration support alongside existing Gemini config
- Create abstract translator pattern with factory for provider selection
- Keep existing GeminiTranslator as fallback/alternative option
- Add OpenRouterTranslator using llm-subtrans's OpenRouter support
- Update config schema to support both providers
- Update Dockerfile to generate llm-subtrans.sh (OpenRouter default)
- Update documentation with multi-provider setup instructions

## Impact

- **NON-BREAKING**: Existing Gemini configurations continue to work
- **NEW**: Users can choose OpenRouter or Gemini based on config
- Affected specs: `translation-service`
- Affected code:
  - `internal/config/config.go` - Add OpenRouterConfig struct
  - `internal/service/translator/translator.go` - Add factory pattern and OpenRouterTranslator
  - `cmd/fusionn-subs/main.go` - Use factory for translator selection
  - `config/config.example.yaml` - Add OpenRouter section
  - `Dockerfile` - Update llm-subtrans installation
  - `openspec/project.md` - Document multi-provider architecture
  - `README.md` - Update setup instructions

## Migration Path

Existing users: No migration needed, Gemini config continues to work.

New users can choose:

- **OpenRouter**: Get API key from <https://openrouter.ai/>, use `openrouter` config section
- **Gemini**: Get API key from <https://aistudio.google.com/apikey>, use `gemini` config section

OpenRouter users can still use Gemini models via OpenRouter (e.g., `google/gemini-2.0-flash-exp`)
