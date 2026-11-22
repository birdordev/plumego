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
	"sort"
	"strconv"
	"strings"
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

	// RetentionPeriod for old snapshot markers (default: 24h). WAL segments
	// remain available for replay while snapshots do not capture messages.
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
	walSynced   uint64 // Last sequence covered by a successful file sync.
	walSequence atomic.Uint64
	walSize     atomic.Int64
	rotationErr error // protected by walMu
	walFlushErr error // first asynchronous flush failure, protected by walMu
	walUnusable bool  // protected by walMu

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

	// Restore before opening the WAL so new records follow existing segments.
	if err := pps.restore(); err != nil {
		cancel()
		_ = ps.Close()
		return nil, fmt.Errorf("failed to restore: %w", err)
	}

	// Initialize WAL
	if err := pps.initWAL(); err != nil {
		cancel()
		_ = ps.Close()
		return nil, fmt.Errorf("failed to initialize WAL: %w", err)
	}

	// Start background workers
	pps.startBackgroundWorkers()

	return pps, nil
}

// initWAL initializes the Write-Ahead Log.
func (pps *PersistentPubSub) initWAL() error {
	file, size, err := pps.openWAL(pps.walSequence.Load())
	if err != nil {
		return err
	}
	pps.setWAL(file, size)
	return nil
}

func (pps *PersistentPubSub) openWAL(seq uint64) (*os.File, int64, error) {
	path := filepath.Join(pps.config.DataDir, fmt.Sprintf("wal-%016d.log", seq))
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, 0, fmt.Errorf("open WAL segment: %w", err)
	}
	stat, err := file.Stat()
	if err != nil {
		return nil, 0, errors.Join(fmt.Errorf("stat WAL segment: %w", err), file.Close())
	}
	return file, stat.Size(), nil
}

// setWAL installs a prepared segment during initialization or under walMu.
func (pps *PersistentPubSub) setWAL(file *os.File, size int64) {
	pps.walFile = file
	pps.walWriter = bufio.NewWriterSize(file, 256*1024)
	pps.walSize.Store(size)
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

// findLatestSnapshot finds the most recent snapshot that contains messages.
func (pps *PersistentPubSub) findLatestSnapshot() (*snapshotData, error) {
	files, err := filepath.Glob(filepath.Join(pps.config.DataDir, "snapshot-*.json"))
	if err != nil {
		return nil, err
	}

	if len(files) == 0 {
		return nil, os.ErrNotExist
	}

	type candidate struct {
		path string
		mod  time.Time
	}
	snapshots := make([]candidate, 0, len(files))
	for _, path := range files {
		if strings.HasSuffix(path, "-marker.json") {
			continue
		}
		if stat, err := os.Stat(path); err == nil {
			snapshots = append(snapshots, candidate{path, stat.ModTime()})
		}
	}
	sort.SliceStable(snapshots, func(i, j int) bool {
		return snapshots[i].mod.After(snapshots[j].mod)
	})
	for _, candidate := range snapshots {
		snap, err := readSnapshot(candidate.path)
		if err != nil {
			return nil, err
		}
		if len(snap.Messages) > 0 {
			return snap, nil
		}
	}
	return nil, os.ErrNotExist
}

func readSnapshot(path string) (*snapshotData, error) {
	data, err := os.ReadFile(path)
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
		if startSeq > 0 && sealedWALBefore(walPath, startSeq) {
			continue
		}
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

// PublishPersistent publishes a message with specific durability.
// If WAL rotation cannot finalize the old segment, subsequent persistent
// writes fail with that error. If only the new segment cannot be opened,
// writes can continue in the old segment and retry rotation. A failed
// asynchronous flush also prevents subsequent persistent writes.
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

	seq, err := pps.appendWAL(topic, msg)
	if err != nil || durability != DurabilitySync {
		return err
	}
	// Other publishers can append before this sync, sharing the same commit.
	return pps.syncThrough(seq)
}

func (pps *PersistentPubSub) appendWAL(topic string, msg Message) (uint64, error) {
	pps.walMu.Lock()
	defer pps.walMu.Unlock()
	if pps.closed.Load() {
		return 0, ErrPersistenceClosed
	}

	if pps.walFlushErr != nil {
		return 0, pps.walFlushErr
	}
	if pps.walUnusable {
		return 0, pps.rotationErr
	}

	// Create entry
	seq := pps.walSequence.Add(1)
	entry := walEntry{
		Sequence:  seq,
		Timestamp: time.Now(),
		Topic:     topic,
		Message:   msg,
	}

	entryData, err := encodeWALRecord(entry)
	if err != nil {
		return 0, err
	}

	var prefix [4]byte
	binary.LittleEndian.PutUint32(prefix[:], uint32(len(entryData)))
	if _, err := pps.walWriter.Write(prefix[:]); err != nil {
		return 0, err
	}

	// Write entry
	if _, err := pps.walWriter.Write(entryData); err != nil {
		return 0, err
	}

	pps.walWrites.Add(1)
	pps.walBytesWrite.Add(int64(4 + len(entryData)))

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

	return seq, nil
}

// encodeWALRecord reuses the encoded message, timestamp and topic for
// the historical CRC envelope and the persisted record.
func encodeWALRecord(entry walEntry) ([]byte, error) {
	message, err := json.Marshal(entry.Message)
	if err != nil {
		return nil, err
	}
	stamp, err := entry.Timestamp.MarshalJSON()
	if err != nil {
		return nil, err
	}
	topic, err := json.Marshal(entry.Topic)
	if err != nil {
		return nil, err
	}

	prefix := make([]byte, 0, len(topic)+len(stamp)+80)
	prefix = append(prefix, `{"Sequence":`...)
	prefix = strconv.AppendUint(prefix, entry.Sequence, 10)
	prefix = append(prefix, `,"Timestamp":`...)
	prefix = append(prefix, stamp...)
	prefix = append(prefix, `,"Topic":`...)
	prefix = append(prefix, topic...)
	prefix = append(prefix, `,"Message":`...)
	crc := crc32.Update(crc32.ChecksumIEEE(prefix), crc32.IEEETable, message)
	crc = crc32.Update(crc, crc32.IEEETable, []byte{'}'})

	record := make([]byte, 0, len(topic)+len(stamp)+len(message)+80)
	record = append(record, `{"seq":`...)
	record = strconv.AppendUint(record, entry.Sequence, 10)
	record = append(record, `,"ts":`...)
	record = append(record, stamp...)
	record = append(record, `,"topic":`...)
	record = append(record, topic...)
	record = append(record, `,"msg":`...)
	record = append(record, message...)
	record = append(record, `,"crc":`...)
	record = strconv.AppendUint(record, uint64(crc), 10)
	return append(record, '}'), nil
}

func (pps *PersistentPubSub) syncThrough(sequence uint64) error {
	pps.walMu.Lock()
	defer pps.walMu.Unlock()
	if sequence <= pps.walSynced {
		return nil
	}
	if pps.walFlushErr != nil {
		return pps.walFlushErr
	}
	if pps.walUnusable {
		return pps.rotationErr
	}
	if pps.walWriter == nil {
		return ErrPersistenceClosed
	}
	if err := pps.walWriter.Flush(); err != nil {
		return err
	}
	if err := pps.walFile.Sync(); err != nil {
		return err
	}
	pps.walSynced = pps.walSequence.Load()
	pps.walFlushes.Add(1)
	return nil
}

// rotateWAL publishes a new segment only after the old one is finalized.
func (pps *PersistentPubSub) rotateWAL() {
	pps.walMu.Lock()
	if pps.closed.Load() || pps.walUnusable || pps.walFlushErr != nil {
		pps.walMu.Unlock()
		return
	}
	pps.rotationErr = pps.rotateWALLocked()
	err := pps.rotationErr
	pps.walMu.Unlock()
	if err != nil {
		pps.persistErrors.Add(1)
		return
	}
	pps.maybeSnapshot()
}

func (pps *PersistentPubSub) rotateWALLocked() error {
	if err := pps.walWriter.Flush(); err != nil {
		pps.walUnusable = true
		return fmt.Errorf("rotate WAL: flush old segment: %w", err)
	}
	if err := pps.walFile.Sync(); err != nil {
		pps.walUnusable = true
		return fmt.Errorf("rotate WAL: sync old segment: %w", err)
	}

	pps.walSynced = pps.walSequence.Load()
	next := pps.walSynced + 1
	file, size, err := pps.openWAL(next)
	if err != nil {
		return fmt.Errorf("rotate WAL: %w", err)
	}
	sealedPath := pps.walFile.Name()
	sealedInfo, statErr := pps.walFile.Stat()
	if err := pps.walFile.Close(); err != nil {
		pps.walUnusable = true
		return errors.Join(fmt.Errorf("rotate WAL: close old segment: %w", err), file.Close())
	}
	if statErr == nil {
		writeWALSeal(sealedPath, sealedInfo, pps.walSynced)
	}
	pps.walSequence.Store(next)
	pps.setWAL(file, size)
	return nil
}

// Snapshot records a checkpoint marker. It does not compact the WAL.
// Recovery replays the retained WAL unless a historical snapshot contains
// messages. Records already deleted from disk cannot be restored.
func (pps *PersistentPubSub) Snapshot() error {
	pps.snapshotMu.Lock()
	defer pps.snapshotMu.Unlock()
	return pps.snapshotLocked()
}

func (pps *PersistentPubSub) snapshotLocked() error {
	if pps.closed.Load() {
		return ErrPersistenceClosed
	}

	// Create snapshot data
	snap := snapshotData{
		Version:   1,
		Timestamp: time.Now(),
		Messages:  []persistedMessage{},
	}

	// Write snapshot
	snapPath := filepath.Join(
		pps.config.DataDir,
		fmt.Sprintf("snapshot-%d-marker.json", snap.Timestamp.UnixNano()),
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

	pps.cleanup()

	return nil
}

// maybeSnapshot checks if snapshot is needed
func (pps *PersistentPubSub) maybeSnapshot() {
	pps.snapshotMu.Lock()
	defer pps.snapshotMu.Unlock()
	if time.Since(pps.lastSnap) < pps.config.SnapshotInterval {
		return
	}

	_ = pps.snapshotLocked()
}

// cleanup expires old markers and retains snapshots with recovery data.
func (pps *PersistentPubSub) cleanup() {
	cutoff := time.Now().Add(-pps.config.RetentionPeriod)
	snaps, _ := filepath.Glob(filepath.Join(pps.config.DataDir, "snapshot-*.json"))
	for _, path := range snaps[:max(0, len(snaps)-1)] {
		stat, err := os.Stat(path)
		if err != nil || !stat.ModTime().Before(cutoff) {
			continue
		}
		if snap, err := readSnapshot(path); err == nil && len(snap.Messages) == 0 {
			_ = os.Remove(path)
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
				pps.flushAsyncWAL()

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

func (pps *PersistentPubSub) flushAsyncWAL() {
	pps.walMu.Lock()
	defer pps.walMu.Unlock()
	if pps.closed.Load() || pps.walWriter == nil || pps.walUnusable || pps.walFlushErr != nil {
		return
	}
	if err := pps.walWriter.Flush(); err != nil {
		pps.walFlushErr = fmt.Errorf("flush WAL asynchronously: %w", err)
		pps.persistErrors.Add(1)
		return
	}
	pps.walFlushes.Add(1)
}

// Close stops the workers, flushes and syncs the WAL, and closes the broker.
// It reports unresolved rotation, asynchronous flush, and finalization errors.
// Once shutdown has started, subsequent calls return nil without repeating cleanup.
func (pps *PersistentPubSub) Close() error {
	if pps.closed.Swap(true) {
		return nil
	}

	stopBackground(pps.cancel, &pps.wg)

	var errs []error
	// Finish any in-progress snapshot before finalizing the WAL.
	pps.snapshotMu.Lock()
	pps.walMu.Lock()
	if pps.rotationErr != nil {
		errs = append(errs, pps.rotationErr)
	}
	if pps.walFlushErr != nil {
		errs = append(errs, pps.walFlushErr)
	} else if pps.walWriter != nil {
		if err := pps.walWriter.Flush(); err != nil {
			errs = append(errs, fmt.Errorf("flush WAL: %w", err))
		}
	}
	pps.walWriter = nil
	if pps.walFile != nil {
		if err := pps.walFile.Sync(); err != nil {
			errs = append(errs, fmt.Errorf("sync WAL: %w", err))
		} else if pps.walFlushErr == nil && len(errs) == 0 {
			pps.walSynced = pps.walSequence.Load()
		}
		if err := pps.walFile.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close WAL: %w", err))
		}
		pps.walFile = nil
	}
	pps.walMu.Unlock()
	pps.snapshotMu.Unlock()

	return errors.Join(append(errs, pps.InProcBroker.Close())...)
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
	WALFlushes     uint64 // Successful flushes counted by the write and background paths.
	WALBytesWrite  int64
	Snapshots      uint64
	RestoreCount   uint64
	PersistErrors  uint64
	CurrentWALSize int64
}
