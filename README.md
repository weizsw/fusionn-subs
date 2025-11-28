# fusionn-subs

Go worker that polls Redis for subtitle translation jobs, runs `gemini-subtrans.sh`, and posts callback payloads once the Chinese subtitles are generated.

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

### Config File

Copy `config/config.example.yaml` to `config/config.yaml`:

```yaml
redis:
  url: "redis://localhost:6379"
  queue: "translate_queue"

callback:
  url: "http://localhost:4664/api/v1/async_merge"
  timeout: 15s
  max_retries: 3

gemini:
  api_key: ""  # Set via FUSIONN_SUBS_GEMINI_API_KEY
  model: "gemini-2.5-flash-latest"
  script_path: "/opt/llm-subtrans/gemini-subtrans.sh"
  working_dir: "/opt/llm-subtrans"
  instruction: ""
  max_batch_size: 20
  rate_limit: 8
  timeout: 15m

translator:
  target_language: "Chinese"
  output_suffix: "chs"

worker:
  poll_timeout: 5s
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
| `FUSIONN_SUBS_GEMINI_API_KEY` | `AIza...` | Gemini API key |
| `FUSIONN_SUBS_GEMINI_MODEL` | `gemini-2.5-flash` | Model to use |

## Project Structure

```
fusionn-subs/
├── cmd/fusionn-subs/        # Application entry point
├── config/                   # Configuration files
├── internal/
│   ├── client/callback/     # HTTP client for callbacks
│   ├── config/              # Viper config with hot-reload
│   ├── service/
│   │   ├── translator/      # Gemini translation service
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
      FUSIONN_SUBS_GEMINI_API_KEY: ${GEMINI_API_KEY}
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
2. **Translator** executes `gemini-subtrans.sh` with job parameters
3. **Callback Client** POSTs result to configured endpoint

## License

MIT
