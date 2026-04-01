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

	logger.Infof("🤖 Gemini translator: primary=%s, secondary=%s", cfg.PrimaryModel.Name, cfg.SecondaryModel.Name)

	t.startDailyReset(ctx)

	return t
}

func (t *GeminiTranslator) Translate(ctx context.Context, msg types.JobMessage) (string, error) {
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

	if t.instruction != "" {
		args = append(args, "--instruction", t.instruction)
	}

	if model.RateLimit > 0 {
		args = append(args, "--ratelimit", strconv.Itoa(model.RateLimit))
	}

	if model.MaxBatchSize > 0 {
		args = append(args, "--maxbatchsize", strconv.Itoa(model.MaxBatchSize))
	}

	cmd := exec.CommandContext(ctxTimeout, t.scriptPath, args...)
	if t.workDir != "" {
		cmd.Dir = t.workDir
	}

	cmd.Env = append(os.Environ(), "GEMINI_API_KEY="+t.apiKey, "PYTHONUNBUFFERED=1")

	logger.Infof("🔄 Starting translation (Gemini/%s): %s → %s", model.Name, msg.SubtitlePath, outputPath)
	logger.Debugf("Command: %s", maskAPIKeyInCommand(buildCommandLine(t.scriptPath, args)))

	resultPath, combinedOutput, err := executeScript(cmd, outputPath)
	if err != nil {
		os.Remove(outputPath)

		if isRateLimitError(combinedOutput) {
			if isPrimary {
				t.switchToSecondary()
				return "", fmt.Errorf("%w: %s exhausted, switched to %s", ErrRateLimited, model.Name, t.secondaryModel.Name)
			}
			return "", fmt.Errorf("%w: %s also exhausted", ErrAllModelsExhausted, model.Name)
		}

		return "", err
	}

	return resultPath, nil
}

func (t *GeminiTranslator) switchToSecondary() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.primaryExhausted = true
	t.activeModel = &t.secondaryModel
	logger.Infof("⚠️ Primary model rate-limited, switching to secondary: %s", t.secondaryModel.Name)
}

func (t *GeminiTranslator) ResetToPrimary() {
	t.mu.Lock()
	defer t.mu.Unlock()
	wasExhausted := t.primaryExhausted
	t.primaryExhausted = false
	t.activeModel = &t.primaryModel
	if wasExhausted {
		logger.Infof("🔄 Daily reset: switched back to primary model (%s)", t.primaryModel.Name)
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

	logger.Infof("🔄 Gemini config reloaded: primary=%s, secondary=%s", geminiCfg.PrimaryModel.Name, geminiCfg.SecondaryModel.Name)
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
				t.ResetToPrimary()
			}
		}
	}()
}

func isRateLimitError(output string) bool {
	lower := strings.ToLower(output)

	geminiIndicators := []string{
		"resource_exhausted",
		"quota exceeded",
	}
	for _, ind := range geminiIndicators {
		if strings.Contains(lower, ind) {
			return true
		}
	}

	genericIndicators := []string{
		"429 too many requests",
		"429 resource exhausted",
		"rate limit exceeded",
	}
	for _, ind := range genericIndicators {
		if strings.Contains(lower, ind) {
			return true
		}
	}
	return false
}
