# fusionn-subs

Go worker that polls Redis for subtitle translation jobs, translates subtitles using AI (OpenRouter or Gemini), and posts callback payloads once translations are complete.

## Quick Start

### Local Development

```bash
# Copy and configure
cp config/config.example.yaml config/config.yaml
# Edit config/config.yaml with your settings

# Build and run
make build
make run

# Or with Docker
make docker
make docker-run
```

### Docker

Pull from Docker Hub or GitHub Container Registry:

```bash
docker pull ghcr.io/weizsw/fusionn-subs:latest
# or
docker pull weizsw/fusionn-subs:latest
```

## Configuration

Configuration is managed via YAML file with environment variable overrides.

### Provider Selection

Choose **one** translation provider:

- **OpenRouter** (Recommended): Access to 100+ models from OpenAI, Anthropic, Google, Meta, etc.
- **Gemini**: Direct Google Gemini API access

### Config File

Copy `config/config.example.yaml` to `config/config.yaml`:

#### Option 1: OpenRouter (Recommended)

```yaml
redis:
  url: "redis://localhost:6379"
  queue: "translate_queue"

callback:
  url: "http://localhost:4664/api/v1/async_merge"

openrouter:
  api_key: ""                          # Get from https://openrouter.ai/
  model: "openai/gpt-4o-mini"          # Model in provider/model format
  instruction: ""                      # Custom translation instruction (optional)
  max_batch_size: 20                   # Tune for performance
  rate_limit: 10                       # Default: 10 RPM (tune based on your plan)

translator:
  target_language: "Chinese"
  output_suffix: "chs"
```

**Popular OpenRouter Models:**

- `openai/gpt-4o-mini` - Fast and affordable
- `anthropic/claude-3-5-sonnet` - High quality translations
- `google/gemini-2.0-flash-exp` - Gemini via OpenRouter

#### OpenRouter with Auto Model Selection

Let AI automatically select the best free translation model daily:

```yaml
openrouter:
  api_key: ""                          # Get from https://openrouter.ai/
  
  # Enable auto-selection
  auto_select_model: true
  fallback_model: "google/gemini-3-flash:free"  # Safety fallback
  
  evaluator:
    provider: "gemini"                 # Uses Gemini to evaluate models
    gemini_api_key: ""                 # Or reuse from gemini section
    model: "gemini-3-flash"            # Evaluation model (default)
    schedule_hour: 3                   # Daily evaluation at 3 AM (respects TZ env var)

# Optional: Gemini config for evaluator (if not specified above)
gemini:
  api_key: ""                          # Reused by evaluator if evaluator.gemini_api_key is empty
```

**Timezone Configuration:**
The `schedule_hour` respects your container's `TZ` environment variable:

```yaml
# In docker-compose.yml
services:
  fusionn-subs:
    environment:
      - TZ=Asia/Shanghai  # Evaluation runs at 3 AM Shanghai time
      # - TZ=UTC          # Or use UTC explicitly
```

Default behavior: Uses system timezone if `TZ` is not set.

**How Auto-Selection Works:**

1. Daily at scheduled hour, fetch all free models from OpenRouter
2. Filter out code-focused models
3. Use Gemini 3 Flash to evaluate translation quality
4. Automatically switch to the best model

```yaml
translator:
  target_language: "Chinese"
  output_suffix: "chs"
```

**How it works:**

- Fetches free models from OpenRouter API (excluding code-focused models)
- Uses Gemini 3 Flash to evaluate which model is best for English→Chinese translation
- Automatically selects best model on startup and daily at 3 AM UTC
- Fallback chain: selected → last-known-good → fallback_model

**Benefits:**

- No manual tracking of free model changes
- Always uses best available free model
- Cost-effective: evaluation is free (Gemini 3 Flash)

#### Option 2: Gemini Direct

```yaml
redis:
  url: "redis://localhost:6379"
  queue: "translate_queue"

callback:
  url: "http://localhost:4664/api/v1/async_merge"

gemini:
  api_key: ""                       # Get from https://aistudio.google.com/apikey
  model: "gemini-2.5-flash-latest"
  instruction: ""                   # Custom translation instruction (optional)
  max_batch_size: 20                # Tune for performance
  rate_limit: 8                     # Depends on your Gemini plan

translator:
  target_language: "Chinese"
  output_suffix: "chs"
```

### Environment Variables

Override any config value using the format `FUSIONN_SUBS_<SECTION>_<KEY>`:

| Variable | Example | Description |
|----------|---------|-------------|
| `CONFIG_PATH` | `config/config.yaml` | Path to config file |
| `ENV` | `production` | Set to `production` for release mode |
| `FUSIONN_SUBS_REDIS_URL` | `redis://host:6379` | Redis connection URL |
| `FUSIONN_SUBS_REDIS_QUEUE` | `translate_queue` | Queue to consume from |
| `FUSIONN_SUBS_CALLBACK_URL` | `http://host/callback` | Callback endpoint |
| **OpenRouter** | | |
| `FUSIONN_SUBS_OPENROUTER_API_KEY` | `sk-or-...` | OpenRouter API key |
| `FUSIONN_SUBS_OPENROUTER_MODEL` | `openai/gpt-4o-mini` | OpenRouter model (ignored if auto_select_model is true) |
| `FUSIONN_SUBS_OPENROUTER_AUTO_SELECT_MODEL` | `true` | Enable auto model selection |
| `FUSIONN_SUBS_OPENROUTER_FALLBACK_MODEL` | `google/gemini-3-flash:free` | Fallback model for auto-selection |
| `FUSIONN_SUBS_OPENROUTER_EVALUATOR_PROVIDER` | `gemini` | Evaluator provider |
| `FUSIONN_SUBS_OPENROUTER_EVALUATOR_GEMINI_API_KEY` | `AIza...` | Gemini API key for evaluator |
| `FUSIONN_SUBS_OPENROUTER_EVALUATOR_SCHEDULE_HOUR` | `3` | Hour (0-23) for daily evaluation |
| **Gemini** | | |
| `FUSIONN_SUBS_GEMINI_API_KEY` | `AIza...` | Gemini API key |
| `FUSIONN_SUBS_GEMINI_MODEL` | `gemini-2.5-flash` | Gemini model |

## Project Structure

```
fusionn-subs/
├── cmd/fusionn-subs/        # Application entry point
├── config/                   # Configuration files
├── internal/
│   ├── client/
│   │   ├── callback/        # HTTP client for callbacks
│   │   └── openrouter/      # OpenRouter API client
│   ├── config/              # Viper config with hot-reload
│   ├── service/
│   │   ├── modelselection/  # Auto model selection service
│   │   │   ├── evaluator.go # Gemini-based model evaluator
│   │   │   └── selector.go  # Model selector with scheduling
│   │   ├── translator/      # Multi-provider translation service
│   │   │   ├── factory.go   # Provider selection
│   │   │   ├── openrouter.go # OpenRouter implementation
│   │   │   └── gemini.go    # Gemini implementation
│   │   └── worker/          # Redis queue consumer
│   ├── types/               # Domain types (JobMessage)
│   └── version/             # Version info
├── pkg/logger/              # Shared logger
├── .github/workflows/       # CI/CD (test, lint, docker)
├── Dockerfile
├── Makefile
└── docker-compose.yml
```

## Docker Workflow

### Build & Run

```bash
# Build image
make docker

# Run with docker-compose
make docker-run

# View logs
make docker-logs

# Stop
make docker-stop
```

### docker-compose.yml

```yaml
services:
  fusionn-subs:
    image: ghcr.io/weizsw/fusionn-subs:latest
    volumes:
      - /path/to/media:/data
      - ./config:/app/config:ro
    environment:
      ENV: production
      CONFIG_PATH: /app/config/config.yaml
      # Use OpenRouter (recommended)
      FUSIONN_SUBS_OPENROUTER_API_KEY: ${OPENROUTER_API_KEY}
      # OR use Gemini
      # FUSIONN_SUBS_GEMINI_API_KEY: ${GEMINI_API_KEY}
      TZ: Asia/Shanghai
```

## Development

```bash
# Run tests
make test

# Run linter
make lint

# Update dependencies
make tidy

# Build binary
make build
```

## CI/CD

- **ci.yml**: Runs on push/PR - builds, tests, lints, and pushes Docker image to GHCR
- **release.yml**: Runs on tags (v*) - builds and pushes to both GHCR and Docker Hub

## Architecture

1. **Worker** polls Redis queue for translation jobs
2. **Translator Factory** selects provider (OpenRouter or Gemini) based on config
3. **Translator** executes appropriate script (`llm-subtrans.sh` or `gemini-subtrans.sh`)
4. **Callback Client** POSTs result to configured endpoint

### Rate Limiting

Default rate limit is **10 requests/minute** (conservative, works for most providers).

Tune based on your provider plan:

- OpenRouter: Varies by model (check your plan)
- Gemini Free: 15 RPM
- Gemini Pro: Higher limits

### Migration from Gemini-only

Existing Gemini configurations continue to work without changes. To switch to OpenRouter:

1. Get API key from <https://openrouter.ai/>
2. Add `openrouter` section to config
3. Remove or comment out `gemini` section
4. Restart service

You can still use Gemini models via OpenRouter: `model: "google/gemini-2.0-flash-exp"`
