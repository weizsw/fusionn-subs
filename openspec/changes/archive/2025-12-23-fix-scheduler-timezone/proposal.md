# Change: Fix Model Selection Scheduler to Respect Container Timezone

## Why

The model selection scheduler is currently hardcoded to use UTC timezone, ignoring the container's `TZ` environment variable. This causes confusion when users set `schedule_hour: 3` expecting 3 AM in their local timezone (e.g., `TZ=Asia/Shanghai`), but the evaluation actually runs at 3 AM UTC (11 AM Shanghai time).

This is a bug that breaks user expectations and makes the `schedule_hour` configuration misleading. Users deploying in different timezones cannot control when the evaluation runs relative to their local time.

## What Changes

- **MODIFIED**: Scheduler uses local timezone (`time.Now()`) instead of forcing UTC (`time.Now().UTC()`)
- **MODIFIED**: Log messages clarify timezone being used (e.g., "3:00 Asia/Shanghai" instead of "3:00 UTC")
- **MODIFIED**: Documentation updated to explain that `schedule_hour` respects container's `TZ` environment variable
- **ADDED**: Log the detected timezone on service startup for transparency

## Impact

- **BEHAVIOR CHANGE**: Existing deployments with `TZ` set to non-UTC will see evaluation time shift to their local timezone
  - Example: `TZ=Asia/Shanghai` with `schedule_hour: 3` → evaluation moves from 11 AM to 3 AM Shanghai time
- **BREAKING**: Users relying on UTC behavior will need to adjust their `schedule_hour` or set `TZ=UTC` explicitly
- Affected specs: `model-selection`
- Affected code:
  - `internal/service/modelselection/selector.go` - Change `time.Now().UTC()` to `time.Now()`
  - `cmd/fusionn-subs/main.go` - Update log messages
  - `config/config.example.yaml` - Update comment to mention timezone
  - `README.md` - Document timezone behavior

## Migration Path

**For users who want to keep UTC behavior:**
```yaml
environment:
  - TZ=UTC  # Explicitly set UTC timezone
```

**For users who want local time (most common case):**
```yaml
environment:
  - TZ=Asia/Shanghai  # Or any timezone
openrouter:
  evaluator:
    schedule_hour: 3  # Now evaluates at 3 AM Shanghai time
```

**Recommendation:** Most users should set their container's `TZ` to their local timezone for intuitive behavior.

