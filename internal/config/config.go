package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cast"
)

type Config struct {
	RedisURL           string
	RedisQueue         string
	CallbackURL        string
	GeminiAPIKey       string
	GeminiModel        string
	GeminiScriptPath   string
	GeminiWorkingDir   string
	GeminiInstruction  string
	GeminiMaxBatchSize int
	TargetLanguage     string
	OutputSuffix       string
	PollTimeout        time.Duration
	ScriptTimeout      time.Duration
	HTTPTimeout        time.Duration
	HTTPMaxRetries     int
	RateLimit          int
	LogLevel           string
}

func Load() (Config, error) {
	cfg := Config{
		RedisURL:           getEnv("REDIS_URL", "redis://192.168.50.135:6379"),
		RedisQueue:         getEnv("REDIS_QUEUE", "translate_queue"),
		CallbackURL:        getEnv("CALLBACK_URL", "http://192.168.50.135:4664/api/v1/async_merge"),
		GeminiAPIKey:       os.Getenv("GEMINI_API_KEY"),
		GeminiModel:        getEnv("GEMINI_MODEL", "gemini-2.5-flash-latest"),
		GeminiScriptPath:   getEnv("GEMINI_SCRIPT_PATH", "/opt/llm-subtrans/gemini-subtrans.sh"),
		GeminiWorkingDir:   getEnv("GEMINI_WORKDIR", "/opt/llm-subtrans"),
		GeminiInstruction:  getEnv("GEMINI_INSTRUCTION", ""),
		GeminiMaxBatchSize: cast.ToInt(getEnv("GEMINI_MAX_BATCH_SIZE", "20")),
		TargetLanguage:     getEnv("TARGET_LANGUAGE", "Chinese"),
		OutputSuffix:       getEnv("OUTPUT_SUFFIX", "chs"),
		LogLevel:           getEnv("LOG_LEVEL", "info"),
		RateLimit:          cast.ToInt(getEnv("RATE_LIMIT", "8")),
	}

	var err error
	if cfg.PollTimeout, err = parseDuration("POLL_TIMEOUT", "5s"); err != nil {
		return Config{}, err
	}

	if cfg.ScriptTimeout, err = parseDuration("SCRIPT_TIMEOUT", "15m"); err != nil {
		return Config{}, err
	}

	if cfg.HTTPTimeout, err = parseDuration("HTTP_TIMEOUT", "15s"); err != nil {
		return Config{}, err
	}

	if cfg.HTTPMaxRetries, err = parseInt("HTTP_MAX_RETRIES", 3); err != nil {
		return Config{}, err
	}

	if val := getEnv("GEMINI_RATELIMIT", ""); val != "" {
		if cfg.RateLimit, err = strconv.Atoi(val); err != nil {
			return Config{}, fmt.Errorf("invalid integer for GEMINI_RATELIMIT: %w", err)
		}
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func (c Config) Validate() error {
	switch {
	case c.RedisURL == "":
		return errors.New("REDIS_URL is required")
	case c.RedisQueue == "":
		return errors.New("REDIS_QUEUE is required")
	case c.CallbackURL == "":
		return errors.New("CALLBACK_URL is required")
	case c.GeminiAPIKey == "":
		return errors.New("GEMINI_API_KEY is required")
	case c.GeminiScriptPath == "":
		return errors.New("GEMINI_SCRIPT_PATH is required")
	case c.GeminiWorkingDir == "":
		return errors.New("GEMINI_WORKDIR is required")
	}

	return nil
}

func getEnv(key string, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}

	return fallback
}

func parseDuration(key string, fallback string) (time.Duration, error) {
	value := getEnv(key, fallback)
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("invalid duration for %s: %w", key, err)
	}

	return d, nil
}

func parseInt(key string, fallback int) (int, error) {
	value := getEnv(key, "")
	if value == "" {
		return fallback, nil
	}

	i, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid integer for %s: %w", key, err)
	}

	return i, nil
}

func (c Config) SafeLogValues() map[string]any {
	return map[string]any{
		"redis_url":          c.RedisURL,
		"redis_queue":        c.RedisQueue,
		"callback_url":       c.CallbackURL,
		"gemini_api_key":     maskSecret(c.GeminiAPIKey),
		"gemini_model":       c.GeminiModel,
		"gemini_script_path": c.GeminiScriptPath,
		"gemini_working_dir": c.GeminiWorkingDir,
		"gemini_instruction": c.GeminiInstruction,
		"gemini_max_batch":   c.GeminiMaxBatchSize,
		"target_language":    c.TargetLanguage,
		"output_suffix":      c.OutputSuffix,
		"poll_timeout":       c.PollTimeout,
		"script_timeout":     c.ScriptTimeout,
		"http_timeout":       c.HTTPTimeout,
		"http_max_retries":   c.HTTPMaxRetries,
		"rate_limit":         c.RateLimit,
		"log_level":          c.LogLevel,
	}
}

func maskSecret(value string) string {
	if value == "" {
		return ""
	}

	const keep = 4
	if len(value) <= keep {
		return strings.Repeat("*", len(value))
	}

	return value[:keep] + strings.Repeat("*", len(value)-keep)
}

func (c Config) SafeLogPretty() string {
	data, err := json.MarshalIndent(c.SafeLogValues(), "", "  ")
	if err != nil {
		return fmt.Sprintf("marshal config: %v", err)
	}

	return string(data)
}
