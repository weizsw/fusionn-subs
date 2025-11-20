package app

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/fusionn-subs/internal/callback"
	"github.com/fusionn-subs/internal/config"
	"github.com/fusionn-subs/internal/translator"
	"github.com/fusionn-subs/internal/worker"
)

type App struct {
	redis  *redis.Client
	worker *worker.Worker
	logger *zap.Logger
}

func New(cfg config.Config, logger *zap.Logger) (*App, error) {
	if logger == nil {
		logger = zap.NewNop()
	}
	redisOpts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}

	redisClient := redis.NewClient(redisOpts)

	translatorLogger := logger.With(zap.String("component", "translator"))
	translatorSvc := translator.NewGeminiTranslator(translator.GeminiConfig{
		ScriptPath:     cfg.GeminiScriptPath,
		WorkingDir:     cfg.GeminiWorkingDir,
		APIKey:         cfg.GeminiAPIKey,
		Model:          cfg.GeminiModel,
		TargetLanguage: cfg.TargetLanguage,
		OutputSuffix:   cfg.OutputSuffix,
		Timeout:        cfg.ScriptTimeout,
		RateLimit:      cfg.RateLimit,
		Logger:         translatorLogger,
	})

	callbackLogger := logger.With(zap.String("component", "callback"))
	callbackClient := callback.NewClient(cfg.CallbackURL, cfg.HTTPTimeout, cfg.HTTPMaxRetries, callbackLogger)

	workerLogger := logger.With(zap.String("component", "worker"))
	workerInstance := worker.New(redisClient, worker.Config{
		Queue:       cfg.RedisQueue,
		PollTimeout: cfg.PollTimeout,
	}, translatorSvc, callbackClient, workerLogger)

	return &App{
		redis:  redisClient,
		worker: workerInstance,
		logger: logger,
	}, nil
}

func (a *App) Run(ctx context.Context) error {
	a.logger.Info("fusionn-subs worker starting")
	err := a.worker.Run(ctx)
	if err != nil && err != context.Canceled {
		return err
	}
	return nil
}

func (a *App) Close() error {
	a.logger.Info("shutting down")
	return a.redis.Close()
}
