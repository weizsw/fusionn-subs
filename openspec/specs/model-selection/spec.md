# Model Selection Service Specification

## Purpose

Automatically select the best free OpenRouter model for English-to-Chinese subtitle translation by evaluating available models using AI-driven criteria. This eliminates manual model selection and adapts to OpenRouter's rapidly changing free model landscape.
## Requirements
### Requirement: Automatic Free Model Selection
The system SHALL automatically select the best free OpenRouter model for English-to-Chinese subtitle translation.

#### Scenario: Startup model selection
- **WHEN** the service starts with `auto_select_model: true`
- **THEN** the system fetches free models from OpenRouter API
- **AND** filters out code-focused models
- **AND** uses Gemini evaluator to select best model
- **AND** blocks startup until model selected or timeout (30s)
- **AND** falls back to `fallback_model` if evaluation fails

#### Scenario: Daily re-evaluation
- **WHEN** the scheduler triggers at 3 AM UTC
- **THEN** the system fetches latest free models
- **AND** evaluates with Gemini
- **AND** updates translator if model changed
- **AND** logs the model change

#### Scenario: Evaluation failure fallback
- **WHEN** model evaluation fails during operation
- **THEN** the system uses last known good model
- **AND** if no last known model, uses `fallback_model`
- **AND** logs the failure with details

### Requirement: OpenRouter Model API Integration
The system SHALL fetch and parse free models from OpenRouter's API.

#### Scenario: Fetch free models
- **WHEN** model selection is triggered
- **THEN** the system calls `GET https://openrouter.ai/api/v1/models`
- **AND** filters models with `:free` suffix
- **AND** parses model metadata (name, context length, description)
- **AND** returns structured model list

#### Scenario: Filter code models
- **WHEN** processing model list
- **THEN** the system excludes models containing "code", "coder", "coding", "programmer" in name or description
- **AND** logs excluded models for transparency

#### Scenario: API error handling
- **WHEN** OpenRouter API is unreachable
- **THEN** the system retries up to 3 times with exponential backoff
- **AND** logs detailed error
- **AND** falls back to last known model list if available

### Requirement: Gemini-based Model Evaluator
The system SHALL use Gemini to evaluate and select the best model for translation, returning only the model ID without explanations.

**Previous**: System instruction encouraged "deep reasoning and research" with Google Search enabled, resulting in verbose explanations instead of concise model IDs.

**Modified**: 
- System instruction emphasizes terse, direct output
- Google Search tool removed (not needed for model comparison)
- Stronger prompt formatting to enforce ID-only response
- Robust parsing to extract model ID even from verbose responses

#### Scenario: Evaluate model list
- **WHEN** evaluator receives filtered model list
- **THEN** it constructs prompt with model metadata
- **AND** calls Gemini API without search grounding
- **AND** system instruction emphasizes returning only model ID
- **AND** parses response to extract model ID
- **AND** validates model exists in provided list

#### Scenario: Terse response format
- **WHEN** Gemini is called for evaluation
- **THEN** the system instruction specifies "Respond with ONLY the model ID"
- **AND** Google Search tool is NOT enabled
- **AND** the prompt ends with clear format instructions
- **AND** examples show the exact expected format

#### Scenario: Verbose response handling
- **WHEN** Gemini returns explanation along with model ID
- **THEN** the system logs a warning about unexpected verbosity
- **AND** extracts the model ID using pattern matching
- **AND** validates the extracted ID against the model list
- **AND** succeeds if a valid model ID is found

#### Scenario: Invalid evaluator response
- **WHEN** Gemini returns invalid or missing model name
- **THEN** the system logs the raw response
- **AND** attempts to extract model ID using regex pattern
- **AND** retries evaluation once if extraction fails
- **AND** falls back if retry fails

### Requirement: Scheduled Model Re-evaluation
The system SHALL re-evaluate models daily at the configured hour **in the container's local timezone**.

**Previous**: Used hardcoded UTC timezone, ignoring system timezone settings.

**Modified**: 
- Respects the system's local timezone (determined by `TZ` environment variable)
- Falls back to system default timezone if `TZ` is not set
- Logs the detected timezone on startup for transparency
- Schedule hour is interpreted in local time, not UTC

#### Scenario: Daily schedule trigger
- **WHEN** the clock reaches the configured hour in local timezone
- **THEN** the system triggers model evaluation
- **AND** does not interrupt ongoing translations
- **AND** updates model only after current jobs complete

#### Scenario: Timezone detection and logging
- **WHEN** the service starts
- **THEN** detect and log the current timezone (e.g., "Asia/Shanghai")
- **AND** log the schedule time in local timezone (e.g., "3:00 Asia/Shanghai")
- **AND** use this timezone for all scheduling decisions

#### Scenario: Timezone handling
- **WHEN** scheduler initializes
- **THEN** it calculates next scheduled hour from current local time
- **AND** uses `time.Now()` instead of `time.Now().UTC()`
- **AND** respects daylight saving time (DST) changes automatically

#### Scenario: Concurrent evaluation protection
- **WHEN** evaluation is already running
- **AND** scheduler triggers again
- **THEN** the system skips the duplicate evaluation
- **AND** logs the skipped attempt

#### Scenario: UTC timezone behavior
- **WHEN** `TZ=UTC` is explicitly set
- **THEN** scheduler behaves identically to previous UTC-only behavior
- **AND** logs show "UTC" as the timezone

#### Scenario: Unset timezone fallback
- **WHEN** `TZ` environment variable is not set
- **THEN** use the system's default timezone
- **AND** log the detected timezone on startup
- **AND** warn if timezone cannot be determined

### Requirement: Fallback Model Strategy
The system SHALL implement three-tier fallback for model selection failures.

#### Scenario: Three-tier fallback chain
- **WHEN** current selected model is unavailable
- **THEN** attempt to use last known good model
- **AND** if last known unavailable, use configured fallback_model
- **AND** if all fail, log error and prevent service start

#### Scenario: Fallback model validation
- **WHEN** using fallback_model from config
- **THEN** validate it exists in free model list
- **AND** warn user if fallback is not free
- **AND** use anyway if explicitly configured

#### Scenario: Last known good persistence
- **WHEN** model evaluation succeeds
- **THEN** store selected model as last known good
- **AND** keep timestamp of selection
- **AND** clear on service restart (in-memory only)

### Requirement: Model Update Notification
The system SHALL notify components when selected model changes.

#### Scenario: Model change notification
- **WHEN** evaluation selects different model than current
- **THEN** log the change with old and new model names
- **AND** update translator configuration
- **AND** verify translator accepts new model

#### Scenario: No-op on same model
- **WHEN** evaluation selects same model as current
- **THEN** log "model unchanged"
- **AND** skip translator update
- **AND** update last evaluation timestamp

