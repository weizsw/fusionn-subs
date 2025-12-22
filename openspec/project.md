# Project Context

## Purpose
`fusionn-subs` is a Go worker service that automates subtitle translation for media files. It polls Redis queues for translation jobs, translates subtitles using AI providers (OpenRouter or Gemini), and sends completion callbacks with the translated subtitle paths. Supports multiple LLM providers through a factory pattern for flexibility. Designed for 24/7 operation in containerized environments with graceful shutdown and config hot-reload.

## Tech Stack
- **Language**: Go 1.23
- **Queue**: Redis (BRPOP blocking consumer)
- **HTTP Client**: go-resty/resty (callbacks)
- **Config**: spf13/viper (YAML + env overrides, hot-reload via polling)
- **Logging**: uber/zap (structured JSON logging)
- **External Scripts**: 
  - `llm-subtrans.sh` (OpenRouter - default, supports 100+ models)
  - `gemini-subtrans.sh` (Gemini direct API access)
- **Container**: Docker + docker-compose
- **CI/CD**: GitHub Actions (test, lint, multi-arch builds to GHCR + Docker Hub)

## Project Conventions

### Code Style
- **Follows Uber Go Style Guide, Go Code Review Comments, and Effective Go** (as specified in user rules)
- Use structured logging with zap (`logger.Infof`, `logger.Errorf`, etc.)
- Emoji prefixes for log categories: `📁` config, `🔗` connections, `📥` messages, `🤖` services, `✅` success, `❌` errors
- Secret masking: API keys and sensitive values shown as `sk-...xyz` in logs
- Exported types/functions have godoc comments
- Error wrapping with `fmt.Errorf("context: %w", err)`
- Context propagation for cancellation and timeouts
- Graceful shutdown: listen for `SIGINT`/`SIGTERM`, stop workers cleanly

### Architecture Patterns
- **Worker pattern**: Long-running consumer (`worker.Run`) with exponential backoff on Redis errors
- **Factory pattern**: `NewTranslator()` selects provider (OpenRouter or Gemini) based on config
- **Service abstraction**: `Translator` interface with multiple implementations (`OpenRouterTranslator`, `GeminiTranslator`)
- **Auto model selection**: Optional AI-driven model selector using Gemini to evaluate and pick best free OpenRouter model daily
- **Config hot-reload**: `config.Manager` polls file changes every 10s, notifies subscribers (Docker bind mount safe)
- **Callback client**: Retries with exponential backoff (max 3 retries, 15s timeout per attempt)
- **Dependency injection**: Services passed as constructor args (`NewWorker(redis, config, translator, callback)`)
- **Single responsibility**: Each package has one clear purpose (`worker`, `translator`, `callback`, `config`, `modelselection`)

### Testing Strategy
- Standard Go testing with `go test -v ./...`
- Makefile target: `make test`
- CI runs tests on every push/PR
- Prefer unit tests for business logic, integration tests where external deps required

### Git Workflow
- Main branch: `main`
- CI on push/PR: builds, tests, lints, pushes Docker images to GHCR
- Release workflow: tag with `v*` to trigger multi-registry publish (GHCR + Docker Hub)
- Commit messages: casual but clear (see user rules)

## Domain Context

### Translation Workflow
1. External service pushes job to Redis queue (JSON message with file paths, provider, overview)
2. Worker polls with `BRPOP` (blocking, 5s timeout)
3. Job message contains: `path` (input subtitle), `video_path`, `provider`, `file_name`, `overview` (context for AI)
4. Factory selects translator based on config (OpenRouter or Gemini)
5. **Optional**: If auto-selection enabled, model selector evaluates and picks best free OpenRouter model on startup and daily at 3 AM
6. Translator invokes appropriate script:
   - OpenRouter: `/opt/llm-subtrans/llm-subtrans.sh` (default, 100+ models)
   - Gemini: `/opt/llm-subtrans/gemini-subtrans.sh` (direct API)
7. Script generates translated subtitle (default: `{original}.chs.srt`)
8. Worker POSTs callback to configured endpoint with result paths
9. Callback includes: `chs_subtitle_path`, `eng_subtitle_path`, `video_path`

### Auto Model Selection Workflow (Optional)
When `openrouter.auto_select_model: true`:
1. **Startup**: Blocks until initial model evaluation completes
2. **Fetching**: Get free models from OpenRouter API (filter out code-focused models)
3. **Evaluation**: Use Gemini 3 Flash to evaluate models based on:
   - Translation quality (English→Chinese)
   - Instruction following (format compliance)
   - Consistency and reliability
   - Context handling capability
4. **Selection**: Update translator with best model
5. **Scheduling**: Re-evaluate daily at configured hour (default: 3 AM UTC)
6. **Fallback**: If evaluation fails: selected → last-known-good → fallback_model

### Job Message Schema
```go
type JobMessage struct {
    Path      string // Input subtitle file path (required)
    VideoPath string // Associated video (optional, for callback)
    Provider  string // Service identifier (e.g., "emby", "jellyfin")
    FileName  string // Display name
    Overview  string // Movie/show description (helps AI translation)
}
```

### Configuration Hot-Reload
- Uses file polling (not fsnotify) for Docker bind mount compatibility
- Changes take effect without restart (except Redis connection params)
- Provider selection: Configure either `openrouter` or `gemini` section (not both)
- API keys, models, instructions, rate limits can be tuned live

## Important Constraints

### Technical
- LLM API rate limits: Default 10 RPM (conservative, works for all providers)
  - OpenRouter: Varies by model and plan
  - Gemini: 8-15 RPM depending on plan
- Script timeout: 15 minutes per job (hardcoded `DefaultGeminiTimeout`)
- Callback timeout: 15 seconds with 3 retries
- Redis connection: single client, blocks on `BRPOP` (no connection pooling)
- File access: Requires shared volume with external service (media files and subtitles)

### Operational
- Designed for single-worker deployment (no distributed locking)
- No dead-letter queue: failed jobs log error and move on
- No job persistence: Redis queue is the only state

### Security
- API key passed via environment variable to script (not visible in process list)
- Logs mask secrets: `util.MaskSecret()` shows first 3 + last 3 chars
- No authentication on callback endpoint (assumes trusted internal network)

## External Dependencies

### Required Services
- **Redis**: Job queue source (must be reachable at startup)
- **LLM Provider**: Choose one:
  - **OpenRouter**: Unified API for 100+ models (recommended)
  - **Gemini API**: Google's generative AI service (direct access)
- **llm-subtrans scripts**: Python scripts from `llm-subtrans` project (bundled in `/opt/llm-subtrans/`)
  - `llm-subtrans.sh` for OpenRouter
  - `gemini-subtrans.sh` for Gemini
- **Callback endpoint**: External service receiving completion notifications (e.g., `http://localhost:4664/api/v1/async_merge`)

### Environment Variables
- `CONFIG_PATH`: Path to YAML config (default: `config/config.yaml`)
- `ENV`: Set to `production` for release mode (JSON logging)
- `FUSIONN_SUBS_*`: Override any config value
  - `FUSIONN_SUBS_OPENROUTER_API_KEY`: OpenRouter API key
  - `FUSIONN_SUBS_OPENROUTER_MODEL`: OpenRouter model (ignored if auto-selection enabled)
  - `FUSIONN_SUBS_OPENROUTER_AUTO_SELECT_MODEL`: Enable auto model selection (true/false)
  - `FUSIONN_SUBS_OPENROUTER_FALLBACK_MODEL`: Fallback model for auto-selection
  - `FUSIONN_SUBS_OPENROUTER_EVALUATOR_PROVIDER`: Evaluator provider (gemini)
  - `FUSIONN_SUBS_OPENROUTER_EVALUATOR_GEMINI_API_KEY`: Gemini API key for evaluator
  - `FUSIONN_SUBS_OPENROUTER_EVALUATOR_MODEL`: Evaluator model (default: gemini-3-flash)
  - `FUSIONN_SUBS_OPENROUTER_EVALUATOR_SCHEDULE_HOUR`: Daily evaluation hour 0-23 (default: 3)
  - `FUSIONN_SUBS_GEMINI_API_KEY`: Gemini API key
  - `FUSIONN_SUBS_GEMINI_MODEL`: Gemini model
- Script paths:
  - `LLM_SUBTRANS_SCRIPT_PATH`: OpenRouter script (default: `/opt/llm-subtrans/llm-subtrans.sh`)
  - `GEMINI_SCRIPT_PATH`: Gemini script (default: `/opt/llm-subtrans/gemini-subtrans.sh`)
  - `LLM_SUBTRANS_DIR`: Working directory (default: `/opt/llm-subtrans`)
  - `GEMINI_WORKDIR`: Gemini working directory (default: `/opt/llm-subtrans`)

### Docker Volumes
- `/data`: Media files and subtitles (must match paths in job messages)
- `/app/config`: Config file mount (read-only recommended)
