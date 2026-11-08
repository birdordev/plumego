// Package redis adapts caller-owned Redis clients to store/cache.Cache.
//
// The package is dependency-free: concrete Redis drivers stay at application
// edges and must be mapped into the small interfaces in this package. Cache miss
// errors are not guessed; callers must configure WithNotFound or NewAdapter's
// compatibility mapper for their chosen driver.
package redis

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/birdordev/plumego/store/cache"
)

var (
	ErrClearUnsupported  = errors.New("redis cache: clear unsupported")
	ErrNilClient         = errors.New("redis cache: client is nil")
	ErrAtomicUnsupported = cache.ErrCapabilityUnsupported
	ErrFlushDBDisabled   = errors.New("redis cache: flushdb disabled")
)

const (
	// DefaultMaxKeyLength is the default maximum key length.
	DefaultMaxKeyLength = 256

	minInt64 = -1 << 63
)

// Client captures the minimal Redis operations required by the adapter.
type Client interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	Del(ctx context.Context, keys ...string) (int64, error)
	Exists(ctx context.Context, keys ...string) (int64, error)
}

// Flusher allows the adapter to clear all keys.
type Flusher interface {
	FlushDB(ctx context.Context) error
}

// PrefixFlusher allows the adapter to clear keys in a configured namespace.
type PrefixFlusher interface {
	FlushPrefix(ctx context.Context, prefix string) error
}

// Incrementer captures Redis-native atomic integer mutation.
type Incrementer interface {
	IncrBy(ctx context.Context, key string, delta int64) (int64, error)
}

// Appender captures Redis-native append behavior.
type Appender interface {
	Append(ctx context.Context, key string, data []byte) (int64, error)
}

// AdapterCapabilities reports optional behavior supported by an adapter's client
// and selected options.
type AdapterCapabilities struct {
	Atomic      bool
	Append      bool
	Clear       bool
	PrefixClear bool
	FlushDB     bool
}

// Adapter implements cache.Cache using a Redis client.
type Adapter struct {
	// Client is the wrapped Redis client. It is exported for compatibility with
	// older direct-field wiring and must not be mutated concurrently with cache
	// operations.
	Client Client

	// IsNotFound maps client errors to cache misses for compatibility
	// constructors. Prefer WithNotFound for new wiring. Do not mutate this field
	// concurrently with cache operations.
	IsNotFound func(error) bool

	// MaxKeyLength configures key validation for compatibility constructors.
	// Prefer WithMaxKeyLength for new wiring. Do not mutate this field
	// concurrently with cache operations.
	MaxKeyLength int

	// AllowFlushDB enables DB-wide Clear for compatibility constructors. Prefer
	// WithAllowFlushDB for new wiring. Do not mutate this field concurrently with
	// cache operations.
	AllowFlushDB bool

	// ClearPrefix selects namespaced Clear for compatibility constructors. Prefer
	// WithClearPrefix for new wiring. Do not mutate this field concurrently with
	// cache operations.
	ClearPrefix string

	options adapterOptions
}

// CounterAdapter exposes cache.CounterCache only for clients with Incrementer support.
type CounterAdapter struct {
	*Adapter
	incrementer Incrementer
}

// AppenderAdapter exposes cache.AppenderCache only for clients with Appender support.
type AppenderAdapter struct {
	*Adapter
	appender Appender
}

// AtomicAdapter exposes both cache.CounterCache and cache.AppenderCache for clients with both capabilities.
type AtomicAdapter struct {
	*Adapter
	incrementer Incrementer
	appender    Appender
}

// Option configures a Redis cache adapter.
//
// The package-provided With* options are the canonical constructor contract for
// new wiring: they validate and freeze behavior inside the adapter. Custom
// Option functions remain a compatibility hook for direct field configuration;
// their effective field values are validated by NewValidatedAdapterWithOptions,
// but they are not part of the future stable frozen-option contract.
type Option func(*Adapter)

type adapterOptions struct {
	isNotFound      func(error) bool
	hasNotFound     bool
	maxKeyLength    int
	hasMaxKeyLength bool
	allowFlushDB    bool
	hasAllowFlushDB bool
	clearPrefix     string
	hasClearPrefix  bool
}

// WithNotFound configures Redis driver errors that represent cache misses.
// Without this mapper, Get returns the wrapped client's original miss error.
func WithNotFound(isNotFound func(error) bool) Option {
	return func(a *Adapter) {
		a.IsNotFound = isNotFound
		a.options.isNotFound = isNotFound
		a.options.hasNotFound = true
	}
}

// WithMaxKeyLength configures the maximum accepted Redis key length.
func WithMaxKeyLength(max int) Option {
	return func(a *Adapter) {
		a.MaxKeyLength = max
		a.options.maxKeyLength = max
		a.options.hasMaxKeyLength = true
	}
}

// WithAllowFlushDB enables DB-wide Clear when no prefix clear is configured.
// Prefer WithClearPrefix in production; FlushDB is intentionally opt-in because
// it clears the selected Redis database, not only Plumego-owned keys.
func WithAllowFlushDB(allow bool) Option {
	return func(a *Adapter) {
		a.AllowFlushDB = allow
		a.options.allowFlushDB = allow
		a.options.hasAllowFlushDB = true
	}
}

// WithClearPrefix configures Clear to use PrefixFlusher for namespaced keys.
// This is the preferred production Clear contract because it scopes destructive
// operations to a caller-owned namespace.
func WithClearPrefix(prefix string) Option {
	return func(a *Adapter) {
		a.ClearPrefix = prefix
		a.options.clearPrefix = prefix
		a.options.hasClearPrefix = true
	}
}

// NewAdapter wraps a Redis client in a cache.Cache adapter.
//
// NewAdapter is a compatibility constructor for older call sites. Prefer
// NewValidatedAdapterWithOptions for new wiring that needs construction-time
// validation and explicit production options.
func NewAdapter(client Client, isNotFound func(error) bool) *Adapter {
	return &Adapter{
		Client:       client,
		IsNotFound:   isNotFound,
		MaxKeyLength: DefaultMaxKeyLength,
	}
}

// NewAdapterWithOptions wraps a Redis client with explicit adapter options.
//
// It preserves legacy behavior by not validating options at construction time.
// Prefer NewValidatedAdapterWithOptions for new production call sites.
func NewAdapterWithOptions(client Client, opts ...Option) *Adapter {
	adapter := &Adapter{
		Client:       client,
		MaxKeyLength: DefaultMaxKeyLength,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(adapter)
		}
	}
	return adapter
}

// NewValidatedAdapterWithOptions wraps a Redis client and validates adapter
// options during construction. This is the canonical constructor for new Redis
// adapter wiring.
func NewValidatedAdapterWithOptions(client Client, opts ...Option) (*Adapter, error) {
	adapter := NewAdapterWithOptions(client, opts...)
	if err := adapter.validateOptions(); err != nil {
		return nil, err
	}
	return adapter, nil
}

func newCompatibilityAdapter(client Client, isNotFound func(error) bool, opts ...Option) *Adapter {
	adapter := NewAdapterWithOptions(client, opts...)
	adapter.IsNotFound = isNotFound
	if !adapter.options.hasNotFound {
		adapter.options.isNotFound = isNotFound
	}
	return adapter
}

// NewCounterAdapter wraps a Redis client with atomic counter support.
//
// NewCounterAdapter is a compatibility constructor and does not validate
// options at construction time. Prefer NewValidatedCounterAdapterWithOptions
// for new wiring.
func NewCounterAdapter(client Client, isNotFound func(error) bool, opts ...Option) (*CounterAdapter, error) {
	incrementer, ok := client.(Incrementer)
	if !ok {
		return nil, cache.ErrCapabilityUnsupported
	}
	adapter := newCompatibilityAdapter(client, isNotFound, opts...)
	return &CounterAdapter{
		Adapter:     adapter,
		incrementer: incrementer,
	}, nil
}

// NewValidatedCounterAdapterWithOptions wraps a Redis client with atomic
// counter support and validates adapter options during construction.
func NewValidatedCounterAdapterWithOptions(client Client, opts ...Option) (*CounterAdapter, error) {
	adapter, err := NewValidatedAdapterWithOptions(client, opts...)
	if err != nil {
		return nil, err
	}
	incrementer, ok := client.(Incrementer)
	if !ok {
		return nil, cache.ErrCapabilityUnsupported
	}
	return &CounterAdapter{
		Adapter:     adapter,
		incrementer: incrementer,
	}, nil
}

// NewAppenderAdapter wraps a Redis client with atomic append support.
//
// NewAppenderAdapter is a compatibility constructor and does not validate
// options at construction time. Prefer NewValidatedAppenderAdapterWithOptions
// for new wiring.
func NewAppenderAdapter(client Client, isNotFound func(error) bool, opts ...Option) (*AppenderAdapter, error) {
	appender, ok := client.(Appender)
	if !ok {
		return nil, cache.ErrCapabilityUnsupported
	}
	adapter := newCompatibilityAdapter(client, isNotFound, opts...)
	return &AppenderAdapter{
		Adapter:  adapter,
		appender: appender,
	}, nil
}

// NewValidatedAppenderAdapterWithOptions wraps a Redis client with append
// support and validates adapter options during construction.
func NewValidatedAppenderAdapterWithOptions(client Client, opts ...Option) (*AppenderAdapter, error) {
	adapter, err := NewValidatedAdapterWithOptions(client, opts...)
	if err != nil {
		return nil, err
	}
	appender, ok := client.(Appender)
	if !ok {
		return nil, cache.ErrCapabilityUnsupported
	}
	return &AppenderAdapter{
		Adapter:  adapter,
		appender: appender,
	}, nil
}

// NewAtomicAdapter wraps a Redis client with counter and append support.
//
// NewAtomicAdapter is a compatibility constructor and does not validate options
// at construction time. Prefer NewValidatedAtomicAdapterWithOptions for new
// wiring.
func NewAtomicAdapter(client Client, isNotFound func(error) bool, opts ...Option) (*AtomicAdapter, error) {
	incrementer, ok := client.(Incrementer)
	if !ok {
		return nil, cache.ErrCapabilityUnsupported
	}
	appender, ok := client.(Appender)
	if !ok {
		return nil, cache.ErrCapabilityUnsupported
	}
	adapter := newCompatibilityAdapter(client, isNotFound, opts...)
	return &AtomicAdapter{
		Adapter:     adapter,
		incrementer: incrementer,
		appender:    appender,
	}, nil
}

// NewValidatedAtomicAdapterWithOptions wraps a Redis client with counter and
// append support and validates adapter options during construction.
func NewValidatedAtomicAdapterWithOptions(client Client, opts ...Option) (*AtomicAdapter, error) {
	adapter, err := NewValidatedAdapterWithOptions(client, opts...)
	if err != nil {
		return nil, err
	}
	incrementer, ok := client.(Incrementer)
	if !ok {
		return nil, cache.ErrCapabilityUnsupported
	}
	appender, ok := client.(Appender)
	if !ok {
		return nil, cache.ErrCapabilityUnsupported
	}
	return &AtomicAdapter{
		Adapter:     adapter,
		incrementer: incrementer,
		appender:    appender,
	}, nil
}

// Capabilities returns a snapshot of optional Redis adapter capabilities.
func (a *Adapter) Capabilities() AdapterCapabilities {
	if a == nil || a.Client == nil {
		return AdapterCapabilities{}
	}
	_, incrementer := a.Client.(Incrementer)
	_, appender := a.Client.(Appender)
	_, prefixFlusher := a.Client.(PrefixFlusher)
	_, flusher := a.Client.(Flusher)
	clearPrefix := a.clearPrefix()
	allowFlushDB := a.allowFlushDB()

	return AdapterCapabilities{
		Atomic:      incrementer,
		Append:      appender,
		PrefixClear: clearPrefix != "" && prefixFlusher,
		FlushDB:     clearPrefix == "" && allowFlushDB && flusher,
		Clear:       (clearPrefix != "" && prefixFlusher) || (clearPrefix == "" && allowFlushDB && flusher),
	}
}

func (a *Adapter) validateOptions() error {
	if a == nil || a.Client == nil {
		return ErrNilClient
	}
	if a.maxKeyLength() < 0 {
		return fmt.Errorf("%w: MaxKeyLength cannot be negative", cache.ErrInvalidConfig)
	}
	prefix := a.clearPrefix()
	if prefix != "" {
		if err := a.validateKey(prefix); err != nil {
			return err
		}
	}
	if a.options.hasClearPrefix {
		if prefix == "" {
			return fmt.Errorf("%w: %w: clear prefix cannot be empty", cache.ErrInvalidConfig, cache.ErrInvalidKey)
		}
	}
	return nil
}

// validateKey checks if a key is valid and safe.
// This prevents cache key pollution, tenant isolation bypass, and injection attacks.
func (a *Adapter) validateKey(key string) error {
	if key == "" {
		return fmt.Errorf("%w: %w", cache.ErrInvalidConfig, cache.ErrInvalidKey)
	}

	maxKeyLength := a.maxKeyLength()
	if maxKeyLength > 0 && len(key) > maxKeyLength {
		return fmt.Errorf("%w: key length %d exceeds maximum %d",
			cache.ErrKeyTooLong, len(key), maxKeyLength)
	}

	// Prevent cache key pollution by rejecting keys with control characters
	// These characters could be used to bypass tenant isolation or manipulate logging
	for i := 0; i < len(key); i++ {
		c := key[i]
		// Reject ASCII control characters (0x00-0x1F, 0x7F)
		// and newlines which could pollute logs or break key formatting
		if c < 0x20 || c == 0x7F {
			return fmt.Errorf("%w: %w: control character at position %d", cache.ErrInvalidConfig, cache.ErrInvalidKey, i)
		}
	}

	return nil
}

func (a *Adapter) isNotFound(err error) bool {
	if a.options.hasNotFound {
		return a.options.isNotFound != nil && a.options.isNotFound(err)
	}
	return a.IsNotFound != nil && a.IsNotFound(err)
}

func (a *Adapter) maxKeyLength() int {
	if a.options.hasMaxKeyLength {
		return a.options.maxKeyLength
	}
	return a.MaxKeyLength
}

func (a *Adapter) allowFlushDB() bool {
	if a.options.hasAllowFlushDB {
		return a.options.allowFlushDB
	}
	return a.AllowFlushDB
}

func (a *Adapter) clearPrefix() string {
	if a.options.hasClearPrefix {
		return a.options.clearPrefix
	}
	return a.ClearPrefix
}

// Get returns the cached value for the provided key.
func (a *Adapter) Get(ctx context.Context, key string) ([]byte, error) {
	if a == nil || a.Client == nil {
		return nil, ErrNilClient
	}

	if err := a.validateKey(key); err != nil {
		return nil, err
	}

	value, err := a.Client.Get(ctx, key)
	if err != nil {
		if a.isNotFound(err) {
			return nil, cache.ErrNotFound
		}
		return nil, err
	}

	return append([]byte(nil), value...), nil
}

// Set stores a value with the specified TTL.
func (a *Adapter) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if a == nil || a.Client == nil {
		return ErrNilClient
	}

	if err := a.validateKey(key); err != nil {
		return err
	}

	return a.Client.Set(ctx, key, append([]byte(nil), value...), ttl)
}

// Delete removes the key from Redis.
func (a *Adapter) Delete(ctx context.Context, key string) error {
	if a == nil || a.Client == nil {
		return ErrNilClient
	}

	if err := a.validateKey(key); err != nil {
		return err
	}

	_, err := a.Client.Del(ctx, key)
	return err
}

// Exists reports whether a key exists.
func (a *Adapter) Exists(ctx context.Context, key string) (bool, error) {
	if a == nil || a.Client == nil {
		return false, ErrNilClient
	}

	if err := a.validateKey(key); err != nil {
		return false, err
	}

	count, err := a.Client.Exists(ctx, key)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// Clear removes namespaced keys through PrefixFlusher or, when explicitly
// enabled, all keys through FlushDB.
func (a *Adapter) Clear(ctx context.Context) error {
	if a == nil || a.Client == nil {
		return ErrNilClient
	}
	clearPrefix := a.clearPrefix()
	if clearPrefix != "" {
		if err := a.validateKey(clearPrefix); err != nil {
			return err
		}
		prefixFlusher, ok := a.Client.(PrefixFlusher)
		if !ok {
			return ErrClearUnsupported
		}
		return prefixFlusher.FlushPrefix(ctx, clearPrefix)
	}
	if !a.allowFlushDB() {
		return ErrFlushDBDisabled
	}
	flusher, ok := a.Client.(Flusher)
	if !ok {
		return ErrClearUnsupported
	}
	return flusher.FlushDB(ctx)
}

func incr(ctx context.Context, a *Adapter, incrementer Incrementer, key string, delta int64) (int64, error) {
	if a == nil || a.Client == nil {
		return 0, ErrNilClient
	}
	if incrementer == nil {
		return 0, cache.ErrCapabilityUnsupported
	}

	if err := a.validateKey(key); err != nil {
		return 0, err
	}

	return incrementer.IncrBy(ctx, key, delta)
}

func decr(ctx context.Context, a *Adapter, incrementer Incrementer, key string, delta int64) (int64, error) {
	if delta == minInt64 {
		return 0, fmt.Errorf("%w: integer overflow", cache.ErrNotInteger)
	}
	return incr(ctx, a, incrementer, key, -delta)
}

func appendValue(ctx context.Context, a *Adapter, appender Appender, key string, data []byte) error {
	if a == nil || a.Client == nil {
		return ErrNilClient
	}
	if appender == nil {
		return cache.ErrCapabilityUnsupported
	}

	if err := a.validateKey(key); err != nil {
		return err
	}

	_, err := appender.Append(ctx, key, append([]byte(nil), data...))
	return err
}

// Incr atomically increments the integer value of a key by delta.
func (a *CounterAdapter) Incr(ctx context.Context, key string, delta int64) (int64, error) {
	if a == nil {
		return 0, ErrNilClient
	}
	return incr(ctx, a.Adapter, a.incrementer, key, delta)
}

// Decr atomically decrements the integer value of a key by delta.
func (a *CounterAdapter) Decr(ctx context.Context, key string, delta int64) (int64, error) {
	if a == nil {
		return 0, ErrNilClient
	}
	return decr(ctx, a.Adapter, a.incrementer, key, delta)
}

// Append appends data to the end of an existing value.
func (a *AppenderAdapter) Append(ctx context.Context, key string, data []byte) error {
	if a == nil {
		return ErrNilClient
	}
	return appendValue(ctx, a.Adapter, a.appender, key, data)
}

// Incr atomically increments the integer value of a key by delta.
func (a *AtomicAdapter) Incr(ctx context.Context, key string, delta int64) (int64, error) {
	if a == nil {
		return 0, ErrNilClient
	}
	return incr(ctx, a.Adapter, a.incrementer, key, delta)
}

// Decr atomically decrements the integer value of a key by delta.
func (a *AtomicAdapter) Decr(ctx context.Context, key string, delta int64) (int64, error) {
	if a == nil {
		return 0, ErrNilClient
	}
	return decr(ctx, a.Adapter, a.incrementer, key, delta)
}

// Append appends data to the end of an existing value.
func (a *AtomicAdapter) Append(ctx context.Context, key string, data []byte) error {
	if a == nil {
		return ErrNilClient
	}
	return appendValue(ctx, a.Adapter, a.appender, key, data)
}
