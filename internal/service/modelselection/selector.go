package modelselection

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/fusionn-subs/internal/client/openrouter"
	"github.com/fusionn-subs/pkg/logger"
)

// ModelUpdateCallback is called when a new model is selected.
type ModelUpdateCallback func(newModel string)

// Selector manages automatic model selection with scheduling.
type Selector struct {
	openRouterClient *openrouter.Client
	evaluator        Evaluator
	fallbackModel    string
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
	FallbackModel    string
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
	if cfg.FallbackModel == "" {
		return nil, fmt.Errorf("fallback model required")
	}

	// Default schedule hour to 3 AM if not set
	if cfg.ScheduleHour < 0 || cfg.ScheduleHour > 23 {
		cfg.ScheduleHour = 3
	}

	return &Selector{
		openRouterClient: openrouter.NewClient(cfg.OpenRouterAPIKey),
		evaluator:        NewGeminiEvaluator(cfg.EvaluatorAPIKey, cfg.EvaluatorModel),
		fallbackModel:    cfg.FallbackModel,
		scheduleHour:     cfg.ScheduleHour,
		stop:             make(chan struct{}),
	}, nil
}

// Start performs initial evaluation and starts the scheduler.
// Blocks until initial evaluation completes or times out.
func (s *Selector) Start(ctx context.Context) error {
	// Detect and log timezone
	zone, offset := time.Now().Zone()
	offsetHours := offset / 3600
	var offsetSign string
	if offsetHours >= 0 {
		offsetSign = "+"
	}
	logger.Infof("🕐 Using timezone: %s (UTC%s%d)", zone, offsetSign, offsetHours)
	logger.Infof("🚀 Starting model selector (daily evaluation at %02d:00 %s)", s.scheduleHour, zone)

	// Initial evaluation (blocking)
	if err := s.evaluate(); err != nil {
		logger.Errorf("❌ Initial model evaluation failed: %v", err)
		logger.Infof("⚠️  Using fallback model: %s", s.fallbackModel)
		s.mu.Lock()
		s.selected = s.fallbackModel
		s.lastKnown = s.fallbackModel
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
// Falls back through: selected → lastKnown → fallbackModel.
func (s *Selector) GetCurrentModel() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.selected != "" {
		return s.selected
	}
	if s.lastKnown != "" {
		return s.lastKnown
	}
	return s.fallbackModel
}

// OnModelUpdate registers a callback for model changes.
func (s *Selector) OnModelUpdate(cb ModelUpdateCallback) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.callbacks = append(s.callbacks, cb)
}

// evaluate fetches models and selects the best one.
func (s *Selector) evaluate() error {
	logger.Infof("🔍 Fetching free models from OpenRouter...")

	models, err := s.openRouterClient.GetFreeModels()
	if err != nil {
		return fmt.Errorf("fetch models: %w", err)
	}

	if len(models) == 0 {
		return fmt.Errorf("no free models available")
	}

	logger.Infof("📋 Found %d free models (evaluator will select best for translation)", len(models))

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
		logger.Infof("🔄 Model changed: %s → %s", oldModel, selected)
		for _, cb := range callbacks {
			cb(selected)
		}
	} else if oldModel == "" {
		logger.Infof("🎯 Initial model selected: %s", selected)
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
					logger.Infof("⏰ Daily evaluation triggered")
					if err := s.evaluate(); err != nil {
						logger.Errorf("❌ Scheduled evaluation failed: %v", err)
						logger.Infof("⚠️  Continuing with last known good model")
					}
				}
			}
		}
	}
}
