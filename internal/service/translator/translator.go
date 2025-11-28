package translator

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fusionn-subs/internal/types"
	"github.com/fusionn-subs/internal/util"
	"github.com/fusionn-subs/pkg/logger"
)

// ANSI escape codes for dimmed output (like Docker build logs)
const (
	dimStart = "\033[2m"
	dimEnd   = "\033[0m"
)

type Translator interface {
	Translate(ctx context.Context, msg types.JobMessage) (string, error)
}

type GeminiTranslator struct {
	scriptPath     string
	workDir        string
	apiKey         string
	model          string
	instruction    string
	maxBatchSize   int
	targetLanguage string
	outputSuffix   string
	timeout        time.Duration
	rateLimit      int
}

type Config struct {
	ScriptPath     string
	WorkingDir     string
	APIKey         string
	Model          string
	Instruction    string
	MaxBatchSize   int
	TargetLanguage string
	OutputSuffix   string
	Timeout        time.Duration
	RateLimit      int
}

func NewGeminiTranslator(cfg Config) *GeminiTranslator {
	return &GeminiTranslator{
		scriptPath:     cfg.ScriptPath,
		workDir:        cfg.WorkingDir,
		apiKey:         cfg.APIKey,
		model:          cfg.Model,
		instruction:    cfg.Instruction,
		maxBatchSize:   cfg.MaxBatchSize,
		targetLanguage: cfg.TargetLanguage,
		outputSuffix:   cfg.OutputSuffix,
		timeout:        cfg.Timeout,
		rateLimit:      cfg.RateLimit,
	}
}

func (t *GeminiTranslator) Translate(ctx context.Context, msg types.JobMessage) (string, error) {
	if err := msg.Validate(); err != nil {
		return "", fmt.Errorf("invalid message: %w", err)
	}

	outputPath := msg.OutputPath(t.outputSuffix)

	ctxTimeout, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	// Build args
	// Note: API key passed via -k is visible in process list (ps).
	// Also set via env var for scripts that support it.
	args := []string{
		msg.Path,
		"-o", outputPath,
		"-l", t.targetLanguage,
		"-k", t.apiKey,
	}

	if t.model != "" {
		args = append(args, "-m", t.model)
	}

	if overview := strings.TrimSpace(msg.Overview); overview != "" {
		args = append(args, "-d", overview)
	}

	if t.instruction != "" {
		args = append(args, "--instruction", t.instruction)
	}

	if t.rateLimit > 0 {
		args = append(args, "--ratelimit", strconv.Itoa(t.rateLimit))
	}

	if t.maxBatchSize > 0 {
		args = append(args, "--maxbatchsize", strconv.Itoa(t.maxBatchSize))
	}

	cmd := exec.CommandContext(ctxTimeout, t.scriptPath, args...)
	if t.workDir != "" {
		cmd.Dir = t.workDir
	}

	// Pass API key via environment only (security: not visible in process list)
	cmd.Env = append(os.Environ(), "GEMINI_API_KEY="+t.apiKey)

	logger.Infof("🔄 Starting translation: %s → %s", msg.Path, outputPath)
	logger.Debugf("Command: %s", maskAPIKeyInCommand(buildCommandLine(t.scriptPath, args)))

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return "", fmt.Errorf("stderr pipe: %w", err)
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	var wg sync.WaitGroup

	// Stream output in dim/grey (like Docker build logs)
	wg.Add(2)
	go func() {
		defer wg.Done()
		streamDimmed(stdoutPipe, &stdoutBuf)
	}()
	go func() {
		defer wg.Done()
		streamDimmed(stderrPipe, &stderrBuf)
	}()

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start script: %w", err)
	}

	wg.Wait()
	err = cmd.Wait()

	stdoutStr := strings.TrimSpace(stdoutBuf.String())
	stderrStr := strings.TrimSpace(stderrBuf.String())

	if err != nil {
		logger.Errorf("Translation failed: %v", err)
		if stderrStr != "" {
			logger.Errorf("Script stderr: %s", stderrStr)
		}
		return "", fmt.Errorf("script failed: %w", err)
	}

	if _, statErr := os.Stat(outputPath); statErr != nil {
		logger.Errorf("Output file not found after script completed")
		return "", fmt.Errorf("output not found: %w", statErr)
	}

	if reason, failed := detectScriptFailure(stdoutStr, stderrStr); failed {
		logger.Errorf("Script failure detected: %s", reason)
		return "", fmt.Errorf("script reported failure: %s", reason)
	}

	logger.Infof("✅ Translation completed: %s", outputPath)
	return outputPath, nil
}

func buildCommandLine(cmd string, args []string) string {
	// Pre-allocate capacity
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, shellQuote(cmd))
	for _, arg := range args {
		parts = append(parts, shellQuote(arg))
	}
	return strings.Join(parts, " ")
}

func shellQuote(arg string) string {
	if arg == "" {
		return "''"
	}
	if !strings.ContainsAny(arg, " \t\n\"'\\") {
		return arg
	}
	return strconv.Quote(arg)
}

var apiKeyPattern = regexp.MustCompile(`(-k\s+)(\S+)`)

// maskAPIKeyInCommand replaces -k value with masked version for logging
func maskAPIKeyInCommand(cmd string) string {
	return apiKeyPattern.ReplaceAllStringFunc(cmd, func(match string) string {
		parts := apiKeyPattern.FindStringSubmatch(match)
		if len(parts) == 3 {
			return parts[1] + util.MaskSecret(parts[2])
		}
		return match
	})
}

// streamDimmed reads from r, writes to buf for capture, and prints dimmed to stderr.
// This creates a Docker-build-like experience where script output is visible but greyed out.
func streamDimmed(r io.Reader, buf *bytes.Buffer) {
	scanner := bufio.NewScanner(r)
	// Increase buffer for potentially long lines
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		buf.WriteString(line)
		buf.WriteByte('\n')
		// Print dimmed to stderr (doesn't interfere with structured logs)
		fmt.Fprintf(os.Stderr, "%s  │ %s%s\n", dimStart, line, dimEnd)
	}

	if err := scanner.Err(); err != nil {
		logger.Debugf("Scanner error (may be normal): %v", err)
	}
}

func detectScriptFailure(stdoutStr, stderrStr string) (string, bool) {
	fatalIndicators := []string{
		"translationimpossibleerror",
		"failed to translate",
		"failed to communicate with provider",
		"saving partial results",
		"traceback (most recent call last)",
	}

	combined := strings.ToLower(stdoutStr + "\n" + stderrStr)
	for _, indicator := range fatalIndicators {
		if strings.Contains(combined, indicator) {
			return indicator, true
		}
	}
	return "", false
}
