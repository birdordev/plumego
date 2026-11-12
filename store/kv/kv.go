// Package kvstore provides a small embedded persistent key-value primitive.
//
// The stable surface intentionally stays narrow:
//   - atomic file-backed persistence for small datasets
//   - TTL-aware get/set/delete operations
//   - key enumeration and basic runtime stats
//
// Durable-engine tuning such as WAL, snapshots, serializer selection,
// compression, and shard configuration lives in x/data/kvengine.
package kvstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

var (
	ErrKeyNotFound   = errors.New("kv: key not found")
	ErrKeyExpired    = errors.New("kv: key expired")
	ErrInvalidKey    = errors.New("kv: key is required")
	ErrStoreClosed   = errors.New("kv: store is closed")
	ErrValueTooLarge = errors.New("kv: value too large")
	ErrCorruptState  = errors.New("kv: corrupt state")
)

type entry struct {
	Value     []byte    `json:"value"`
	ExpireAt  time.Time `json:"expire_at,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
	Size      int64     `json:"size"`
}

// KVStore is a small embedded persistent key-value store.
type KVStore struct {
	mu     sync.RWMutex
	opts   Options
	data   map[string]*entry
	closed bool

	syncStateDir func(string) error

	hits   int64
	misses int64
}

// Stats provides runtime statistics.
type Stats struct {
	Entries     int64   `json:"entries"`
	Hits        int64   `json:"hits"`
	Misses      int64   `json:"misses"`
	MemoryUsage int64   `json:"memory_usage"`
	HitRatio    float64 `json:"hit_ratio"`
}

// NewKVStore creates a new embedded KV primitive backed by a single state file.
//
// DataDir must be explicitly configured so callers choose where filesystem
// state is created.
func NewKVStore(opts Options) (*KVStore, error) {
	setConfigDefaults(&opts)
	if err := validateConfig(opts); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(opts.DataDir, 0755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	store := &KVStore{
		opts:         opts,
		data:         make(map[string]*entry),
		syncStateDir: syncDir,
	}
	if err := store.load(); err != nil {
		return nil, err
	}
	store.pruneExpiredLocked(time.Now())
	store.evictIfNeededLocked()
	return store, nil
}

// Set stores a value with an optional TTL.
func (kv *KVStore) Set(key string, value []byte, ttl time.Duration) error {
	return kv.SetContext(context.Background(), key, value, ttl)
}

// SetContext stores a value with an optional TTL using the caller-provided context.
func (kv *KVStore) SetContext(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if kv == nil {
		return ErrStoreClosed
	}
	kv.mu.Lock()
	defer kv.mu.Unlock()

	if err := contextErr(ctx); err != nil {
		return err
	}
	if kv.closed || kv.data == nil {
		return ErrStoreClosed
	}
	if err := validateKey(key); err != nil {
		return err
	}

	now := time.Now()
	var expireAt time.Time
	if ttl > 0 {
		expireAt = now.Add(ttl)
	}
	size := entrySize(key, value)
	if size > kv.maxMemoryBytes() {
		return fmt.Errorf("%w: size %d limit %d", ErrValueTooLarge, size, kv.maxMemoryBytes())
	}
	before := kv.cloneDataLocked()
	kv.data[key] = &entry{
		Value:     cloneBytes(value),
		ExpireAt:  expireAt,
		UpdatedAt: now,
		Size:      size,
	}
	kv.evictIfNeededLocked()
	if err := contextErr(ctx); err != nil {
		kv.data = before
		return err
	}
	if committed, err := kv.persistLocked(); err != nil {
		if !committed {
			kv.data = before
		}
		return err
	}
	return nil
}

// Get returns a defensive copy of a value.
func (kv *KVStore) Get(key string) ([]byte, error) {
	return kv.GetContext(context.Background(), key)
}

// GetContext returns a defensive copy of a value using the caller-provided context.
func (kv *KVStore) GetContext(ctx context.Context, key string) ([]byte, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	if kv == nil {
		return nil, ErrStoreClosed
	}
	kv.mu.RLock()
	defer kv.mu.RUnlock()

	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	if kv.closed || kv.data == nil {
		return nil, ErrStoreClosed
	}
	if err := validateKey(key); err != nil {
		return nil, err
	}

	item, ok := kv.data[key]
	if !ok {
		atomic.AddInt64(&kv.misses, 1)
		return nil, ErrKeyNotFound
	}
	if kv.isExpired(item, time.Now()) {
		atomic.AddInt64(&kv.misses, 1)
		return nil, ErrKeyExpired
	}

	atomic.AddInt64(&kv.hits, 1)
	return cloneBytes(item.Value), nil
}

// Delete removes a key and returns ErrKeyNotFound when the key is missing.
func (kv *KVStore) Delete(key string) error {
	return kv.DeleteContext(context.Background(), key)
}

// DeleteContext removes a key using the caller-provided context and returns
// ErrKeyNotFound when the key is missing.
func (kv *KVStore) DeleteContext(ctx context.Context, key string) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if kv == nil {
		return ErrStoreClosed
	}
	kv.mu.Lock()
	defer kv.mu.Unlock()

	if err := contextErr(ctx); err != nil {
		return err
	}
	if kv.closed || kv.data == nil {
		return ErrStoreClosed
	}
	if err := validateKey(key); err != nil {
		return err
	}
	if _, ok := kv.data[key]; !ok {
		return ErrKeyNotFound
	}
	before := kv.cloneDataLocked()
	delete(kv.data, key)
	if err := contextErr(ctx); err != nil {
		kv.data = before
		return err
	}
	if committed, err := kv.persistLocked(); err != nil {
		if !committed {
			kv.data = before
		}
		return err
	}
	return nil
}

// ExistsContext reports whether a non-expired key exists using the caller-provided context.
func (kv *KVStore) ExistsContext(ctx context.Context, key string) (bool, error) {
	if err := contextErr(ctx); err != nil {
		return false, err
	}
	if kv == nil {
		return false, ErrStoreClosed
	}
	kv.mu.RLock()
	defer kv.mu.RUnlock()

	if err := contextErr(ctx); err != nil {
		return false, err
	}
	if kv.closed || kv.data == nil {
		return false, ErrStoreClosed
	}
	if err := validateKey(key); err != nil {
		return false, err
	}
	item, ok := kv.data[key]
	if !ok {
		return false, nil
	}
	if kv.isExpired(item, time.Now()) {
		return false, nil
	}
	return true, nil
}

// KeysContext returns all non-expired keys in sorted order using the caller-provided context.
func (kv *KVStore) KeysContext(ctx context.Context) ([]string, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	if kv == nil {
		return nil, ErrStoreClosed
	}
	kv.mu.RLock()
	defer kv.mu.RUnlock()

	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	if kv.closed || kv.data == nil {
		return nil, ErrStoreClosed
	}

	now := time.Now()
	keys := make([]string, 0, len(kv.data))
	for key, item := range kv.data {
		if kv.isExpired(item, now) {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys, nil
}

// SizeContext returns the number of non-expired keys using the caller-provided context.
func (kv *KVStore) SizeContext(ctx context.Context) (int, error) {
	if err := contextErr(ctx); err != nil {
		return 0, err
	}
	if kv == nil {
		return 0, ErrStoreClosed
	}
	kv.mu.RLock()
	defer kv.mu.RUnlock()

	if err := contextErr(ctx); err != nil {
		return 0, err
	}
	if kv.closed || kv.data == nil {
		return 0, ErrStoreClosed
	}

	entries, _ := kv.currentUsageLocked(time.Now())
	return entries, nil
}

// GetStatsContext returns point-in-time statistics using the caller-provided context.
func (kv *KVStore) GetStatsContext(ctx context.Context) (Stats, error) {
	if err := contextErr(ctx); err != nil {
		return Stats{}, err
	}
	if kv == nil {
		return Stats{}, ErrStoreClosed
	}
	kv.mu.RLock()
	defer kv.mu.RUnlock()

	if err := contextErr(ctx); err != nil {
		return Stats{}, err
	}
	if kv.closed || kv.data == nil {
		return Stats{}, ErrStoreClosed
	}

	hits := atomic.LoadInt64(&kv.hits)
	misses := atomic.LoadInt64(&kv.misses)
	entries, memoryUsage := kv.currentUsageLocked(time.Now())

	var hitRatio float64
	if hits+misses > 0 {
		hitRatio = float64(hits) / float64(hits+misses)
	}

	return Stats{
		Entries:     int64(entries),
		Hits:        hits,
		Misses:      misses,
		MemoryUsage: memoryUsage,
		HitRatio:    hitRatio,
	}, nil
}

func validateKey(key string) error {
	if key == "" {
		return ErrInvalidKey
	}
	if !utf8.ValidString(key) {
		return fmt.Errorf("%w: key must be valid UTF-8", ErrInvalidKey)
	}
	for i := 0; i < len(key); i++ {
		c := key[i]
		if c < 0x20 || c == 0x7F {
			return fmt.Errorf("%w: control character at position %d", ErrInvalidKey, i)
		}
	}
	return nil
}

// Close closes the store.
func (kv *KVStore) Close() error {
	if kv == nil {
		return nil
	}
	kv.mu.Lock()
	defer kv.mu.Unlock()

	if kv.closed {
		return nil
	}
	kv.closed = true
	return nil
}

func (kv *KVStore) currentUsage() (int, int64) {
	kv.mu.RLock()
	defer kv.mu.RUnlock()
	return kv.currentUsageLocked(time.Now())
}

func (kv *KVStore) currentUsageLocked(now time.Time) (int, int64) {
	count := 0
	var memoryUsage int64
	for _, item := range kv.data {
		if kv.isExpired(item, now) {
			continue
		}
		count++
		memoryUsage += item.Size
	}
	return count, memoryUsage
}

func cloneBytes(in []byte) []byte {
	if in == nil {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}

func (kv *KVStore) pruneExpiredLocked(now time.Time) {
	for key, item := range kv.data {
		if kv.isExpired(item, now) {
			delete(kv.data, key)
		}
	}
}

func (kv *KVStore) evictIfNeededLocked() {
	kv.pruneExpiredLocked(time.Now())
	maxMemory := int64(kv.opts.MaxMemoryMB) * 1024 * 1024
	for len(kv.data) > kv.opts.MaxEntries || kv.memoryUsageLocked() > maxMemory {
		key := kv.oldestKeyLocked()
		if key == "" {
			return
		}
		delete(kv.data, key)
	}
}

func (kv *KVStore) oldestKeyLocked() string {
	var (
		oldestKey string
		oldestAt  time.Time
	)
	for key, item := range kv.data {
		if oldestKey == "" || item.UpdatedAt.Before(oldestAt) {
			oldestKey = key
			oldestAt = item.UpdatedAt
		}
	}
	return oldestKey
}

func (kv *KVStore) memoryUsageLocked() int64 {
	var total int64
	now := time.Now()
	for _, item := range kv.data {
		if kv.isExpired(item, now) {
			continue
		}
		total += item.Size
	}
	return total
}

func (kv *KVStore) maxMemoryBytes() int64 {
	return int64(kv.opts.MaxMemoryMB) * 1024 * 1024
}

func entrySize(key string, value []byte) int64 {
	return int64(len(key) + len(value) + 64)
}

func (kv *KVStore) isExpired(item *entry, now time.Time) bool {
	return !item.ExpireAt.IsZero() && !item.ExpireAt.After(now)
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
