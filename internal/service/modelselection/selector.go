package modelselection

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/fusionn-subs/internal/client/openrouter"
	"github.com/fusionn-subs/pkg/logger"
)

var log = logger.Component("model_selection")

// ModelUpdateCallback is called when a new model is selected.
type ModelUpdateCallback func(newModel string)

// Selector manages automatic model selection with scheduling.
type Selector struct {
	openRouterClient *openrouter.Client
	evaluator        Evaluator
	defaultModel     string
	scheduleHour     int // Hour of day (0-23) to run evaluation

	mu           sync.RWMutex
	selected     string
	lastKnown    string
	lastEvalTime time.Time

	callbacks []ModelUpdateCallback
	stop      chan struct{}
}

// Config for model selector.
type Config struct {
	OpenRouterAPIKey string
	EvaluatorAPIKey  string
	EvaluatorModel   string
	DefaultModel     string
	ScheduleHour     int // Hour of day (0-23) for daily evaluation
}

// NewSelector creates a model selector service.
func NewSelector(cfg Config) (*Selector, error) {
	if cfg.OpenRouterAPIKey == "" {
		return nil, fmt.Errorf("openrouter API key required")
	}
	if cfg.EvaluatorAPIKey == "" {
		return nil, fmt.Errorf("evaluator API key required")
	}
	if cfg.DefaultModel == "" {
		return nil, fmt.Errorf("default model required")
	}

	// Default schedule hour to 3 AM if not set
	if cfg.ScheduleHour < 0 || cfg.ScheduleHour > 23 {
		cfg.ScheduleHour = 3
	}

	return &Selector{
		openRouterClient: openrouter.NewClient(cfg.OpenRouterAPIKey),
		evaluator:        NewGeminiEvaluator(cfg.EvaluatorAPIKey, cfg.EvaluatorModel),
		defaultModel:     cfg.DefaultModel,
		scheduleHour:     cfg.ScheduleHour,
		stop:             make(chan struct{}),
	}, nil
}

// Start performs initial evaluation and starts the scheduler.
// Blocks until initial evaluation completes or times out.
func (s *Selector) Start(ctx context.Context) error {
	zone, _ := time.Now().Zone()
	log.Info(ctx, "selector_started", "Model selector started",
		zap.String("timezone", zone),
		zap.Int("schedule_hour", s.scheduleHour),
	)

	// Initial evaluation (blocking)
	if err := s.evaluate(ctx); err != nil {
		log.Warn(ctx, "evaluation_failed", "Initial model evaluation failed",
			zap.String("fallback_model", s.defaultModel),
			zap.Error(err),
		)
		s.mu.Lock()
		s.selected = s.defaultModel
		s.lastKnown = s.defaultModel
		s.mu.Unlock()
	}

	// Start background scheduler
	go s.runScheduler()

	return nil
}

// Stop stops the scheduler.
func (s *Selector) Stop() {
	close(s.stop)
}

// GetCurrentModel returns the currently selected model (thread-safe).
// Falls back through: selected → lastKnown → defaultModel.
func (s *Selector) GetCurrentModel() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.selected != "" {
		return s.selected
	}
	if s.lastKnown != "" {
		return s.lastKnown
	}
	return s.defaultModel
}

// OnModelUpdate registers a callback for model changes.
func (s *Selector) OnModelUpdate(cb ModelUpdateCallback) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.callbacks = append(s.callbacks, cb)
}

// evaluate fetches models and selects the best one.
func (s *Selector) evaluate(ctx context.Context) error {
	log.Debug(ctx, "models_fetch_started", "Fetching free models")

	models, err := s.openRouterClient.GetFreeModels()
	if err != nil {
		return fmt.Errorf("fetch models: %w", err)
	}

	if len(models) == 0 {
		return fmt.Errorf("no free models available")
	}

	log.Info(ctx, "models_fetched", "Free models fetched", zap.Int("model_count", len(models)))

	selected, err := s.evaluator.SelectBestModel(models)
	if err != nil {
		return fmt.Errorf("select model: %w", err)
	}

	s.mu.Lock()
	oldModel := s.selected
	s.selected = selected
	s.lastKnown = selected
	s.lastEvalTime = time.Now()
	callbacks := s.callbacks
	s.mu.Unlock()

	// Notify callbacks if model changed
	if oldModel != selected && oldModel != "" {
		log.Info(ctx, "model_changed", "Model changed",
			zap.String("from_model", oldModel),
			zap.String("to_model", selected),
		)
		for _, cb := range callbacks {
			cb(selected)
		}
	} else if oldModel == "" {
		log.Info(ctx, "model_selected", "Model selected", zap.String("model", selected))
	}

	return nil
}

// runScheduler runs daily evaluation at the configured hour.
func (s *Selector) runScheduler() {
	ticker := time.NewTicker(1 * time.Hour) // Check every hour
	defer ticker.Stop()

	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			now := time.Now()
			if now.Hour() == s.scheduleHour {
				// Only evaluate once per day
				s.mu.RLock()
				lastEval := s.lastEvalTime
				s.mu.RUnlock()

				if time.Since(lastEval) >= 23*time.Hour {
					log.Info(context.Background(), "evaluation_triggered", "Daily evaluation triggered")
					if err := s.evaluate(context.Background()); err != nil {
						log.Warn(context.Background(), "evaluation_failed", "Scheduled evaluation failed",
							zap.String("fallback_model", s.GetCurrentModel()),
							zap.Error(err),
						)
					}
				}
			}
		}
	}
}
