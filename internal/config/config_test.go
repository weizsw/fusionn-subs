package config

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/fusionn-subs/internal/util"
	"github.com/fusionn-subs/pkg/logger"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func validConfigForTest() *Config {
	return &Config{
		Redis: RedisConfig{
			URL:   "redis://localhost:6379",
			Queue: "translate_queue",
		},
		Callback: CallbackConfig{URL: "http://localhost/callback"},
		Gemini: GeminiConfig{
			APIKey: "gemini-key",
			PrimaryModel: GeminiModelConfig{
				Name: "gemini-2.5-flash",
			},
			SecondaryModel: GeminiModelConfig{
				Name: "gemini-2.5-pro",
			},
		},
		Translator: TranslatorConfig{
			TargetLanguage: "Chinese",
			OutputSuffix:   "chs",
		},
	}
}

func TestGlossaryDisabledDoesNotRequireLLMConfig(t *testing.T) {
	cfg := validConfigForTest()
	cfg.Glossary.Enabled = false

	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestGlossaryEnabledAppliesDefaults(t *testing.T) {
	cfg := validConfigForTest()
	cfg.Glossary.Enabled = true
	cfg.Glossary.DBPath = "/tmp/glossary.db"
	cfg.Glossary.LLM.Provider = "openai_compatible"
	cfg.Glossary.LLM.BaseURL = "http://127.0.0.1:8045"
	cfg.Glossary.LLM.Model = "qwen3:8b"

	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}

	if cfg.Glossary.TargetLanguage != "Chinese" {
		t.Fatalf("target language = %q", cfg.Glossary.TargetLanguage)
	}
	if cfg.Glossary.InjectMinConfidence != 0.80 {
		t.Fatalf("inject min confidence = %v", cfg.Glossary.InjectMinConfidence)
	}
	if cfg.Glossary.LLM.Timeout != time.Minute {
		t.Fatalf("timeout = %v", cfg.Glossary.LLM.Timeout)
	}
}

func TestGlossaryEnabledRequiresDBPath(t *testing.T) {
	cfg := validConfigForTest()
	cfg.Glossary.Enabled = true
	cfg.Glossary.LLM.Provider = "openai_compatible"
	cfg.Glossary.LLM.BaseURL = "http://127.0.0.1:8045"
	cfg.Glossary.LLM.Model = "qwen3:8b"

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "glossary.db_path is required") {
		t.Fatalf("error = %v", err)
	}
}

func TestGlossaryEnabledRequiresSupportedProvider(t *testing.T) {
	cfg := validConfigForTest()
	cfg.Glossary.Enabled = true
	cfg.Glossary.DBPath = "/tmp/glossary.db"
	cfg.Glossary.LLM.Provider = "unknown"
	cfg.Glossary.LLM.Model = "qwen3:8b"

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "unsupported glossary.llm.provider") {
		t.Fatalf("error = %v", err)
	}
}

func TestGlossaryEnabledRequiresTargetLanguage(t *testing.T) {
	cfg := validConfigForTest()
	cfg.Translator.TargetLanguage = ""
	cfg.Glossary.Enabled = true
	cfg.Glossary.DBPath = "/tmp/glossary.db"
	cfg.Glossary.LLM.Provider = "openai_compatible"
	cfg.Glossary.LLM.BaseURL = "http://127.0.0.1:8045"
	cfg.Glossary.LLM.Model = "qwen3:8b"

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "glossary.target_language is required") {
		t.Fatalf("error = %v", err)
	}
}

func TestGlossaryEnabledRequiresLLMModel(t *testing.T) {
	cfg := validConfigForTest()
	cfg.Glossary.Enabled = true
	cfg.Glossary.DBPath = "/tmp/glossary.db"
	cfg.Glossary.LLM.Provider = "openai_compatible"
	cfg.Glossary.LLM.BaseURL = "http://127.0.0.1:8045"

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "glossary.llm.model is required") {
		t.Fatalf("error = %v", err)
	}
}

func TestGlossaryOpenAICompatibleRequiresBaseURL(t *testing.T) {
	cfg := validConfigForTest()
	cfg.Glossary.Enabled = true
	cfg.Glossary.DBPath = "/tmp/glossary.db"
	cfg.Glossary.LLM.Provider = "openai_compatible"
	cfg.Glossary.LLM.BaseURL = "   "
	cfg.Glossary.LLM.Model = "qwen3:8b"

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "glossary.llm.base_url is required") {
		t.Fatalf("error = %v", err)
	}
}

func TestGlossaryGeminiRequiresAPIKeyFallback(t *testing.T) {
	cfg := validConfigForTest()
	cfg.Gemini.APIKey = ""
	cfg.Translator.Providers = []string{"local_llm"}
	cfg.LocalLLM.BaseURL = "http://127.0.0.1:8044"
	cfg.Glossary.Enabled = true
	cfg.Glossary.DBPath = "/tmp/glossary.db"
	cfg.Glossary.LLM.Provider = "gemini"
	cfg.Glossary.LLM.Model = "gemini-2.5-flash"

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "glossary.llm.api_key or gemini.api_key is required") {
		t.Fatalf("error = %v", err)
	}
}

func TestGlossaryGeminiUsesGeminiAPIKeyFallback(t *testing.T) {
	cfg := validConfigForTest()
	cfg.Glossary.Enabled = true
	cfg.Glossary.DBPath = "/tmp/glossary.db"
	cfg.Glossary.LLM.Provider = "gemini"
	cfg.Glossary.LLM.Model = "gemini-2.5-flash"

	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestGlossaryRejectsWhitespaceIdentityFields(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{
			name: "target language",
			mutate: func(cfg *Config) {
				cfg.Translator.TargetLanguage = ""
				cfg.Glossary.TargetLanguage = "   "
			},
			wantErr: "glossary.target_language is required",
		},
		{
			name: "provider",
			mutate: func(cfg *Config) {
				cfg.Glossary.LLM.Provider = "   "
			},
			wantErr: "unsupported glossary.llm.provider",
		},
		{
			name: "model",
			mutate: func(cfg *Config) {
				cfg.Glossary.LLM.Model = "   "
			},
			wantErr: "glossary.llm.model is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfigForTest()
			cfg.Glossary.Enabled = true
			cfg.Glossary.DBPath = "/tmp/glossary.db"
			cfg.Glossary.LLM.Provider = "openai_compatible"
			cfg.Glossary.LLM.BaseURL = "http://127.0.0.1:8045"
			cfg.Glossary.LLM.Model = "qwen3:8b"
			tt.mutate(cfg)

			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestGlossaryRejectsInvalidNumericValues(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{name: "min confidence below range", mutate: func(cfg *Config) { cfg.Glossary.MinConfidence = -0.1 }, wantErr: "glossary.min_confidence must be between 0 and 1"},
		{name: "min confidence above range", mutate: func(cfg *Config) { cfg.Glossary.MinConfidence = 1.1 }, wantErr: "glossary.min_confidence must be between 0 and 1"},
		{name: "inject min confidence below range", mutate: func(cfg *Config) { cfg.Glossary.InjectMinConfidence = -0.1 }, wantErr: "glossary.inject_min_confidence must be between 0 and 1"},
		{name: "promote min confidence above range", mutate: func(cfg *Config) { cfg.Glossary.PromoteMinConfidence = 1.1 }, wantErr: "glossary.promote_min_confidence must be between 0 and 1"},
		{name: "max prompt entries", mutate: func(cfg *Config) { cfg.Glossary.MaxPromptEntries = -1 }, wantErr: "glossary.max_prompt_entries must be positive"},
		{name: "max candidates", mutate: func(cfg *Config) { cfg.Glossary.MaxCandidates = -1 }, wantErr: "glossary.max_candidates must be positive"},
		{name: "max snippets per candidate", mutate: func(cfg *Config) { cfg.Glossary.MaxSnippetsPerCandidate = -1 }, wantErr: "glossary.max_snippets_per_candidate must be positive"},
		{name: "max subtitle bytes", mutate: func(cfg *Config) { cfg.Glossary.MaxSubtitleBytes = -1 }, wantErr: "glossary.max_subtitle_bytes must be positive"},
		{name: "max cues", mutate: func(cfg *Config) { cfg.Glossary.MaxCues = -1 }, wantErr: "glossary.max_cues must be positive"},
		{name: "max active variants per term", mutate: func(cfg *Config) { cfg.Glossary.MaxActiveVariantsPerTerm = -1 }, wantErr: "glossary.max_active_variants_per_term must be positive"},
		{name: "max observations per variant", mutate: func(cfg *Config) { cfg.Glossary.MaxObservationsPerVariant = -1 }, wantErr: "glossary.max_observations_per_variant must be positive"},
		{name: "promote min media count", mutate: func(cfg *Config) { cfg.Glossary.PromoteMinMediaCount = -1 }, wantErr: "glossary.promote_min_media_count must be positive"},
		{name: "llm timeout", mutate: func(cfg *Config) { cfg.Glossary.LLM.Timeout = -time.Second }, wantErr: "glossary.llm.timeout must be positive"},
		{name: "llm temperature", mutate: func(cfg *Config) { cfg.Glossary.LLM.Temperature = -0.1 }, wantErr: "glossary.llm.temperature must be non-negative"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfigForTest()
			cfg.Glossary.Enabled = true
			cfg.Glossary.DBPath = "/tmp/glossary.db"
			cfg.Glossary.LLM.Provider = "openai_compatible"
			cfg.Glossary.LLM.BaseURL = "http://127.0.0.1:8045"
			cfg.Glossary.LLM.Model = "qwen3:8b"
			tt.mutate(cfg)

			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestSafeLogValuesIncludesAllGlossaryValues(t *testing.T) {
	cfg := validConfigForTest()
	cfg.Glossary.Enabled = true
	cfg.Glossary.DBPath = "/tmp/glossary.db"
	cfg.Glossary.LLM.Provider = "openai_compatible"
	cfg.Glossary.LLM.BaseURL = "http://127.0.0.1:8045"
	cfg.Glossary.LLM.APIKey = "glossary-key"
	cfg.Glossary.LLM.Model = "qwen3:8b"

	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}

	values := cfg.SafeLogValues()
	want := map[string]any{
		"glossary.enabled":                      cfg.Glossary.Enabled,
		"glossary.db_path":                      cfg.Glossary.DBPath,
		"glossary.target_language":              cfg.Glossary.TargetLanguage,
		"glossary.min_confidence":               cfg.Glossary.MinConfidence,
		"glossary.inject_min_confidence":        cfg.Glossary.InjectMinConfidence,
		"glossary.max_prompt_entries":           cfg.Glossary.MaxPromptEntries,
		"glossary.max_candidates":               cfg.Glossary.MaxCandidates,
		"glossary.max_snippets_per_candidate":   cfg.Glossary.MaxSnippetsPerCandidate,
		"glossary.max_subtitle_bytes":           cfg.Glossary.MaxSubtitleBytes,
		"glossary.max_cues":                     cfg.Glossary.MaxCues,
		"glossary.max_active_variants_per_term": cfg.Glossary.MaxActiveVariantsPerTerm,
		"glossary.max_observations_per_variant": cfg.Glossary.MaxObservationsPerVariant,
		"glossary.promote_min_confidence":       cfg.Glossary.PromoteMinConfidence,
		"glossary.promote_min_media_count":      cfg.Glossary.PromoteMinMediaCount,
		"glossary.llm.provider":                 cfg.Glossary.LLM.Provider,
		"glossary.llm.base_url":                 cfg.Glossary.LLM.BaseURL,
		"glossary.llm.endpoint":                 cfg.Glossary.LLM.Endpoint,
		"glossary.llm.api_key":                  util.MaskSecret(cfg.Glossary.LLM.APIKey),
		"glossary.llm.model":                    cfg.Glossary.LLM.Model,
		"glossary.llm.timeout":                  cfg.Glossary.LLM.Timeout.String(),
		"glossary.llm.temperature":              cfg.Glossary.LLM.Temperature,
	}

	for key, wantValue := range want {
		if got := values[key]; got != wantValue {
			t.Fatalf("%s = %#v, want %#v", key, got, wantValue)
		}
	}
	if values["glossary.llm.api_key"] == cfg.Glossary.LLM.APIKey {
		t.Fatalf("glossary.llm.api_key was not masked")
	}
	for key, value := range values {
		text, ok := value.(string)
		if ok && strings.Contains(text, cfg.Glossary.LLM.APIKey) {
			t.Fatalf("%s contains raw glossary API key", key)
		}
	}
}

func TestLogChangesMasksGlossaryLLMAPIKey(t *testing.T) {
	const oldSecret = "old-glossary-secret"
	const newSecret = "new-glossary-secret"

	oldCfg := validConfigForTest()
	oldCfg.Glossary.LLM.APIKey = oldSecret
	newCfg := validConfigForTest()
	newCfg.Glossary.LLM.APIKey = newSecret

	var buf bytes.Buffer
	encoderConfig := zapcore.EncoderConfig{MessageKey: "msg"}
	core := zapcore.NewCore(
		zapcore.NewConsoleEncoder(encoderConfig),
		zapcore.AddSync(&buf),
		zapcore.InfoLevel,
	)
	previous := logger.Log
	logger.Log = zap.New(core).Sugar()
	t.Cleanup(func() {
		logger.Log = previous
	})

	logChanges(oldCfg, newCfg, "")

	output := buf.String()
	if strings.Contains(output, oldSecret) || strings.Contains(output, newSecret) {
		t.Fatalf("log output leaked raw secret: %s", output)
	}
	if !strings.Contains(output, util.MaskSecret(oldSecret)) || !strings.Contains(output, util.MaskSecret(newSecret)) {
		t.Fatalf("log output did not include masked secrets: %s", output)
	}
}
