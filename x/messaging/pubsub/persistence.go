package pubsub

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
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

// Persistent errors

// DurabilityLevel defines how messages are persisted
type DurabilityLevel int

const (
	// DurabilityNone - no persistence (default in-memory behavior)
	DurabilityNone DurabilityLevel = iota

	// DurabilityAsync - write to WAL asynchronously (batch flush)
	DurabilityAsync

	// DurabilitySync - fsync after each write (strong durability)
	DurabilitySync
)

// PersistenceConfig configures the persistence layer
type PersistenceConfig struct {
	// Enabled enables persistence
	Enabled bool

	// DataDir is the directory for WAL and snapshot files
	DataDir string

	// WALSegmentSize is the max size before rotating WAL (default: 64MB)
	WALSegmentSize int64

	// FlushInterval for async writes (default: 100ms)
	FlushInterval time.Duration

	// SnapshotInterval for automatic snapshots (default: 1h)
	SnapshotInterval time.Duration

	// RetentionPeriod for old WAL segments (default: 24h)
	RetentionPeriod time.Duration

	// MaxWALSegments before forcing snapshot (default: 10)
	MaxWALSegments int

	// DefaultDurability level for Publish operations
	DefaultDurability DurabilityLevel

	// CompressWAL enables gzip compression (reduces I/O but adds CPU)
	CompressWAL bool

	// OnRestore callback when restoring messages
	OnRestore func(msg Message, timestamp time.Time)
}

// DefaultPersistenceConfig returns default persistence configuration
func DefaultPersistenceConfig() PersistenceConfig {
	return PersistenceConfig{
		Enabled:           false,
		WALSegmentSize:    64 << 20, // 64MB
		FlushInterval:     100 * time.Millisecond,
		SnapshotInterval:  1 * time.Hour,
		RetentionPeriod:   24 * time.Hour,
		MaxWALSegments:    10,
		DefaultDurability: DurabilityAsync,
		CompressWAL:       false,
	}
}

// PersistentPubSub wraps InProcBroker with persistence capabilities
type PersistentPubSub struct {
	*InProcBroker

	config PersistenceConfig

	// WAL writer
	walFile     *os.File
	walWriter   *bufio.Writer
	walMu       sync.Mutex
	walSequence atomic.Uint64
	walSize     atomic.Int64

	// Snapshot
	snapshotMu sync.Mutex
	lastSnap   time.Time

	// Background workers
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	closed      atomic.Bool
	walRotating atomic.Bool // guards against concurrent rotateWAL goroutines

	// Metrics
	walWrites     atomic.Uint64
	walFlushes    atomic.Uint64
	snapshots     atomic.Uint64
	restoreCount  atomic.Uint64
	walBytesWrite atomic.Int64
	persistErrors atomic.Uint64
}

// walEntry represents a single WAL entry
type walEntry struct {
	Sequence  uint64    `json:"seq"`
	Timestamp time.Time `json:"ts"`
	Topic     string    `json:"topic"`
	Message   Message   `json:"msg"`
	CRC       uint32    `json:"crc"`
}

// snapshotData represents a complete system snapshot
type snapshotData struct {
	Version       int                     `json:"version"`
	Timestamp     time.Time               `json:"timestamp"`
	WALSequence   uint64                  `json:"wal_sequence"`
	Messages      []persistedMessage      `json:"messages"`
	Subscriptions []persistedSubscription `json:"subscriptions"`
}

type persistedMessage struct {
	Topic     string    `json:"topic"`
	Message   Message   `json:"message"`
	Timestamp time.Time `json:"timestamp"`
}

type persistedSubscription struct {
	Topic   string     `json:"topic"`
	Opts    SubOptions `json:"opts"`
	Pattern bool       `json:"pattern"`
}

// NewPersistent creates a new persistent pubsub instance
func NewPersistent(config PersistenceConfig, opts ...Option) (*PersistentPubSub, error) {
	if !config.Enabled {
		return nil, ErrPersistenceDisabled
	}

	if config.DataDir == "" {
		return nil, errors.New("persistence data directory is required")
	}

	// Apply defaults
	if config.WALSegmentSize == 0 {
		config.WALSegmentSize = 64 << 20
	}
	if config.FlushInterval == 0 {
		config.FlushInterval = 100 * time.Millisecond
	}
	if config.SnapshotInterval == 0 {
		config.SnapshotInterval = 1 * time.Hour
	}
	if config.RetentionPeriod == 0 {
		config.RetentionPeriod = 24 * time.Hour
	}
	if config.MaxWALSegments == 0 {
		config.MaxWALSegments = 10
	}

	// Create data directory
	if err := os.MkdirAll(config.DataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}

	// Create base pubsub
	ps := New(opts...)

	ctx, cancel := newBackgroundLifecycle()

	pps := &PersistentPubSub{
		InProcBroker: ps,
		config:       config,
		ctx:          ctx,
		cancel:       cancel,
		lastSnap:     time.Now(),
	}

	// Initialize WAL
	if err := pps.initWAL(); err != nil {
		cancel()
		_ = ps.Close()
		return nil, fmt.Errorf("failed to initialize WAL: %w", err)
	}

	// Restore from WAL and snapshots
	if err := pps.restore(); err != nil {
		cancel()
		_ = ps.Close()
		return nil, fmt.Errorf("failed to restore: %w", err)
	}

	// Start background workers
	pps.startBackgroundWorkers()

	return pps, nil
}

// initWAL initializes the Write-Ahead Log
func (pps *PersistentPubSub) initWAL() error {
	walPath := pps.currentWALPath()

	// Open or create WAL file
	f, err := os.OpenFile(walPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open WAL: %w", err)
	}

	// Get current size
	stat, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("failed to stat WAL: %w", err)
	}

	pps.walFile = f
	pps.walWriter = bufio.NewWriterSize(f, 256*1024) // 256KB buffer
	pps.walSize.Store(stat.Size())

	return nil
}

// currentWALPath returns the current WAL file path
func (pps *PersistentPubSub) currentWALPath() string {
	seq := pps.walSequence.Load()
	return filepath.Join(pps.config.DataDir, fmt.Sprintf("wal-%016d.log", seq))
}

// restore restores state from snapshots and WAL
func (pps *PersistentPubSub) restore() error {
	// Find latest snapshot
	snapshot, err := pps.findLatestSnapshot()
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to find snapshot: %w", err)
	}

	var startSeq uint64
	if snapshot != nil {
		// Restore from snapshot
		if err := pps.restoreFromSnapshot(snapshot); err != nil {
			return fmt.Errorf("failed to restore snapshot: %w", err)
		}
		startSeq = snapshot.WALSequence + 1
	}

	// Replay WAL entries
	if err := pps.replayWAL(startSeq); err != nil {
		return fmt.Errorf("failed to replay WAL: %w", err)
	}

	return nil
}

// findLatestSnapshot finds the most recent snapshot file
func (pps *PersistentPubSub) findLatestSnapshot() (*snapshotData, error) {
	files, err := filepath.Glob(filepath.Join(pps.config.DataDir, "snapshot-*.json"))
	if err != nil {
		return nil, err
	}

	if len(files) == 0 {
		return nil, os.ErrNotExist
	}

	// Find newest snapshot
	var newest string
	var newestTime time.Time

	for _, f := range files {
		stat, err := os.Stat(f)
		if err != nil {
			continue
		}
		if stat.ModTime().After(newestTime) {
			newest = f
			newestTime = stat.ModTime()
		}
	}

	if newest == "" {
		return nil, os.ErrNotExist
	}

	// Load snapshot
	data, err := os.ReadFile(newest)
	if err != nil {
		return nil, err
	}

	var snap snapshotData
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, err
	}

	return &snap, nil
}

// restoreFromSnapshot restores state from a snapshot
func (pps *PersistentPubSub) restoreFromSnapshot(snap *snapshotData) error {
	// Restore messages
	for _, pm := range snap.Messages {
		if pps.config.OnRestore != nil {
			pps.config.OnRestore(pm.Message, pm.Timestamp)
		}

		// Republish to in-memory system
		_ = pps.InProcBroker.Publish(pm.Topic, pm.Message)
		pps.restoreCount.Add(1)
	}

	// Update WAL sequence
	if snap.WALSequence > pps.walSequence.Load() {
		pps.walSequence.Store(snap.WALSequence)
	}

	return nil
}

// replayWAL replays WAL entries from the specified sequence
func (pps *PersistentPubSub) replayWAL(startSeq uint64) error {
	files, err := filepath.Glob(filepath.Join(pps.config.DataDir, "wal-*.log"))
	if err != nil {
		return err
	}

	if len(files) == 0 {
		return nil
	}

	for _, walPath := range files {
		if err := pps.replayWALFile(walPath, startSeq); err != nil {
			return fmt.Errorf("failed to replay %s: %w", walPath, err)
		}
	}

	return nil
}

// replayWALFile replays a single WAL file
func (pps *PersistentPubSub) replayWALFile(path string, startSeq uint64) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	reader := bufio.NewReader(f)

	for {
		entry, err := pps.readWALEntry(reader)
		if err == io.EOF {
			break
		}
		if err != nil {
			// Log corruption but continue
			continue
		}

		if entry.Sequence < startSeq {
			continue
		}

		// Verify CRC
		if !pps.verifyWALEntry(entry) {
			continue
		}

		// Restore message
		if pps.config.OnRestore != nil {
			pps.config.OnRestore(entry.Message, entry.Timestamp)
		}

		_ = pps.InProcBroker.Publish(entry.Topic, entry.Message)
		pps.restoreCount.Add(1)

		// Update sequence
		if entry.Sequence > pps.walSequence.Load() {
			pps.walSequence.Store(entry.Sequence)
		}
	}

	return nil
}

// readWALEntry reads a single WAL entry
func (pps *PersistentPubSub) readWALEntry(r *bufio.Reader) (*walEntry, error) {
	// Read length prefix
	var length uint32
	if err := binary.Read(r, binary.LittleEndian, &length); err != nil {
		return nil, err
	}

	if length > 10<<20 { // Max 10MB per entry
		return nil, ErrInvalidWALEntry
	}

	// Read entry data
	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, err
	}

	// Unmarshal
	var entry walEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, err
	}

	return &entry, nil
}

// verifyWALEntry verifies the CRC of a WAL entry
func (pps *PersistentPubSub) verifyWALEntry(entry *walEntry) bool {
	// Compute CRC without the CRC field
	data, _ := json.Marshal(struct {
		Sequence  uint64
		Timestamp time.Time
		Topic     string
		Message   Message
	}{
		Sequence:  entry.Sequence,
		Timestamp: entry.Timestamp,
		Topic:     entry.Topic,
		Message:   entry.Message,
	})

	computed := crc32.ChecksumIEEE(data)
	return computed == entry.CRC
}

// PublishPersistent publishes a message with specific durability
func (pps *PersistentPubSub) PublishPersistent(topic string, msg Message, durability DurabilityLevel) error {
	if pps.closed.Load() {
		return ErrPersistenceClosed
	}

	// Write to WAL first
	if err := pps.writeWAL(topic, msg, durability); err != nil {
		pps.persistErrors.Add(1)
		return fmt.Errorf("WAL write failed: %w", err)
	}

	// Then publish in-memory
	return pps.InProcBroker.Publish(topic, msg)
}

// Publish overrides to use default durability
func (pps *PersistentPubSub) Publish(topic string, msg Message) error {
	return pps.PublishPersistent(topic, msg, pps.config.DefaultDurability)
}

// writeWAL writes a message to the WAL
func (pps *PersistentPubSub) writeWAL(topic string, msg Message, durability DurabilityLevel) error {
	if durability == DurabilityNone {
		return nil
	}

	pps.walMu.Lock()
	defer pps.walMu.Unlock()

	// Create entry
	seq := pps.walSequence.Add(1)
	entry := walEntry{
		Sequence:  seq,
		Timestamp: time.Now(),
		Topic:     topic,
		Message:   msg,
	}

	// Compute CRC
	data, _ := json.Marshal(struct {
		Sequence  uint64
		Timestamp time.Time
		Topic     string
		Message   Message
	}{
		Sequence:  entry.Sequence,
		Timestamp: entry.Timestamp,
		Topic:     entry.Topic,
		Message:   entry.Message,
	})
	entry.CRC = crc32.ChecksumIEEE(data)

	// Marshal entry
	entryData, err := json.Marshal(entry)
	if err != nil {
		return err
	}

	// Write length prefix
	length := uint32(len(entryData))
	if err := binary.Write(pps.walWriter, binary.LittleEndian, length); err != nil {
		return err
	}

	// Write entry
	if _, err := pps.walWriter.Write(entryData); err != nil {
		return err
	}

	pps.walWrites.Add(1)
	pps.walBytesWrite.Add(int64(4 + len(entryData)))

	// Handle durability
	switch durability {
	case DurabilitySync:
		if err := pps.walWriter.Flush(); err != nil {
			return err
		}
		if err := pps.walFile.Sync(); err != nil {
			return err
		}
		pps.walFlushes.Add(1)

	case DurabilityAsync:
		// Will be flushed by background worker
	}

	// Check if rotation needed; use CAS to ensure at most one goroutine rotates.
	pps.walSize.Add(int64(4 + len(entryData)))
	if pps.walSize.Load() >= pps.config.WALSegmentSize {
		if pps.walRotating.CompareAndSwap(false, true) {
			go func() {
				pps.rotateWAL()
				pps.walRotating.Store(false)
			}()
		}
	}

	return nil
}

// rotateWAL rotates to a new WAL file
func (pps *PersistentPubSub) rotateWAL() {
	pps.walMu.Lock()
	defer pps.walMu.Unlock()

	// Flush current WAL
	if pps.walWriter != nil {
		_ = pps.walWriter.Flush()
	}

	if pps.walFile != nil {
		_ = pps.walFile.Close()
	}

	// Create new WAL
	pps.walSequence.Add(1)
	_ = pps.initWAL()

	// Trigger snapshot if too many segments
	go pps.maybeSnapshot()
}

// Snapshot creates a point-in-time snapshot
func (pps *PersistentPubSub) Snapshot() error {
	if pps.closed.Load() {
		return ErrPersistenceClosed
	}

	pps.snapshotMu.Lock()
	defer pps.snapshotMu.Unlock()

	// Create snapshot data
	snap := snapshotData{
		Version:     1,
		Timestamp:   time.Now(),
		WALSequence: pps.walSequence.Load(),
		Messages:    []persistedMessage{},
	}

	// Capture current state (limited to history buffer)
	// Note: Full message capture would require adding to config

	// Write snapshot
	snapPath := filepath.Join(
		pps.config.DataDir,
		fmt.Sprintf("snapshot-%d.json", snap.Timestamp.Unix()),
	)

	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}

	if err := os.WriteFile(snapPath, data, 0644); err != nil {
		return err
	}

	pps.snapshots.Add(1)
	pps.lastSnap = time.Now()

	// Cleanup old snapshots and WAL
	go pps.cleanup()

	return nil
}

// maybeSnapshot checks if snapshot is needed
func (pps *PersistentPubSub) maybeSnapshot() {
	if time.Since(pps.lastSnap) < pps.config.SnapshotInterval {
		return
	}

	_ = pps.Snapshot()
}

// cleanup removes old snapshots and WAL segments
func (pps *PersistentPubSub) cleanup() {
	cutoff := time.Now().Add(-pps.config.RetentionPeriod)

	// Cleanup old snapshots (keep at least 1)
	snaps, _ := filepath.Glob(filepath.Join(pps.config.DataDir, "snapshot-*.json"))
	if len(snaps) > 1 {
		for _, snap := range snaps[:len(snaps)-1] {
			if stat, err := os.Stat(snap); err == nil {
				if stat.ModTime().Before(cutoff) {
					_ = os.Remove(snap)
				}
			}
		}
	}

	// Cleanup old WAL segments
	wals, _ := filepath.Glob(filepath.Join(pps.config.DataDir, "wal-*.log"))
	for _, wal := range wals {
		if stat, err := os.Stat(wal); err == nil {
			if stat.ModTime().Before(cutoff) {
				_ = os.Remove(wal)
			}
		}
	}
}

// startBackgroundWorkers starts periodic tasks
func (pps *PersistentPubSub) startBackgroundWorkers() {
	// Flush worker (for async writes)
	pps.wg.Add(1)
	go func() {
		defer pps.wg.Done()
		ticker := time.NewTicker(pps.config.FlushInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				pps.walMu.Lock()
				if pps.walWriter != nil {
					_ = pps.walWriter.Flush()
					pps.walFlushes.Add(1)
				}
				pps.walMu.Unlock()

			case <-pps.ctx.Done():
				return
			}
		}
	}()

	// Snapshot worker
	pps.wg.Add(1)
	go func() {
		defer pps.wg.Done()
		ticker := time.NewTicker(pps.config.SnapshotInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				_ = pps.Snapshot()

			case <-pps.ctx.Done():
				return
			}
		}
	}()
}

// Close closes the persistent pubsub
func (pps *PersistentPubSub) Close() error {
	if pps.closed.Swap(true) {
		return nil
	}

	// Stop background workers
	stopBackground(pps.cancel, &pps.wg)

	// Final flush and snapshot
	pps.walMu.Lock()
	if pps.walWriter != nil {
		_ = pps.walWriter.Flush()
	}
	if pps.walFile != nil {
		_ = pps.walFile.Sync()
		_ = pps.walFile.Close()
	}
	pps.walMu.Unlock()

	_ = pps.Snapshot()

	// Close base pubsub
	return pps.InProcBroker.Close()
}

// Stats returns persistence statistics
func (pps *PersistentPubSub) PersistenceStats() PersistenceStats {
	return PersistenceStats{
		WALWrites:      pps.walWrites.Load(),
		WALFlushes:     pps.walFlushes.Load(),
		WALBytesWrite:  pps.walBytesWrite.Load(),
		Snapshots:      pps.snapshots.Load(),
		RestoreCount:   pps.restoreCount.Load(),
		PersistErrors:  pps.persistErrors.Load(),
		CurrentWALSize: pps.walSize.Load(),
	}
}

// PersistenceStats holds persistence metrics
type PersistenceStats struct {
	WALWrites      uint64
	WALFlushes     uint64
	WALBytesWrite  int64
	Snapshots      uint64
	RestoreCount   uint64
	PersistErrors  uint64
	CurrentWALSize int64
}
