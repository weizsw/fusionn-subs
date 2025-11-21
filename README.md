# fusionn-subs

Go worker that polls Redis for subtitle translation jobs, runs `gemini-subtrans.sh`, and posts callback payloads once the Chinese subtitles are generated.

## Quick start

### Local Development

```bash
make build
make test
docker compose up --build
```

### Docker

You can pull the pre-built image from Docker Hub or GitHub Container Registry:

```bash
docker pull ghcr.io/weizsw/fusionn-subs:latest
```

## Configuration

All knobs are standard env vars (wired automatically in `docker-compose.yml`):

| Variable | Default | Description |
|----------|---------|-------------|
| `REDIS_URL` | `redis://redis:6379/0` | Connection string for Redis |
| `REDIS_QUEUE` | `translate_queue` | Redis List key to BRPOP jobs from |
| `CALLBACK_URL` | | Endpoint to POST results to after translation |
| `GEMINI_API_KEY` | | API Key for Gemini (Google AI Studio) |
| `GEMINI_MODEL` | `gemini-2.5-flash` | Model ID to use |
| `TARGET_LANGUAGE` | `zh` | Target language code |
| `OUTPUT_SUFFIX` | `chs` | Suffix for output file (e.g., `.chs.srt`) |
| `RATE_LIMIT` | `5` | Requests per minute (RPM) limit for Gemini |
| `GEMINI_MAX_BATCH_SIZE` | `30` | Max lines per batch for translation |
| `LOG_LEVEL` | `info` | Logging verbosity |
| `PUID` / `PGID` | `501` / `20` | User/Group ID for file permissions |
| `TZ` | `Asia/Shanghai` | Container timezone |

## Docker Workflow

### Build & Run

```bash
docker compose up --build
```

### CI/CD

The repository automatically builds and pushes Docker images to Docker Hub and GHCR on every release.

## Architecture

1. **Build Stage**: Compiles the Go worker binary.
2. **Runtime Stage**:
   - Installs Python runtime dependencies.
   - Clones `llm-subtrans` and runs its `install.sh`.
   - Runs the Go worker which wraps the `gemini-subtrans.sh` script.
