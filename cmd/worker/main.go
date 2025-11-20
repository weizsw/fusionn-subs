package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/fusionn-subs/internal/app"
	"github.com/fusionn-subs/internal/config"
	"github.com/fusionn-subs/internal/logging"
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
