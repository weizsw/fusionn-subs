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
	"github.com/fusionn-subs/internal/service/glossary"
	"github.com/fusionn-subs/internal/service/translator"
	"github.com/fusionn-subs/internal/service/worker"
	sqlitestore "github.com/fusionn-subs/internal/storage/sqlite"
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
	defer cfgMgr.Stop()
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Initialize services
	translatorSvc, err := translator.NewTranslator(ctx, cfg)
	if err != nil {
		return fmt.Errorf("translator error: %w", err)
	}

	glossarySvc, cleanupGlossary, err := initGlossary(ctx, cfg)
	if err != nil {
		return err
	}
	defer cleanupGlossary()

	// Set default retry config if not provided
	if cfg.Callback.MaxRetries == 0 {
		cfg.Callback.MaxRetries = 5
	}
	if len(cfg.Callback.RetryBackoffSeconds) == 0 {
		cfg.Callback.RetryBackoffSeconds = []int{1, 2, 4, 8, 16}
	}
	if cfg.Callback.Timeout == 0 {
		cfg.Callback.Timeout = config.DefaultCallbackTimeout
	}

	callbackClient := callback.NewClient(
		cfg.Callback.URL,
		cfg.Callback.Timeout,
		cfg.Callback.MaxRetries,
		cfg.Callback.RetryBackoffSeconds,
	)
	logger.Infof("📤 Callback: %s (retries: %d)", cfg.Callback.URL, cfg.Callback.MaxRetries)

	// Set default translator retry config if not provided
	if cfg.Translator.MaxTranslationRetries == 0 {
		cfg.Translator.MaxTranslationRetries = 3
	}

	if updater, ok := translatorSvc.(translator.ConfigUpdater); ok {
		cfgMgr.OnChange(func(old, new *config.Config) {
			updater.UpdateFromConfig(new)
		})
	}

	workerSvc := worker.New(redisClient, worker.Config{
		Queue:                 cfg.Redis.Queue,
		PollTimeout:           config.DefaultWorkerPollTimeout,
		MaxTranslationRetries: cfg.Translator.MaxTranslationRetries,
	}, translatorSvc, glossarySvc, callbackClient)

	logger.Info("")
	logger.Info("────────────────────────────────────────────")
	logger.Infof("✅ Ready! Listening on queue: %s", cfg.Redis.Queue)
	logger.Info("────────────────────────────────────────────")

	// Run worker (blocks until context canceled)
	err = workerSvc.Run(ctx)

	fmt.Println()
	logger.Info("👋 Goodbye!")

	return err
}

func initGlossary(ctx context.Context, cfg *config.Config) (worker.GlossaryPreparer, func(), error) {
	if !cfg.Glossary.Enabled {
		return nil, func() {}, nil
	}

	db, err := sqlitestore.Open(ctx, cfg.Glossary.DBPath)
	if err != nil {
		return nil, func() {}, fmt.Errorf("glossary sqlite error: %w", err)
	}
	cleanup := func() {
		if closeErr := db.Close(); closeErr != nil {
			logger.Errorf("Glossary DB close error: %v", closeErr)
		}
	}

	glossaryLLM, err := newGlossaryLLM(ctx, cfg)
	if err != nil {
		cleanup()
		return nil, func() {}, err
	}

	logger.Infof("📚 Glossary enabled: %s", cfg.Glossary.DBPath)
	return glossary.NewService(glossary.ServiceConfig{
		Enabled:                   cfg.Glossary.Enabled,
		TargetLanguage:            cfg.Glossary.TargetLanguage,
		MinConfidence:             cfg.Glossary.MinConfidence,
		InjectMinConfidence:       cfg.Glossary.InjectMinConfidence,
		MaxPromptEntries:          cfg.Glossary.MaxPromptEntries,
		MaxCandidates:             cfg.Glossary.MaxCandidates,
		MaxSnippetsPerCandidate:   cfg.Glossary.MaxSnippetsPerCandidate,
		MaxSubtitleBytes:          cfg.Glossary.MaxSubtitleBytes,
		MaxCues:                   cfg.Glossary.MaxCues,
		MaxActiveVariantsPerTerm:  cfg.Glossary.MaxActiveVariantsPerTerm,
		MaxObservationsPerVariant: cfg.Glossary.MaxObservationsPerVariant,
		PromoteMinConfidence:      cfg.Glossary.PromoteMinConfidence,
		PromoteMinMediaCount:      cfg.Glossary.PromoteMinMediaCount,
		LLMTimeout:                cfg.Glossary.LLM.Timeout,
	}, sqlitestore.NewGlossaryStore(db), glossaryLLM), cleanup, nil
}

func newGlossaryLLM(ctx context.Context, cfg *config.Config) (glossary.LLMClient, error) {
	switch cfg.Glossary.LLM.Provider {
	case "openai_compatible":
		return glossary.NewOpenAICompatibleClient(glossary.OpenAICompatibleConfig{
			BaseURL:     cfg.Glossary.LLM.BaseURL,
			Endpoint:    cfg.Glossary.LLM.Endpoint,
			APIKey:      cfg.Glossary.LLM.APIKey,
			Model:       cfg.Glossary.LLM.Model,
			Temperature: cfg.Glossary.LLM.Temperature,
		}), nil
	case "gemini":
		apiKey := cfg.Glossary.LLM.APIKey
		if apiKey == "" {
			apiKey = cfg.Gemini.APIKey
		}
		client, err := glossary.NewGeminiClient(ctx, apiKey, cfg.Glossary.LLM.Model)
		if err != nil {
			return nil, fmt.Errorf("glossary gemini client error: %w", err)
		}
		return client, nil
	default:
		return nil, fmt.Errorf("unsupported glossary llm provider: %s", cfg.Glossary.LLM.Provider)
	}
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
