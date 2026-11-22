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
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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

	directoryLock *os.File
	syncStateDir  func(string) error

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
// state is created. Only one store can open a data directory at a time.
// Call Close to release it.
func NewKVStore(opts Options) (*KVStore, error) {
	setConfigDefaults(&opts)
	if err := validateConfig(opts); err != nil {
		return nil, err
	}
	dir, err := filepath.Abs(opts.DataDir)
	if err != nil {
		return nil, fmt.Errorf("resolve data dir: %w", err)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	opts.DataDir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve data dir: %w", err)
	}
	// Keep the sidecar in place so every opener coordinates on the same file.
	lock, err := openDirectoryLock(filepath.Join(opts.DataDir, "store.lock"))
	if err != nil {
		return nil, fmt.Errorf("lock data dir: %w", err)
	}

	store := &KVStore{
		opts:          opts,
		data:          make(map[string]*entry),
		directoryLock: lock,
		syncStateDir:  syncDir,
	}
	if err := store.load(); err != nil {
		return nil, errors.Join(err, store.Close())
	}
	store.evictIfNeededLocked("")
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
	err := kv.setWithCancellation(contextDone(ctx), key, value, ttl)
	if err == errMutationCanceled {
		return ctx.Err()
	}
	return err
}

func (kv *KVStore) setWithCancellation(done <-chan struct{}, key string, value []byte, ttl time.Duration) error {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	if cancellationSignaled(done) {
		return errMutationCanceled
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
	kv.evictIfNeededLocked(key)
	return kv.persistMutationLocked(done, func() { kv.data = before })
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
	done := contextDone(ctx)
	kv.mu.RLock()
	if cancellationSignaled(done) {
		kv.mu.RUnlock()
		return nil, ctx.Err()
	}
	defer kv.mu.RUnlock()
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
	err := kv.deleteWithCancellation(contextDone(ctx), key)
	if err == errMutationCanceled {
		return ctx.Err()
	}
	return err
}

func (kv *KVStore) deleteWithCancellation(done <-chan struct{}, key string) error {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	if cancellationSignaled(done) {
		return errMutationCanceled
	}
	if kv.closed || kv.data == nil {
		return ErrStoreClosed
	}
	if err := validateKey(key); err != nil {
		return err
	}
	item, ok := kv.data[key]
	if !ok {
		return ErrKeyNotFound
	}
	delete(kv.data, key)
	return kv.persistMutationLocked(done, func() { kv.data[key] = item })
}

// ExistsContext reports whether a non-expired key exists using the caller-provided context.
func (kv *KVStore) ExistsContext(ctx context.Context, key string) (bool, error) {
	if err := contextErr(ctx); err != nil {
		return false, err
	}
	if kv == nil {
		return false, ErrStoreClosed
	}
	done := contextDone(ctx)
	kv.mu.RLock()
	if cancellationSignaled(done) {
		kv.mu.RUnlock()
		return false, ctx.Err()
	}
	defer kv.mu.RUnlock()
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

	keys, err := kv.snapshotKeys(ctx)
	if err != nil {
		return nil, err
	}
	sort.Strings(keys)
	return keys, nil
}

func (kv *KVStore) snapshotKeys(ctx context.Context) ([]string, error) {
	done := contextDone(ctx)
	kv.mu.RLock()
	if cancellationSignaled(done) {
		kv.mu.RUnlock()
		return nil, ctx.Err()
	}
	defer kv.mu.RUnlock()
	if kv.closed || kv.data == nil {
		return nil, ErrStoreClosed
	}

	now := time.Now()
	// Keep small snapshots independent of the number of expired entries.
	var inline [32]string
	small := inline[:0]
	var keys []string
	for key, item := range kv.data {
		if kv.isExpired(item, now) {
			continue
		}
		if keys == nil {
			if len(small) < cap(small) {
				small = append(small, key)
				continue
			}
			keys = make([]string, len(small), len(kv.data))
			copy(keys, small)
		}
		keys = append(keys, key)
	}
	if keys == nil {
		return append([]string{}, small...), nil
	}
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
	done := contextDone(ctx)
	kv.mu.RLock()
	if cancellationSignaled(done) {
		kv.mu.RUnlock()
		return 0, ctx.Err()
	}
	defer kv.mu.RUnlock()
	if kv.closed || kv.data == nil {
		return 0, ErrStoreClosed
	}

	entries, _ := kv.currentUsageLocked(time.Now())
	return entries, nil
}

// GetStatsContext returns statistics using the caller-provided context.
// Entries and MemoryUsage use the same time to evaluate expiration. Hits and
// Misses are sampled independently and can reflect different points in time
// during concurrent reads. HitRatio is computed from the returned counters.
func (kv *KVStore) GetStatsContext(ctx context.Context) (Stats, error) {
	if err := contextErr(ctx); err != nil {
		return Stats{}, err
	}
	if kv == nil {
		return Stats{}, ErrStoreClosed
	}
	done := contextDone(ctx)
	kv.mu.RLock()
	if cancellationSignaled(done) {
		kv.mu.RUnlock()
		return Stats{}, ctx.Err()
	}
	defer kv.mu.RUnlock()
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

// Close releases the directory lock and discards this handle's in-memory snapshot.
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
	kv.data = nil
	if kv.directoryLock != nil {
		err := kv.directoryLock.Close()
		kv.directoryLock = nil
		if err != nil {
			return fmt.Errorf("release data dir lock: %w", err)
		}
	}
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

func (kv *KVStore) evictIfNeededLocked(writtenKey string) {
	now := time.Now()
	var memoryUsage int64
	var oldest evictionCandidate
	for key, item := range kv.data {
		if kv.isExpired(item, now) {
			delete(kv.data, key)
			continue
		}
		memoryUsage += item.Size
		if key != writtenKey {
			candidate := evictionCandidate{key: key, item: item}
			if oldest.item == nil || compareEvictionCandidates(candidate, oldest) < 0 {
				oldest = candidate
			}
		}
	}

	maxMemory := kv.maxMemoryBytes()
	if len(kv.data) <= kv.opts.MaxEntries && memoryUsage <= maxMemory {
		return
	}
	if oldest.item == nil {
		return
	}

	// Removing the oldest entry first avoids sorting for a single eviction.
	delete(kv.data, oldest.key)
	memoryUsage -= oldest.item.Size
	if len(kv.data) <= kv.opts.MaxEntries && memoryUsage <= maxMemory {
		return
	}

	candidates := make([]evictionCandidate, 0, len(kv.data))
	for key, item := range kv.data {
		if key != writtenKey {
			candidates = append(candidates, evictionCandidate{key: key, item: item})
		}
	}
	slices.SortFunc(candidates, compareEvictionCandidates)

	for _, candidate := range candidates {
		delete(kv.data, candidate.key)
		memoryUsage -= candidate.item.Size
		if len(kv.data) <= kv.opts.MaxEntries && memoryUsage <= maxMemory {
			return
		}
	}
}

type evictionCandidate struct {
	key  string
	item *entry
}

func compareEvictionCandidates(a, b evictionCandidate) int {
	if order := a.item.UpdatedAt.Compare(b.item.UpdatedAt); order != 0 {
		return order
	}
	return cmp.Compare(a.key, b.key)
}

func (kv *KVStore) maxMemoryBytes() int64 {
	return int64(kv.opts.MaxMemoryMB) * bytesPerMiB
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

// Obtain the channel before locking so a caller's context implementation is
// never invoked while the store is locked.
func contextDone(ctx context.Context) <-chan struct{} {
	if ctx == nil {
		return nil
	}
	return ctx.Done()
}

func cancellationSignaled(done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	default:
		return false
	}
}

// errMutationCanceled carries cancellation out of the locked mutation before
// consulting the caller's context for the concrete cancellation reason.
var errMutationCanceled = errors.New("kv: mutation canceled before persistence")

func (kv *KVStore) persistMutationLocked(done <-chan struct{}, rollback func()) error {
	if cancellationSignaled(done) {
		rollback()
		return errMutationCanceled
	}
	committed, err := kv.persistLocked()
	if err != nil && !committed {
		rollback()
	}
	return err
}
