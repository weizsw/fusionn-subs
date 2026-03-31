package translator

import (
	"context"
	"errors"
	"fmt"

	"github.com/fusionn-subs/internal/config"
	"github.com/fusionn-subs/internal/types"
	"github.com/fusionn-subs/pkg/logger"
)

type Translator interface {
	Translate(ctx context.Context, msg types.JobMessage) (string, error)
}

type ConfigUpdater interface {
	UpdateFromConfig(cfg *config.Config)
}

type namedTranslator struct {
	name       string
	translator Translator
}

type FallbackTranslator struct {
	translators []namedTranslator
}

func (f *FallbackTranslator) Translate(ctx context.Context, msg types.JobMessage) (string, error) {
	var lastErr error
	for _, nt := range f.translators {
		out, err := nt.translator.Translate(ctx, msg)
		if err == nil {
			return out, nil
		}
		if errors.Is(err, ErrRateLimited) {
			return "", err
		}
		if errors.Is(err, ErrAllModelsExhausted) {
			logger.Warnf("translator provider %s: all models exhausted, trying next provider", nt.name)
			lastErr = err
			continue
		}
		logger.Warnf("translator provider %s failed: %v, trying next provider", nt.name, err)
		lastErr = err
	}
	if lastErr != nil {
		return "", fmt.Errorf("all providers failed, last error: %w", lastErr)
	}
	return "", fmt.Errorf("all providers failed")
}

func (f *FallbackTranslator) UpdateFromConfig(cfg *config.Config) {
	for _, nt := range f.translators {
		if u, ok := nt.translator.(ConfigUpdater); ok {
			u.UpdateFromConfig(cfg)
		}
	}
}

func NewTranslator(ctx context.Context, cfg *config.Config) (Translator, error) {
	targetLang := cfg.Translator.TargetLanguage
	outputSuffix := cfg.Translator.OutputSuffix

	if len(cfg.Translator.Providers) > 0 {
		list := make([]namedTranslator, 0, len(cfg.Translator.Providers))
		for _, p := range cfg.Translator.Providers {
			var t Translator
			switch p {
			case "gemini":
				t = NewGeminiTranslator(ctx, cfg.Gemini, targetLang, outputSuffix)
			case "openrouter":
				t = NewOpenRouterTranslator(cfg.OpenRouter, targetLang, outputSuffix)
			case "local_llm":
				t = NewLocalLLMTranslator(cfg.LocalLLM, targetLang, outputSuffix)
			default:
				return nil, fmt.Errorf("unknown translator provider: %q", p)
			}
			list = append(list, namedTranslator{name: p, translator: t})
		}
		logger.Infof("🤖 Using providers: %v", cfg.Translator.Providers)
		if len(list) == 1 {
			return list[0].translator, nil
		}
		return &FallbackTranslator{translators: list}, nil
	}

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
