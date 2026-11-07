package cache

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"strconv"
	"sync"
	"time"
)

const (
	maxInt64 = int64(1<<63 - 1)
	minInt64 = -1 << 63
)

// MemoryCache is an in-memory cache implementation using sync.Map.
type MemoryCache struct {
	store     sync.Map
	config    Config
	writeMu   sync.Mutex
	stateMu   sync.RWMutex
	size      int
	memory    uint64
	closed    bool
	stopChan  chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup
}

type cacheItem struct {
	value      []byte
	expiration time.Time
}

// NewMemoryCache creates an empty MemoryCache instance with the default configuration.
// It panics if the default configuration is invalid, which indicates a programming error
// (DefaultConfig must always return a valid Config).
func NewMemoryCache() *MemoryCache {
	cache, err := NewMemoryCacheWithConfig(DefaultConfig())
	if err != nil {
		panic(fmt.Sprintf("cache: NewMemoryCache with default config: %v", err))
	}
	return cache
}

// NewMemoryCacheWithConfig creates a MemoryCache with custom configuration.
func NewMemoryCacheWithConfig(config Config) (*MemoryCache, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}

	cache := &MemoryCache{
		config:   config,
		stopChan: make(chan struct{}),
	}

	if config.CleanupInterval > 0 {
		cache.startCleanup()
	}

	return cache, nil
}

// startCleanup starts the background cleanup goroutine.
func (mc *MemoryCache) startCleanup() {
	mc.wg.Add(1)
	go func() {
		defer mc.wg.Done()

		ticker := time.NewTicker(mc.config.CleanupInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				mc.cleanupExpired()
			case <-mc.stopChan:
				return
			}
		}
	}()
}

// removeExpiredItem removes an expired item and updates internal state.
// Returns true if the item was removed.
func (mc *MemoryCache) removeExpiredItem(key any, item cacheItem) bool {
	if !expired(item.expiration) {
		return false
	}

	mc.writeMu.Lock()
	defer mc.writeMu.Unlock()
	return mc.removeExpiredItemLocked(key, item)
}

func (mc *MemoryCache) removeExpiredItemLocked(key any, item cacheItem) bool {
	if !expired(item.expiration) {
		return false
	}
	current, ok := mc.store.Load(key)
	if !ok {
		return false
	}
	currentItem := current.(cacheItem)
	if currentItem.expiration != item.expiration || !bytes.Equal(currentItem.value, item.value) {
		return false
	}

	mc.store.Delete(key)
	mc.adjustStoredValue(-1, -int64(len(item.value)))
	return true
}

// cleanupExpired removes expired items from the cache.
func (mc *MemoryCache) cleanupExpired() {
	if err := mc.operationErr(context.Background()); err != nil {
		return
	}

	mc.store.Range(func(key, value any) bool {
		item := value.(cacheItem)
		mc.removeExpiredItem(key, item)
		return true
	})
}

// validateKey checks if a key is valid for stable in-process cache storage.
func (mc *MemoryCache) validateKey(key string) error {
	if key == "" {
		return ErrInvalidKey
	}
	if mc.config.MaxKeyLength > 0 && len(key) > mc.config.MaxKeyLength {
		return fmt.Errorf("%w: key length %d exceeds maximum %d", ErrKeyTooLong, len(key), mc.config.MaxKeyLength)
	}

	// Reject control characters so keys remain safe for logs and serializers.
	for i := 0; i < len(key); i++ {
		c := key[i]
		if c < 0x20 || c == 0x7F {
			return fmt.Errorf("%w: control character at position %d", ErrInvalidKey, i)
		}
	}

	return nil
}

// checkMemoryLimit checks if adding the value would exceed the tracked payload limit.
func (mc *MemoryCache) checkMemoryLimit(valueSize, existingSize uint64) error {
	if mc.config.MaxMemoryUsage == 0 {
		return nil
	}

	currentMemory := mc.currentMemoryUsage()
	if currentMemory >= existingSize {
		currentMemory -= existingSize
	}

	if currentMemory+valueSize > mc.config.MaxMemoryUsage {
		return fmt.Errorf("%w: memory limit exceeded (current: %d, adding: %d, limit: %d)",
			ErrCacheFull, currentMemory, valueSize, mc.config.MaxMemoryUsage)
	}

	return nil
}

// Get returns the cached value for the provided key if it exists and has not expired.
func (mc *MemoryCache) Get(ctx context.Context, key string) ([]byte, error) {
	if err := mc.operationErr(ctx); err != nil {
		return nil, err
	}
	if err := mc.validateKey(key); err != nil {
		return nil, err
	}
	val, ok := mc.store.Load(key)
	if !ok {
		return nil, ErrNotFound
	}

	item := val.(cacheItem)
	if expired(item.expiration) {
		mc.removeExpiredItem(key, item)
		return nil, ErrNotFound
	}

	return cloneBytes(item.value), nil
}

// Set stores a value with the specified TTL. A non-positive TTL uses DefaultTTL
// when configured; otherwise the value is stored without an expiration.
func (mc *MemoryCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if err := mc.operationErr(ctx); err != nil {
		return err
	}
	if err := mc.validateKey(key); err != nil {
		return err
	}

	if err := mc.lockWriteOperation(ctx); err != nil {
		return err
	}
	defer mc.writeMu.Unlock()
	return mc.setLocked(key, value, ttl)
}

func (mc *MemoryCache) setLocked(key string, value []byte, ttl time.Duration) error {
	return mc.setLockedWithExpiration(key, value, mc.expirationForTTL(ttl, time.Now()))
}

func (mc *MemoryCache) setLockedWithExpiration(key string, value []byte, exp time.Time) error {
	existingSize := uint64(0)
	existingFound := false
	if existing, ok := mc.store.Load(key); ok {
		existingItem := existing.(cacheItem)
		if expired(existingItem.expiration) {
			mc.removeExpiredItemLocked(key, existingItem)
		} else {
			existingSize = uint64(len(existingItem.value))
			existingFound = true
		}
	}

	valueSize := uint64(len(value))
	if err := mc.checkMemoryLimit(valueSize, existingSize); err != nil {
		return err
	}

	mc.store.Store(key, cacheItem{
		value:      cloneBytes(value),
		expiration: exp,
	})

	deltaSize := 0
	if !existingFound {
		deltaSize = 1
	}
	mc.adjustStoredValue(deltaSize, int64(valueSize)-int64(existingSize))

	return nil
}

func (mc *MemoryCache) expirationForTTL(ttl time.Duration, now time.Time) time.Time {
	if ttl > 0 {
		return now.Add(ttl)
	}
	if mc.config.DefaultTTL > 0 {
		return now.Add(mc.config.DefaultTTL)
	}
	return time.Time{}
}

// Delete removes the key from the cache. Missing keys are treated as a
// successful idempotent delete.
func (mc *MemoryCache) Delete(ctx context.Context, key string) error {
	if err := mc.operationErr(ctx); err != nil {
		return err
	}
	if err := mc.validateKey(key); err != nil {
		return err
	}

	if err := mc.lockWriteOperation(ctx); err != nil {
		return err
	}
	defer mc.writeMu.Unlock()
	if existing, ok := mc.store.Load(key); ok {
		item := existing.(cacheItem)
		mc.store.Delete(key)
		mc.adjustStoredValue(-1, -int64(len(item.value)))
	}

	return nil
}

// Exists reports whether a key exists and has not expired.
func (mc *MemoryCache) Exists(ctx context.Context, key string) (bool, error) {
	if err := mc.operationErr(ctx); err != nil {
		return false, err
	}
	if err := mc.validateKey(key); err != nil {
		return false, err
	}
	val, ok := mc.store.Load(key)
	if !ok {
		return false, nil
	}

	item := val.(cacheItem)
	if expired(item.expiration) {
		mc.removeExpiredItem(key, item)
		return false, nil
	}

	return true, nil
}

// Clear removes all keys from the cache.
func (mc *MemoryCache) Clear(ctx context.Context) error {
	if err := mc.operationErr(ctx); err != nil {
		return err
	}
	if err := mc.lockWriteOperation(ctx); err != nil {
		return err
	}
	defer mc.writeMu.Unlock()
	mc.store.Range(func(key, value any) bool {
		item := value.(cacheItem)
		mc.store.Delete(key)
		mc.adjustStoredValue(-1, -int64(len(item.value)))
		return true
	})

	return nil
}

// Incr increments the integer value of a key by delta.
func (mc *MemoryCache) Incr(ctx context.Context, key string, delta int64) (int64, error) {
	if err := mc.operationErr(ctx); err != nil {
		return 0, err
	}
	if err := mc.validateKey(key); err != nil {
		return 0, err
	}

	if err := mc.lockWriteOperation(ctx); err != nil {
		return 0, err
	}
	defer mc.writeMu.Unlock()

	// Get current value
	var currentVal int64
	exp := mc.expirationForTTL(0, time.Now())
	if val, ok := mc.store.Load(key); ok {
		item := val.(cacheItem)
		if expired(item.expiration) {
			mc.removeExpiredItemLocked(key, item)
		} else {
			exp = item.expiration
			// Try to parse as int64
			num, err := decodeInt64(item.value)
			if err != nil {
				return 0, ErrNotInteger
			}
			currentVal = num
		}
	}

	newVal, err := addInt64(currentVal, delta)
	if err != nil {
		return 0, err
	}

	encoded, err := encodeInt64(newVal)
	if err != nil {
		return 0, err
	}

	// Store new value
	if err := mc.setLockedWithExpiration(key, encoded, exp); err != nil {
		return 0, err
	}

	return newVal, nil
}

// Decr decrements the integer value of a key by delta.
func (mc *MemoryCache) Decr(ctx context.Context, key string, delta int64) (int64, error) {
	if delta == minInt64 {
		return 0, fmt.Errorf("%w: integer overflow", ErrNotInteger)
	}
	return mc.Incr(ctx, key, -delta)
}

// Append appends data to the end of an existing value.
func (mc *MemoryCache) Append(ctx context.Context, key string, data []byte) error {
	if err := mc.operationErr(ctx); err != nil {
		return err
	}
	if err := mc.validateKey(key); err != nil {
		return err
	}

	if err := mc.lockWriteOperation(ctx); err != nil {
		return err
	}
	defer mc.writeMu.Unlock()

	// Get existing value
	var existingData []byte
	exp := mc.expirationForTTL(0, time.Now())
	if val, ok := mc.store.Load(key); ok {
		item := val.(cacheItem)
		if expired(item.expiration) {
			mc.removeExpiredItemLocked(key, item)
		} else {
			existingData = item.value
			exp = item.expiration
		}
	}

	// Append new data
	newData := append(cloneBytes(existingData), data...)

	// Store new value
	return mc.setLockedWithExpiration(key, newData, exp)
}

// Close stops the background cleanup goroutine.
func (mc *MemoryCache) Close() error {
	if mc == nil {
		return nil
	}
	mc.closeOnce.Do(func() {
		mc.stateMu.Lock()
		mc.closed = true
		mc.stateMu.Unlock()

		if mc.stopChan != nil {
			close(mc.stopChan)
			mc.wg.Wait()
		}
		// Barrier: acquiring writeMu after marking the cache closed blocks
		// until any in-flight write operation has fully released the lock,
		// so Close does not return while a write is still mid-flight.
		mc.writeMu.Lock()
		mc.writeMu.Unlock() //nolint:staticcheck // SA2001: empty critical section is an intentional barrier
	})
	return nil
}

func (mc *MemoryCache) closedErr() error {
	return mc.operationErr(context.Background())
}

func (mc *MemoryCache) lockWriteOperation(ctx context.Context) error {
	mc.writeMu.Lock()
	if err := mc.operationErr(ctx); err != nil {
		mc.writeMu.Unlock()
		return err
	}
	return nil
}

func (mc *MemoryCache) operationErr(ctx context.Context) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if mc == nil {
		return ErrCacheClosed
	}
	mc.stateMu.RLock()
	closed := mc.closed || mc.stopChan == nil
	mc.stateMu.RUnlock()
	if closed {
		return ErrCacheClosed
	}
	return nil
}

func (mc *MemoryCache) currentMemoryUsage() uint64 {
	mc.stateMu.RLock()
	defer mc.stateMu.RUnlock()
	return mc.memory
}

func (mc *MemoryCache) adjustStoredValue(deltaSize int, deltaMemory int64) {
	mc.stateMu.Lock()
	defer mc.stateMu.Unlock()

	mc.size += deltaSize
	if mc.size < 0 {
		mc.size = 0
	}

	if deltaMemory < 0 {
		reduction := uint64(-deltaMemory)
		if mc.memory >= reduction {
			mc.memory -= reduction
		} else {
			mc.memory = 0
		}
		return
	}

	mc.memory += uint64(deltaMemory)
}

func expired(exp time.Time) bool {
	return expiredAt(exp, time.Now())
}

func expiredAt(exp, now time.Time) bool {
	return !exp.IsZero() && !exp.After(now)
}

func cloneBytes(in []byte) []byte {
	if in == nil {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func decodeInt64(data []byte) (int64, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return 0, fmt.Errorf("empty integer")
	}
	if num, err := strconv.ParseInt(string(trimmed), 10, 64); err == nil {
		return num, nil
	}

	var num int64
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&num); err != nil {
		return 0, err
	}
	return num, nil
}

func encodeInt64(num int64) ([]byte, error) {
	return strconv.AppendInt(nil, num, 10), nil
}

func addInt64(value, delta int64) (int64, error) {
	if delta > 0 && value > maxInt64-delta {
		return 0, fmt.Errorf("%w: integer overflow", ErrNotInteger)
	}
	if delta < 0 && value < minInt64-delta {
		return 0, fmt.Errorf("%w: integer overflow", ErrNotInteger)
	}
	return value + delta, nil
}
