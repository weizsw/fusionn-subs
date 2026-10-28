package sqlite

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/pressly/goose/v3"
	"go.uber.org/zap"

	"github.com/fusionn-subs/pkg/logger"
)

// Migrations report under one stable module and event name.
const (
	migrationsComponent = "migrations"
	migrationsEvent     = "migration_status"
)

var migrationsLog = logger.Component(migrationsComponent)

// gooseLogger routes goose's package-level Logger into the unified logger.
// goose v3.24.3 keeps that logger in a package global that Up/Down read at
// call time, so the adapter is bound once at init. It holds no logger pointer:
// Component and logger.Log resolve the process-wide logger on every call, so
// the binding stays correct after logger.Configure.
type gooseLogger struct{}

var _ goose.Logger = gooseLogger{}

// Printf carries goose's informational progress (applied migration, current
// version) at INFO. One call renders as one physical line: only the trailing
// newline is removed here, the shared core escapes and redacts the rest.
func (gooseLogger) Printf(format string, v ...interface{}) {
	migrationsLog.Info(context.Background(), migrationsEvent, gooseMessage(format, v...))
}

// Fatalf keeps goose's fatal contract: one ERROR line with component/event,
// then exit code 1. It goes through zap's own Fatal path, so the entry is
// written even when the configured level would filter normal output.
func (gooseLogger) Fatalf(format string, v ...interface{}) {
	if l := logger.Log; l != nil {
		l.Desugar().Fatal(gooseMessage(format, v...),
			zap.String("component", migrationsComponent),
			zap.String("event", migrationsEvent),
		)
	}
	os.Exit(1)
}

func gooseMessage(format string, v ...interface{}) string {
	return strings.TrimRight(fmt.Sprintf(format, v...), "\r\n")
}

func init() { goose.SetLogger(gooseLogger{}) }
