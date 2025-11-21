package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"sort"
	"syscall"

	"github.com/fusionn-subs/internal/app"
	"github.com/fusionn-subs/internal/config"
	"github.com/fusionn-subs/internal/logging"
	"github.com/fusionn-subs/internal/version"
	"go.uber.org/zap"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		panic(err)
	}

	logger, err := logging.New(cfg.LogLevel)
	if err != nil {
		log.Fatalf("init logger: %v", err)
	}
	defer func(logger *zap.Logger) {
		_ = logger.Sync()
	}(logger)

	version.PrintBanner(nil)

	cfgValues := cfg.SafeLogValues()
	keys := make([]string, 0, len(cfgValues))
	for k := range cfgValues {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		logger.Info("config",
			zap.String("key", key),
			zap.Any("value", cfgValues[key]),
		)
	}

	appInstance, err := app.New(cfg, logger)
	if err != nil {
		logger.Fatal("failed to initialize app", zap.Error(err))
	}
	defer appInstance.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := appInstance.Run(ctx); err != nil {
		logger.Fatal("worker exited with error", zap.Error(err))
	}
}
