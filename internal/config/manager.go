package config

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spcent/plumego/log"
)

var (
	errConfigClosed = errors.New("config manager is closed")
)

// WatcherCallback represents a configuration change callback.
type WatcherCallback func(oldValue, newValue any)

// Manager manages application configuration with dependency injection.
type Manager struct {
	sources    []Source
	data       map[string]any
	sourceData map[string]map[string]any // tracks each source's last known data
	mu         sync.RWMutex
	watchers   map[string][]WatcherCallback
	ctx        context.Context
	cancel     context.CancelFunc
	watchWg    sync.WaitGroup
	logger     log.StructuredLogger
}

// NewManager creates a new Manager instance.
func NewManager(logger log.StructuredLogger) *Manager {
	if logger == nil {
		panic("config: logger is required")
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		sources:    make([]Source, 0),
		data:       make(map[string]any),
		sourceData: make(map[string]map[string]any),
		watchers:   make(map[string][]WatcherCallback),
		ctx:        ctx,
		cancel:     cancel,
		logger:     logger,
	}
}

// AddSource adds a configuration source.
func (m *Manager) AddSource(source Source) error {
	if source == nil {
		return errors.New("source cannot be nil")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ctx.Err() != nil {
		return errConfigClosed
	}

	m.sources = append(m.sources, source)
	return nil
}

// loadSources loads configuration from all given sources.
// If bestEffort is true, individual source errors are logged and skipped;
// an error is returned only when every source fails.
// If bestEffort is false, the first source error aborts and is returned.
func (m *Manager) loadSources(ctx context.Context, sources []Source, bestEffort bool) (map[string]any, map[string]map[string]any, error) {
	allSourceData := make(map[string]map[string]any, len(sources))
	data := make(map[string]any)
	loaded := 0
	var lastErr error

	for _, source := range sources {
		sd, err := source.Load(ctx)
		if err != nil {
			m.logger.Error("Failed to load configuration", log.Fields{
				"source": source.Name(),
				"error":  err,
			})
			if !bestEffort {
				return nil, nil, fmt.Errorf("failed to load from source %s: %w", source.Name(), err)
			}
			lastErr = err
			continue
		}

		loaded++
		normalized := normalizeData(sd, m.logger)
		allSourceData[source.Name()] = normalized
		for key, value := range normalized {
			data[key] = value
		}

		m.logger.Info("Loaded configuration", log.Fields{
			"source": source.Name(),
			"keys":   len(normalized),
		})
	}

	if bestEffort && loaded == 0 && lastErr != nil {
		return nil, nil, fmt.Errorf("failed to load any configuration sources: %w", lastErr)
	}

	return data, allSourceData, nil
}

// Load loads configuration from all sources.
// This is a pure load operation — it does NOT notify watchers.
// Use Reload to load and notify watchers of changes.
func (m *Manager) Load(ctx context.Context) error {
	return m.loadIntoState(ctx, false)
}

// LoadBestEffort loads configuration from all sources, skipping failures.
// It returns an error only if all sources fail.
// This is a pure load operation — it does NOT notify watchers.
func (m *Manager) LoadBestEffort(ctx context.Context) error {
	return m.loadIntoState(ctx, true)
}

func (m *Manager) loadIntoState(ctx context.Context, bestEffort bool) error {
	if ctx == nil {
		ctx = context.Background()
	}

	sources, err := m.snapshotSources()
	if err != nil {
		return err
	}

	data, allSourceData, err := m.loadSources(ctx, sources, bestEffort)
	if err != nil {
		return err
	}

	m.mu.Lock()
	if m.ctx.Err() != nil {
		m.mu.Unlock()
		return errConfigClosed
	}
	m.data = data
	m.sourceData = allSourceData
	m.mu.Unlock()

	return nil
}

func (m *Manager) snapshotSources() ([]Source, error) {
	m.mu.RLock()
	if m.ctx.Err() != nil {
		m.mu.RUnlock()
		return nil, errConfigClosed
	}
	sources := append([]Source(nil), m.sources...)
	m.mu.RUnlock()
	return sources, nil
}

func (m *Manager) getValue(key string) (any, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return lookupValue(m.data, key)
}

// Get retrieves a configuration value as string.
func (m *Manager) Get(key string) string {
	value, exists := m.getValue(key)
	if exists {
		return toString(value)
	}
	return ""
}

// GetString retrieves a configuration value as string with default.
// Returns defaultValue when the key does not exist; returns the stored value
// (including empty string) when the key exists.
func (m *Manager) GetString(key, defaultValue string) string {
	value, exists := m.getValue(key)
	if !exists {
		return defaultValue
	}
	return strings.TrimSpace(toString(value))
}

// GetInt retrieves a configuration value as int.
func (m *Manager) GetInt(key string, defaultValue int) int {
	value, exists := m.getValue(key)
	if !exists {
		return defaultValue
	}
	return toInt(value, defaultValue)
}

// GetBool retrieves a configuration value as bool.
func (m *Manager) GetBool(key string, defaultValue bool) bool {
	value, exists := m.getValue(key)
	if !exists {
		return defaultValue
	}
	return toBool(value, defaultValue)
}

// GetFloat retrieves a configuration value as float64.
func (m *Manager) GetFloat(key string, defaultValue float64) float64 {
	value, exists := m.getValue(key)
	if !exists {
		return defaultValue
	}
	return toFloat64(value, defaultValue)
}

// GetDurationMs retrieves a configuration value as time.Duration (milliseconds).
func (m *Manager) GetDurationMs(key string, defaultValueMs int) time.Duration {
	return time.Duration(m.GetInt(key, defaultValueMs)) * time.Millisecond
}

// Has reports whether a configuration key exists (regardless of its value).
func (m *Manager) Has(key string) bool {
	_, exists := m.getValue(key)
	return exists
}

// GetStringSlice retrieves a configuration value as a string slice,
// splitting on sep. If the key does not exist, defaultValue is returned.
// Example: KEY=a,b,c with sep="," returns ["a","b","c"].
func (m *Manager) GetStringSlice(key, sep string, defaultValue []string) []string {
	value, exists := m.getValue(key)
	if !exists {
		return defaultValue
	}
	raw := strings.TrimSpace(toString(value))
	if raw == "" {
		return defaultValue
	}
	parts := strings.Split(raw, sep)
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			result = append(result, s)
		}
	}
	if len(result) == 0 {
		return defaultValue
	}
	return result
}

// GetDuration retrieves a configuration value as time.Duration.
// The value must be expressed as a Go duration string (e.g. "30s", "5m", "1h").
func (m *Manager) GetDuration(key string, defaultValue time.Duration) time.Duration {
	value, exists := m.getValue(key)
	if !exists {
		return defaultValue
	}
	s := strings.TrimSpace(toString(value))
	if s == "" {
		return defaultValue
	}
	if d, err := time.ParseDuration(s); err == nil {
		return d
	}
	return defaultValue
}

// GetAll returns all configuration as a map.
func (m *Manager) GetAll() map[string]any {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]any, len(m.data))
	for key, value := range m.data {
		result[key] = value
	}
	return result
}

// Watch registers a callback for configuration changes to a specific key.
func (m *Manager) Watch(key string, callback WatcherCallback) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ctx.Err() != nil {
		return errConfigClosed
	}

	if key != "*" {
		key = normalizeKey(key)
	}

	m.watchers[key] = append(m.watchers[key], callback)
	return nil
}

// WatchGlobal registers a callback for any configuration change.
func (m *Manager) WatchGlobal(callback WatcherCallback) error {
	return m.Watch("*", callback)
}

// StartWatchers starts watching all configured sources for changes.
func (m *Manager) StartWatchers(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	sources, err := m.snapshotSources()
	if err != nil {
		return err
	}

	for _, source := range sources {
		results := source.Watch(ctx)

		m.watchWg.Add(1)
		go func(src Source, results <-chan WatchResult) {
			defer m.watchWg.Done()
			for {
				select {
				case <-m.ctx.Done():
					return
				case <-ctx.Done():
					return
				case result, ok := <-results:
					if !ok {
						return
					}
					if result.Err != nil {
						m.logger.Error("Watch error", log.Fields{
							"source": src.Name(),
							"error":  result.Err,
						})
					} else if result.Data != nil {
						m.handleSourceUpdate(src.Name(), result.Data)
					}
				}
			}
		}(source, results)
	}

	return nil
}

// mergeAllSources re-merges all tracked source snapshots in source-declaration order
// (later sources win). Unregistered sources are merged last.
// Must be called with m.mu held (at least read-locked; safe under write lock too).
func (m *Manager) mergeAllSources() map[string]any {
	merged := make(map[string]any)
	seen := make(map[string]bool, len(m.sources))
	for _, src := range m.sources {
		seen[src.Name()] = true
		for k, v := range m.sourceData[src.Name()] {
			merged[k] = v
		}
	}
	// Include snapshots from sources not formally registered (e.g. direct test calls)
	for name, data := range m.sourceData {
		if !seen[name] {
			for k, v := range data {
				merged[k] = v
			}
		}
	}
	return merged
}

// detectAndNotify computes changed keys between oldData and newData, copies
// watcher callbacks under RLock, then fires them outside any lock.
// Returns the number of changed keys.
func (m *Manager) detectAndNotify(oldData, newData map[string]any) int {
	changes := make(map[string]struct{})
	oldValues := make(map[string]any)

	for key, newVal := range newData {
		if oldVal, exists := oldData[key]; !exists || !valuesEqual(oldVal, newVal) {
			changes[key] = struct{}{}
			oldValues[key] = oldVal
		}
	}
	for key, oldVal := range oldData {
		if _, stillPresent := newData[key]; !stillPresent {
			changes[key] = struct{}{}
			oldValues[key] = oldVal
		}
	}

	if len(changes) == 0 {
		return 0
	}

	m.mu.RLock()
	if len(m.watchers) == 0 {
		m.mu.RUnlock()
		return len(changes)
	}
	keyWatchers := make(map[string][]WatcherCallback, len(changes))
	for key := range changes {
		if cbs, exists := m.watchers[key]; exists && len(cbs) > 0 {
			copied := make([]WatcherCallback, len(cbs))
			copy(copied, cbs)
			keyWatchers[key] = copied
		}
	}
	globalWatchers := append([]WatcherCallback(nil), m.watchers["*"]...)
	m.mu.RUnlock()

	m.notifyWatchers(changes, oldValues, newData, keyWatchers, globalWatchers)
	return len(changes)
}

// handleSourceUpdate handles configuration updates from a source.
// It re-merges all known source data so that keys removed from the updated
// source are also removed from the effective configuration (no stale keys).
func (m *Manager) handleSourceUpdate(sourceName string, newData map[string]any) {
	newData = normalizeData(newData, m.logger)

	m.mu.Lock()
	m.sourceData[sourceName] = newData
	merged := m.mergeAllSources()
	oldData := m.data
	m.data = merged
	m.mu.Unlock()

	if n := m.detectAndNotify(oldData, merged); n > 0 {
		m.logger.Info("Configuration updated", log.Fields{
			"source":  sourceName,
			"changes": n,
		})
	}
}

// notifyWatchers fires per-key and global watcher callbacks outside any lock.
func (m *Manager) notifyWatchers(changes map[string]struct{}, oldValues, newData map[string]any, keyWatchers map[string][]WatcherCallback, globalWatchers []WatcherCallback) {
	for key := range changes {
		for _, cb := range keyWatchers[key] {
			m.callWatcher(cb, oldValues[key], newData[key])
		}
	}

	if len(globalWatchers) == 0 {
		return
	}

	// Shallow-copy newData for global watchers to prevent mutation by callbacks.
	snapshot := make(map[string]any, len(newData))
	for k, v := range newData {
		snapshot[k] = v
	}
	for _, cb := range globalWatchers {
		m.callWatcher(cb, nil, snapshot)
	}
}

func (m *Manager) callWatcher(callback WatcherCallback, old, new any) {
	defer func() {
		if r := recover(); r != nil {
			m.logger.Error("Watcher callback panicked", log.Fields{
				"error": r,
			})
		}
	}()
	callback(old, new)
}

// Reload reloads configuration from all sources and notifies registered watchers
// of any changes. Use Load when watcher notification is not desired.
func (m *Manager) Reload(ctx context.Context) error {
	// Snapshot old data before loading
	m.mu.RLock()
	oldData := make(map[string]any, len(m.data))
	for k, v := range m.data {
		oldData[k] = v
	}
	m.mu.RUnlock()

	if err := m.Load(ctx); err != nil {
		return err
	}

	m.mu.RLock()
	newData := m.data
	m.mu.RUnlock()

	m.detectAndNotify(oldData, newData)
	return nil
}

// ReloadWithValidation reloads configuration, validates the new values, and
// only commits + notifies watchers if validation passes.
// On validation failure the previous configuration (data and sourceData) is restored.
func (m *Manager) ReloadWithValidation(ctx context.Context, validate func(map[string]any) error) error {
	if validate == nil {
		return m.Reload(ctx)
	}

	// Snapshot current state (data + per-source snapshots) for rollback
	m.mu.RLock()
	oldData := make(map[string]any, len(m.data))
	for k, v := range m.data {
		oldData[k] = v
	}
	oldSourceData := make(map[string]map[string]any, len(m.sourceData))
	for name, sd := range m.sourceData {
		cp := make(map[string]any, len(sd))
		for k, v := range sd {
			cp[k] = v
		}
		oldSourceData[name] = cp
	}
	m.mu.RUnlock()

	// Load new config (does NOT notify watchers)
	if err := m.Load(ctx); err != nil {
		return err
	}

	// Read new data for validation (copy to prevent mutation by validator)
	m.mu.RLock()
	newData := m.data
	m.mu.RUnlock()

	validationInput := make(map[string]any, len(newData))
	for k, v := range newData {
		validationInput[k] = v
	}

	if err := validate(validationInput); err != nil {
		// Rollback both data and sourceData
		m.mu.Lock()
		m.data = oldData
		m.sourceData = oldSourceData
		m.mu.Unlock()
		return err
	}

	// Commit succeeded — notify watchers
	m.detectAndNotify(oldData, newData)
	return nil
}

// Close stops all watchers and releases resources.
func (m *Manager) Close() error {
	m.mu.Lock()
	if m.ctx.Err() != nil {
		m.mu.Unlock()
		return nil
	}
	m.cancel()
	m.mu.Unlock()

	m.watchWg.Wait()
	m.logger.Info("Config manager closed")
	return nil
}

// Unmarshal populates a struct with configuration values.
// It copies the configuration map under a short read-lock, then performs the
// reflection-based population lock-free to minimise lock contention.
func (m *Manager) Unmarshal(dst any) error {
	val := reflect.ValueOf(dst)
	if val.Kind() != reflect.Pointer || val.IsNil() {
		return fmt.Errorf("dst must be a non-nil pointer")
	}

	// Copy data under RLock to avoid holding the lock during reflection.
	m.mu.RLock()
	data := make(map[string]any, len(m.data))
	for k, v := range m.data {
		data[k] = v
	}
	m.mu.RUnlock()

	return unmarshalValue(data, val.Elem())
}

// unmarshalValue recursively populates a struct from the provided data map.
func unmarshalValue(data map[string]any, val reflect.Value) error {
	if val.Kind() != reflect.Struct {
		return fmt.Errorf("dst must point to a struct")
	}

	typ := val.Type()
	timeType := reflect.TypeOf(time.Time{})

	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		fieldValue := val.Field(i)

		if !fieldValue.CanSet() {
			continue
		}

		// Recurse into embedded/nested structs (but not time.Time)
		if fieldValue.Kind() == reflect.Struct && fieldValue.Type() != timeType {
			if err := unmarshalValue(data, fieldValue); err != nil {
				return err
			}
			continue
		}

		if fieldValue.Kind() == reflect.Pointer &&
			fieldValue.Type().Elem().Kind() == reflect.Struct &&
			fieldValue.Type().Elem() != timeType {
			if fieldValue.IsNil() {
				fieldValue.Set(reflect.New(fieldValue.Type().Elem()))
			}
			if err := unmarshalValue(data, fieldValue.Elem()); err != nil {
				return err
			}
			continue
		}

		key := field.Tag.Get("config")
		if key == "" {
			key = field.Name
		}

		if value, exists := lookupValue(data, key); exists {
			if err := setField(fieldValue, value); err != nil {
				return fmt.Errorf("failed to set field %s: %w", field.Name, err)
			}
		}
	}

	return nil
}

var durationType = reflect.TypeOf(time.Duration(0))
var maxDurationMilliseconds = int64(1<<63-1) / int64(time.Millisecond)
var minDurationMilliseconds = -maxDurationMilliseconds - 1

// setField sets a struct field value from a raw configuration value.
// Supports: string, all int/uint sizes, float32/64, bool, time.Duration, []string.
func setField(fieldValue reflect.Value, value any) error {
	strValue := toString(value)

	// Handle time.Duration before the generic int64 case.
	if fieldValue.Type() == durationType {
		d, err := time.ParseDuration(strValue)
		if err != nil {
			// Fallback: treat as milliseconds
			ms, err2 := strconv.ParseInt(strValue, 10, 64)
			if err2 != nil {
				return fmt.Errorf("cannot parse %q as duration or milliseconds: %w", strValue, err)
			}
			if ms < minDurationMilliseconds || ms > maxDurationMilliseconds {
				return fmt.Errorf("duration value %q overflows %v", strValue, fieldValue.Type())
			}
			fieldValue.SetInt(int64(time.Duration(ms) * time.Millisecond))
		} else {
			fieldValue.SetInt(int64(d))
		}
		return nil
	}

	switch fieldValue.Kind() {
	case reflect.String:
		fieldValue.SetString(strValue)

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		intValue, err := strconv.ParseInt(strValue, 10, 64)
		if err != nil {
			return err
		}
		if fieldValue.OverflowInt(intValue) {
			return fmt.Errorf("value %q overflows %v", strValue, fieldValue.Type())
		}
		fieldValue.SetInt(intValue)

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		uintValue, err := strconv.ParseUint(strValue, 10, 64)
		if err != nil {
			return err
		}
		if fieldValue.OverflowUint(uintValue) {
			return fmt.Errorf("value %q overflows %v", strValue, fieldValue.Type())
		}
		fieldValue.SetUint(uintValue)

	case reflect.Float32, reflect.Float64:
		floatValue, err := strconv.ParseFloat(strValue, 64)
		if err != nil {
			return err
		}
		if fieldValue.OverflowFloat(floatValue) {
			return fmt.Errorf("value %q overflows %v", strValue, fieldValue.Type())
		}
		fieldValue.SetFloat(floatValue)

	case reflect.Bool:
		bv, err := parseBoolErr(strValue)
		if err != nil {
			return err
		}
		fieldValue.SetBool(bv)

	case reflect.Slice:
		if fieldValue.Type().Elem().Kind() != reflect.String {
			return fmt.Errorf("unsupported slice element type: %v", fieldValue.Type().Elem())
		}
		parts := strings.Split(strValue, ",")
		slice := reflect.MakeSlice(fieldValue.Type(), 0, len(parts))
		for _, p := range parts {
			if s := strings.TrimSpace(p); s != "" {
				slice = reflect.Append(slice, reflect.ValueOf(s))
			}
		}
		fieldValue.Set(slice)

	default:
		return fmt.Errorf("unsupported field type: %v", fieldValue.Kind())
	}

	return nil
}
