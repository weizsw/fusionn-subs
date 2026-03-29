package translator

import (
	"context"
	"fmt"

	"github.com/fusionn-subs/internal/config"
	"github.com/fusionn-subs/internal/types"
	"github.com/fusionn-subs/pkg/logger"
)

type Translator interface {
	Translate(ctx context.Context, msg types.JobMessage) (string, error)
}

func NewTranslator(ctx context.Context, cfg *config.Config) (Translator, error) {
	targetLang := cfg.Translator.TargetLanguage
	outputSuffix := cfg.Translator.OutputSuffix

	if cfg.Gemini.APIKey != "" {
		logger.Infof("🤖 Using Gemini translator (primary: %s, secondary: %s)",
			cfg.Gemini.PrimaryModel.Name, cfg.Gemini.SecondaryModel.Name)
		return NewGeminiTranslator(ctx, cfg.Gemini, targetLang, outputSuffix), nil
	}

	if cfg.OpenRouter.APIKey != "" {
		if cfg.OpenRouter.AutoSelectModel {
			logger.Infof("🤖 Using OpenRouter translator (auto-selection enabled)")
		} else {
			logger.Infof("🤖 Using OpenRouter translator (model: %s)", cfg.OpenRouter.Model)
		}
		return NewOpenRouterTranslator(cfg.OpenRouter, targetLang, outputSuffix), nil
	}

	return nil, fmt.Errorf("no translator configured: gemini.api_key is required")
}
