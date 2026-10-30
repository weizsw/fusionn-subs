package translator

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"go.uber.org/zap/zapcore"

	"github.com/fusionn-subs/internal/config"
	"github.com/fusionn-subs/internal/types"
	"github.com/fusionn-subs/pkg/logger"
)

func TestScriptStreamDetectsEveryFailureIndicatorAcrossChunkBoundaries(t *testing.T) {
	for _, indicator := range fatalIndicators {
		t.Run(indicator, func(t *testing.T) {
			stream := &scriptStream{}
			// One byte per read forces the indicator to span many chunks.
			text := "phase one complete\n" + strings.ToUpper(indicator) + " while processing\n"
			if _, err := io.Copy(stream, iotest.OneByteReader(strings.NewReader(text))); err != nil {
				t.Fatalf("copy: %v", err)
			}
			if stream.failure != indicator {
				t.Fatalf("failure = %q, want %q", stream.failure, indicator)
			}
			if stream.total != int64(len(text)) {
				t.Fatalf("total = %d, want %d", stream.total, len(text))
			}
		})
	}
}

func TestScriptStreamDetectsEveryRateLimitIndicator(t *testing.T) {
	for _, indicator := range rateLimitIndicators {
		t.Run(indicator, func(t *testing.T) {
			stream := &scriptStream{}
			text := "prefix " + strings.ToUpper(indicator) + " suffix"
			if _, err := io.Copy(stream, iotest.OneByteReader(strings.NewReader(text))); err != nil {
				t.Fatalf("copy: %v", err)
			}
			if !stream.rateLimited {
				t.Fatalf("rate limit indicator %q was not detected", indicator)
			}
		})
	}
}

func TestScriptStreamKeepsBoundedTailAfterLongOutput(t *testing.T) {
	stream := &scriptStream{}
	filler := strings.Repeat("0123456789abcdef", 8192) // 128 KiB
	if _, err := io.WriteString(stream, filler); err != nil {
		t.Fatalf("write: %v", err)
	}

	if stream.total != int64(len(filler)) {
		t.Fatalf("total = %d, want %d", stream.total, len(filler))
	}
	if len(stream.tail) != scriptTailLimit {
		t.Fatalf("tail length = %d, want %d", len(stream.tail), scriptTailLimit)
	}
	if !stream.truncated() {
		t.Fatal("stream should report truncation")
	}
	if got, want := string(stream.tail), filler[len(filler)-scriptTailLimit:]; got != want {
		t.Fatalf("tail does not hold the newest bytes")
	}
}

func TestScriptStreamMatchesIndicatorAfterTailEviction(t *testing.T) {
	stream := &scriptStream{}
	text := strings.Repeat("x", 128*1024) + "quota exceeded" + strings.Repeat("y", 8*1024)
	if _, err := io.WriteString(stream, text); err != nil {
		t.Fatalf("write: %v", err)
	}

	if !stream.rateLimited {
		t.Fatal("indicator was missed once it left the retained tail")
	}
	if strings.Contains(string(stream.tail), "quota exceeded") {
		t.Fatal("test setup: indicator should no longer be in the bounded tail")
	}
}

func TestScriptStreamLatchesFirstFailureIndicator(t *testing.T) {
	stream := &scriptStream{}
	if _, err := io.WriteString(stream, "traceback (most recent call last)"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := io.WriteString(stream, "\nfailed to translate\n"); err != nil {
		t.Fatalf("write: %v", err)
	}

	if stream.failure != "traceback (most recent call last)" {
		t.Fatalf("failure = %q, want the first indicator", stream.failure)
	}
}

func TestScriptStreamHandlesMultibyteText(t *testing.T) {
	const text = "字幕翻譯完成：日誌輸出\n"
	stream := &scriptStream{}
	if _, err := io.Copy(stream, iotest.OneByteReader(strings.NewReader(text))); err != nil {
		t.Fatalf("copy: %v", err)
	}
	if string(stream.tail) != text {
		t.Fatalf("tail = %q, want %q", string(stream.tail), text)
	}
}

func runScript(t *testing.T, script, outputPath string) (scriptResult, error) {
	t.Helper()
	return executeScript(context.Background(), "test-tool", exec.Command("/bin/sh", "-c", script), outputPath)
}

func TestExecuteScriptSucceedsOnCleanExitWithOutputFile(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out.srt")
	res, err := runScript(t, "printf 'hello\\n' > '"+out+"'", out)
	if err != nil {
		t.Fatalf("executeScript() error = %v", err)
	}
	if res.Path != out {
		t.Fatalf("path = %q, want %q", res.Path, out)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0", res.ExitCode)
	}
	if res.Recovered {
		t.Fatal("clean exit must not report recovery")
	}
	if res.Duration <= 0 {
		t.Fatalf("duration = %v, want > 0", res.Duration)
	}
}

func TestExecuteScriptTreatsExistingOutputAsSuccessWhenProcessFails(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out.srt")
	res, err := runScript(t, "printf 'partial' > '"+out+"'; echo 'killed during cleanup' >&2; exit 3", out)
	if err != nil {
		t.Fatalf("executeScript() error = %v, want nil", err)
	}
	if !res.Recovered {
		t.Fatal("expected recovery to be reported")
	}
	if res.ExitCode != 3 {
		t.Fatalf("exit code = %d, want 3", res.ExitCode)
	}
	if res.Path != out {
		t.Fatalf("path = %q, want %q", res.Path, out)
	}
	if !strings.Contains(res.StderrTail, "killed during cleanup") {
		t.Fatalf("stderr tail = %q, want the drain to keep stderr", res.StderrTail)
	}
}

func TestExecuteScriptFailsWhenProcessFailsWithoutOutputFile(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out.srt")
	res, err := runScript(t, "echo boom >&2; exit 4", out)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "script failed") {
		t.Fatalf("error = %q, want script failed", err.Error())
	}
	if res.ExitCode != 4 {
		t.Fatalf("exit code = %d, want 4", res.ExitCode)
	}
	if res.Path != "" {
		t.Fatalf("path = %q, want empty", res.Path)
	}
}

func TestExecuteScriptFailsWhenOutputMissingAfterCleanExit(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out.srt")
	res, err := runScript(t, "echo done", out)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "output not found") {
		t.Fatalf("error = %q, want output not found", err.Error())
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0", res.ExitCode)
	}
}

func TestExecuteScriptReportsFailureIndicatorRegardlessOfExitCode(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out.srt")
	res, err := runScript(t, "echo 'TranslationImpossibleError: no audio track'; printf 'x' > '"+out+"'", out)
	if err == nil {
		t.Fatal("expected a failure")
	}
	if !strings.Contains(err.Error(), "script reported failure: translationimpossibleerror") {
		t.Fatalf("error = %q, want the detected indicator", err.Error())
	}
	if res.Failure != "translationimpossibleerror" {
		t.Fatalf("failure = %q, want translationimpossibleerror", res.Failure)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0", res.ExitCode)
	}
}

func TestExecuteScriptSetsRateLimitedFlag(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out.srt")
	res, err := runScript(t, "echo '429 Too Many Requests'; printf 'x' > '"+out+"'", out)
	if err != nil {
		t.Fatalf("executeScript() error = %v", err)
	}
	if !res.RateLimited {
		t.Fatal("rate limit indicator was not flagged")
	}
}

func TestExecuteScriptDrainsLargeSingleLineWithBoundedTail(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out.srt")
	res, err := runScript(t, "yes x | head -c 2097152; printf 'x' > '"+out+"'", out)
	if err != nil {
		t.Fatalf("executeScript() error = %v", err)
	}
	if got := len(res.StdoutTail); got != scriptTailLimit {
		t.Fatalf("stdout tail length = %d, want %d", got, scriptTailLimit)
	}
	if !res.Truncated {
		t.Fatal("expected the result to report truncation")
	}
}

func TestExecuteScriptHandlesCarriageReturnsAndMissingTrailingNewline(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out.srt")
	res, err := runScript(t, "printf 'progress 10pct\\rprogress 20pct\\r'; printf 'x' > '"+out+"'", out)
	if err != nil {
		t.Fatalf("executeScript() error = %v", err)
	}
	if !strings.Contains(res.StdoutTail, "progress 20pct") {
		t.Fatalf("stdout tail = %q, want the progress output", res.StdoutTail)
	}
}

func TestExecuteScriptHandlesMultibyteToolOutput(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out.srt")
	res, err := runScript(t, "echo '字幕翻譯完成'; printf 'x' > '"+out+"'", out)
	if err != nil {
		t.Fatalf("executeScript() error = %v", err)
	}
	if !strings.Contains(res.StdoutTail, "字幕翻譯完成") {
		t.Fatalf("stdout tail = %q, want the multibyte line", res.StdoutTail)
	}
}

func TestExecuteScriptReturnsErrorWhenCommandIsCancelled(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out.srt")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	started := time.Now()
	res, err := executeScript(ctx, "test-tool", exec.CommandContext(ctx, "/bin/sh", "-c", "sleep 5"), out)
	if err == nil {
		t.Fatal("expected an error after cancellation")
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("cancellation took %v, want the drain to stop promptly", elapsed)
	}
	if res.Path != "" {
		t.Fatalf("path = %q, want empty", res.Path)
	}
}

// TestExecuteScriptCancellationStopsAForkedChild uses a fixture that forces a
// real fork: a backgrounded subshell inherits the pipes on every platform,
// while macOS execs a plain `sh -c "sleep 5"` into the child and hides the
// Linux hang. The child heartbeats, so a stopped heartbeat shows that it died
// with the canceled group, without asking whether a Linux zombie still answers
// signal 0.
func TestExecuteScriptCancellationStopsAForkedChild(t *testing.T) {
	dir := t.TempDir()
	heartbeat := filepath.Join(dir, "heartbeat")
	out := filepath.Join(dir, "out.srt")
	script := "( i=0; while [ $i -lt 100 ]; do i=$((i+1)); echo $i > " + shellQuote(heartbeat) +
		"; sleep 0.05; done ) & wait"

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	started := time.Now()
	res, err := executeScript(ctx, "test-tool", exec.CommandContext(ctx, "/bin/sh", "-c", script), out)
	elapsed := time.Since(started)

	if err == nil {
		t.Fatal("expected an error after cancellation")
	}
	if elapsed > 3*time.Second {
		t.Fatalf("cancellation took %v, want the drain to stop promptly", elapsed)
	}
	if res.Path != "" {
		t.Fatalf("path = %q, want empty", res.Path)
	}

	// Let the group kill land, then check that the heartbeat stopped.
	time.Sleep(250 * time.Millisecond)
	settled := readHeartbeat(t, heartbeat)
	time.Sleep(250 * time.Millisecond)
	if later := readHeartbeat(t, heartbeat); later != settled {
		t.Fatalf("forked child kept running after cancellation: heartbeat %q -> %q", settled, later)
	}
}

func readHeartbeat(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if raw, err := os.ReadFile(path); err == nil {
			if beat := strings.TrimSpace(string(raw)); beat != "" {
				return beat
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("forked child never wrote its heartbeat to %s", path)
	return ""
}

func TestExecuteScriptWritesNothingToProcessStdStreams(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out.srt")

	outReader, outWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	errReader, errWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}

	originalStdout, originalStderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outWriter, errWriter
	t.Cleanup(func() {
		os.Stdout, os.Stderr = originalStdout, originalStderr
	})

	if _, err := runScript(t, "echo '\\033[2m dimmed \\033[0m'; echo 'plain stderr' >&2; printf 'x' > '"+out+"'", out); err != nil {
		t.Fatalf("executeScript() error = %v", err)
	}

	if err := outWriter.Close(); err != nil {
		t.Fatalf("close stdout pipe: %v", err)
	}
	if err := errWriter.Close(); err != nil {
		t.Fatalf("close stderr pipe: %v", err)
	}
	gotStdout, _ := io.ReadAll(outReader)
	gotStderr, _ := io.ReadAll(errReader)

	if len(gotStdout) != 0 {
		t.Fatalf("script output was forwarded to stdout: %q", gotStdout)
	}
	if len(gotStderr) != 0 {
		t.Fatalf("script output was forwarded to stderr: %q", gotStderr)
	}
}

func TestExecuteScriptReportsSafeFailureReasonInsteadOfRawOutput(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out.srt")
	res, err := runScript(t, "echo 'ModuleNotFoundError: No module named llm_subtrans' >&2; exit 1", out)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "missing module") {
		t.Fatalf("error = %q, want the safe reason", err.Error())
	}
	if strings.Contains(err.Error(), "llm_subtrans") {
		t.Fatalf("error embeds raw tool output: %q", err.Error())
	}
	if res.ExitCode != 1 {
		t.Fatalf("exit code = %d, want 1", res.ExitCode)
	}
	if !strings.Contains(res.StderrTail, "ModuleNotFoundError") {
		t.Fatalf("stderr tail = %q, want the bounded diagnostic retained", res.StderrTail)
	}
}

func TestExecuteScriptKeepsCredentialsOutOfTheFailureReason(t *testing.T) {
	const secret = "sk-live-credential-12345"
	out := filepath.Join(t.TempDir(), "out.srt")
	_, err := runScript(t, "echo 'HTTP 401 Unauthorized: invalid api key "+secret+"' >&2; exit 1", out)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "authentication failed") {
		t.Fatalf("error = %q, want the safe reason", err.Error())
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("credential leaked into the error text: %q", err.Error())
	}
}

func TestExecuteScriptReportsRateLimitAsSafeReason(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out.srt")
	_, err := runScript(t, "echo '429 Too Many Requests' >&2; exit 1", out)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "quota exhausted") {
		t.Fatalf("error = %q, want the safe reason", err.Error())
	}
}

// TestSampleToolLogLines renders real tool events through the shared logger.
func TestSampleToolLogLines(t *testing.T) {
	t.Setenv("LOG_LEVEL", "DEBUG")

	var buf bytes.Buffer
	previous := logger.Log
	t.Cleanup(func() { logger.Log = previous })
	logger.Configure(logger.Config{Output: zapcore.AddSync(&buf)})

	dir := t.TempDir()
	script := filepath.Join(dir, "fake-subtrans.sh")
	body := "#!/bin/sh\necho 'translating batch 1/3'\necho 'ModuleNotFoundError: No module named llm_subtrans' >&2\nexit 1\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatalf("write fake script: %v", err)
	}
	t.Setenv("LLM_SUBTRANS_SCRIPT_PATH", script)
	t.Setenv("LLM_SUBTRANS_DIR", dir)

	tr := NewLocalLLMTranslator(config.LocalLLMConfig{
		BaseURL:  "http://localhost:11434",
		Model:    "local-model",
		Endpoint: config.DefaultOpenAIChatCompletionsEndpoint,
		Timeout:  10 * time.Second,
	}, "Chinese", ".chs")

	ctx := logger.WithJobID(context.Background(), "job-7f3c")
	if _, err := tr.Translate(ctx, Request{Job: types.JobMessage{
		JobID:        "job-7f3c",
		VideoPath:    "/media/v.mkv",
		SubtitlePath: "/data/v.eng.srt",
	}}); err == nil {
		t.Fatal("expected the script failure")
	}

	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		t.Log(line)
	}
}

func TestLogToolCommandMasksContentArguments(t *testing.T) {
	t.Setenv("LOG_LEVEL", "DEBUG")

	var buf bytes.Buffer
	previous := logger.Log
	t.Cleanup(func() { logger.Log = previous })
	logger.Configure(logger.Config{Output: zapcore.AddSync(&buf)})

	ctx := logger.WithJobID(context.Background(), "job-1")
	logToolCommand(ctx, toolLLMSubtrans, "/opt/llm-subtrans/llm-subtrans.sh", []string{
		"/data/v.eng.srt",
		"-o", "/data/v.chs.srt",
		"-l", "Chinese",
		"-k", "sk-secret-key",
		"--apikey=sk-equals-secret",
		"--instruction", "Never translate the names.",
		"--terminology", "SO15::SO15",
		"--moviename", "The Capture",
		"--ratelimit", "10",
	})

	output := buf.String()
	for _, leaked := range []string{"sk-secret-key", "sk-s", "sk-equals-secret", "Never translate the names.", "SO15::SO15", "The Capture"} {
		if strings.Contains(output, leaked) {
			t.Fatalf("command preview leaked %q: %s", leaked, output)
		}
	}
	// Safe metadata stays so a failure can still be reproduced, and every
	// credential renders as the shared placeholder with no prefix.
	for _, kept := range []string{"-o /data/v.chs.srt", "-l Chinese", "--ratelimit 10", "-k ***", "--apikey=***"} {
		if !strings.Contains(output, kept) {
			t.Fatalf("command preview dropped %q: %s", kept, output)
		}
	}
}

// probeOpaqueKey has no label and no known prefix, so the shared redaction
// cannot guess it; only the explicit known-secret masking can hide it.
const probeOpaqueKey = "7f3c2b7e14a85d60c3e2f1b4d5a69e08"

func captureLogs(t *testing.T, level string) *bytes.Buffer {
	t.Helper()
	t.Setenv("LOG_LEVEL", level)
	var buf bytes.Buffer
	previous := logger.Log
	t.Cleanup(func() { logger.Log = previous })
	logger.Configure(logger.Config{Output: zapcore.AddSync(&buf)})
	return &buf
}

// writeProbeStub writes an executable stub. The command passes the credential
// the same way the providers do: as a flag value and in the environment.
func writeProbeStub(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "probe-stub.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	return path
}

func credentialProbeCmd(stub string) *exec.Cmd {
	cmd := exec.Command(stub, "-k", probeOpaqueKey)
	cmd.Env = append(os.Environ(), "OPENAI_API_KEY="+probeOpaqueKey)
	return cmd
}

func assertNoCredentialFragment(t *testing.T, what, text string) {
	t.Helper()
	for i := 0; i+4 <= len(probeOpaqueKey); i++ {
		if strings.Contains(text, probeOpaqueKey[i:i+4]) {
			t.Fatalf("%s leaked credential fragment %q: %s", what, probeOpaqueKey[i:i+4], text)
		}
	}
}

func TestExecuteScriptMasksKnownCredentialsInDiagnostics(t *testing.T) {
	// The stub prints the key bare, in the `-k <key>` form, and split across
	// two writes, so the key also crosses a read boundary.
	stub := writeProbeStub(t, `
printf '%s\n' "$2" >&2
printf -- '-k %s\n' "$2" >&2
printf '%s' "$(printf '%s' "$2" | cut -c1-16)" >&2
sleep 0.05
printf '%s\n' "$(printf '%s' "$2" | cut -c17-)" >&2
echo 'TranslationImpossibleError: boom' >&2
exit 1
`)

	for _, level := range []string{"DEBUG", "INFO", "WARN", "ERROR"} {
		t.Run(level, func(t *testing.T) {
			logs := captureLogs(t, level)
			out := filepath.Join(t.TempDir(), "out.srt")

			result, err := executeScript(context.Background(), "probe-tool", credentialProbeCmd(stub), out)
			if err == nil {
				t.Fatal("expected the script failure")
			}
			// Failure detection still reads the raw stream.
			if result.Failure != "translationimpossibleerror" {
				t.Fatalf("failure = %q, want the raw indicator", result.Failure)
			}
			logged := logs.String()
			assertNoCredentialFragment(t, level+" logs", logged)
			switch level {
			case "DEBUG":
				if !strings.Contains(logged, "***") {
					t.Fatalf("DEBUG logs carry no masked excerpt: %s", logged)
				}
			case "ERROR":
				if logs.Len() != 0 {
					t.Fatalf("%s logged %q; check any new event for raw output", level, logged)
				}
			default:
				// INFO and WARN show only the failure line. Its detail is the
				// error line, never the lines that carry the key.
				lines := strings.Split(strings.TrimSpace(logged), "\n")
				if len(lines) != 1 || !strings.Contains(lines[0], "WARN component=translator event=tool_completed tool=probe-tool ") ||
					!strings.Contains(lines[0], ` detail="TranslationImpossibleError: boom" `) {
					t.Fatalf("%s logged %q; check any new event for raw output", level, logged)
				}
			}
		})
	}
}

func TestExecuteScriptMasksCredentialCutByTheBoundedTailWindow(t *testing.T) {
	// The retained window starts inside the key: 100 bytes of filler, the key,
	// then filler and the failure marker.
	const head = 100
	const marker = "\nTranslationImpossibleError: boom\n"
	total := 4210
	stub := writeProbeStub(t, fmt.Sprintf(`
head -c %d /dev/zero | tr '\0' x
printf '%%s' "$2"
head -c %d /dev/zero | tr '\0' y
printf '%s'
exit 1
`, head, total-head-len(probeOpaqueKey)-len(marker), marker))

	logs := captureLogs(t, "DEBUG")
	out := filepath.Join(t.TempDir(), "out.srt")

	result, err := executeScript(context.Background(), "probe-tool", credentialProbeCmd(stub), out)
	if err == nil {
		t.Fatal("expected the script failure")
	}
	cutStart := result.StdoutBytes - int64(len(result.StdoutTail))
	if cutStart <= head || cutStart >= head+int64(len(probeOpaqueKey)) {
		t.Fatalf("fixture does not cut the key: window starts at %d, key spans [%d,%d)", cutStart, head, head+len(probeOpaqueKey))
	}
	logged := logs.String()
	assertNoCredentialFragment(t, "DEBUG logs", logged)
	if !strings.Contains(logged, "***") {
		t.Fatalf("DEBUG logs carry no masked excerpt: %s", logged)
	}
}

func TestExecuteScriptKeepsRateLimitDetectionWithMaskedCredentials(t *testing.T) {
	stub := writeProbeStub(t, `
printf 'requesting with %s\n' "$2" >&2
echo '429 Too Many Requests' >&2
exit 1
`)

	logs := captureLogs(t, "DEBUG")
	out := filepath.Join(t.TempDir(), "out.srt")

	result, err := executeScript(context.Background(), "probe-tool", credentialProbeCmd(stub), out)
	if err == nil {
		t.Fatal("expected the script failure")
	}
	if !result.RateLimited {
		t.Fatal("rate-limit detection must stay on the raw stream")
	}
	if !strings.Contains(err.Error(), "quota exhausted") {
		t.Fatalf("error = %q, want the safe rate-limit reason", err.Error())
	}
	assertNoCredentialFragment(t, "DEBUG logs", logs.String())
}

func TestCommandSecretsAndMasking(t *testing.T) {
	cmd := exec.Command("tool", "--apikey", "opaque-key-1", "--apikey=opaque-key-2", "--model", "m")
	cmd.Env = []string{"PATH=/bin", "OPENROUTER_API_KEY=opaque-key-3", "HOME=/root"}

	secrets := commandSecrets(cmd)
	joined := strings.Join(secrets, ",")
	for _, want := range []string{"opaque-key-1", "opaque-key-2", "opaque-key-3"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("secrets = %v, want %q", secrets, want)
		}
	}
	for _, unwanted := range []string{"/bin", "/root", ",m"} {
		if strings.Contains(joined, unwanted) {
			t.Fatalf("secrets = %v picked up %q", secrets, unwanted)
		}
	}

	masked := maskSecrets("using opaque-key-1 and opaque-key-3\n", secrets, false)
	if strings.Contains(masked, "opaque-key") {
		t.Fatalf("masked = %q, want no credential", masked)
	}
	// A credential whose earlier bytes the bounded window dropped leaves only
	// a suffix at the window start.
	if got := maskSecrets("2ff1b4d5a6", []string{"14d52ff1b4d5a69e08"}, true); strings.Contains(got, "14d5") {
		t.Fatalf("cut fragment not masked: %q", got)
	}
	if got := maskSecrets("2ff1b4d5a6", []string{"14d52ff1b4d5a69e08"}, false); !strings.Contains(got, "2ff1") {
		t.Fatalf("uncut text must stay intact: %q", got)
	}
	// Credential length is a deployment decision: a short known value is
	// masked in full, never rendered.
	if got := maskSecrets("filler abc filler", []string{"abc"}, false); strings.Contains(got, "abc") {
		t.Fatalf("short credential leaked: %q", got)
	}
}

// A failed run logs one WARN at the tool boundary. Its detail holds the error
// lines of the output, so the cause shows without DEBUG.
func TestExecuteScriptFailureWarnsWithErrorDetail(t *testing.T) {
	logs := captureLogs(t, "INFO")
	out := filepath.Join(t.TempDir(), "out.srt")
	_, err := runScript(t, "echo 'Translating batch 3' >&2; echo 'openai.APIConnectionError: Connection error.' >&2; exit 1", out)
	if err == nil {
		t.Fatal("expected the script failure")
	}
	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], "WARN component=translator event=tool_completed tool=test-tool ") {
		t.Fatalf("want one tool failure line, got %q", lines)
	}
	for _, want := range []string{` detail="openai.APIConnectionError: Connection error." `, " exit_code=1 ", ` error="script failed`} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("failure line lacks %q: %s", want, lines[0])
		}
	}
	if strings.Contains(lines[0], "Translating batch") {
		t.Errorf("non-error output reached the WARN line: %s", lines[0])
	}
}

// A run stopped by its timeout says so, instead of only "signal: killed".
func TestExecuteScriptReportsTimeout(t *testing.T) {
	captureLogs(t, "ERROR")
	out := filepath.Join(t.TempDir(), "out.srt")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	_, err := executeScript(ctx, "test-tool", exec.CommandContext(ctx, "/bin/sh", "-c", "sleep 5"), out)
	if err == nil || !strings.HasPrefix(err.Error(), "script timed out after ") {
		t.Fatalf("error = %v, want a timeout", err)
	}
}

// A run killed by a signal before its timeout, for example by the
// out-of-memory killer, is not reported as a timeout.
func TestExecuteScriptReportsASignalBeforeTheTimeout(t *testing.T) {
	captureLogs(t, "ERROR")
	out := filepath.Join(t.TempDir(), "out.srt")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	_, err := executeScript(ctx, "test-tool", exec.CommandContext(ctx, "/bin/sh", "-c", "kill -9 $$"), out)
	want := "script stopped by a signal before its timeout, possibly out of memory: signal: killed"
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}
