# Implementation Tasks

## 1. Core Scheduler Changes
- [x] 1.1 Change `selector.go:166` from `time.Now().UTC()` to `time.Now()`
- [x] 1.2 Update `selector.go:70` log message to show detected timezone
- [x] 1.3 Add startup log showing timezone (e.g., "🕐 Using timezone: Asia/Shanghai (UTC+8)")
- [x] 1.4 Update `cmd/fusionn-subs/main.go:110` to show timezone-aware message

## 2. Documentation Updates
- [x] 2.1 Update `config/config.example.yaml` comment for `schedule_hour` to mention TZ env var
- [x] 2.2 Update `README.md` section on model selection to document timezone behavior
- [x] 2.3 Add example showing how to set TZ environment variable in Docker Compose

## 3. Validation
- [x] 3.1 Test with `TZ=UTC` - evaluation runs at expected UTC hour
- [x] 3.2 Test with `TZ=Asia/Shanghai` - evaluation runs at expected local hour  
- [x] 3.3 Test without TZ set - uses system default timezone
- [x] 3.4 Verify logs clearly show which timezone is being used

