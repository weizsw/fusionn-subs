# Model Selection Service Specification Deltas

## MODIFIED Requirements

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

