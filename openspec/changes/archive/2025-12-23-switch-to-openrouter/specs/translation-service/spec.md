# Translation Service Specification

## ADDED Requirements

### Requirement: OpenRouter Provider Support
The system SHALL support OpenRouter as the translation provider, allowing access to multiple LLM models through a unified API.

#### Scenario: OpenRouter configuration loaded
- **WHEN** the service starts with OpenRouter configuration
- **THEN** the translator service initializes with OpenRouter API credentials
- **AND** the configured model is validated

#### Scenario: OpenRouter API invocation
- **WHEN** a translation job is processed
- **THEN** the system invokes `llm-subtrans` command (which defaults to OpenRouter)
- **AND** passes the API key via `--apikey` flag
- **AND** passes the model via `--model` flag in `provider/model` format
- **AND** executes translation using the specified model

#### Scenario: OpenRouter model selection
- **WHEN** a user configures an OpenRouter model (e.g., `openai/gpt-4o-mini`)
- **THEN** the system accepts the provider/model format
- **AND** passes it correctly to the llm-subtrans script

### Requirement: Provider-Agnostic Configuration
The system SHALL use a provider-agnostic configuration structure that can support different LLM providers.

#### Scenario: Configuration hot-reload with OpenRouter
- **WHEN** the OpenRouter config file is modified (API key, model, or rate limits)
- **THEN** the system detects the change within 10 seconds
- **AND** reloads the configuration without restart
- **AND** logs the changed fields

#### Scenario: Environment variable override
- **WHEN** `FUSIONN_SUBS_OPENROUTER_API_KEY` environment variable is set
- **THEN** it overrides the config file value
- **AND** the API key is masked in logs

### Requirement: OpenRouter Error Handling
The system SHALL handle OpenRouter-specific errors and provide meaningful feedback.

#### Scenario: Invalid OpenRouter API key
- **WHEN** translation fails due to invalid API key
- **THEN** the error is logged with context
- **AND** the job is marked as failed
- **AND** the worker continues processing subsequent jobs

#### Scenario: OpenRouter rate limit exceeded
- **WHEN** the OpenRouter rate limit is exceeded
- **THEN** the system logs the rate limit error
- **AND** the job fails with appropriate error message

#### Scenario: Model not available
- **WHEN** the configured model is not available on OpenRouter
- **THEN** the system logs a clear error message
- **AND** suggests checking the model name format

### Requirement: Translation Service Configuration
The system SHALL load translation provider configuration from YAML with environment variable overrides, supporting multiple providers.

The system supports both Gemini and OpenRouter configurations:
- `gemini` section for Gemini direct API
- `openrouter` section for OpenRouter users
- System selects translator based on which config section has API key

OpenRouter configuration with `openrouter` section including:
- `api_key`: OpenRouter API key (required)
- `model`: Model identifier in `provider/model` format (e.g., `openai/gpt-4o-mini`) (required)
- `instruction`: Custom translation instructions (optional)
- `max_batch_size`: Batch size for subtitle translation (optional)
- `rate_limit`: Requests per minute limit (optional)

#### Scenario: Valid OpenRouter configuration
- **WHEN** config file contains valid OpenRouter section
- **THEN** configuration loads successfully
- **AND** all OpenRouter parameters are accessible
- **AND** API key is required and validated

#### Scenario: Missing required OpenRouter fields
- **WHEN** config is missing `openrouter.api_key`
- **THEN** validation fails with clear error message
- **AND** the service does not start

### Requirement: Script Invocation
The system SHALL invoke the llm-subtrans translation script with appropriate parameters.

The system invokes translation scripts based on provider:
- OpenRouter: Uses `llm-subtrans.sh` (defaults to OpenRouter provider)
- Gemini: Uses `gemini-subtrans.sh` (Gemini-specific script)

Common script invocation:
- Script path: `/opt/llm-subtrans/llm-subtrans.sh` or `/opt/llm-subtrans/gemini-subtrans.sh`
- Passes `--model` in `provider/model` format (e.g., `google/gemini-2.0-flash-exp`)
- Passes `--apikey` for authentication
- Passes `-l` or `--target_language` for target language
- Supports optional `--ratelimit`, `--maxbatchsize`, `--instruction` parameters
- Passes `-o` for output path

#### Scenario: OpenRouter script invocation
- **WHEN** processing a translation job
- **THEN** the system executes `/opt/llm-subtrans/llm-subtrans.sh`
- **AND** passes model in `provider/model` format
- **AND** passes all configured parameters
- **AND** sets appropriate working directory

#### Scenario: API key security in process list
- **WHEN** the translation script is invoked
- **THEN** the API key is passed via environment variable
- **AND** the API key is not visible in process arguments
- **AND** logs mask the API key value

### Requirement: Multi-Provider Translator Selection
The system SHALL support multiple translation providers with automatic selection based on configuration.

#### Scenario: Provider selection via factory
- **WHEN** the service initializes with config containing provider sections
- **THEN** the factory pattern selects the appropriate translator
- **AND** OpenRouter is chosen if `openrouter.api_key` is present
- **AND** Gemini is chosen if `gemini.api_key` is present and no OpenRouter config
- **AND** an error is returned if no valid provider config exists

#### Scenario: Backward compatibility with Gemini
- **WHEN** existing Gemini configuration is used without OpenRouter config
- **THEN** the system creates a GeminiTranslator
- **AND** continues to work exactly as before
- **AND** uses gemini-subtrans.sh script

#### Scenario: OpenRouter with llm-subtrans.sh
- **WHEN** OpenRouter configuration is present
- **THEN** the system creates an OpenRouterTranslator
- **AND** uses llm-subtrans.sh script
- **AND** passes provider/model format parameters

### Requirement: Conservative Rate Limiting
The system SHALL use conservative default rate limits that work across all providers.

#### Scenario: Default rate limit
- **WHEN** no rate limit is specified in config
- **THEN** the system defaults to 10 requests per minute
- **AND** this is safe for most provider plans

#### Scenario: Provider-specific rate limits
- **WHEN** rate limit is configured per provider section
- **THEN** each translator uses its configured rate limit
- **AND** users can tune based on their specific plan

