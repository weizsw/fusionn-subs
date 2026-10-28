package translator

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/fusionn-subs/internal/config"
	"github.com/fusionn-subs/internal/types"
)

type Translator interface {
	Translate(ctx context.Context, req Request) (string, error)
}

type Request struct {
	Job                 types.JobMessage
	ExtraInstruction    string
	Terminology         []Terminology
	BuildTerminologyMap bool
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

func (f *FallbackTranslator) Translate(ctx context.Context, req Request) (string, error) {
	var lastErr error
	for _, nt := range f.translators {
		out, err := nt.translator.Translate(ctx, req)
		if err == nil {
			return out, nil
		}
		if errors.Is(err, ErrRateLimited) {
			return "", err
		}
		if errors.Is(err, ErrAllModelsExhausted) {
			log.Warn(ctx, "provider_exhausted", "Provider exhausted",
				zap.String("provider", nt.name),
				zap.Error(err),
			)
			lastErr = err
			continue
		}
		log.Warn(ctx, "provider_failed", "Provider failed",
			zap.String("provider", nt.name),
			zap.Error(err),
		)
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
			provider := strings.TrimSpace(p)
			var t Translator
			switch provider {
			case config.ProviderGemini:
				t = NewGeminiTranslator(ctx, cfg.Gemini, targetLang, outputSuffix)
			case config.ProviderOpenRouter:
				t = NewOpenRouterTranslator(cfg.OpenRouter, targetLang, outputSuffix)
			case config.ProviderOpenAI:
				t = NewOpenAITranslator(cfg.OpenAI, targetLang, outputSuffix)
			case config.ProviderLocalLLM:
				t = NewLocalLLMTranslator(cfg.LocalLLM, targetLang, outputSuffix)
			default:
				return nil, fmt.Errorf("unknown translator provider: %q", p)
			}
			list = append(list, namedTranslator{name: provider, translator: t})
		}
		providers := make([]string, 0, len(list))
		for _, nt := range list {
			providers = append(providers, nt.name)
		}
		log.Info(ctx, "translator_configured", "Translator configured",
			zap.String("providers", strings.Join(providers, ",")),
		)
		if len(list) == 1 {
			return list[0].translator, nil
		}
		return &FallbackTranslator{translators: list}, nil
	}

	if cfg.Gemini.APIKey != "" {
		log.Info(ctx, "translator_configured", "Translator configured",
			zap.String("provider", "gemini"),
			zap.String("primary_model", cfg.Gemini.PrimaryModel.Name),
			zap.String("secondary_model", cfg.Gemini.SecondaryModel.Name),
		)
		return NewGeminiTranslator(ctx, cfg.Gemini, targetLang, outputSuffix), nil
	}

	if cfg.OpenRouter.APIKey != "" {
		fields := []zap.Field{zap.String("provider", "openrouter")}
		if cfg.OpenRouter.AutoSelectModel {
			fields = append(fields, zap.Bool("auto_select_model", true))
		} else {
			fields = append(fields, zap.String("model", cfg.OpenRouter.Model))
		}
		log.Info(ctx, "translator_configured", "Translator configured", fields...)
		return NewOpenRouterTranslator(cfg.OpenRouter, targetLang, outputSuffix), nil
	}

	return nil, fmt.Errorf("no translator configured: set translator.providers or configure gemini/openrouter/openai credentials")
}
