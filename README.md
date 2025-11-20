# fusionn-subs

Go worker that polls Redis for subtitle translation jobs, runs `gemini-subtrans.sh`, and posts callback payloads once the Chinese subtitles are generated.

## Quick start

```bash
make build
make test
docker compose up --build
```

## Configuration

All knobs are standard env vars (wired automatically in `docker-compose.yml`):

- `REDIS_URL`, `REDIS_QUEUE` – BRPOP source (default queue `translate_queue`)
- `CALLBACK_URL` – async merge endpoint to notify when translation finishes
- `GEMINI_API_KEY`, `GEMINI_MODEL` – passed through to `gemini-subtrans.sh`
- `TARGET_LANGUAGE` – fixed to `Chinese` by default
- `OUTPUT_SUFFIX` – replaces `.eng.srt` with `.chs.srt`
- `GEMINI_RATELIMIT` – per-minute cap for `--ratelimit` flag (default 8, safe for free Gemini 2.5 Flash usage)
- `POLL_TIMEOUT`, `SCRIPT_TIMEOUT`, `HTTP_TIMEOUT`, `HTTP_MAX_RETRIES`, `LOG_LEVEL`

## Docker workflow

```bash
docker compose up --build
```

Build stage compiles the Go worker, runtime stage runs `install.sh` from llm-subtrans so the generated `gemini-subtrans.sh` wrapper is identical to a manual setup.

See `docs/` (coming soon) for architecture notes.

