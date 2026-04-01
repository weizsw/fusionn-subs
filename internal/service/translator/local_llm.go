package translator

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fusionn-subs/internal/config"
	"github.com/fusionn-subs/internal/types"
	"github.com/fusionn-subs/pkg/logger"
)

// LocalLLMTranslator implements translation using a custom OpenAI-compatible server via llm-subtrans.sh
type LocalLLMTranslator struct {
	scriptPath     string
	workDir        string
	mu             sync.RWMutex
	baseURL        string
	apiKey         string
	model          string
	endpoint       string
	instruction    string
	rateLimit      int
	maxBatchSize   int
	timeout        time.Duration
	targetLanguage string
	outputSuffix   string
}

// NewLocalLLMTranslator creates a new local LLM (custom server) translator
func NewLocalLLMTranslator(cfg config.LocalLLMConfig, targetLang, outputSuffix string) *LocalLLMTranslator {
	scriptPath := os.Getenv("LLM_SUBTRANS_SCRIPT_PATH")
	if scriptPath == "" {
		scriptPath = "/opt/llm-subtrans/llm-subtrans.sh"
	}
	workDir := os.Getenv("LLM_SUBTRANS_DIR")
	if workDir == "" {
		workDir = "/opt/llm-subtrans"
	}

	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = "/v1/chat/completions"
	}

	rateLimit := cfg.RateLimit
	if rateLimit == 0 {
		rateLimit = 10
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = config.DefaultLocalLLMTimeout
	}

	return &LocalLLMTranslator{
		scriptPath:     scriptPath,
		workDir:        workDir,
		baseURL:        cfg.BaseURL,
		apiKey:         cfg.APIKey,
		model:          cfg.Model,
		endpoint:       endpoint,
		instruction:    cfg.Instruction,
		rateLimit:      rateLimit,
		maxBatchSize:   cfg.MaxBatchSize,
		timeout:        timeout,
		targetLanguage: targetLang,
		outputSuffix:   outputSuffix,
	}
}

// Translate translates subtitles using a local/custom OpenAI-compatible endpoint
func (t *LocalLLMTranslator) Translate(ctx context.Context, msg types.JobMessage) (string, error) {
	if err := msg.Validate(); err != nil {
		return "", fmt.Errorf("invalid message: %w", err)
	}

	outputPath := msg.OutputPath(t.outputSuffix)

	t.mu.RLock()
	baseURL := t.baseURL
	apiKey := t.apiKey
	model := t.model
	endpoint := t.endpoint
	instruction := t.instruction
	rateLimit := t.rateLimit
	maxBatchSize := t.maxBatchSize
	timeout := t.timeout
	targetLanguage := t.targetLanguage
	t.mu.RUnlock()

	ctxTimeout, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := []string{
		msg.SubtitlePath,
		"-o", outputPath,
		"-l", targetLanguage,
		"-s", baseURL,
		"-e", endpoint,
	}

	if strings.TrimSpace(apiKey) != "" {
		args = append(args, "-k", apiKey)
	}

	if strings.TrimSpace(model) != "" {
		args = append(args, "-m", model)
	}

	if strings.Contains(strings.ToLower(endpoint), "chat") {
		args = append(args, "--chat", "--systemmessages")
	}

	if mediaTitle := strings.TrimSpace(msg.MediaTitle); mediaTitle != "" {
		args = append(args, "--moviename", mediaTitle)
	}

	if instruction != "" {
		args = append(args, "--instruction", instruction)
	}

	if rateLimit > 0 {
		args = append(args, "--ratelimit", strconv.Itoa(rateLimit))
	}

	if maxBatchSize > 0 {
		args = append(args, "--maxbatchsize", strconv.Itoa(maxBatchSize))
	}

	cmd := exec.CommandContext(ctxTimeout, t.scriptPath, args...)
	if t.workDir != "" {
		cmd.Dir = t.workDir
	}

	cmd.Env = append(os.Environ(), "PYTHONUNBUFFERED=1")

	logger.Infof("🔄 Starting translation (Local LLM): %s → %s", msg.SubtitlePath, outputPath)
	logger.Debugf("Command: %s", maskAPIKeyInCommand(buildCommandLine(t.scriptPath, args)))

	resultPath, _, err := executeScript(cmd, outputPath)
	if err != nil {
		os.Remove(outputPath)
		return "", err
	}

	return resultPath, nil
}

// UpdateFromConfig reloads translator settings from the full config (hot-reload)
func (t *LocalLLMTranslator) UpdateFromConfig(cfg *config.Config) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.baseURL = cfg.LocalLLM.BaseURL
	t.apiKey = cfg.LocalLLM.APIKey
	t.model = cfg.LocalLLM.Model
	t.endpoint = cfg.LocalLLM.Endpoint
	if t.endpoint == "" {
		t.endpoint = "/v1/chat/completions"
	}
	t.instruction = cfg.LocalLLM.Instruction
	t.rateLimit = cfg.LocalLLM.RateLimit
	t.maxBatchSize = cfg.LocalLLM.MaxBatchSize
	t.timeout = cfg.LocalLLM.Timeout
	if t.timeout == 0 {
		t.timeout = config.DefaultLocalLLMTimeout
	}

	logger.Infof("🔄 Local LLM config reloaded: %s (model: %s)", t.baseURL, t.model)
}
