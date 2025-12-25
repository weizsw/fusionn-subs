# Change: Fix Gemini Evaluation to Return Only Model ID

## Why

The Gemini evaluator is returning lengthy explanations instead of just the model ID, despite the prompt explicitly requesting "Respond with ONLY the model ID". This happens because:

1. **Google Search grounding is enabled** - This encourages detailed research-style responses
2. **System instruction emphasizes reasoning** - "Use deep reasoning and research capabilities" contradicts the "only model ID" instruction
3. **No response format constraint** - The API has no schema enforcement for simple text output

When testing the same prompt directly on Gemini website (without search grounding), it correctly returns only the model ID. The issue is specific to how we're configuring the API call.

**Current behavior**: Returns 500+ word explanation with model ID buried inside  
**Expected behavior**: Returns just `google/gemini-2.0-flash-exp:free`

## What Changes

- **REMOVED**: Google Search tool from generation config (not needed for this task)
- **MODIFIED**: System instruction to emphasize terse output over reasoning
- **MODIFIED**: Prompt template to be more explicit about format requirements
- **MODIFIED**: Template to output models in compact format (remove extra newlines between models)
- **ADDED**: Response format guidance using Gemini's `response_mime_type` (if supported)
- **ADDED**: Stronger post-processing to extract model ID from verbose responses

## Impact

- **NON-BREAKING**: Improves reliability of model selection
- **BEHAVIOR CHANGE**: Gemini will return concise responses instead of verbose explanations
- Affected specs: `model-selection`
- Affected code:
  - `internal/service/modelselection/evaluator.go` - Remove googleSearch tool, update system instruction
  - `internal/service/modelselection/prompts/model_evaluation.tmpl` - Strengthen output format requirements

## Migration Path

No migration needed - this is a bug fix. Users will see:
- Faster evaluation (less tokens generated)
- More reliable model ID extraction
- Lower API costs (fewer output tokens)

