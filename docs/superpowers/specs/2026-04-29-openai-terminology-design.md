# OpenAI Translation Provider and llm-subtrans Terminology Design

## Summary

fusionn-subs currently shells out to llm-subtrans for Gemini, OpenRouter, and local OpenAI-compatible custom servers. It does not expose llm-subtrans's first-class OpenAI provider. The automatic glossary integration also injects glossary entries as prose in `--instruction`, even though llm-subtrans now supports terminology maps through `--terminology` and `--build-terminology-map`.

This change adds a first-class `openai` translation provider backed by `gpt-subtrans.sh` and switches glossary delivery from instruction prose to structured terminology arguments. `--build-terminology-map` is enabled only when `glossary.enabled` is true.

## Goals

1. Add `openai` as a valid `translator.providers` value.
2. Use llm-subtrans's OpenAI CLI support instead of direct Go API calls.
3. Support official OpenAI and custom OpenAI instances through `api_base`.
4. Support llm-subtrans's `-httpx` OpenAI option when `api_base` is set.
5. Pass automatic glossary entries as repeatable `--terminology SOURCE::TRANSLATION` arguments.
6. Enable `--build-terminology-map` only for glossary-enabled jobs.
7. Preserve existing fallback, retry, logging, and glossary persistence behavior.

## Non-Goals

1. Do not replace the existing SQLite-backed glossary system with llm-subtrans's per-job terminology map.
2. Do not add direct OpenAI HTTP translation calls in Go.
3. Do not change OpenRouter, Gemini, or local LLM provider semantics except to accept structured terminology flags.
4. Do not persist terminology discovered by llm-subtrans's internal map back into SQLite.

## Current Context

Translation providers are created in `internal/service/translator/factory.go`. Provider-specific translators build command-line arguments and execute upstream scripts through the shared `executeScript` helper. Glossary preparation currently returns a string instruction block from `worker.GlossaryPreparer`, and the worker passes that string as `translator.Request.ExtraInstruction`.

The upstream llm-subtrans README documents:

1. OpenAI CLI usage through `gpt-subtrans`.
2. OpenAI flags `-k/--apikey`, `-b/--apibase`, `-httpx`, and `-m/--model`.
3. `--terminology` as a repeatable seed for `SOURCE::TRANSLATION` pairs or a terminology file path.
4. `--build-terminology-map` as an opt-in per-translation consistency map across batches.

## Design

### OpenAI Provider

Add an `OpenAIConfig` section to runtime config:

```yaml
openai:
  api_key: ""
  model: "gpt-5-mini"
  api_base: ""
  use_httpx: false
  instruction: ""
  rate_limit: 10
  max_batch_size: 20
  timeout: 30m
```

Add `ProviderOpenAI = "openai"` and include it in provider validation. When `openai` appears in `translator.providers`, validation requires:

1. Either `openai.api_key` or `OPENAI_API_KEY` is non-empty.
2. `openai.model` is non-empty.
3. `openai.use_httpx` is false unless `openai.api_base` is non-empty.

The new `OpenAITranslator` shells out to `gpt-subtrans.sh`. It uses an `OPENAI_SCRIPT_PATH` environment variable when present, otherwise `/opt/llm-subtrans/gpt-subtrans.sh`. It reuses `LLM_SUBTRANS_DIR` as the working directory, defaulting to `/opt/llm-subtrans`.

Command-line mapping:

```text
gpt-subtrans.sh <subtitle_path>
  -o <output_path>
  -l <target_language>
  -k <api_key>
  -m <model>
  -b <api_base>        # only when api_base is set
  -httpx               # only when use_httpx is true and api_base is set
  --moviename <title>  # only when media title is present
  --instruction <text> # only for configured translation style instructions
  --ratelimit <rpm>    # only when > 0
  --maxbatchsize <n>   # only when > 0
```

When `openai.api_key` is configured, the process environment includes `OPENAI_API_KEY=<api_key>` and the command includes `-k <api_key>`. When `openai.api_key` is empty but `OPENAI_API_KEY` already exists in the environment, the translator relies on upstream's environment lookup and does not add `-k`. The process environment always includes `PYTHONUNBUFFERED=1`. Logging must continue masking `-k` values when the flag is present.

### Docker Runtime

Update the llm-subtrans install step so the runtime image generates `llm-subtrans.sh`, `gemini-subtrans.sh`, and `gpt-subtrans.sh`. Add `OPENAI_SCRIPT_PATH=/opt/llm-subtrans/gpt-subtrans.sh` to the runtime environment.

Use upstream install script input that chooses command-line-only install and the "all except Bedrock" provider option. This generates the OpenAI and Gemini scripts required by this service while keeping the OpenRouter/default `llm-subtrans.sh`.

### Structured Terminology

Replace glossary-as-instruction handoff with structured terminology:

```go
type Terminology struct {
    Source string
    Target string
}

type Request struct {
    Job                 types.JobMessage
    ExtraInstruction    string
    Terminology         []Terminology
    BuildTerminologyMap bool
}
```

`ExtraInstruction` remains for configured provider style instructions and future non-glossary guidance. Glossary code no longer writes prose into `ExtraInstruction`.

Change the worker glossary interface from returning a string to returning a terminology payload:

```go
type GlossaryPayload struct {
    Terminology         []translator.Terminology
    BuildTerminologyMap bool
}

type GlossaryPreparer interface {
    Prepare(ctx context.Context, msg types.JobMessage) (GlossaryPayload, error)
}
```

When glossary is disabled, the worker passes the zero value: no terminology and `BuildTerminologyMap: false`.

When glossary is enabled, the glossary service:

1. Loads existing prompt-eligible entries for the media and common scopes.
2. Extracts candidates and attempts LLM generation as it does today.
3. Stores generated entries and promotes common entries as it does today.
4. Reloads prompt-eligible entries.
5. Converts selected entries to `Terminology{Source, Target}`.
6. Sets `BuildTerminologyMap: true`.

Selection rules remain the same as today's prompt builder:

1. Entry must be active.
2. Normalized source term and target text must be non-empty.
3. Non-curated entries must meet `inject_min_confidence`.
4. Media-scoped entries must match the current media key.
5. Common entries remain eligible.
6. At most `max_prompt_entries` terms are passed.
7. Ranking continues to prefer matching media entries, curated entries, confidence, evidence count, and recency.

Use the display term as the CLI source when present; otherwise use the normalized term. Use target text as the CLI target. Translation mode and definition are not passed because llm-subtrans's terminology seed accepts only `SOURCE::TRANSLATION` pairs. Preserve mode/definition in SQLite for future glossary generation and promotion decisions.

### Terminology CLI Helper

Add a shared helper in the translator package that appends terminology flags to provider args:

```text
--terminology <source>::<target>
--build-terminology-map
```

Rules:

1. Repeat `--terminology` once per non-empty source/target pair.
2. Skip invalid blank pairs defensively.
3. Append `--build-terminology-map` only when `BuildTerminologyMap` is true.
4. If glossary is disabled, no terminology-related flags are emitted.

This helper is used by OpenAI, OpenRouter, Gemini, and local LLM translators.

### Data Flow

1. Worker receives a translation job.
2. Worker calls glossary preparation when a glossary service is configured.
3. Disabled glossary produces no terminology flags.
4. Enabled glossary loads/generates/stores/reloads entries and returns a structured terminology payload.
5. Worker calls the selected translator with the job, provider style instruction, terminology pairs, and build-map flag.
6. Translator builds provider-specific llm-subtrans arguments.
7. Translator appends shared terminology args.
8. llm-subtrans uses seeded terminology and its own per-job terminology map for batch consistency.

## Error Handling

OpenAI provider errors use the existing `executeScript` path and fallback behavior. Script failures, missing output files, and provider errors behave like existing translators.

Glossary error behavior remains unchanged:

1. SQLite open, migration, corruption, transaction, load, store, promotion, or job-recording failures fail the job.
2. Subtitle extraction failures do not block translation; existing loaded terminology may still be passed.
3. Glossary LLM generation failures do not block translation; existing loaded terminology may still be passed.

Config validation rejects unsupported providers, duplicate providers, invalid OpenAI settings, and invalid glossary settings before the worker starts.

## Testing

Add or update tests for:

1. `openai` provider validation success.
2. Missing both `openai.api_key` and `OPENAI_API_KEY` when `openai` is configured.
3. Missing `openai.model` when `openai` is configured.
4. `openai.use_httpx: true` without `openai.api_base`.
5. Factory support for `translator.providers: ["openai"]`.
6. OpenAI argument construction, including `-b`, `-httpx`, and key masking.
7. Shared terminology arg construction.
8. Worker passing terminology payload instead of glossary instruction.
9. Glossary conversion from selected entries to terminology pairs.
10. No terminology flags when glossary is disabled.
11. Existing translation fallback tests still pass.

Run:

```sh
go test ./...
```

## References

1. llm-subtrans README: https://github.com/machinewrapped/llm-subtrans
2. OpenAI API models documentation: https://platform.openai.com/docs/models
