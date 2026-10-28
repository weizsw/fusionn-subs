package version_test

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"go.uber.org/zap/zapcore"

	"github.com/fusionn-subs/internal/version"
	"github.com/fusionn-subs/pkg/logger"
)

func TestPrintBannerNamesProjectVersionAndLevel(t *testing.T) {
	previous := version.Version
	version.Version = "v9.9.9-test"
	t.Cleanup(func() { version.Version = previous })

	var buf bytes.Buffer
	version.PrintBanner(&buf, "WARN")

	out := buf.String()
	for _, want := range []string{
		version.Banner(),
		"fusionn-subs v9.9.9-test",
		"Subtitle Translation Worker",
		"  Log level: WARN\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("banner is missing %q:\n%s", want, out)
		}
	}
}

// TestPrintBannerShowsTheEffectiveLogLevel renders the banner with the level
// the process would really log at: a configured level, the WARNING alias, and
// a value that falls back to INFO.
func TestPrintBannerShowsTheEffectiveLogLevel(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want string
	}{
		{name: "default", env: "", want: "INFO"},
		{name: "debug", env: "DEBUG", want: "DEBUG"},
		{name: "lowercase", env: "debug", want: "DEBUG"},
		{name: "error", env: "ERROR", want: "ERROR"},
		{name: "warning alias", env: "WARNING", want: "WARN"},
		{name: "invalid falls back to info", env: "verbose", want: "INFO"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("LOG_LEVEL", tt.env)

			// Configure the real logger, discard its output, and restore the
			// package-level logger when the case ends.
			previous := logger.Log
			t.Cleanup(func() { logger.Log = previous })
			logger.Configure(logger.Config{Output: zapcore.AddSync(io.Discard)})

			var buf bytes.Buffer
			version.PrintBanner(&buf, logger.Log.Level().CapitalString())

			want := "  Log level: " + tt.want + "\n"
			if !strings.Contains(buf.String(), want) {
				t.Fatalf("banner = %q, want the line %q", buf.String(), want)
			}
		})
	}
}
