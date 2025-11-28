package config

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/spf13/viper"

	"github.com/fusionn-subs/internal/util"
	"github.com/fusionn-subs/pkg/logger"
)

// Service-level defaults (not user-configurable)
const (
	DefaultCallbackTimeout    = 15 * time.Second
	DefaultCallbackMaxRetries = 3
	DefaultGeminiTimeout      = 15 * time.Minute
	DefaultWorkerPollTimeout  = 5 * time.Second
)

type Config struct {
	Redis      RedisConfig      `mapstructure:"redis"`
	Callback   CallbackConfig   `mapstructure:"callback"`
	Gemini     GeminiConfig     `mapstructure:"gemini"`
	Translator TranslatorConfig `mapstructure:"translator"`
}

type RedisConfig struct {
	URL   string `mapstructure:"url"`
	Queue string `mapstructure:"queue"`
}

type CallbackConfig struct {
	URL string `mapstructure:"url"`
}

type GeminiConfig struct {
	APIKey       string `mapstructure:"api_key"`
	Model        string `mapstructure:"model"`
	Instruction  string `mapstructure:"instruction"`
	MaxBatchSize int    `mapstructure:"max_batch_size"`
	RateLimit    int    `mapstructure:"rate_limit"`
}

type TranslatorConfig struct {
	TargetLanguage string `mapstructure:"target_language"`
	OutputSuffix   string `mapstructure:"output_suffix"`
}

// ChangeCallback is called when config changes. Receives old and new config.
type ChangeCallback func(old, new *Config)

// Manager handles config loading and hot-reload.
type Manager struct {
	mu        sync.RWMutex
	cfg       *Config
	callbacks []ChangeCallback
	stop      chan struct{}

	// Polling state
	path        string
	lastModTime time.Time
}

// NewManager creates a config manager with hot-reload support via polling.
// Config changes are detected automatically every 10 seconds.
func NewManager(path string) (*Manager, error) {
	viper.SetConfigFile(path)
	viper.SetConfigType("yaml")

	// Environment variable override support
	viper.SetEnvPrefix("FUSIONN_SUBS")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		return nil, err
	}

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, err
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	// Get initial file mod time
	var lastMod time.Time
	if stat, err := os.Stat(path); err == nil {
		lastMod = stat.ModTime()
	}

	m := &Manager{
		cfg:         &cfg,
		stop:        make(chan struct{}),
		path:        path,
		lastModTime: lastMod,
	}

	// Start polling for config changes
	go m.pollForChanges(10 * time.Second)

	logger.Infof("📋 Config loaded (polling every 10s for changes)")

	return m, nil
}

// Get returns the current config (thread-safe).
func (m *Manager) Get() *Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg
}

// OnChange registers a callback for config changes.
func (m *Manager) OnChange(cb ChangeCallback) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callbacks = append(m.callbacks, cb)
}

// Stop stops the config polling goroutine.
func (m *Manager) Stop() {
	close(m.stop)
}

// pollForChanges checks file modtime periodically for Docker bind mount compatibility.
func (m *Manager) pollForChanges(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stop:
			return
		case <-ticker.C:
			stat, err := os.Stat(m.path)
			if err != nil {
				continue
			}

			m.mu.RLock()
			lastMod := m.lastModTime
			m.mu.RUnlock()

			if stat.ModTime().After(lastMod) {
				logger.Infof("🔄 Config file changed, reloading...")

				if err := viper.ReadInConfig(); err != nil {
					logger.Errorf("❌ Failed to re-read config: %v", err)
					continue
				}

				m.mu.Lock()
				m.lastModTime = stat.ModTime()
				m.mu.Unlock()

				m.reload()
			}
		}
	}
}

// reload re-reads config and notifies subscribers.
func (m *Manager) reload() {
	var newCfg Config
	if err := viper.Unmarshal(&newCfg); err != nil {
		logger.Errorf("❌ Failed to reload config: %v", err)
		return
	}

	if err := newCfg.Validate(); err != nil {
		logger.Errorf("❌ Invalid config after reload: %v", err)
		return
	}

	m.mu.Lock()
	oldCfg := m.cfg
	m.cfg = &newCfg
	callbacks := m.callbacks
	m.mu.Unlock()

	// Log what changed
	logChanges(oldCfg, &newCfg, "")

	// Notify subscribers outside lock
	for _, cb := range callbacks {
		cb(oldCfg, &newCfg)
	}
}

// Validate checks required config fields.
func (c *Config) Validate() error {
	switch {
	case c.Redis.URL == "":
		return fmt.Errorf("redis.url is required")
	case c.Redis.Queue == "":
		return fmt.Errorf("redis.queue is required")
	case c.Callback.URL == "":
		return fmt.Errorf("callback.url is required")
	case c.Gemini.APIKey == "":
		return fmt.Errorf("gemini.api_key is required")
	}
	return nil
}

// logChanges logs field-level differences between old and new config.
func logChanges(old, cur any, prefix string) {
	oldVal := reflect.ValueOf(old)
	newVal := reflect.ValueOf(cur)

	// Dereference pointers
	if oldVal.Kind() == reflect.Ptr {
		oldVal = oldVal.Elem()
	}
	if newVal.Kind() == reflect.Ptr {
		newVal = newVal.Elem()
	}

	if oldVal.Kind() != reflect.Struct {
		return
	}

	t := oldVal.Type()
	for i := range t.NumField() {
		field := t.Field(i)
		oldField := oldVal.Field(i)
		newField := newVal.Field(i)

		fieldName := field.Name
		if prefix != "" {
			fieldName = prefix + "." + fieldName
		}

		// Recurse into nested structs
		if oldField.Kind() == reflect.Struct {
			logChanges(oldField.Interface(), newField.Interface(), fieldName)
			continue
		}

		// Compare values
		if !reflect.DeepEqual(oldField.Interface(), newField.Interface()) {
			oldStr := formatValue(oldField)
			newStr := formatValue(newField)
			logger.Infof("  📝 %s: %s → %s", fieldName, oldStr, newStr)
		}
	}
}

// formatValue formats a reflect.Value for logging, masking sensitive fields.
func formatValue(v reflect.Value) string {
	if v.Kind() == reflect.Slice {
		return fmt.Sprintf("%v", v.Interface())
	}
	return fmt.Sprintf("%v", v.Interface())
}

// Load is a convenience function for one-time loading (backwards compatible).
func Load(path string) (*Config, error) {
	viper.SetConfigFile(path)
	viper.SetConfigType("yaml")

	viper.SetEnvPrefix("FUSIONN_SUBS")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		return nil, err
	}

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, err
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// SafeLogValues returns config values safe for logging (masks secrets).
func (c *Config) SafeLogValues() map[string]any {
	return map[string]any{
		"redis.url":              c.Redis.URL,
		"redis.queue":            c.Redis.Queue,
		"callback.url":           c.Callback.URL,
		"gemini.api_key":         util.MaskSecret(c.Gemini.APIKey),
		"gemini.model":           c.Gemini.Model,
		"gemini.instruction":     c.Gemini.Instruction,
		"gemini.max_batch_size":  c.Gemini.MaxBatchSize,
		"gemini.rate_limit":      c.Gemini.RateLimit,
		"translator.target_lang": c.Translator.TargetLanguage,
		"translator.suffix":      c.Translator.OutputSuffix,
	}
}
