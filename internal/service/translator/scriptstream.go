package translator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/fusionn-subs/internal/types"
	"github.com/fusionn-subs/pkg/logger"
)

var log = logger.Component("translator")

const (
	toolGeminiSubtrans = "gemini-subtrans"
	toolGPTSubtrans    = "gpt-subtrans"
	toolLLMSubtrans    = "llm-subtrans"
)

func logToolStarted(ctx context.Context, tool string, msg types.JobMessage, outputPath string, fields ...zap.Field) {
	base := []zap.Field{
		zap.String("tool", tool),
		zap.String("subtitle_path", msg.SubtitlePath),
		zap.String("output_path", outputPath),
	}
	log.Info(ctx, "tool_started", "Tool started", append(base, fields...)...)
}

// contentArgs carry user content; only a placeholder is ever logged for them.
var contentArgs = map[string]bool{
	"-k":            true,
	"--apikey":      true,
	"--instruction": true,
	"--terminology": true,
	"--moviename":   true,
}

// logToolCommand logs the masked command line at DEBUG. The preview policy
// caps it, redacts credentials and drops it outside DEBUG; content-bearing
// arguments keep only a placeholder, so no prompt, terminology or media title
// body is written back.
func logToolCommand(ctx context.Context, tool, scriptPath string, args []string) {
	safe := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if contentArgs[arg] && i+1 < len(args) {
			safe = append(safe, arg, "***")
			i++
			continue
		}
		safe = append(safe, arg)
	}
	log.Debug(ctx, "tool_invocation", "Tool invocation",
		zap.String("tool", tool),
		logger.Preview(maskAPIKeyInCommand(buildCommandLine(scriptPath, safe))),
	)
}

// logToolFailureOutput writes at most one controlled diagnostic excerpt for a
// failed run: stderr when it has content, stdout otherwise. Known credentials
// are masked before the preview cap, and the other stream contributes its size
// only, so a failure can never reassemble the full text.
func logToolFailureOutput(ctx context.Context, tool string, result scriptResult, secrets []string) {
	stream, tail, cut := "", "", false
	switch {
	case result.StderrTail != "":
		stream, tail, cut = "stderr", result.StderrTail, result.StderrBytes > int64(len(result.StderrTail))
	case result.StdoutTail != "":
		stream, tail, cut = "stdout", result.StdoutTail, result.StdoutBytes > int64(len(result.StdoutTail))
	}
	if stream == "" {
		return
	}
	log.Debug(ctx, "tool_output", "Tool output",
		zap.String("tool", tool),
		zap.String("stream", stream),
		zap.Int64("stdout_bytes", result.StdoutBytes),
		zap.Int64("stderr_bytes", result.StderrBytes),
		logger.Preview(maskSecrets(tail, secrets, cut)),
	)
}

// minSecretFragment is the shortest suffix of a known credential that the
// window-cut guard masks. Complete values are masked whatever their length;
// a 1-3 character leading fragment is not a usable credential and matching
// shorter suffixes would corrupt unrelated excerpt text.
const minSecretFragment = 4

// commandSecrets collects the credentials this run hands to the tool: the
// value of a credential flag and any API key environment variable. The shared
// redaction matches known patterns, but an opaque key that a script prints
// without a label is not guessable, so the diagnostic copy replaces these
// exact values.
func commandSecrets(cmd *exec.Cmd) []string {
	var secrets []string
	for i := 0; i+1 < len(cmd.Args); i++ {
		switch cmd.Args[i] {
		case "-k", "--apikey":
			if v := strings.TrimSpace(cmd.Args[i+1]); v != "" {
				secrets = append(secrets, v)
			}
			i++
		}
	}
	for _, arg := range cmd.Args {
		if v, ok := strings.CutPrefix(arg, "--apikey="); ok {
			if v = strings.TrimSpace(v); v != "" {
				secrets = append(secrets, v)
			}
		}
	}
	for _, kv := range cmd.Env {
		name, value, ok := strings.Cut(kv, "=")
		if !ok || !strings.Contains(strings.ToUpper(name), "API_KEY") {
			continue
		}
		if value = strings.TrimSpace(value); value != "" {
			secrets = append(secrets, value)
		}
	}
	// Longest first: a shorter credential that is a prefix of a longer one
	// must not split it into a masked remainder.
	slices.SortFunc(secrets, func(a, b string) int { return len(b) - len(a) })
	return secrets
}

// maskSecrets replaces known credentials in a diagnostic copy. cut reports
// that the retained window starts mid-stream, where a credential whose earlier
// bytes were dropped can leave a suffix at the window start.
func maskSecrets(s string, secrets []string, cut bool) string {
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		s = strings.ReplaceAll(s, secret, "***")
		if !cut || len(secret) <= minSecretFragment {
			continue
		}
		for n := len(secret) - 1; n >= minSecretFragment; n-- {
			if strings.HasPrefix(s, secret[len(secret)-n:]) {
				s = "***" + s[n:]
				break
			}
		}
	}
	return s
}

// scriptTailLimit bounds the raw bytes kept per stream for diagnostics.
// The complete stream still drives failure and rate-limit detection; only the
// retained diagnostic copy is bounded.
const scriptTailLimit = 4 * 1024

// scriptWaitDelay bounds how long Wait waits once the context ends or the
// process is gone, so a descriptor inherited by a survivor cannot hold the job.
const scriptWaitDelay = 2 * time.Second

// scriptMatchOverlap keeps the tail of the previous chunk so an indicator is
// still found when it is split across two reads.
const scriptMatchOverlap = 64

// scriptStream drains one tool output stream with bounded memory.
type scriptStream struct {
	window      string
	tail        []byte
	total       int64
	failure     string
	rateLimited bool
}

func (s *scriptStream) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	s.total += int64(len(p))

	window := s.window + string(p)
	if s.failure == "" {
		if reason, failed := detectScriptFailure(window, ""); failed {
			s.failure = reason
		}
	}
	if !s.rateLimited {
		s.rateLimited = isRateLimitError(window)
	}
	if len(window) > scriptMatchOverlap {
		window = window[len(window)-scriptMatchOverlap:]
	}
	s.window = window

	s.tail = append(s.tail, p...)
	if len(s.tail) > scriptTailLimit {
		s.tail = append(s.tail[:0], s.tail[len(s.tail)-scriptTailLimit:]...)
	}

	return len(p), nil
}

func (s *scriptStream) truncated() bool { return s.total > int64(len(s.tail)) }

// safeFailureMarkers map common tool failure text to a fixed reason. The raw
// matched text is never logged, so unknown tool output cannot push arbitrary
// content or credentials into the log through the failure reason.
var safeFailureMarkers = []struct {
	marker string
	reason string
}{
	{"modulenotfounderror", "missing module"},
	{"importerror", "missing module"},
	{"command not found", "command not found"},
	{"permission denied", "permission denied"},
	{"permissionerror", "permission denied"},
	{"no such file or directory", "file not found"},
	{"filenotfounderror", "file not found"},
	{"401 unauthorized", "authentication failed"},
	{"invalid api key", "authentication failed"},
	{"authenticationerror", "authentication failed"},
	{"quota exceeded", "quota exhausted"},
	{"resource_exhausted", "quota exhausted"},
	{"connection refused", "connection refused"},
}

// scriptResult describes one tool run. StdoutTail and StderrTail are bounded
// diagnostic copies; failure and rate-limit detection ran on the full streams.
type scriptResult struct {
	Path        string
	ExitCode    int
	Duration    time.Duration
	Failure     string
	RateLimited bool
	Recovered   bool
	StdoutTail  string
	StderrTail  string
	StdoutBytes int64
	StderrBytes int64
	Truncated   bool
}

// failureReason returns a fixed, safe reason for a failed run: the matched
// failure indicator, the rate-limit flag, or a curated marker. It never
// returns raw tool text.
func (r scriptResult) failureReason() string {
	if r.Failure != "" {
		return r.Failure
	}
	if r.RateLimited {
		return "quota exhausted"
	}

	combined := strings.ToLower(r.StdoutTail + "\n" + r.StderrTail)
	for _, candidate := range safeFailureMarkers {
		if strings.Contains(combined, candidate.marker) {
			return candidate.reason
		}
	}
	return ""
}

// executeScript runs a translation tool and drains both output streams without
// forwarding raw tool text. Diagnostics stay in the returned result so the
// caller decides what to log.
func executeScript(ctx context.Context, tool string, cmd *exec.Cmd, outputPath string) (scriptResult, error) {
	stdout := &scriptStream{}
	stderr := &scriptStream{}
	secrets := commandSecrets(cmd)

	// os/exec owns the pipes: it drains these writers before Wait returns and
	// bounds the drain with WaitDelay, so the bounded tail and the sticky
	// failure and rate-limit markers of scriptStream stay as they are.
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	// The tool runs in its own process group and canceling the context kills
	// the whole group. os/exec only kills the process it started, and a shell
	// that forked the tool (Linux /bin/sh does for `sh -c "sleep 5"`) would
	// otherwise survive as a child holding the pipes, so the drain would keep
	// waiting for it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// WaitDelay only bounds the extreme case of a survivor that inherited the
	// descriptors; the group kill below is what stops the tree.
	cmd.WaitDelay = scriptWaitDelay
	// A command built by exec.CommandContext already carries a Cancel function;
	// replace it so the whole group stops. A command built by exec.Command has
	// no Cancel and keeps its current semantics: it does not die with ctx.
	if cmd.Cancel != nil {
		cmd.Cancel = func() error {
			err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			if errors.Is(err, syscall.ESRCH) {
				// The group is already gone. Report it the way os.Process.Kill
				// does, so os/exec does not add a spurious cancel error.
				return os.ErrProcessDone
			}
			return err
		}
	}

	started := time.Now()
	if err := cmd.Start(); err != nil {
		return scriptResult{}, fmt.Errorf("start script: %w", err)
	}

	runErr := cmd.Wait()

	result := scriptResult{
		ExitCode:    scriptExitCode(runErr),
		Duration:    time.Since(started),
		Failure:     stdout.failure,
		RateLimited: stdout.rateLimited,
		StdoutTail:  string(stdout.tail),
		StderrTail:  string(stderr.tail),
		StdoutBytes: stdout.total,
		StderrBytes: stderr.total,
		Truncated:   stdout.truncated() || stderr.truncated(),
	}
	if result.Failure == "" {
		result.Failure = stderr.failure
	}
	result.RateLimited = result.RateLimited || stderr.rateLimited

	if result.Failure != "" {
		err := fmt.Errorf("script reported failure: %s", result.Failure)
		logToolFailed(ctx, tool, result, err, secrets)
		return result, err
	}

	if runErr != nil {
		// Process failed (e.g. killed by timeout), but check if the output file
		// was already written. llm-subtrans saves the file before exiting, so a
		// kill during cleanup should not discard a valid result.
		if _, statErr := os.Stat(outputPath); statErr == nil {
			result.Path = outputPath
			result.Recovered = true
			log.Warn(ctx, "tool_completed", "Tool completed after non-zero exit",
				zap.String("tool", tool),
				zap.Int("exit_code", result.ExitCode),
				logger.DurMs(result.Duration),
				zap.Bool("recovered", true),
				zap.String("output_path", outputPath),
			)
			logToolFailureOutput(ctx, tool, result, secrets)
			return result, nil
		}
		err := scriptRunError(ctx, result, runErr)
		logToolFailed(ctx, tool, result, err, secrets)
		return result, err
	}

	if _, statErr := os.Stat(outputPath); statErr != nil {
		err := fmt.Errorf("output not found: %w", statErr)
		logToolFailed(ctx, tool, result, err, secrets)
		return result, err
	}

	result.Path = outputPath
	log.Info(ctx, "tool_completed", "Tool completed",
		zap.String("tool", tool),
		zap.Int("exit_code", result.ExitCode),
		logger.DurMs(result.Duration),
		zap.String("output_path", outputPath),
	)
	return result, nil
}

// scriptRunError describes a run that exited with an error. ctx must be the
// context that bounds the command: a run that ctx stopped says whether its
// timeout expired or it was canceled. A run killed by a signal before that
// points at the system, often the out-of-memory killer, not at the script.
func scriptRunError(ctx context.Context, result scriptResult, runErr error) error {
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return fmt.Errorf("script timed out after %s: %w", result.Duration.Round(time.Second), runErr)
	case ctx.Err() != nil:
		return fmt.Errorf("script canceled: %w", runErr)
	}
	if reason := result.failureReason(); reason != "" {
		return fmt.Errorf("script failed (exit %d): %s", result.ExitCode, reason)
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return fmt.Errorf("script stopped by a signal before its timeout, possibly out of memory: %w", runErr)
		}
	}
	return fmt.Errorf("script failed: %w", runErr)
}

// logToolFailed records a failed run once at the tool boundary: a WARN whose
// detail holds the error lines of the output, so the cause shows without
// DEBUG, then the masked output tail as a DEBUG preview.
func logToolFailed(ctx context.Context, tool string, result scriptResult, err error, secrets []string) {
	stdout := maskSecrets(result.StdoutTail, secrets, result.StdoutBytes > int64(len(result.StdoutTail)))
	stderr := maskSecrets(result.StderrTail, secrets, result.StderrBytes > int64(len(result.StderrTail)))
	log.Warn(ctx, "tool_completed", "Tool failed",
		zap.String("tool", tool),
		zap.Int("exit_code", result.ExitCode),
		logger.DurMs(result.Duration),
		zap.Error(err),
		// stderr comes last because FailureDetail keeps the newest error lines.
		logger.FailureDetail(stdout+"\n"+stderr, secrets...),
	)
	logToolFailureOutput(ctx, tool, result, secrets)
}

func scriptExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

// withJobID attaches the business job ID to ctx so every event of one run
// carries job_id without repeating it at each call site.
func withJobID(ctx context.Context, id string) context.Context {
	return logger.WithJobID(ctx, id)
}
