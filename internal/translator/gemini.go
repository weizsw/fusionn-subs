package translator

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return "", fmt.Errorf("create output dir: %w", err)
	}

	ctxTimeout, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	args := []string{
		msg.Path,
		"--output", outputPath,
		"--target_language", t.targetLanguage,
		"--apikey", t.apiKey,
	}

	if t.model != "" {
		args = append(args, "--model", t.model)
	}

	if overview := strings.TrimSpace(msg.Overview); overview != "" {
		args = append(args, "--description", overview)
	}

	if t.rateLimit > 0 {
		args = append(args, "--ratelimit", strconv.Itoa(t.rateLimit))
	}

	cmd := exec.CommandContext(ctxTimeout, t.scriptPath, args...)
	if t.workDir != "" {
		cmd.Dir = t.workDir
	}

	cmd.Env = append(os.Environ(), fmt.Sprintf("GEMINI_API_KEY=%s", t.apiKey))

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	t.logger.Info("starting gemini translation",
		zap.String("input", msg.Path),
		zap.String("output", outputPath),
		zap.String("model", t.model),
	)

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("gemini translator failed: %w: %s", err, stderr.String())
	}

	if _, err := os.Stat(outputPath); err != nil {
		return "", fmt.Errorf("output not found: %w", err)
	}

	t.logger.Info("translation completed", zap.String("output", outputPath))

	return outputPath, nil
}
