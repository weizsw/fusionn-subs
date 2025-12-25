# Implementation Tasks

## 1. Update Configuration Structure
- [x] 1.1 Remove `FallbackModel` field from `OpenRouterConfig` struct in `config/config.go`
- [x] 1.2 Update `Validate()` to always require `model` field (even with auto-selection)
- [x] 1.3 Remove `fallback_model` from `SafeLogValues()`
- [x] 1.4 Update config error messages to reflect new behavior

## 2. Update Model Selector
- [x] 2.1 Change `SelectorConfig` to use `FallbackModel` → `DefaultModel` in `selector.go`
- [x] 2.2 Update selector validation to accept renamed field
- [x] 2.3 Change `s.fallbackModel` references to `s.defaultModel`
- [x] 2.4 Update fallback logs to say "using configured model" instead of "fallback model"
- [x] 2.5 Update `GetCurrentModel()` comment to reflect new fallback logic

## 3. Update Main Initialization
- [x] 3.1 Pass `cfg.OpenRouter.Model` as fallback in `main.go` (instead of `FallbackModel`)
- [x] 3.2 Update initialization comments

## 4. Update Configuration Files
- [x] 4.1 Remove `fallback_model` from `config/config.example.yaml`
- [x] 4.2 Update `model` field comment to clarify dual purpose
- [x] 4.3 Add comment explaining fallback behavior

## 5. Update Documentation
- [x] 5.1 Update `README.md` to remove `fallback_model` references
- [x] 5.2 Update fallback chain description: selected → last-known → model
- [x] 5.3 Update auto-selection example configuration

## 6. Validation
- [x] 6.1 Test config validation rejects missing `model` field
- [x] 6.2 Test fallback works when evaluation fails
- [x] 6.3 Test auto-selection uses selected model when successful
- [x] 6.4 Build and verify no regressions

