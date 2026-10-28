package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	stdlog "log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"go.uber.org/zap/zapcore"

	"github.com/fusionn-subs/pkg/logger"
)

var unifiedLineRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}\+08:00 `)

// captureUnifiedLog redirects the process-wide logger into a buffer for the
// test and pins LOG_LEVEL so a developer environment cannot change the result.
// Cleanup restores the previous logger pointer so later tests keep the real
// process logger instead of a leftover test sink.
func captureUnifiedLog(t *testing.T, level string) *bytes.Buffer {
	t.Helper()
	t.Setenv("LOG_LEVEL", level)
	prevLog := logger.Log
	var buf bytes.Buffer
	logger.Configure(logger.Config{Output: zapcore.AddSync(&buf)})
	t.Cleanup(func() { logger.Log = prevLog })
	return &buf
}

// captureStdlibLog captures anything written through the stdlib logger, the
// path goose used before the adapter.
func captureStdlibLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prevWriter := stdlog.Writer()
	stdlog.SetOutput(&buf)
	t.Cleanup(func() { stdlog.SetOutput(prevWriter) })
	return &buf
}

// goose routes progress through a package-global logger that defaults to the
// stdlib log prefix. Applying a migration and then opening the same database
// again must both report through the unified logger, with the version and no
// ERROR level; nothing may bypass it through stdlib log.
func TestMigrationsRouteThroughUnifiedLogger(t *testing.T) {
	unified := captureUnifiedLog(t, "INFO")
	stdlibLog := captureStdlibLog(t)

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "migrate.db")

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}

	db, err = Open(ctx, path)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}

	out := unified.String()
	for _, want := range []string{
		"component=migrations event=migration_status",
		`msg="OK   00001_glossary.sql (`,
		`msg="goose: successfully migrated database to version: 1"`,
		`msg="goose: no migrations to run. current version: 1"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("unified output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "service=") {
		t.Errorf("unified output must not carry the service field:\n%s", out)
	}
	if strings.Contains(out, " ERROR ") {
		t.Errorf("normal migrations must not log ERROR:\n%s", out)
	}
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if !unifiedLineRe.MatchString(line) || !strings.Contains(line, " INFO ") ||
			!strings.Contains(line, "component=migrations event=migration_status") {
			t.Errorf("line is not an INFO migration_status event: %q", line)
		}
	}
	if got := stdlibLog.String(); got != "" {
		t.Errorf("goose output bypassed the unified logger through stdlib log:\n%s", got)
	}
}

// LOG_LEVEL=ERROR suppresses goose's informational output on both real paths
// (first migration and already up to date) and leaves nothing in the stdlib
// fallback. A failing migration still returns the error to the caller, and the
// adapter must not log it a second time: the final error boundary owns that
// single ERROR entry.
func TestMigrationFailureAtErrorLevelReturnsErrorWithoutOutput(t *testing.T) {
	unified := captureUnifiedLog(t, "ERROR")
	stdlibLog := captureStdlibLog(t)

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "quiet.db")

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}

	db, err = Open(ctx, path)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}

	if out := unified.String(); out != "" {
		t.Errorf("LOG_LEVEL=ERROR must suppress unified migration output, got:\n%s", out)
	}
	if got := stdlibLog.String(); got != "" {
		t.Errorf("LOG_LEVEL=ERROR must suppress stdlib migration output, got:\n%s", got)
	}

	closed, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "closed.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := closed.Close(); err != nil {
		t.Fatalf("close sqlite: %v", err)
	}
	if err := migrate(ctx, closed); err == nil {
		t.Fatal("migrate on a closed database must fail")
	}
	if out := unified.String(); out != "" {
		t.Errorf("migration failure must not add a duplicate log entry, got:\n%s", out)
	}
}

// goose's Fatalf must stay fatal: one unified ERROR line with component/event,
// then exit code 1, even when LOG_LEVEL would filter normal output. Runs in a
// subprocess so the test binary itself never exits.
func TestGooseFatalLogsErrorAndExitsOne(t *testing.T) {
	if os.Getenv("GOOSE_FATAL_HELPER") == "1" {
		logger.Configure(logger.Config{})
		gooseLogger{}.Fatalf("goose: fatal %v", errors.New("boom"))
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestGooseFatalLogsErrorAndExitsOne") //nolint:gosec // re-runs this test binary
	cmd.Env = append(os.Environ(), "GOOSE_FATAL_HELPER=1", "LOG_LEVEL=ERROR")
	out, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected exit error, got %v (output: %s)", err, out)
	}
	if exitErr.ExitCode() != 1 {
		t.Errorf("exit code = %d, want 1", exitErr.ExitCode())
	}
	got := string(out)
	for _, want := range []string{" ERROR ", "component=migrations event=migration_status", `msg="goose: fatal boom"`} {
		if !strings.Contains(got, want) {
			t.Errorf("fatal output missing %q:\n%s", want, got)
		}
	}
}
