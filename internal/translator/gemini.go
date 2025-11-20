package translator

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/fusionn-subs/internal/job"
)

type Translator interface {
	Translate(ctx context.Context, msg job.Message) (string, error)
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
	logger         *zap.Logger
}

type GeminiConfig struct {
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
	Logger         *zap.Logger
}

func NewGeminiTranslator(cfg GeminiConfig) *GeminiTranslator {
	logger := cfg.Logger
	if logger == nil {
		logger = zap.NewNop()
	}
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
		logger:         logger,
	}
}

func (t *GeminiTranslator) Translate(ctx context.Context, msg job.Message) (string, error) {
	if err := msg.Validate(); err != nil {
		return "", err
	}

	outputPath := msg.OutputPath(t.outputSuffix)

	ctxTimeout, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

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

	cmd.Env = append(os.Environ(), fmt.Sprintf("GEMINI_API_KEY=%s", t.apiKey))

	commandLine := buildCommandLine(t.scriptPath, args)

	t.logger.Info("starting gemini translation",
		zap.String("input", msg.Path),
		zap.String("output", outputPath),
		zap.String("model", t.model),
		zap.String("command", commandLine),
	)

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

	wg.Add(1)
	go func() {
		defer wg.Done()
		streamCmdOutput(stdoutPipe, &stdoutBuf, func(line string) {
			t.logger.Info("gemini stdout", zap.String("line", line))
		})
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		streamCmdOutput(stderrPipe, &stderrBuf, func(line string) {
			t.logger.Warn("gemini stderr", zap.String("line", line))
		})
	}()

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start gemini translator: %w", err)
	}

	err = cmd.Wait()
	wg.Wait()

	stdoutStr := strings.TrimSpace(stdoutBuf.String())
	stderrStr := strings.TrimSpace(stderrBuf.String())

	logFields := []zap.Field{
		zap.String("stdout", stdoutStr),
		zap.String("stderr", stderrStr),
	}

	if err != nil {
		t.logger.Error("gemini translator failed", append(logFields, zap.Error(err))...)
		return "", fmt.Errorf("gemini translator failed: %w: %s", err, stderrStr)
	}

	if _, err := os.Stat(outputPath); err != nil {
		t.logger.Error("gemini translator missing output", append(logFields, zap.Error(err))...)
		return "", fmt.Errorf("output not found: %w", err)
	}

	if reason, ok := detectScriptFailure(stdoutStr, stderrStr); ok {
		err := fmt.Errorf("script reported failure: %s", reason)
		t.logger.Error("gemini translator reported failure", append(logFields, zap.Error(err))...)
		return "", err
	}

	t.logger.Info("translation completed",
		append(logFields, zap.String("output", outputPath))...,
	)

	return outputPath, nil
}

func buildCommandLine(cmd string, args []string) string {
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

func streamCmdOutput(r io.Reader, buf *bytes.Buffer, logLine func(string)) {
	reader := bufio.NewReader(io.TeeReader(r, buf))
	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			logLine(strings.TrimRight(line, "\r\n"))
		}
		if err != nil {
			if err != io.EOF {
				logLine(fmt.Sprintf("stream error: %v", err))
			}
			return
		}
	}
}

func detectScriptFailure(stdoutStr, stderrStr string) (string, bool) {
	fatalIndicators := []string{
		"translationimpossibleerror",
		"failed to translate",
		"failed to communicate with provider",
		"saving partial results",
		"traceback (most recent call last)",
		"error:",
	}

	combined := strings.ToLower(stdoutStr + "\n" + stderrStr)
	for _, indicator := range fatalIndicators {
		if indicator == "" {
			continue
		}
		if strings.Contains(combined, indicator) {
			return indicator, true
		}
	}

	return "", false
}
