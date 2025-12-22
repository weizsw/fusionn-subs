package translator

import (
	"context"
	"fmt"

	"github.com/fusionn-subs/internal/config"
	"github.com/fusionn-subs/internal/types"
	"github.com/fusionn-subs/pkg/logger"
)

// Translator interface for subtitle translation
type Translator interface {
	Translate(ctx context.Context, msg types.JobMessage) (string, error)
}

// NewTranslator creates a translator based on the provided configuration.
// It selects OpenRouter if configured, otherwise falls back to Gemini.
// If OpenRouter auto-selection is enabled, the model will be set by the ModelSelector.
func NewTranslator(cfg *config.Config) (Translator, error) {
	targetLang := cfg.Translator.TargetLanguage
	outputSuffix := cfg.Translator.OutputSuffix

	// Prefer OpenRouter if configured
	if cfg.OpenRouter.APIKey != "" {
		if cfg.OpenRouter.AutoSelectModel {
			logger.Infof("🤖 Using OpenRouter translator (auto-selection enabled)")
		} else {
			logger.Infof("🤖 Using OpenRouter translator (model: %s)", cfg.OpenRouter.Model)
		}
		return NewOpenRouterTranslator(cfg.OpenRouter, targetLang, outputSuffix), nil
	}

	// Fall back to Gemini
	if cfg.Gemini.APIKey != "" {
		logger.Infof("🤖 Using Gemini translator (model: %s)", cfg.Gemini.Model)
		return NewGeminiTranslator(cfg.Gemini, targetLang, outputSuffix), nil
	}

	return nil, fmt.Errorf("no translator configured: either openrouter.api_key or gemini.api_key required")
}
