package translator

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/fusionn-subs/internal/config"
	"github.com/fusionn-subs/internal/types"
	"github.com/fusionn-subs/pkg/logger"
)

// GeminiTranslator implements translation using Gemini API via gemini-subtrans.sh
type GeminiTranslator struct {
	scriptPath     string
	workDir        string
	apiKey         string
	model          string
	instruction    string
	maxBatchSize   int
	rateLimit      int
	targetLanguage string
	outputSuffix   string
}

// NewGeminiTranslator creates a new Gemini translator
func NewGeminiTranslator(cfg config.GeminiConfig, targetLang, outputSuffix string) *GeminiTranslator {
	scriptPath := os.Getenv("GEMINI_SCRIPT_PATH")
	if scriptPath == "" {
		scriptPath = "/opt/llm-subtrans/gemini-subtrans.sh"
	}
	workDir := os.Getenv("GEMINI_WORKDIR")
	if workDir == "" {
		workDir = "/opt/llm-subtrans"
	}

	return &GeminiTranslator{
		scriptPath:     scriptPath,
		workDir:        workDir,
		apiKey:         cfg.APIKey,
		model:          cfg.PrimaryModel.Name,
		instruction:    cfg.Instruction,
		maxBatchSize:   cfg.PrimaryModel.MaxBatchSize,
		rateLimit:      cfg.PrimaryModel.RateLimit,
		targetLanguage: targetLang,
		outputSuffix:   outputSuffix,
	}
}

// Translate translates subtitles using Gemini
func (t *GeminiTranslator) Translate(ctx context.Context, msg types.JobMessage) (string, error) {
	if err := msg.Validate(); err != nil {
		return "", fmt.Errorf("invalid message: %w", err)
	}

	outputPath := msg.OutputPath(t.outputSuffix)

	ctxTimeout, cancel := context.WithTimeout(ctx, config.DefaultGeminiTimeout)
	defer cancel()

	// Build args for gemini-subtrans.sh
	args := []string{
		msg.SubtitlePath,
		"-o", outputPath,
		"-l", t.targetLanguage,
		"-k", t.apiKey,
	}

	if t.model != "" {
		args = append(args, "-m", t.model)
	}

	if mediaTitle := strings.TrimSpace(msg.MediaTitle); mediaTitle != "" {
		args = append(args, "--moviename", mediaTitle)
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

	logger.Infof("🔄 Starting translation (Gemini): %s → %s", msg.SubtitlePath, outputPath)
	logger.Debugf("Command: %s", maskAPIKeyInCommand(buildCommandLine(t.scriptPath, args)))

	return executeScript(cmd, outputPath)
}
