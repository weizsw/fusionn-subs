# Automatic Glossary Generation Design

**Date:** 2026-04-27
**Status:** Approved for spec review

## Problem

Subtitle translation quality drops when the translator does not understand recurring terminology, acronyms, organization names, character titles, or media-specific phrases. For example, a show may repeatedly use "SO15"; a translation model can preserve, mistranslate, or explain it inconsistently unless the prompt supplies glossary guidance.

`fusionn-subs` currently sends jobs directly from the worker to the configured translator. Translators pass configured instructions to `llm-subtrans`, but there is no persistent glossary, no per-series/movie terminology memory, and no pre-translation step that gathers relevant terminology.

## Goals

1. Add a fully automatic glossary preparation step before translation.
2. Store per-media glossary entries and common glossary entries in SQLite.
3. Generate glossary entries from subtitle-derived candidates with a separate, configurable LLM.
4. Inject a compact glossary block into `llm-subtrans` through existing provider instruction support.
5. Deduplicate repeated terms across episodes without storing noisy near-duplicate variants.
6. Promote repeated high-confidence terms into common glossary entries conservatively.
7. Preserve the current translation flow when glossary is disabled.
8. Keep package boundaries idiomatic for a Go service: concrete business logic, narrow interfaces at I/O boundaries, reusable storage infrastructure.

## Non-Goals

- Human review UI or admin API in v1.
- Direct upstream writes into the glossary database in v1.
- Replacing `llm-subtrans`.
- Building a general NLP framework.
- Using LangChainGo. The feature only needs a narrow "return strict JSON glossary entries" call.
- Sending the full subtitle text to the glossary LLM.

## High-Level Flow

1. The upstream client receives Sonarr/Radarr webhooks and pushes a Redis job to `fusionn-subs`.
2. The worker pops the job and validates it.
3. If glossary is enabled, the worker calls `GlossaryService.Prepare(ctx, job)`.
4. The glossary service resolves a media key from stable IDs when available, then falls back to normalized title/type/path.
5. It parses the subtitle with `go-astisub`, extracts candidate terms and short evidence snippets locally, and loads existing media/common glossary entries.
6. It sends only candidates, snippets, media metadata, and existing glossary context to the glossary LLM.
7. It stores generated entries in SQLite, merging compatible repeated observations into existing variants.
8. It runs conservative promotion from per-media entries to common glossary entries.
9. It builds a compact glossary instruction block from eligible entries.
10. The worker calls the translator with the original job plus the per-job glossary instruction block.
11. Translators append the glossary block to their configured instruction and pass the combined instruction to `llm-subtrans`.
12. The worker sends the normal callback after translation succeeds.

## Architecture

Use worker orchestration rather than hiding glossary behavior inside translators:

```text
Redis job -> worker -> glossary.Service -> translator -> callback
```

Business package:

```text
internal/service/glossary/
  service.go          # orchestration
  media_key.go        # stable/fallback media key resolution
  extractor.go        # candidate extraction from parsed subtitle text
  prompt_builder.go   # glossary instruction block selection/formatting
  promoter.go         # media-to-common promotion
  types.go            # domain structs
  store.go            # Store interface
  llm.go              # LLMClient interface
```

Reusable infrastructure:

```text
internal/storage/sqlite/
  db.go               # shared SQLite opener, pragmas, migrations
  glossary.go         # glossary.Store implementation
  migrations/*.sql    # embedded SQL migrations
```

LLM clients can start glossary-specific:

```text
internal/service/glossary/llm_openai.go
internal/service/glossary/llm_gemini.go
```

If another service later needs LLM calls, these clients can move to `internal/llm` behind a stable interface.

The worker depends on `glossary.Service`. Translators do not know how terms are generated or stored; they only receive final extra instruction text.

## Translator API Change

Extend translation input so the worker can pass per-job context:

```go
type Request struct {
    Job              types.JobMessage
    ExtraInstruction string
}

type Translator interface {
    Translate(ctx context.Context, req Request) (string, error)
}
```

Provider implementations append `ExtraInstruction` after their configured instruction before passing `--instruction` to `llm-subtrans`.

Compatibility rule: an empty `ExtraInstruction` must produce the same behavior as today.

## Job Message Identity

Extend `types.JobMessage` with optional fields. Existing producers remain compatible because these fields are optional.

Suggested additions:

```go
MediaID      string            `json:"media_id,omitempty"`
SourceSystem string            `json:"source_system,omitempty"` // sonarr, radarr, etc.
ExternalIDs  map[string]string `json:"external_ids,omitempty"`  // tmdb, imdb, tvdb, sonarr, radarr
Season       int               `json:"season,omitempty"`
Episode      int               `json:"episode,omitempty"`
```

Media key resolution priority:

1. Stable external IDs: `tvdb`, `tmdb`, `imdb`.
2. Sonarr/Radarr IDs when present.
3. Explicit `media_id` scoped by `source_system`.
4. Normalized `media_type + media_title`.
5. Hashed normalized path fallback.

The resolver records the source of the chosen key for logs and job metadata.

## Subtitle Parsing And Candidate Extraction

Use `github.com/asticode/go-astisub` for subtitle parsing. Do not hand-roll SRT parsing.

The extractor owns only project-specific logic:

- Strip formatting and collect cue text from parsed subtitle items.
- Normalize whitespace.
- Extract acronyms and mixed alpha-numeric terms such as `SO15`.
- Extract repeated proper nouns and short capitalized phrases.
- Extract likely organization/title terms.
- Track frequency and cue positions.
- Select bounded snippets around occurrences.

Performance guards:

- `max_subtitle_bytes`
- `max_cues`
- `max_candidates`
- `max_snippets_per_candidate`
- per-job extraction timeout through context

If parsing or extraction fails, glossary generation for that job is skipped and translation continues with any existing eligible glossary entries.

## Glossary LLM

Glossary extraction uses separate config from translation providers.

Interface:

```go
type LLMClient interface {
    GenerateGlossary(ctx context.Context, req GenerateRequest) (GenerateResponse, error)
}
```

The LLM request includes:

- target language
- media title/type/season/episode when available
- existing relevant glossary entries
- extracted candidates with frequency and snippets
- strict JSON response instructions

It does not include the full subtitle text.

Expected response fields:

- `source_term`
- `normalized_term`
- `target_language`
- `target_text`
- `definition`
- `translation_mode`: `translate`, `preserve`, `transliterate`, `contextual`
- `category`: `acronym`, `organization`, `character`, `place`, `technical_term`, `phrase`
- `confidence`
- `evidence`

Provider support:

- `openai_compatible`: OpenRouter, local LLM servers, vLLM, Ollama-compatible servers.
- `gemini`: direct Gemini REST through the official Go client.

Use JSON decoding with validation. Invalid JSON, missing required fields, or out-of-range confidence fails glossary generation for that job but does not fail translation.

## Config

Add a `glossary` section:

```yaml
glossary:
  enabled: false # set true to run glossary preparation before translation
  db_path: "/app/data/glossary.db"
  target_language: "zh-Hans"
  min_confidence: 0.75
  inject_min_confidence: 0.80
  max_prompt_entries: 30
  max_candidates: 80
  max_snippets_per_candidate: 3
  max_subtitle_bytes: 1048576
  max_cues: 3000
  max_active_variants_per_term: 3
  max_observations_per_variant: 10
  promote_min_confidence: 0.85
  promote_min_media_count: 3
  llm:
    provider: "openai_compatible"
    base_url: "http://127.0.0.1:8045"
    endpoint: "/v1/chat/completions"
    api_key: ""
    model: "qwen3:8b"
    timeout: 60s
    temperature: 0.1
```

Rules:

- `enabled=false` preserves current behavior.
- Default to `enabled=false` for compatibility; deployments opt in by setting it to `true`.
- DB path changes require restart.
- Thresholds and LLM settings may be hot-reloaded if the existing config manager can update the service safely.
- If `target_language` is empty, default to `translator.target_language`; for English-to-Chinese deployments prefer `zh-Hans`.

## SQLite Storage

Use SQLite through `database/sql`. The store interface belongs to the glossary service package; the SQLite implementation belongs to `internal/storage/sqlite`.

Driver choice:

- Use `modernc.org/sqlite` for v1 because it is CGO-free and avoids container/cross-build complexity.
- Do not use `github.com/mattn/go-sqlite3` in v1 because it requires CGO, a C compiler, and more careful runtime compatibility.
- Keep SQL behind `database/sql` and the `glossary.Store` interface so changing drivers later is contained.

Open behavior:

- Open once at startup.
- Enable WAL.
- Set busy timeout.
- Enable foreign keys.
- Set a small connection pool appropriate for a single-worker service.
- Run migrations at startup.

DB init, migration, or integrity-check failure is fatal when glossary is enabled.

## Schema

Tables:

### `glossary_terms`

- `id`
- `scope`: `media` or `common`
- `media_key`: nullable for common
- `normalized_term`
- `display_term`
- `created_at`
- `updated_at`
- `last_seen_at`

Unique lookup:

- partial unique index on `scope, media_key, normalized_term` where `scope = 'media'`
- partial unique index on `scope, normalized_term` where `scope = 'common'`

### `glossary_variants`

- `id`
- `term_id`
- `target_language`
- `target_text`
- `definition`
- `translation_mode`
- `category`
- `status`: `active`, `suppressed`, `candidate`
- `source`: `generated`, `promoted`, `curated`
- `confidence`
- `evidence_count`
- `created_at`
- `updated_at`
- `last_seen_at`

Indexes:

- `term_id, target_language, status, confidence`
- `target_language, status, source`

### `glossary_observations`

- `id`
- `variant_id`
- `job_id`
- `media_key`
- `subtitle_path_hash`
- `season`
- `episode`
- `snippet`
- `confidence`
- `created_at`

Indexes:

- `variant_id, created_at`
- `job_id`
- `media_key, subtitle_path_hash`

### `glossary_jobs`

- `job_id`
- `media_key`
- `subtitle_path_hash`
- `status`
- `error`
- `created_at`
- `completed_at`

## Deduplication And Variants

Repeated observations should not create repeated glossary entries.

Dedup key starts with:

```text
scope + media_key + normalized_term + target_language
```

When a new LLM result arrives:

1. Find the term row or create it.
2. Compare the result against existing variants for that term.
3. Merge into an existing variant if target text and definition are compatible.
4. Increment `evidence_count`.
5. Update `last_seen_at`.
6. Raise confidence with a capped rolling score.
7. Add a bounded observation snippet if useful.
8. Create a new variant only when the meaning or target text is materially different.
9. If confidence is below `min_confidence`, store the variant as `candidate` rather than `active`.
10. Keep at most `max_active_variants_per_term` generated active variants; weaker extras become `suppressed`.
11. Keep at most `max_observations_per_variant` recent snippets per variant while preserving aggregate `evidence_count`.

Compatibility for v1 can be deterministic and conservative:

- normalized target text exact match -> merge
- same `translation_mode` and high textual overlap in definition -> merge
- otherwise create variant if under cap

This avoids accumulating one row per episode when the LLM phrases the same meaning slightly differently.

## Common Glossary Promotion

Generated entries start as per-media entries.

Promotion creates or updates common glossary candidates only when:

- the same normalized term appears in at least `promote_min_media_count` distinct media keys
- winning variants are high confidence
- target language matches
- target text/definition are compatible
- no strong cross-media conflict exists

Default recommended thresholds:

- `promote_min_media_count >= 3`
- `promote_min_confidence >= 0.85`

When conflicts exist, do not promote globally. Per-media entries continue to win during prompt injection.

## Prompt Injection

Build a compact instruction block:

```text
Glossary guidance for this subtitle:
- SO15: keep as "SO15"; definition: London police counter-terrorism unit.
- DCI: translate as the configured Chinese detective-rank term; definition: Detective Chief Inspector.
```

Selection priority:

1. Same media key entries.
2. Entries with `source = curated`.
3. Generated/promoted entries with `confidence >= inject_min_confidence`.
4. Higher confidence.
5. Higher evidence count.
6. Newer `last_seen_at` as tie-breaker.
7. Common entries fill gaps after media-specific entries.

Only one winning variant per `normalized_term` is injected. The prompt block is capped by `max_prompt_entries` and an implementation token/character estimate.

## Error Handling

Startup:

- If glossary is enabled and SQLite cannot open, migrate, or pass integrity checks, startup fails.
- If glossary is disabled, no SQLite connection is required.

Per job:

- Subtitle parse/extraction failure: log warning, skip new generation, continue with existing glossary if available.
- Glossary LLM failure: log warning, continue with existing glossary if available.
- Invalid LLM JSON: log warning, continue with existing glossary if available.
- SQLite transaction failure or corruption signal: fail the job.
- Translation failures remain handled by the existing translator/fallback/retry behavior.

If existing eligible glossary entries can be loaded successfully, they may be injected even when new generation fails.

## Observability

Log counts and decisions, not full subtitle text:

- media key and resolver source
- candidates extracted
- existing entries loaded
- entries returned by LLM
- variants created, merged, suppressed
- common promotions created or skipped
- prompt entries injected
- glossary preparation status per job

Store job-level status in `glossary_jobs`.

Do not log snippets or full prompt blocks by default. A later explicit debug option can add redacted prompt logging if needed.

## Dependency Choices

Use existing libraries for generic infrastructure and parsing:

- `github.com/asticode/go-astisub` for subtitle parsing. Latest checked: `v0.40.0`, Go 1.13 module.
- `modernc.org/sqlite` for SQLite driver. Pin a Go 1.23-compatible version such as `v1.38.2`; latest checked required Go 1.25.
- `github.com/pressly/goose/v3` for embedded SQL migrations. Pin a Go 1.23-compatible version such as `v3.24.3`; latest checked required Go 1.25.7.
- `github.com/openai/openai-go/v3` for OpenAI-compatible providers. Latest checked: `v3.32.0`, Go 1.22 module, supports custom base URL.
- `google.golang.org/genai` for Gemini if direct Gemini glossary extraction is included in v1. Pin a Go 1.23-compatible version such as `v1.0.0`; latest checked required Go 1.24.

Do not use `prose` for v1 candidate extraction. It is general-purpose NLP, while this feature needs domain-specific bounded heuristics for subtitle terminology.

Implementation must verify pinned dependency compatibility with:

- `go test ./...`
- Docker build
- runtime startup with glossary enabled

## Testing

Unit tests:

- media key resolution priority and fallback behavior
- candidate extraction from parsed subtitle cues
- candidate limits and snippet limits
- variant merge vs new-variant decisions
- variant cap and suppression behavior
- prompt entry ranking and formatting
- promotion rules
- LLM JSON response validation

Storage tests:

- SQLite migrations on temp DB
- term/variant upsert behavior
- transaction rollback on errors
- selection queries for prompt building
- promotion queries across distinct media keys

Service tests:

- fake LLM client returns entries
- LLM failure continues with existing glossary
- extraction failure continues with existing glossary
- DB failure fails the job

Translator/worker tests:

- `ExtraInstruction` appends to provider instruction
- empty extra instruction preserves old command behavior
- worker continues translation when glossary generation fails
- worker fails on glossary DB failure

## Rollout

1. Add config with `glossary.enabled=false` by default unless the user chooses otherwise.
2. Add storage and migrations.
3. Add glossary service with fake LLM tests.
4. Add real LLM client(s).
5. Extend translator request API.
6. Wire worker orchestration.
7. Update README/config example.
8. Verify with a local subtitle sample and inspect stored glossary entries.
