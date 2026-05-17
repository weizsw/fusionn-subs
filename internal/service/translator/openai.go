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
	"github.com/fusionn-subs/pkg/logger"
)

type OpenAITranslator struct {
	scriptPath     string
	workDir        string
	mu             sync.RWMutex
	apiKey         string
	model          string
	fallbackModels []string
	apiBase        string
	useHTTPX       bool
	instruction    string
	rateLimit      int
	maxBatchSize   int
	timeout        time.Duration
	targetLanguage string
	outputSuffix   string
}

type openAISnapshot struct {
	apiKey         string
	model          string
	fallbackModels []string
	apiBase        string
	useHTTPX       bool
	instruction    string
	rateLimit      int
	maxBatchSize   int
	timeout        time.Duration
	targetLanguage string
	outputSuffix   string
}

func NewOpenAITranslator(cfg config.OpenAIConfig, targetLang, outputSuffix string) *OpenAITranslator {
	scriptPath := os.Getenv("OPENAI_SCRIPT_PATH")
	if scriptPath == "" {
		scriptPath = "/opt/llm-subtrans/gpt-subtrans.sh"
	}
	workDir := os.Getenv("LLM_SUBTRANS_DIR")
	if workDir == "" {
		workDir = "/opt/llm-subtrans"
	}

	rateLimit := cfg.RateLimit
	if rateLimit == 0 {
		rateLimit = 10
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = config.DefaultOpenAITimeout
	}

	return &OpenAITranslator{
		scriptPath:     scriptPath,
		workDir:        workDir,
		apiKey:         cfg.APIKey,
		model:          cfg.Model,
		fallbackModels: append([]string(nil), cfg.FallbackModels...),
		apiBase:        cfg.APIBase,
		useHTTPX:       cfg.UseHTTPX,
		instruction:    cfg.Instruction,
		rateLimit:      rateLimit,
		maxBatchSize:   cfg.MaxBatchSize,
		timeout:        timeout,
		targetLanguage: targetLang,
		outputSuffix:   outputSuffix,
	}
}

func (t *OpenAITranslator) Translate(ctx context.Context, req Request) (string, error) {
	msg := req.Job
	if err := msg.Validate(); err != nil {
		return "", fmt.Errorf("invalid message: %w", err)
	}

	snapshot := t.snapshot()
	outputPath := msg.OutputPath(snapshot.outputSuffix)

	var lastErr error
	for _, model := range openAIModelAttempts(snapshot) {
		attempt := snapshot
		attempt.model = model

		ctxTimeout, cancel := context.WithTimeout(ctx, attempt.timeout)
		args := buildOpenAIArgs(req, outputPath, attempt)
		cmd := exec.CommandContext(ctxTimeout, t.scriptPath, args...)
		if t.workDir != "" {
			cmd.Dir = t.workDir
		}

		cmd.Env = append(os.Environ(), "PYTHONUNBUFFERED=1")
		if strings.TrimSpace(attempt.apiKey) != "" {
			cmd.Env = append(cmd.Env, "OPENAI_API_KEY="+attempt.apiKey)
		}

		logger.Infof("🔄 Starting translation (OpenAI/%s): %s → %s", model, msg.SubtitlePath, outputPath)
		logger.Debugf("Command: %s", maskAPIKeyInCommand(buildCommandLine(t.scriptPath, args)))

		resultPath, _, err := executeScript(cmd, outputPath)
		cancel()
		if err == nil {
			return resultPath, nil
		}
		lastErr = err
		os.Remove(outputPath)
		logger.Warnf("OpenAI model failed: model=%s error=%v", model, err)
	}

	if lastErr == nil {
		return "", fmt.Errorf("openai model is required")
	}
	return "", fmt.Errorf("openai models failed: %w", lastErr)
}

func (t *OpenAITranslator) buildArgs(req Request, outputPath string) []string {
	return buildOpenAIArgs(req, outputPath, t.snapshot())
}

func (t *OpenAITranslator) snapshot() openAISnapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return openAISnapshot{
		apiKey:         t.apiKey,
		model:          t.model,
		fallbackModels: append([]string(nil), t.fallbackModels...),
		apiBase:        t.apiBase,
		useHTTPX:       t.useHTTPX,
		instruction:    t.instruction,
		rateLimit:      t.rateLimit,
		maxBatchSize:   t.maxBatchSize,
		timeout:        t.timeout,
		targetLanguage: t.targetLanguage,
		outputSuffix:   t.outputSuffix,
	}
}

func buildOpenAIArgs(req Request, outputPath string, snapshot openAISnapshot) []string {
	msg := req.Job
	args := []string{
		msg.SubtitlePath,
		"-o", outputPath,
		"-l", snapshot.targetLanguage,
	}

	if strings.TrimSpace(snapshot.apiKey) != "" {
		args = append(args, "-k", snapshot.apiKey)
	}
	if strings.TrimSpace(snapshot.model) != "" {
		args = append(args, "-m", snapshot.model)
	}
	if strings.TrimSpace(snapshot.apiBase) != "" {
		args = append(args, "-b", snapshot.apiBase)
		if snapshot.useHTTPX {
			args = append(args, "--httpx")
		}
	}

	if mediaTitle := strings.TrimSpace(msg.MediaTitle); mediaTitle != "" {
		args = append(args, "--moviename", mediaTitle)
	}

	if instruction := combineInstructions(snapshot.instruction, req.ExtraInstruction); instruction != "" {
		args = append(args, "--instruction", instruction)
	}

	if snapshot.rateLimit > 0 {
		args = append(args, "--ratelimit", strconv.Itoa(snapshot.rateLimit))
	}

	if snapshot.maxBatchSize > 0 {
		args = append(args, "--maxbatchsize", strconv.Itoa(snapshot.maxBatchSize))
	}

	return appendTerminologyArgs(args, req)
}

func (t *OpenAITranslator) UpdateFromConfig(cfg *config.Config) {
	t.mu.Lock()
	defer t.mu.Unlock()

	openAICfg := cfg.OpenAI
	rateLimit := openAICfg.RateLimit
	if rateLimit == 0 {
		rateLimit = 10
	}
	timeout := openAICfg.Timeout
	if timeout == 0 {
		timeout = config.DefaultOpenAITimeout
	}

	t.apiKey = openAICfg.APIKey
	t.model = openAICfg.Model
	t.fallbackModels = append([]string(nil), openAICfg.FallbackModels...)
	t.apiBase = openAICfg.APIBase
	t.useHTTPX = openAICfg.UseHTTPX
	t.instruction = openAICfg.Instruction
	t.rateLimit = rateLimit
	t.maxBatchSize = openAICfg.MaxBatchSize
	t.timeout = timeout

	logger.Infof("🔄 OpenAI config reloaded: model=%s", t.model)
}

func openAIModelAttempts(snapshot openAISnapshot) []string {
	models := make([]string, 0, 1+len(snapshot.fallbackModels))
	if strings.TrimSpace(snapshot.model) != "" {
		models = append(models, snapshot.model)
	}
	models = append(models, snapshot.fallbackModels...)
	return models
}
