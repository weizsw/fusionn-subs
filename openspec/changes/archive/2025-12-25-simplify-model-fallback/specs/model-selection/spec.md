# Model Selection Service Specification Deltas

## MODIFIED Requirements

### Requirement: Automatic Free Model Selection
The system SHALL automatically select the best free OpenRouter model for English-to-Chinese subtitle translation.

#### Scenario: Startup model selection
- **WHEN** the service starts with `auto_select_model: true`
- **THEN** the system fetches free models from OpenRouter API
- **AND** filters out code-focused models
- **AND** uses Gemini evaluator to select best model
- **AND** blocks startup until model selected or timeout (30s)
- **AND** falls back to `model` if evaluation fails

#### Scenario: Daily re-evaluation
- **WHEN** the scheduler triggers at 3 AM UTC
- **THEN** the system fetches latest free models
- **AND** evaluates with Gemini
- **AND** updates translator if model changed
- **AND** logs the model change

#### Scenario: Evaluation failure fallback
- **WHEN** model evaluation fails during operation
- **THEN** the system uses last known good model
- **AND** if no last known model, uses `model`
- **AND** logs the failure with details

### Requirement: Fallback Model Strategy
The system SHALL implement a simplified two-tier fallback using the configured `model` field.

#### Scenario: Two-tier fallback chain
- **WHEN** current selected model is unavailable
- **THEN** attempt to use last known good model
- **AND** if last known unavailable, use configured `model` field
- **AND** if all fail, log error and prevent service start

#### Scenario: Fallback model validation
- **WHEN** using `model` from config as fallback
- **THEN** validate it exists in free model list
- **AND** warn user if model is not free
- **AND** use anyway if explicitly configured

#### Scenario: Last known good persistence
- **WHEN** model evaluation succeeds
- **THEN** store selected model as last known good
- **AND** keep timestamp of selection
- **AND** clear on service restart (in-memory only)

