package config

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// ConfigWatcher watches a configuration file for changes and reloads it
type ConfigWatcher struct {
	path     string
	config   *ShardingConfig
	mu       sync.RWMutex
	onChange []func(*ShardingConfig)
	stopCh   chan struct{}
	doneCh   chan struct{}
	interval time.Duration
	lastMod  time.Time
	started  atomic.Bool
	stopped  atomic.Bool
	stopOnce sync.Once
}

// WatcherOption is a functional option for ConfigWatcher
type WatcherOption func(*ConfigWatcher)

// WithWatchInterval sets the watch interval for the configuration file
func WithWatchInterval(interval time.Duration) WatcherOption {
	return func(w *ConfigWatcher) {
		w.interval = interval
	}
}

// WithOnChange adds a callback function to be called when configuration changes
func WithOnChange(fn func(*ShardingConfig)) WatcherOption {
	return func(w *ConfigWatcher) {
		w.onChange = append(w.onChange, fn)
	}
}

// NewConfigWatcher creates a new configuration file watcher
func NewConfigWatcher(path string, opts ...WatcherOption) (*ConfigWatcher, error) {
	// Load initial configuration
	config, err := LoadFromJSONFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to load initial configuration: %w", err)
	}

	// Get initial file modification time
	stat, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}

	watcher := &ConfigWatcher{
		path:     path,
		config:   config,
		onChange: []func(*ShardingConfig){},
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
		interval: 5 * time.Second, // Default watch interval
		lastMod:  stat.ModTime(),
	}

	// Apply options
	for _, opt := range opts {
		opt(watcher)
	}

	return watcher, nil
}

// Start starts watching the configuration file for changes.
// It must be called at most once. Stop will block until Start returns.
func (w *ConfigWatcher) Start(ctx context.Context) error {
	if w.stopped.Load() {
		return fmt.Errorf("config watcher has been stopped")
	}
	if !w.started.CompareAndSwap(false, true) {
		return fmt.Errorf("config watcher already started")
	}
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	defer close(w.doneCh)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-w.stopCh:
			return nil
		case <-ticker.C:
			if err := w.checkAndReload(); err != nil {
				// Log error but continue watching
				continue
			}
		}
	}
}

// Stop signals the watcher to stop and waits for it to exit.
// It is a no-op if Start was never called.
func (w *ConfigWatcher) Stop() {
	w.stopped.Store(true)
	w.stopOnce.Do(func() {
		close(w.stopCh)
	})
	if w.started.Load() {
		<-w.doneCh
	}
}

// checkAndReload checks if the configuration file has changed and reloads it
func (w *ConfigWatcher) checkAndReload() error {
	stat, err := os.Stat(w.path)
	if err != nil {
		return fmt.Errorf("failed to stat file: %w", err)
	}

	// Check if file has been modified
	if !stat.ModTime().After(w.lastMod) {
		return nil
	}

	// Load new configuration
	newConfig, err := LoadFromJSONFile(w.path)
	if err != nil {
		return fmt.Errorf("failed to reload configuration: %w", err)
	}

	// Merge with environment variables
	if err := newConfig.MergeWithEnv(); err != nil {
		return fmt.Errorf("failed to merge with environment: %w", err)
	}

	// Update configuration atomically
	w.mu.Lock()
	w.config = newConfig
	w.lastMod = stat.ModTime()
	callbacks := make([]func(*ShardingConfig), len(w.onChange))
	copy(callbacks, w.onChange)
	w.mu.Unlock()

	// Call onChange callbacks
	for _, fn := range callbacks {
		fn(newConfig)
	}

	return nil
}

// Get returns the current configuration (thread-safe)
func (w *ConfigWatcher) Get() *ShardingConfig {
	w.mu.RLock()
	defer w.mu.RUnlock()

	// Return a copy to prevent modification
	config := *w.config
	return &config
}

// Reload manually reloads the configuration
func (w *ConfigWatcher) Reload() error {
	return w.checkAndReload()
}

// AddOnChange adds a callback to be called when configuration changes
func (w *ConfigWatcher) AddOnChange(fn func(*ShardingConfig)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.onChange = append(w.onChange, fn)
}

// ConfigReloader manages hot reloading of sharding configuration
type ConfigReloader struct {
	watcher *ConfigWatcher
}

// NewConfigReloader creates a new configuration reloader
func NewConfigReloader(path string, opts ...WatcherOption) (*ConfigReloader, error) {
	watcher, err := NewConfigWatcher(path, opts...)
	if err != nil {
		return nil, err
	}

	return &ConfigReloader{
		watcher: watcher,
	}, nil
}

// Start starts the configuration reloader
func (r *ConfigReloader) Start(ctx context.Context) error {
	return r.watcher.Start(ctx)
}

// Stop stops the configuration reloader
func (r *ConfigReloader) Stop() {
	r.watcher.Stop()
}

// Get returns the current configuration
func (r *ConfigReloader) Get() *ShardingConfig {
	return r.watcher.Get()
}

// OnChange registers a callback for configuration changes
func (r *ConfigReloader) OnChange(fn func(*ShardingConfig)) {
	r.watcher.AddOnChange(fn)
}

// Reload manually triggers a configuration reload
func (r *ConfigReloader) Reload() error {
	return r.watcher.Reload()
}
