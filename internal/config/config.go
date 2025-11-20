package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	RedisURL         string
	RedisQueue       string
	CallbackURL      string
	GeminiAPIKey     string
	GeminiModel      string
	GeminiScriptPath string
	GeminiWorkingDir string
	TargetLanguage   string
	OutputSuffix     string
	PollTimeout      time.Duration
	ScriptTimeout    time.Duration
	HTTPTimeout      time.Duration
	HTTPMaxRetries   int
	RateLimit        int
	LogLevel         string
}

func Load() (Config, error) {
	cfg := Config{
		RedisURL:         getEnv("REDIS_URL", "redis://192.168.50.135:6379"),
		RedisQueue:       getEnv("REDIS_QUEUE", "translate_queue"),
		CallbackURL:      getEnv("CALLBACK_URL", "http://192.168.50.135:4664/api/v1/async_merge"),
		GeminiAPIKey:     os.Getenv("GEMINI_API_KEY"),
		GeminiModel:      getEnv("GEMINI_MODEL", "Gemini 2.0 Flash"),
		GeminiScriptPath: getEnv("GEMINI_SCRIPT_PATH", "/opt/llm-subtrans/gemini-subtrans.sh"),
		GeminiWorkingDir: getEnv("GEMINI_WORKDIR", "/opt/llm-subtrans"),
		TargetLanguage:   getEnv("TARGET_LANGUAGE", "Chinese"),
		OutputSuffix:     getEnv("OUTPUT_SUFFIX", ".chs.srt"),
		LogLevel:         getEnv("LOG_LEVEL", "info"),
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

	if cfg.RateLimit, err = parseInt("GEMINI_RATELIMIT", 8); err != nil {
		return Config{}, err
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
