# Model Selection Service Specification Deltas

## MODIFIED Requirements

### Requirement: Scheduled Model Re-evaluation
The system SHALL re-evaluate models daily at the configured hour **in the container's local timezone**.

**Previous**: Used hardcoded UTC timezone, ignoring system timezone settings.

**Modified**: 
- Respects the system's local timezone (determined by `TZ` environment variable)
- Falls back to system default timezone if `TZ` is not set
- Logs the detected timezone on startup for transparency
- Schedule hour is interpreted in local time, not UTC

#### Scenario: Daily schedule trigger
- **WHEN** the clock reaches the configured hour in local timezone
- **THEN** the system triggers model evaluation
- **AND** does not interrupt ongoing translations
- **AND** updates model only after current jobs complete

#### Scenario: Timezone detection and logging
- **WHEN** the service starts
- **THEN** detect and log the current timezone (e.g., "Asia/Shanghai")
- **AND** log the schedule time in local timezone (e.g., "3:00 Asia/Shanghai")
- **AND** use this timezone for all scheduling decisions

#### Scenario: Timezone handling
- **WHEN** scheduler initializes
- **THEN** it calculates next scheduled hour from current local time
- **AND** uses `time.Now()` instead of `time.Now().UTC()`
- **AND** respects daylight saving time (DST) changes automatically

#### Scenario: Concurrent evaluation protection
- **WHEN** evaluation is already running
- **AND** scheduler triggers again
- **THEN** the system skips the duplicate evaluation
- **AND** logs the skipped attempt

#### Scenario: UTC timezone behavior
- **WHEN** `TZ=UTC` is explicitly set
- **THEN** scheduler behaves identically to previous UTC-only behavior
- **AND** logs show "UTC" as the timezone

#### Scenario: Unset timezone fallback
- **WHEN** `TZ` environment variable is not set
- **THEN** use the system's default timezone
- **AND** log the detected timezone on startup
- **AND** warn if timezone cannot be determined

