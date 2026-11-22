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
//	import kvengine "github.com/birdordev/plumego/x/data/kvengine"
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
	"crypto/rand"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"math"
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
	expiring expiryQueue // Guarded by mu.
	mu       sync.RWMutex
	data     map[string]*Entry
	lruHead  *Entry
	lruTail  *Entry
	next     map[*Entry]*Entry // LRU next pointers
	prev     map[*Entry]*Entry // LRU prev pointers
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
	walFile    *os.File
	walWriter  *bufio.Writer
	walMutex   sync.Mutex
	walDirty   bool // Guarded by walMutex.
	opsMu      sync.Mutex
	snapshotMu sync.Mutex

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

	// Finish recovery before starting workers that mutate resident entries.
	if err := kv.loadData(); err != nil {
		return nil, fmt.Errorf("failed to load data: %w", err)
	}

	kv.ctx, kv.cancel = context.WithCancel(context.Background())

	// Initialize WAL
	if !opts.ReadOnly {
		if err := kv.initWAL(); err != nil {
			kv.cancel()
			return nil, fmt.Errorf("failed to initialize WAL: %w", err)
		}

		// Start background workers
		kv.wg.Add(2)
		go kv.walFlusher()
		go kv.cleaner()
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
	if int64(opts.MaxMemoryMB) > math.MaxInt64/(1024*1024) {
		return errors.New("max memory exceeds the representable byte range")
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
	const maxPerShard = 128
	for _, shard := range kv.shards {
		shard.mu.Lock()
		for range maxPerShard {
			if len(shard.expiring.entries) == 0 {
				break
			}
			entry := shard.expiring.entries[0]
			if !now.After(entry.ExpireAt) {
				break
			}
			kv.deleteFromShard(shard, entry.Key, entry)
		}
		shard.mu.Unlock()
	}
}

// Snapshot creates a point-in-time snapshot
func (kv *KVStore) Snapshot() error {
	if kv.isClosed() || kv.opts.ReadOnly {
		return ErrStoreClosed
	}

	kv.snapshotMu.Lock()
	defer kv.snapshotMu.Unlock()
	kv.opsMu.Lock()
	if kv.isClosed() {
		kv.opsMu.Unlock()
		return ErrStoreClosed
	}
	version := atomic.LoadInt64(&kv.version)
	entries := kv.snapshotEntries()
	kv.opsMu.Unlock()

	// Use format-specific extension
	ext := ".bin"
	if kv.serializer.Format() == FormatJSON {
		ext = ".json"
	}
	snapshotPath := filepath.Join(kv.opts.DataDir, "snapshot"+ext)
	tempPath, err := kv.writeSnapshotFile(entries)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tempPath) }()
	kv.opsMu.Lock()
	defer kv.opsMu.Unlock()
	if kv.isClosed() {
		return ErrStoreClosed
	}
	if atomic.LoadInt64(&kv.version) != version {
		if err := os.Remove(tempPath); err != nil {
			return err
		}
		tempPath, err = kv.writeSnapshotFile(kv.snapshotEntries())
		if err != nil {
			return err
		}
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

// snapshotEntries borrows immutable entries while opsMu excludes writers.
func (kv *KVStore) snapshotEntries() []*Entry {
	entries := make([]*Entry, 0)
	now := time.Now()
	for _, shard := range kv.shards {
		shard.mu.RLock()
		for _, entry := range shard.data {
			if entry.ExpireAt.IsZero() || now.Before(entry.ExpireAt) {
				entries = append(entries, entry)
			}
		}
		shard.mu.RUnlock()
	}
	return entries
}

func (kv *KVStore) writeSnapshotFile(entries []*Entry) (string, error) {
	tempPath := filepath.Join(kv.opts.DataDir, ".snapshot-"+rand.Text())
	file, err := os.OpenFile(tempPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0666)
	if err != nil {
		return "", fmt.Errorf("failed to create snapshot: %w", err)
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
		return "", err
	}

	for _, entry := range entries {
		data, err := kv.serializer.EncodeEntry(entry)
		if err != nil {
			cleanup()
			return "", err
		}
		if _, err := bufWriter.Write(data); err != nil {
			cleanup()
			return "", err
		}
	}

	if err := bufWriter.Flush(); err != nil {
		cleanup()
		return "", err
	}

	if gzWriter != nil {
		if err := gzWriter.Close(); err != nil {
			cleanup()
			return "", err
		}
	}

	if err := file.Sync(); err != nil {
		cleanup()
		return "", err
	}

	if err := file.Close(); err != nil {
		_ = os.Remove(tempPath)
		return "", err
	}

	return tempPath, nil
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

		kv.restoreEntry(entry)
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

	kv.opsMu.Lock()
	defer kv.opsMu.Unlock()
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
