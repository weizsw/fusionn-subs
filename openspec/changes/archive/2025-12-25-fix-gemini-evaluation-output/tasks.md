# Implementation Tasks

## 1. Remove Google Search Tool
- [x] 1.1 Remove `googleSearch` tool from request body in `evaluator.go:173-177`
- [x] 1.2 Add comment explaining why search is not needed for model selection

## 2. Update System Instruction
- [x] 2.1 Change system instruction from "Use deep reasoning and research capabilities" to emphasize terse output
- [x] 2.2 Add explicit instruction: "Respond with ONLY the model ID, no explanations"

## 3. Strengthen Prompt Template
- [x] 3.1 Remove newlines between models in template (compact list format)
- [x] 3.2 Move "Respond with ONLY" instruction to the very end of prompt
- [x] 3.3 Add multiple examples showing the exact format
- [x] 3.4 Add explicit "DO NOT include explanations, reasoning, or additional text"
- [x] 3.5 Consider adding separator like "---" before final instruction

## 4. Improve Response Parsing
- [x] 4.1 Add robust extraction logic to handle verbose responses
- [x] 4.2 Look for model ID pattern (provider/model:free or provider/model format)
- [x] 4.3 Extract first matching model ID from response if explanation is present
- [x] 4.4 Log warning if response contains explanation (indicates prompt not followed)

## 5. Validation
- [x] 5.1 Test with current free models list
- [x] 5.2 Verify response is concise (< 100 characters)
- [x] 5.3 Build and verify no regressions

