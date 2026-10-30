package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/fusionn-subs/internal/client/callback"
	"github.com/fusionn-subs/internal/config"
	"github.com/fusionn-subs/internal/service/glossary"
	"github.com/fusionn-subs/internal/service/translator"
	"github.com/fusionn-subs/internal/service/worker"
	sqlitestore "github.com/fusionn-subs/internal/storage/sqlite"
	"github.com/fusionn-subs/internal/types"
	"github.com/fusionn-subs/internal/version"
	"github.com/fusionn-subs/pkg/logger"
)

var (
	log      = logger.Component("main")
	redisLog = logger.Component("redis")
)

func main() {
	if err := run(); err != nil && !errors.Is(err, context.Canceled) {
		log.Error(context.Background(), "service_failed", "Service failed", zap.Error(err))
		logger.Sync()
		os.Exit(1)
	}
}

func run() error {
	// Initialize the process-wide logger. LOG_LEVEL selects the level.
	logger.Configure(logger.Config{})
	defer logger.Sync()

	// Startup banner: once per start, with the level that is actually in effect.
	version.PrintBanner(nil, logger.Log.Level().CapitalString())

	// Load configuration
	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "config/config.yaml"
	}

	cfgMgr, err := config.NewManager(configPath)
	if err != nil {
		return fmt.Errorf("config error: %w", err)
	}
	defer cfgMgr.Stop()
	cfg := cfgMgr.Get()
	logger.SetConfigLevel(cfg.LogLevel)
	cfgMgr.OnChange(func(_, updated *config.Config) { logger.SetConfigLevel(updated.LogLevel) })

	// Log config values (masked)
	logConfig(cfg)

	// Initialize Redis
	redisClient, err := initRedis(cfg.Redis.URL)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := redisClient.Close(); closeErr != nil {
			log.Warn(context.Background(), "resource_close_failed", "Redis close failed",
				zap.String("resource", "redis"),
				zap.Error(closeErr),
			)
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
	log.Debug(context.Background(), "callback_configured", "Callback configured",
		zap.Int("max_retries", cfg.Callback.MaxRetries),
	)

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

	log.Info(context.Background(), "service_started", "Service started",
		zap.String("version", version.Version),
		zap.String("queue", cfg.Redis.Queue),
	)

	// Run worker (blocks until context canceled)
	err = workerSvc.Run(ctx)

	log.Info(context.Background(), "service_stopped", "Service stopped")

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
			log.Warn(context.Background(), "resource_close_failed", "Glossary DB close failed",
				zap.String("resource", "glossary_db"),
				zap.Error(closeErr),
			)
		}
	}

	glossaryLLM, err := newGlossaryLLM(ctx, cfg)
	if err != nil {
		cleanup()
		return nil, func() {}, err
	}

	log.Info(context.Background(), "glossary_enabled", "Glossary enabled",
		zap.String("db_path", cfg.Glossary.DBPath),
	)
	svc := glossary.NewService(glossary.ServiceConfig{
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
	}, sqlitestore.NewGlossaryStore(db), glossaryLLM)
	return glossaryWorkerPreparer{svc: svc}, cleanup, nil
}

type glossaryWorkerPreparer struct {
	svc *glossary.Service
}

func (p glossaryWorkerPreparer) Prepare(ctx context.Context, msg types.JobMessage) (worker.GlossaryPayload, error) {
	payload, err := p.svc.Prepare(ctx, msg)
	if err != nil {
		return worker.GlossaryPayload{}, err
	}
	terminology := make([]translator.Terminology, 0, len(payload.Terminology))
	for _, term := range payload.Terminology {
		terminology = append(terminology, translator.Terminology{
			Source: term.Source,
			Target: term.Target,
		})
	}
	return worker.GlossaryPayload{
		Terminology:         terminology,
		BuildTerminologyMap: payload.BuildTerminologyMap,
	}, nil
}

func newGlossaryLLM(ctx context.Context, cfg *config.Config) (glossary.LLMClient, error) {
	switch cfg.Glossary.LLM.Provider {
	case config.ProviderOpenAICompatible:
		return glossary.NewOpenAICompatibleClient(glossary.OpenAICompatibleConfig{
			BaseURL:         cfg.Glossary.LLM.BaseURL,
			Endpoint:        cfg.Glossary.LLM.Endpoint,
			APIKey:          cfg.Glossary.LLM.APIKey,
			Model:           cfg.Glossary.LLM.Model,
			ReasoningEffort: cfg.Glossary.LLM.ReasoningEffort,
			Temperature:     cfg.Glossary.LLM.Temperature,
		}), nil
	case config.ProviderGemini:
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
	redisLog.Info(context.Background(), "redis_connecting", "Connecting to Redis")

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

	redisLog.Info(context.Background(), "redis_connected", "Redis connected")
	return client, nil
}

func logConfig(cfg *config.Config) {
	// INFO carries a short, safe summary. DEBUG carries the masked values of
	// configuration keys, minus URLs and instruction text, which may hold
	// credentials or prompt content.
	attrs := []zap.Field{
		zap.String("queue", cfg.Redis.Queue),
		zap.String("target_language", cfg.Translator.TargetLanguage),
		zap.Int("max_translation_retries", cfg.Translator.MaxTranslationRetries),
		zap.Bool("glossary_enabled", cfg.Glossary.Enabled),
	}
	if len(cfg.Translator.Providers) > 0 {
		attrs = append(attrs, zap.String("translator_providers", strings.Join(cfg.Translator.Providers, ",")))
	}
	log.Info(context.Background(), "config_effective", "Configuration effective", attrs...)

	cfgValues := cfg.SafeLogValues()
	keys := make([]string, 0, len(cfgValues))
	for k := range cfgValues {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if config.IsOpaqueLogField(key) {
			continue
		}
		log.Debug(context.Background(), "config_value", "Config value",
			zap.String("key", key),
			zap.Any("value", cfgValues[key]),
		)
	}
}
