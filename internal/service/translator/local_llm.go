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

	"go.uber.org/zap"

	"github.com/fusionn-subs/internal/config"
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

type localLLMSnapshot struct {
	baseURL        string
	apiKey         string
	model          string
	endpoint       string
	instruction    string
	rateLimit      int
	maxBatchSize   int
	timeout        time.Duration
	targetLanguage string
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
		endpoint = config.DefaultOpenAIChatCompletionsEndpoint
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
func (t *LocalLLMTranslator) Translate(ctx context.Context, req Request) (string, error) {
	msg := req.Job
	ctx = withJobID(ctx, msg.JobID)
	if err := msg.Validate(); err != nil {
		return "", fmt.Errorf("invalid message: %w", err)
	}

	outputPath := msg.OutputPath(t.outputSuffix)

	t.mu.RLock()
	snapshot := localLLMSnapshot{
		baseURL:        t.baseURL,
		apiKey:         t.apiKey,
		model:          t.model,
		endpoint:       t.endpoint,
		instruction:    t.instruction,
		rateLimit:      t.rateLimit,
		maxBatchSize:   t.maxBatchSize,
		timeout:        t.timeout,
		targetLanguage: t.targetLanguage,
	}
	t.mu.RUnlock()

	ctxTimeout, cancel := context.WithTimeout(ctx, snapshot.timeout)
	defer cancel()

	args := buildLocalLLMArgs(req, outputPath, snapshot)

	cmd := exec.CommandContext(ctxTimeout, t.scriptPath, args...)
	if t.workDir != "" {
		cmd.Dir = t.workDir
	}

	cmd.Env = append(os.Environ(), "PYTHONUNBUFFERED=1")

	logToolStarted(ctx, toolLLMSubtrans, msg, outputPath, zap.String("model", snapshot.model))
	logToolCommand(ctx, toolLLMSubtrans, t.scriptPath, args)

	result, err := executeScript(ctxTimeout, toolLLMSubtrans, cmd, outputPath)
	if err != nil {
		os.Remove(outputPath)
		return "", err
	}

	return result.Path, nil
}

func buildLocalLLMArgs(req Request, outputPath string, snapshot localLLMSnapshot) []string {
	msg := req.Job
	args := []string{
		msg.SubtitlePath,
		"-o", outputPath,
		"-l", snapshot.targetLanguage,
		"-s", snapshot.baseURL,
		"-e", snapshot.endpoint,
	}

	if strings.TrimSpace(snapshot.apiKey) != "" {
		args = append(args, "-k", snapshot.apiKey)
	}

	if strings.TrimSpace(snapshot.model) != "" {
		args = append(args, "-m", snapshot.model)
	}

	if strings.Contains(strings.ToLower(snapshot.endpoint), "chat") {
		args = append(args, "--chat", "--systemmessages")
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

// UpdateFromConfig reloads translator settings from the full config (hot-reload)
func (t *LocalLLMTranslator) UpdateFromConfig(cfg *config.Config) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.baseURL = cfg.LocalLLM.BaseURL
	t.apiKey = cfg.LocalLLM.APIKey
	t.model = cfg.LocalLLM.Model
	t.endpoint = cfg.LocalLLM.Endpoint
	if t.endpoint == "" {
		t.endpoint = config.DefaultOpenAIChatCompletionsEndpoint
	}
	t.instruction = cfg.LocalLLM.Instruction
	t.rateLimit = cfg.LocalLLM.RateLimit
	t.maxBatchSize = cfg.LocalLLM.MaxBatchSize
	t.timeout = cfg.LocalLLM.Timeout
	if t.timeout == 0 {
		t.timeout = config.DefaultLocalLLMTimeout
	}

	log.Info(context.Background(), "config_reloaded", "Translator config reloaded",
		zap.String("provider", "local_llm"),
		zap.String("model", t.model),
	)
}
