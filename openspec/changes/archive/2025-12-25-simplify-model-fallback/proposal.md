# Change: Simplify Model Fallback by Reusing Existing Model Field

## Why

The current configuration has **redundant** model fields:
- `model`: Used when `auto_select_model` is disabled (required field)
- `fallback_model`: Used when `auto_select_model` is enabled and evaluation fails (separate field)

This creates unnecessary complexity and confusion:
1. **Users must specify both fields** even though `model` is always required
2. **Duplicate purpose**: Both serve as the "static model to use"
3. **Config bloat**: Extra field that doesn't add meaningful value
4. **Inconsistent behavior**: Different fields used depending on auto-selection state

**Better design**: 
- If `auto_select_model: false` → use `model`
- If `auto_select_model: true` and evaluation succeeds → use selected model
- If `auto_select_model: true` and evaluation **fails** → **fallback to `model`**

This makes `model` dual-purpose: the primary model when auto-selection is off, and the safety fallback when it's on.

## What Changes

- **REMOVED**: `fallback_model` field from `OpenRouterConfig`
- **MODIFIED**: Auto-selection fallback logic to use `model` instead of `fallback_model`
- **MODIFIED**: Config validation to always require `model` field (even with auto-selection)
- **MODIFIED**: Documentation to reflect simplified configuration
- **MODIFIED**: Logs to clarify that `model` is used as fallback

## Impact

- **BEHAVIOR CHANGE**: Users with `auto_select_model: true` must ensure `model` field is set (was already best practice)
- **BREAKING**: Configs using `fallback_model` will need to be updated
- **MIGRATION**: Copy `fallback_model` value to `model` field
- Affected specs: `model-selection`
- Affected code:
  - `internal/config/config.go` - Remove `FallbackModel` field, update validation
  - `internal/service/modelselection/selector.go` - Use `model` instead of `fallbackModel`
  - `cmd/fusionn-subs/main.go` - Pass `model` as fallback
  - `config/config.example.yaml` - Remove `fallback_model` field
  - `README.md` - Update documentation

## Migration Path

**Before** (old config):
```yaml
openrouter:
  model: "openai/gpt-4o-mini"          # Only used if auto_select_model: false
  auto_select_model: true
  fallback_model: "google/gemini-3-flash:free"  # Separate fallback field
```

**After** (new config):
```yaml
openrouter:
  model: "google/gemini-3-flash:free"  # Used when auto-selection is off OR as fallback
  auto_select_model: true
  # No fallback_model needed - 'model' serves as fallback!
```

**Migration steps**:
1. If `auto_select_model: true`, copy `fallback_model` value to `model`
2. Remove `fallback_model` field
3. Restart service

**Validation**: Service will reject configs missing `model` field with clear error message.

