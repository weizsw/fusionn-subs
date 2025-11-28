package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/fusionn-subs/internal/client/callback"
	"github.com/fusionn-subs/internal/config"
	"github.com/fusionn-subs/internal/service/translator"
	"github.com/fusionn-subs/internal/service/worker"
	"github.com/fusionn-subs/internal/version"
	"github.com/fusionn-subs/pkg/logger"
)

func main() {
	if err := run(); err != nil && !errors.Is(err, context.Canceled) {
		logger.Fatalf("❌ Fatal error: %v", err)
	}
}

func run() error {
	// Initialize logger
	isDev := os.Getenv("ENV") != "production"
	logger.Init(isDev)
	defer logger.Sync()

	version.PrintBanner(nil)

	// Load configuration
	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "config/config.yaml"
	}

	logger.Infof("📁 Loading config: %s", configPath)
	cfgMgr, err := config.NewManager(configPath)
	if err != nil {
		return fmt.Errorf("config error: %w", err)
	}
	cfg := cfgMgr.Get()

	// Log config values (masked)
	logConfig(cfg)

	// Initialize Redis
	redisClient, err := initRedis(cfg.Redis.URL)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := redisClient.Close(); closeErr != nil {
			logger.Errorf("Redis close error: %v", closeErr)
		}
	}()

	// Initialize services
	translatorSvc := translator.NewGeminiTranslator(translator.Config{
		ScriptPath:     cfg.Gemini.ScriptPath,
		WorkingDir:     cfg.Gemini.WorkingDir,
		APIKey:         cfg.Gemini.APIKey,
		Model:          cfg.Gemini.Model,
		Instruction:    cfg.Gemini.Instruction,
		MaxBatchSize:   cfg.Gemini.MaxBatchSize,
		TargetLanguage: cfg.Translator.TargetLanguage,
		OutputSuffix:   cfg.Translator.OutputSuffix,
		Timeout:        cfg.Gemini.Timeout,
		RateLimit:      cfg.Gemini.RateLimit,
	})
	logger.Infof("🤖 Translator: %s", cfg.Gemini.Model)

	callbackClient := callback.NewClient(cfg.Callback.URL, cfg.Callback.Timeout, cfg.Callback.MaxRetries)
	logger.Infof("📤 Callback: %s", cfg.Callback.URL)

	workerSvc := worker.New(redisClient, worker.Config{
		Queue:       cfg.Redis.Queue,
		PollTimeout: cfg.Worker.PollTimeout,
	}, translatorSvc, callbackClient)

	logger.Info("")
	logger.Info("────────────────────────────────────────────")
	logger.Infof("✅ Ready! Listening on queue: %s", cfg.Redis.Queue)
	logger.Info("────────────────────────────────────────────")

	// Setup graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Run worker (blocks until context cancelled)
	err = workerSvc.Run(ctx)

	fmt.Println()
	logger.Info("👋 Goodbye!")

	return err
}

func initRedis(url string) (*redis.Client, error) {
	logger.Info("🔗 Connecting to Redis...")

	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("invalid redis URL: %w", err)
	}

	client := redis.NewClient(opts)

	// Verify connection with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		client.Close()
		return nil, fmt.Errorf("redis connection failed: %w", err)
	}

	logger.Info("✅ Redis connected")
	return client, nil
}

func logConfig(cfg *config.Config) {
	cfgValues := cfg.SafeLogValues()
	keys := make([]string, 0, len(cfgValues))
	for k := range cfgValues {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		logger.Debugf("  %s: %v", key, cfgValues[key])
	}
}
