package translator

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"github.com/fusionn-subs/internal/config"
	"github.com/fusionn-subs/internal/types"
	"github.com/fusionn-subs/pkg/logger"
)

// OpenRouterTranslator implements translation using OpenRouter API via llm-subtrans.sh
type OpenRouterTranslator struct {
	scriptPath     string
	workDir        string
	apiKey         string
	mu             sync.RWMutex // Protects model field
	model          string
	instruction    string
	maxBatchSize   int
	rateLimit      int
	targetLanguage string
	outputSuffix   string
}

// NewOpenRouterTranslator creates a new OpenRouter translator
func NewOpenRouterTranslator(cfg config.OpenRouterConfig, targetLang, outputSuffix string) *OpenRouterTranslator {
	scriptPath := os.Getenv("LLM_SUBTRANS_SCRIPT_PATH")
	if scriptPath == "" {
		scriptPath = "/opt/llm-subtrans/llm-subtrans.sh"
	}
	workDir := os.Getenv("LLM_SUBTRANS_DIR")
	if workDir == "" {
		workDir = "/opt/llm-subtrans"
	}

	// Set default rate limit if not specified (10 RPM is safe for most providers)
	rateLimit := cfg.RateLimit
	if rateLimit == 0 {
		rateLimit = 10
	}

	return &OpenRouterTranslator{
		scriptPath:     scriptPath,
		workDir:        workDir,
		apiKey:         cfg.APIKey,
		model:          cfg.Model,
		instruction:    cfg.Instruction,
		maxBatchSize:   cfg.MaxBatchSize,
		rateLimit:      rateLimit,
		targetLanguage: targetLang,
		outputSuffix:   outputSuffix,
	}
}

// UpdateModel updates the model used for translation (thread-safe).
func (t *OpenRouterTranslator) UpdateModel(newModel string) {
	t.mu.Lock()
	oldModel := t.model
	t.model = newModel
	t.mu.Unlock()

	if oldModel != newModel {
		logger.Infof("🔄 Translator model updated: %s → %s", oldModel, newModel)
	}
}

// GetModel returns the current model (thread-safe).
func (t *OpenRouterTranslator) GetModel() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.model
}

// Translate translates subtitles using OpenRouter
func (t *OpenRouterTranslator) Translate(ctx context.Context, msg types.JobMessage) (string, error) {
	if err := msg.Validate(); err != nil {
		return "", fmt.Errorf("invalid message: %w", err)
	}

	outputPath := msg.OutputPath(t.outputSuffix)

	ctxTimeout, cancel := context.WithTimeout(ctx, config.DefaultGeminiTimeout)
	defer cancel()

	// Get current model (thread-safe)
	t.mu.RLock()
	currentModel := t.model
	t.mu.RUnlock()

	// Build args for llm-subtrans.sh (OpenRouter default)
	args := []string{
		msg.Path,
		"-o", outputPath,
		"-l", t.targetLanguage,
		"--apikey", t.apiKey,
		"--model", currentModel,
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

	// Pass API key via environment (security: not visible in process list)
	cmd.Env = append(os.Environ(), "OPENROUTER_API_KEY="+t.apiKey)

	logger.Infof("🔄 Starting translation (OpenRouter): %s → %s", msg.Path, outputPath)
	logger.Infof("📦 Model: %s", currentModel)
	logger.Debugf("Command: %s", maskAPIKeyInCommand(buildCommandLine(t.scriptPath, args)))

	return executeScript(cmd, outputPath)
}
