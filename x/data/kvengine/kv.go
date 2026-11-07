// Package kvengine provides a durable embedded key-value engine with WAL.
//
// This package implements a high-performance, disk-backed key-value store featuring:
//   - Write-Ahead Logging (WAL) for durability
//   - LRU eviction with configurable memory limits
//   - TTL (time-to-live) support for automatic expiration
//   - Snapshot and restore capabilities
//   - Compression (gzip) for reduced disk usage
//   - Metrics collection and monitoring
//
// The store is optimized for embedded use cases where you need persistence
// without the complexity of a separate database server. It's ideal for
// configuration storage, session management, and small-to-medium datasets.
//
// Example usage:
//
//	import kvengine "github.com/spcent/plumego/x/data/kvengine"
//
//	// Create or open a store
//	store, err := kvengine.NewKVStore(kvengine.Options{
//		DataDir:     "/data/mystore",
//		MaxMemoryMB: 512,
//	})
//	defer store.Close()
//
//	// Set a key with 1-hour TTL
//	err = store.Set("session:abc", sessionData, 1*time.Hour)
//
//	// Get a key
//	val, err := store.Get("session:abc")
package kvengine

import (
	"bufio"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrKeyExists    = errors.New("key already exists")
	ErrKeyNotFound  = errors.New("key not found")
	ErrKeyExpired   = errors.New("key expired")
	ErrStoreClosed  = errors.New("store is closed")
	ErrInvalidEntry = errors.New("invalid WAL entry")
	ErrCloseTimeout = errors.New("close operation timed out")
)

const (
	defaultMaxEntries    = 100000
	defaultMaxMemoryMB   = 200
	defaultFlushInterval = 100 * time.Millisecond
	defaultCleanInterval = 30 * time.Second
	defaultShardCount    = 16
	defaultCloseTimeout  = 10 * time.Second

	magicNumber uint32 = 0x4B565354 // "KVST"
	version     uint32 = 1
)

// Entry represents a key-value pair with metadata
type Entry struct {
	Key      string    `json:"key"`
	Value    []byte    `json:"value"`
	ExpireAt time.Time `json:"expire_at"`
	Size     int64     `json:"size"`
	Version  int64     `json:"version"`
	// LRU chain pointers (not serialized)
	Prev *Entry `json:"-"`
	Next *Entry `json:"-"`
}

// FormatAutoDetectMode controls whether snapshot and WAL format detection can
// override the configured serializer during load.
type FormatAutoDetectMode string

const (
	// AutoDetectEnabled detects persisted snapshot and WAL formats during load.
	AutoDetectEnabled FormatAutoDetectMode = "enabled"
	// AutoDetectDisabled forces the configured serializer during load.
	AutoDetectDisabled FormatAutoDetectMode = "disabled"
)

// Options configures the KV store
type Options struct {
	DataDir           string               `json:"data_dir"`
	MaxEntries        int                  `json:"max_entries"`
	MaxMemoryMB       int                  `json:"max_memory_mb"`
	FlushInterval     time.Duration        `json:"flush_interval"`
	CleanInterval     time.Duration        `json:"clean_interval"`
	ShardCount        int                  `json:"shard_count"`
	EnableCompression bool                 `json:"enable_compression"`
	ReadOnly          bool                 `json:"read_only"`
	CloseTimeout      time.Duration        `json:"close_timeout"`
	WALSyncMode       WALSyncMode          `json:"wal_sync_mode"`
	SerializerFormat  SerializationFormat  `json:"serializer_format"` // Serialization format (binary/json)
	AutoDetectMode    FormatAutoDetectMode `json:"auto_detect_mode"`  // Format auto-detection policy during load
}

// Shard represents a single data shard with optimized locking
type Shard struct {
	mu      sync.RWMutex
	data    map[string]*Entry
	lruHead *Entry
	lruTail *Entry
	next    map[*Entry]*Entry // LRU next pointers
	prev    map[*Entry]*Entry // LRU prev pointers
}

// MetricsObserver captures KV-specific observations without depending on the
// stable metrics root for a feature-owned contract.
type MetricsObserver interface {
	ObserveKV(ctx context.Context, operation, key string, duration time.Duration, err error, hit bool)
}

// KVStore is a simplified, high-performance key-value store
type KVStore struct {
	// Configuration
	opts       Options
	serializer Serializer // Serialization strategy

	// Sharded data
	shards    []*Shard
	shardMask uint32

	// WAL
	walFile   *os.File
	walWriter *bufio.Writer
	walMutex  sync.Mutex
	opsMu     sync.Mutex

	// Background workers
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// Statistics (atomic)
	hits        int64
	misses      int64
	evictions   int64
	entries     int64
	memoryUsage int64
	walSize     int64
	version     int64

	// State
	closed    int32
	closeOnce sync.Once
	closeErr  error

	// Unified metrics collector
	collectorMu sync.RWMutex
	collector   MetricsObserver
}

// Stats provides runtime statistics
type Stats struct {
	Entries     int64   `json:"entries"`
	Hits        int64   `json:"hits"`
	Misses      int64   `json:"misses"`
	Evictions   int64   `json:"evictions"`
	MemoryUsage int64   `json:"memory_usage"`
	WALSize     int64   `json:"wal_size"`
	HitRatio    float64 `json:"hit_ratio"`
}

// NewKVStore creates a new KV store
func NewKVStore(opts Options) (*KVStore, error) {
	if err := setDefaults(&opts); err != nil {
		return nil, err
	}

	if err := validateOptions(&opts); err != nil {
		return nil, err
	}

	// Create data directory
	if err := os.MkdirAll(opts.DataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}

	// Initialize shards
	shards := make([]*Shard, opts.ShardCount)
	for i := 0; i < opts.ShardCount; i++ {
		shards[i] = &Shard{
			data: make(map[string]*Entry),
			next: make(map[*Entry]*Entry),
			prev: make(map[*Entry]*Entry),
		}
	}

	kv := &KVStore{
		opts:       opts,
		shards:     shards,
		shardMask:  uint32(opts.ShardCount - 1),
		serializer: GetSerializer(opts.SerializerFormat),
	}

	kv.ctx, kv.cancel = context.WithCancel(context.Background())

	// Initialize WAL
	if !opts.ReadOnly {
		if err := kv.initWAL(); err != nil {
			return nil, fmt.Errorf("failed to initialize WAL: %w", err)
		}

		// Start background workers
		kv.wg.Add(2)
		go kv.walFlusher()
		go kv.cleaner()
	}

	// Load existing data
	if err := kv.loadData(); err != nil {
		_ = kv.Close()
		return nil, fmt.Errorf("failed to load data: %w", err)
	}

	return kv, nil
}

func setDefaults(opts *Options) error {
	if opts.MaxEntries == 0 {
		opts.MaxEntries = defaultMaxEntries
	}
	if opts.MaxMemoryMB == 0 {
		opts.MaxMemoryMB = defaultMaxMemoryMB
	}
	if opts.FlushInterval == 0 {
		opts.FlushInterval = defaultFlushInterval
	}
	if opts.CleanInterval == 0 {
		opts.CleanInterval = defaultCleanInterval
	}
	if opts.ShardCount == 0 {
		opts.ShardCount = defaultShardCount
	}
	if opts.CloseTimeout == 0 {
		opts.CloseTimeout = defaultCloseTimeout
	}
	if opts.WALSyncMode == "" {
		opts.WALSyncMode = WALSyncImmediate
	}
	if opts.SerializerFormat == "" {
		// Default to binary for best performance
		opts.SerializerFormat = FormatBinary
	}
	if opts.AutoDetectMode == "" {
		opts.AutoDetectMode = AutoDetectEnabled
	}
	return nil
}

func validateOptions(opts *Options) error {
	if opts.DataDir == "" {
		return errors.New("data dir is required")
	}
	if opts.MaxEntries <= 0 {
		return errors.New("max entries must be positive")
	}
	if opts.MaxMemoryMB <= 0 {
		return errors.New("max memory must be positive")
	}
	if opts.ShardCount <= 0 || (opts.ShardCount&(opts.ShardCount-1)) != 0 {
		return errors.New("shard count must be power of 2")
	}
	switch opts.WALSyncMode {
	case WALSyncImmediate, WALSyncInterval:
	default:
		return fmt.Errorf("invalid WAL sync mode: %s", opts.WALSyncMode)
	}
	switch opts.AutoDetectMode {
	case AutoDetectEnabled, AutoDetectDisabled:
	default:
		return fmt.Errorf("invalid auto-detect mode: %s", opts.AutoDetectMode)
	}
	return nil
}

// getShard returns the shard for a given key
func (kv *KVStore) getShard(key string) *Shard {
	hash := crc32.ChecksumIEEE([]byte(key))
	return kv.shards[hash&kv.shardMask]
}

// cleaner handles TTL cleanup
func (kv *KVStore) cleaner() {
	defer kv.wg.Done()

	ticker := time.NewTicker(kv.opts.CleanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			kv.cleanExpired()
		case <-kv.ctx.Done():
			return
		}
	}
}

func (kv *KVStore) cleanExpired() {
	now := time.Now()
	// Sample size per shard per cycle
	// We use a randomized sampling strategy similar to Redis to avoid O(N) scans
	const sampleSize = 20

	for _, shard := range kv.shards {
		// Loop until the percentage of expired keys is low
		for {
			var expired []string
			checkedCount := 0
			expiredCount := 0

			shard.mu.RLock()
			for key, entry := range shard.data {
				if !entry.ExpireAt.IsZero() {
					if now.After(entry.ExpireAt) {
						expired = append(expired, key)
						expiredCount++
					}
					checkedCount++
					if checkedCount >= sampleSize {
						break
					}
				}
			}
			shard.mu.RUnlock()

			if len(expired) > 0 {
				shard.mu.Lock()
				for _, key := range expired {
					if entry, exists := shard.data[key]; exists {
						// Double check expiration under write lock
						if !entry.ExpireAt.IsZero() && now.After(entry.ExpireAt) {
							kv.deleteFromShard(shard, key, entry)
							// No need to log to WAL - expired entries are handled during recovery
						}
					}
				}
				shard.mu.Unlock()
			}

			// If we didn't check enough keys (shard is small or has few items with TTL), stop
			if checkedCount < sampleSize {
				break
			}

			// If less than 25% of checked keys were expired, we assume the shard is mostly clean
			// and move to the next shard.
			if float64(expiredCount)/float64(checkedCount) < 0.25 {
				break
			}

			// Add a yield to prevent starving other goroutines during heavy cleanup
			// runtime.Gosched() could be used, or just relying on the lock release/acquire cycle
		}
	}
}

// Snapshot creates a point-in-time snapshot
func (kv *KVStore) Snapshot() error {
	if kv.isClosed() || kv.opts.ReadOnly {
		return ErrStoreClosed
	}

	kv.opsMu.Lock()
	defer kv.opsMu.Unlock()
	if kv.isClosed() {
		return ErrStoreClosed
	}

	// Use format-specific extension
	ext := ".bin"
	if kv.serializer.Format() == FormatJSON {
		ext = ".json"
	}
	snapshotPath := filepath.Join(kv.opts.DataDir, "snapshot"+ext)
	tempPath := snapshotPath + ".tmp"

	file, err := os.Create(tempPath)
	if err != nil {
		return fmt.Errorf("failed to create snapshot: %w", err)
	}
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(tempPath)
	}

	var writer io.Writer = file
	var gzWriter *gzip.Writer
	if kv.opts.EnableCompression {
		gzWriter = gzip.NewWriter(file)
		writer = gzWriter
	}

	bufWriter := bufio.NewWriter(writer)

	// Write header using serializer
	if err := kv.serializer.WriteSnapshotHeader(bufWriter); err != nil {
		cleanup()
		return err
	}

	// Write all entries
	now := time.Now()
	for _, shard := range kv.shards {
		shard.mu.RLock()
		for _, entry := range shard.data {
			if entry.ExpireAt.IsZero() || now.Before(entry.ExpireAt) {
				data, err := kv.serializer.EncodeEntry(entry)
				if err != nil {
					shard.mu.RUnlock()
					cleanup()
					return err
				}
				if _, err := bufWriter.Write(data); err != nil {
					shard.mu.RUnlock()
					cleanup()
					return err
				}
			}
		}
		shard.mu.RUnlock()
	}

	if err := bufWriter.Flush(); err != nil {
		cleanup()
		return err
	}

	if gzWriter != nil {
		if err := gzWriter.Close(); err != nil {
			cleanup()
			return err
		}
	}

	if err := file.Sync(); err != nil {
		cleanup()
		return err
	}

	if err := file.Close(); err != nil {
		_ = os.Remove(tempPath)
		return err
	}

	// Atomic replace
	if err := os.Rename(tempPath, snapshotPath); err != nil {
		return fmt.Errorf("failed to rename snapshot: %w", err)
	}
	if err := syncDataDir(kv.opts.DataDir); err != nil {
		return fmt.Errorf("sync snapshot directory: %w", err)
	}

	// Reset WAL
	if err := kv.resetWAL(); err != nil {
		return err
	}

	return nil
}

func syncDataDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return f.Sync()
}

// loadData loads data from snapshot and replays WAL
func (kv *KVStore) loadData() error {
	// Load snapshot first
	if err := kv.loadSnapshot(); err != nil {
		return err
	}

	// Replay WAL
	return kv.replayWAL()
}

func (kv *KVStore) loadSnapshot() error {
	// Try both formats
	formats := []string{".bin", ".json"}
	var file *os.File
	var err error

	for _, ext := range formats {
		snapshotPath := filepath.Join(kv.opts.DataDir, "snapshot"+ext)
		file, err = os.Open(snapshotPath)
		if err == nil {
			break
		}
	}

	if file == nil {
		return nil // No snapshot exists
	}
	defer func() { _ = file.Close() }()

	var reader io.Reader = file
	if kv.opts.EnableCompression {
		gzReader, err := gzip.NewReader(file)
		if err != nil {
			return fmt.Errorf("open compressed snapshot: %w", err)
		}
		defer func() { _ = gzReader.Close() }()
		reader = gzReader
	}

	bufReader := bufio.NewReader(reader)
	serializer := kv.serializer
	if kv.opts.AutoDetectMode == AutoDetectEnabled {
		format, err := detectSnapshotFormat(bufReader)
		if err != nil {
			return err
		}
		serializer = GetSerializer(format)
	}

	// Read header using serializer
	if err := serializer.ReadSnapshotHeader(bufReader); err != nil {
		return err
	}

	// Read entries
	for {
		entry, err := serializer.DecodeEntry(bufReader)
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		shard := kv.getShard(entry.Key)
		shard.mu.Lock()
		shard.data[entry.Key] = entry
		kv.moveToFront(shard, entry)
		shard.mu.Unlock()

		atomic.AddInt64(&kv.entries, 1)
		atomic.AddInt64(&kv.memoryUsage, entry.Size)
	}

	return nil
}

func (kv *KVStore) isClosed() bool {
	return atomic.LoadInt32(&kv.closed) != 0
}

func (kv *KVStore) Close() error {
	kv.closeOnce.Do(func() {
		kv.closeErr = kv.close()
	})
	return kv.closeErr
}

func (kv *KVStore) close() error {
	if !atomic.CompareAndSwapInt32(&kv.closed, 0, 1) {
		return nil
	}

	// Signal shutdown
	kv.cancel()

	// Wait for workers with timeout
	done := make(chan struct{})
	go func() {
		kv.wg.Wait()
		close(done)
	}()

	timer := time.NewTimer(kv.opts.CloseTimeout)
	select {
	case <-done:
		// Normal shutdown
		if !timer.Stop() {
			<-timer.C
		}
	case <-timer.C:
		kv.closeErr = ErrCloseTimeout
	}

	if err := kv.closeWAL(); err != nil {
		kv.closeErr = errors.Join(kv.closeErr, err)
	}

	return kv.closeErr
}

// Default creates a KV store with sensible defaults in the caller-provided
// data directory.
func Default(dataDir string) (*KVStore, error) {
	return NewKVStore(Options{
		DataDir:           dataDir,
		MaxEntries:        100000,
		MaxMemoryMB:       200,
		EnableCompression: true,
	})
}

// SetMetricsCollector sets the unified metrics collector
func (kv *KVStore) SetMetricsCollector(collector MetricsObserver) {
	kv.collectorMu.Lock()
	defer kv.collectorMu.Unlock()
	kv.collector = collector
}

// GetMetricsCollector returns the current metrics collector
func (kv *KVStore) GetMetricsCollector() MetricsObserver {
	kv.collectorMu.RLock()
	defer kv.collectorMu.RUnlock()
	return kv.collector
}

// recordMetrics records metrics using the unified collector
func (kv *KVStore) recordMetrics(operation, key string, duration time.Duration, err error, hit bool) {
	collector := kv.GetMetricsCollector()
	if collector != nil {
		collector.ObserveKV(context.Background(), operation, key, duration, err, hit)
	}
}
