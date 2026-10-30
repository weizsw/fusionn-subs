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

var pacificTZ *time.Location

func init() {
	var err error
	pacificTZ, err = time.LoadLocation("America/Los_Angeles")
	if err != nil {
		panic(fmt.Sprintf("failed to load America/Los_Angeles timezone: %v (ensure tzdata is installed)", err))
	}
}

type GeminiTranslator struct {
	scriptPath     string
	workDir        string
	apiKey         string
	instruction    string
	targetLanguage string
	outputSuffix   string

	mu               sync.RWMutex
	primaryModel     config.GeminiModelConfig
	secondaryModel   config.GeminiModelConfig
	activeModel      *config.GeminiModelConfig
	primaryExhausted bool
}

func NewGeminiTranslator(ctx context.Context, cfg config.GeminiConfig, targetLang, outputSuffix string) *GeminiTranslator {
	scriptPath := os.Getenv("GEMINI_SCRIPT_PATH")
	if scriptPath == "" {
		scriptPath = "/opt/llm-subtrans/gemini-subtrans.sh"
	}
	workDir := os.Getenv("GEMINI_WORKDIR")
	if workDir == "" {
		workDir = "/opt/llm-subtrans"
	}

	t := &GeminiTranslator{
		scriptPath:     scriptPath,
		workDir:        workDir,
		apiKey:         cfg.APIKey,
		instruction:    cfg.Instruction,
		targetLanguage: targetLang,
		outputSuffix:   outputSuffix,
		primaryModel:   cfg.PrimaryModel,
		secondaryModel: cfg.SecondaryModel,
	}
	t.activeModel = &t.primaryModel

	log.Debug(ctx, "translator_configured", "Translator configured",
		zap.String("provider", "gemini"),
		zap.String("primary_model", cfg.PrimaryModel.Name),
		zap.String("secondary_model", cfg.SecondaryModel.Name),
	)

	t.startDailyReset(ctx)

	return t
}

func (t *GeminiTranslator) Translate(ctx context.Context, req Request) (string, error) {
	msg := req.Job
	ctx = withJobID(ctx, msg.JobID)
	if err := msg.Validate(); err != nil {
		return "", fmt.Errorf("invalid message: %w", err)
	}

	outputPath := msg.OutputPath(t.outputSuffix)

	t.mu.RLock()
	model := *t.activeModel
	isPrimary := !t.primaryExhausted
	t.mu.RUnlock()

	ctxTimeout, cancel := context.WithTimeout(ctx, config.DefaultGeminiTimeout)
	defer cancel()

	args := t.buildArgs(req, outputPath, model)

	cmd := exec.CommandContext(ctxTimeout, t.scriptPath, args...)
	if t.workDir != "" {
		cmd.Dir = t.workDir
	}

	cmd.Env = append(os.Environ(), "GEMINI_API_KEY="+t.apiKey, "PYTHONUNBUFFERED=1")

	logToolStarted(ctx, toolGeminiSubtrans, msg, outputPath, zap.String("model", model.Name))
	logToolCommand(ctx, toolGeminiSubtrans, t.scriptPath, args)

	result, err := executeScript(ctxTimeout, toolGeminiSubtrans, cmd, outputPath)
	if err != nil {
		os.Remove(outputPath)

		if result.RateLimited {
			if isPrimary {
				t.switchToSecondary(ctx)
				return "", fmt.Errorf("%w: %s exhausted, switched to %s", ErrRateLimited, model.Name, t.secondaryModel.Name)
			}
			return "", fmt.Errorf("%w: %s also exhausted", ErrAllModelsExhausted, model.Name)
		}

		return "", err
	}

	return result.Path, nil
}

func (t *GeminiTranslator) buildArgs(req Request, outputPath string, model config.GeminiModelConfig) []string {
	msg := req.Job
	args := []string{
		msg.SubtitlePath,
		"-o", outputPath,
		"-l", t.targetLanguage,
		"-k", t.apiKey,
	}

	if model.Name != "" {
		args = append(args, "-m", model.Name)
	}

	if mediaTitle := strings.TrimSpace(msg.MediaTitle); mediaTitle != "" {
		args = append(args, "--moviename", mediaTitle)
	}

	if instruction := combineInstructions(t.instruction, req.ExtraInstruction); instruction != "" {
		args = append(args, "--instruction", instruction)
	}

	if model.RateLimit > 0 {
		args = append(args, "--ratelimit", strconv.Itoa(model.RateLimit))
	}

	if model.MaxBatchSize > 0 {
		args = append(args, "--maxbatchsize", strconv.Itoa(model.MaxBatchSize))
	}

	return appendTerminologyArgs(args, req)
}

func (t *GeminiTranslator) switchToSecondary(ctx context.Context) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.primaryExhausted = true
	t.activeModel = &t.secondaryModel
	log.Warn(ctx, "model_switched", "Primary model rate limited",
		zap.String("from_model", t.primaryModel.Name),
		zap.String("to_model", t.secondaryModel.Name),
		zap.String("reason", "rate_limited"),
	)
}

func (t *GeminiTranslator) ResetToPrimary(ctx context.Context) {
	t.mu.Lock()
	defer t.mu.Unlock()
	wasExhausted := t.primaryExhausted
	t.primaryExhausted = false
	t.activeModel = &t.primaryModel
	if wasExhausted {
		log.Info(ctx, "model_switched", "Switched back to primary model",
			zap.String("from_model", t.secondaryModel.Name),
			zap.String("to_model", t.primaryModel.Name),
			zap.String("reason", "daily_reset"),
		)
	}
}

func (t *GeminiTranslator) UpdateFromConfig(cfg *config.Config) {
	t.mu.Lock()
	defer t.mu.Unlock()

	geminiCfg := cfg.Gemini
	wasPrimaryExhausted := t.primaryExhausted

	t.apiKey = geminiCfg.APIKey
	t.instruction = geminiCfg.Instruction
	t.primaryModel = geminiCfg.PrimaryModel
	t.secondaryModel = geminiCfg.SecondaryModel

	if wasPrimaryExhausted {
		t.activeModel = &t.secondaryModel
	} else {
		t.activeModel = &t.primaryModel
	}

	log.Info(context.Background(), "config_reloaded", "Translator config reloaded",
		zap.String("provider", "gemini"),
		zap.String("primary_model", geminiCfg.PrimaryModel.Name),
		zap.String("secondary_model", geminiCfg.SecondaryModel.Name),
	)
}

func (t *GeminiTranslator) startDailyReset(ctx context.Context) {
	go func() {
		for {
			now := time.Now().In(pacificTZ)
			nextMidnight := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, pacificTZ)
			timer := time.NewTimer(time.Until(nextMidnight))

			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
				t.ResetToPrimary(ctx)
			}
		}
	}()
}

// rateLimitIndicators mark provider quota exhaustion; they drive the Gemini
// primary/secondary model switch.
var rateLimitIndicators = []string{
	"resource_exhausted",
	"quota exceeded",
	"429 too many requests",
	"429 resource exhausted",
	"rate limit exceeded",
}

func isRateLimitError(output string) bool {
	lower := strings.ToLower(output)
	for _, indicator := range rateLimitIndicators {
		if strings.Contains(lower, indicator) {
			return true
		}
	}
	return false
}
