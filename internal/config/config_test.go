package config

import (
	"strings"
	"testing"
	"time"
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
