# Model Selection Service Specification

## ADDED Requirements

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
The system SHALL use Gemini to evaluate and select the best model for translation.

#### Scenario: Evaluate model list
- **WHEN** evaluator receives filtered model list
- **THEN** it constructs prompt with model metadata
- **AND** calls Gemini API with evaluation criteria
- **AND** parses single-line model name response
- **AND** validates model exists in provided list

#### Scenario: Evaluation prompt construction
- **WHEN** building evaluation prompt
- **THEN** include all model names with metadata
- **AND** specify translation quality requirements
- **AND** emphasize instruction-following capability
- **AND** request single model name output

#### Scenario: Invalid evaluator response
- **WHEN** Gemini returns invalid or missing model name
- **THEN** the system logs the raw response
- **AND** retries evaluation once
- **AND** falls back if retry fails

### Requirement: Scheduled Model Re-evaluation
The system SHALL re-evaluate models daily at 3 AM UTC.

#### Scenario: Daily schedule trigger
- **WHEN** the clock reaches 3 AM UTC
- **THEN** the system triggers model evaluation
- **AND** does not interrupt ongoing translations
- **AND** updates model only after current jobs complete

#### Scenario: Timezone handling
- **WHEN** scheduler initializes
- **THEN** it calculates next 3 AM UTC from current time
- **AND** accounts for DST changes
- **AND** reschedules after each trigger

#### Scenario: Concurrent evaluation protection
- **WHEN** evaluation is already running
- **AND** scheduler triggers again
- **THEN** the system skips the duplicate evaluation
- **AND** logs the skipped attempt

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

## MODIFIED Requirements

### Requirement: OpenRouter Translator Configuration
The system SHALL support both static and dynamic model configuration for OpenRouter.

**Previous**: Model was set at initialization and never changed.

**Modified**: Model can be updated dynamically when auto-selection is enabled:
- Static mode (default): `model` field in config, never changes
- Dynamic mode: `auto_select_model: true`, model selected by evaluator
- Dynamic model overrides static `model` field when enabled

#### Scenario: Dynamic model configuration
- **WHEN** `auto_select_model: true` in config
- **THEN** ignore static `model` field
- **AND** wait for model selector to provide model
- **AND** update translator when model changes

#### Scenario: Static model configuration (backward compatible)
- **WHEN** `auto_select_model: false` or not set
- **THEN** use `model` field from config
- **AND** never change model at runtime
- **AND** behave exactly as before

