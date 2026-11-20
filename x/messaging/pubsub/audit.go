package pubsub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Audit errors

// AuditOperation represents the type of operation being audited
type AuditOperation string

const (
	OpPublish          AuditOperation = "publish"
	OpSubscribe        AuditOperation = "subscribe"
	OpUnsubscribe      AuditOperation = "unsubscribe"
	OpPatternSubscribe AuditOperation = "pattern_subscribe"
	OpMessageDelivered AuditOperation = "message_delivered"
	OpMessageDropped   AuditOperation = "message_dropped"
	OpTopicCreated     AuditOperation = "topic_created"
	OpTopicDeleted     AuditOperation = "topic_deleted"
	OpClose            AuditOperation = "close"
	OpError            AuditOperation = "error"
	OpConfigChange     AuditOperation = "config_change"
	OpRetry            AuditOperation = "retry"
	OpArchive          AuditOperation = "archive"
	OpReplay           AuditOperation = "replay"
)

// AuditConfig configures the audit logging system
type AuditConfig struct {
	// Enabled enables audit logging
	Enabled bool

	// AsyncMode writes audit logs asynchronously
	AsyncMode bool

	// BufferSize for async writes. Zero uses an unbuffered queue; negative values are invalid in async mode.
	BufferSize int

	// IncludeMessageData is reserved for compatibility. Log always copies the
	// operation data explicitly supplied by the caller.
	IncludeMessageData bool

	// IncludeStackTrace is reserved for compatibility. Log does not collect
	// stack traces.
	IncludeStackTrace bool

	// LogFile path for persistent audit logs (optional)
	LogFile string

	// MaxLogSize maximum size of log file before rotation (bytes)
	MaxLogSize int64

	// MaxLogFiles maximum number of rotated log files to keep
	MaxLogFiles int

	// VerifyIntegrity enables chain hashing for tamper detection
	VerifyIntegrity bool

	// Operations to audit (empty = audit all)
	Operations []AuditOperation

	// Topics to audit (empty = audit all)
	Topics []string
}

// DefaultAuditConfig returns default audit configuration
func DefaultAuditConfig() AuditConfig {
	return AuditConfig{
		Enabled:            true,
		AsyncMode:          true,
		BufferSize:         1000,
		IncludeMessageData: false,
		IncludeStackTrace:  false,
		LogFile:            "",
		MaxLogSize:         100 * 1024 * 1024, // 100 MB
		MaxLogFiles:        10,
		VerifyIntegrity:    true,
		Operations:         nil,
		Topics:             nil,
	}
}

// AuditEntry represents a single audit log entry
type AuditEntry struct {
	// Sequence number (monotonically increasing)
	Sequence uint64

	// Timestamp of the operation
	Timestamp time.Time

	// Operation type
	Operation AuditOperation

	// Topic related to the operation
	Topic string

	// Actor (subscriber ID, client ID, etc.)
	Actor string

	// Source (IP, hostname, etc.)
	Source string

	// MessageID if applicable
	MessageID string

	// Data contains operation-specific details
	Data map[string]any

	// Success indicates if the operation succeeded
	Success bool

	// Error message if operation failed
	Error string

	// PreviousHash for integrity chain (SHA-256 of previous entry)
	PreviousHash string

	// Hash of this entry (SHA-256)
	Hash string
}

// AuditQuery specifies filters for querying audit logs
type AuditQuery struct {
	// Time range
	StartTime time.Time
	EndTime   time.Time

	// Operation filter
	Operations []AuditOperation

	// Topic filter
	Topics []string

	// Actor filter
	Actors []string

	// Success filter (nil = all, true = success only, false = errors only)
	Success *bool

	// Limit and offset
	Limit  int
	Offset int

	// Sort order
	Ascending bool
}

// AuditStats provides audit log statistics
type AuditStats struct {
	TotalEntries   atomic.Uint64
	SuccessfulOps  atomic.Uint64
	FailedOps      atomic.Uint64
	BytesWritten   atomic.Uint64
	CurrentLogSize int64
	LogRotations   atomic.Uint64
	VerifyFailures atomic.Uint64
}

// AuditLogger manages immutable audit logging
type AuditLogger struct {
	config AuditConfig

	// In-memory log (circular buffer)
	entries        []*AuditEntry
	entriesMu      sync.RWMutex
	retainedAnchor string // Predecessor hash of the first retained entry.
	maxInMemory    int
	nextSequence   uint64

	// Last entry hash for chaining
	lastHash string // guarded by entriesMu

	// File logging
	logFile   *os.File
	logFileMu sync.Mutex

	// Async writing
	asyncQueue chan *AuditEntry
	asyncErr   error // Written by asyncWriter and read after wg.Wait.

	// Statistics
	stats AuditStats

	// Control
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	closed      atomic.Bool
	admissionMu sync.Mutex
	activeLogs  sync.WaitGroup
}

// NewAuditLogger creates a new audit logger with its own filter slices.
func NewAuditLogger(config AuditConfig) (*AuditLogger, error) {
	if !config.Enabled {
		return nil, errors.New("audit logging is not enabled")
	}
	if config.AsyncMode && config.BufferSize < 0 {
		return nil, fmt.Errorf("audit buffer size must be nonnegative: %d", config.BufferSize)
	}

	config.Operations = slices.Clone(config.Operations)
	config.Topics = slices.Clone(config.Topics)
	ctx, cancel := context.WithCancel(context.Background())

	al := &AuditLogger{
		config:      config,
		entries:     make([]*AuditEntry, 0),
		maxInMemory: 10000,
		ctx:         ctx,
		cancel:      cancel,
	}

	// Open log file if configured
	if config.LogFile != "" {
		if err := al.openLogFile(); err != nil {
			cancel()
			return nil, err
		}
	}

	// Start async writer if enabled
	if config.AsyncMode {
		al.asyncQueue = make(chan *AuditEntry, config.BufferSize)
		al.wg.Add(1)
		go al.asyncWriter()
	}

	return al, nil
}

// openLogFile opens or creates the audit log file
func (al *AuditLogger) openLogFile() error {
	// Create directory if needed
	dir := filepath.Dir(al.config.LogFile)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create audit log directory: %w", err)
		}
	}

	// Open file in append mode
	file, err := os.OpenFile(al.config.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("failed to open audit log file: %w", err)
	}

	al.logFile = file

	// Get file size
	if info, err := file.Stat(); err == nil {
		al.stats.CurrentLogSize = info.Size()
	}

	return nil
}

// Log records an audit entry and copies ordinary containers in data within
// the clone depth limit. Callers must omit sensitive values from data and
// errors. File encoding invokes custom marshalers outside the file lock.
// Sequence numbers follow in-memory append order.
// With asynchronous writing, success can mean an entry was queued. Close
// waits for accepted calls and reports background write failures. A synchronous
// file rotation failure is returned to the caller; later writes can retry.
func (al *AuditLogger) Log(operation AuditOperation, topic, actor, source string, data map[string]any, success bool, err error) error {
	if al.closed.Load() {
		return ErrAuditClosed
	}

	// Check if we should audit this operation
	if !al.shouldAudit(operation, topic) {
		return nil
	}

	// Create entry
	entry := &AuditEntry{
		Timestamp: time.Now().UTC(),
		Operation: operation,
		Topic:     topic,
		Actor:     actor,
		Source:    source,
		Data:      cloneAuditData(data),
		Success:   success,
	}

	if err != nil {
		entry.Error = err.Error()
	}

	var hashData string
	if al.config.VerifyIntegrity {
		hashData = fmt.Sprint(entry.Data)
	}

	al.admissionMu.Lock()
	if al.closed.Load() {
		al.admissionMu.Unlock()
		return ErrAuditClosed
	}
	al.activeLogs.Add(1)
	al.admissionMu.Unlock()
	defer al.activeLogs.Done()

	al.storeInMemory(entry, hashData)

	// Update stats
	al.stats.TotalEntries.Add(1)
	if success {
		al.stats.SuccessfulOps.Add(1)
	} else {
		al.stats.FailedOps.Add(1)
	}

	// Write to file
	if al.config.AsyncMode {
		select {
		case al.asyncQueue <- entry:
		default:
			// Queue full, write synchronously
			return al.writeToFile(entry)
		}
		return nil
	}
	return al.writeToFile(entry)
}

// shouldAudit checks if an operation should be audited
func (al *AuditLogger) shouldAudit(op AuditOperation, topic string) bool {
	// Check operation filter
	if len(al.config.Operations) > 0 {
		found := false
		for _, allowedOp := range al.config.Operations {
			if op == allowedOp {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// Check topic filter
	if len(al.config.Topics) > 0 && topic != "" {
		found := false
		for _, allowedTopic := range al.config.Topics {
			if topic == allowedTopic {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	return true
}

// calculateHash computes SHA-256 hash of an entry
func (al *AuditLogger) calculateHash(entry *AuditEntry, hashData string) string {
	h := sha256.New()

	// Hash components
	_, _ = fmt.Fprintf(h, "%d", entry.Sequence)
	_, _ = fmt.Fprintf(h, "%s", entry.Timestamp.Format(time.RFC3339Nano))
	_, _ = fmt.Fprintf(h, "%s", entry.Operation)
	_, _ = fmt.Fprintf(h, "%s", entry.Topic)
	_, _ = fmt.Fprintf(h, "%s", entry.Actor)
	_, _ = fmt.Fprintf(h, "%s", entry.Source)
	_, _ = fmt.Fprintf(h, "%s", entry.MessageID)
	_, _ = fmt.Fprint(h, hashData)
	_, _ = fmt.Fprintf(h, "%t", entry.Success)
	_, _ = fmt.Fprintf(h, "%s", entry.Error)
	_, _ = fmt.Fprintf(h, "%s", entry.PreviousHash)

	return hex.EncodeToString(h.Sum(nil))
}

// storeInMemory stores entry in memory (circular buffer)
func (al *AuditLogger) storeInMemory(entry *AuditEntry, hashData string) {
	al.entriesMu.Lock()
	defer al.entriesMu.Unlock()

	al.nextSequence++
	entry.Sequence = al.nextSequence
	if al.config.VerifyIntegrity {
		entry.PreviousHash = al.lastHash
		entry.Hash = al.calculateHash(entry, hashData)
		al.lastHash = entry.Hash
	}
	al.entries = append(al.entries, entry)

	// Keep only last N entries
	if len(al.entries) > al.maxInMemory {
		drop := len(al.entries) - al.maxInMemory
		al.retainedAnchor = al.entries[drop-1].Hash
		clear(al.entries[:drop])
		al.entries = al.entries[drop:]
	}
}

// writeToFile writes entry to log file
func (al *AuditLogger) writeToFile(entry *AuditEntry) error {
	if al.config.LogFile == "" {
		return nil
	}
	// Encoding may invoke caller-supplied marshalers.
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	al.logFileMu.Lock()
	defer al.logFileMu.Unlock()
	if al.logFile == nil {
		if err := al.openLogFile(); err != nil {
			return err
		}
	}

	// Check if rotation is needed
	if al.stats.CurrentLogSize >= al.config.MaxLogSize {
		if err := al.rotateLogFile(); err != nil {
			return err
		}
	}

	n, err := al.logFile.Write(data)
	if err != nil {
		return err
	}

	al.stats.CurrentLogSize += int64(n)
	al.stats.BytesWritten.Add(uint64(n))

	return nil
}

// rotateLogFile rotates the log under logFileMu. Every attempt restores an
// active handle when possible. A failed archive operation may have moved
// older files, but the active path is only renamed after those moves succeed.
func (al *AuditLogger) rotateLogFile() error {
	closeErr := al.logFile.Close()
	al.logFile = nil
	if closeErr != nil {
		return errors.Join(fmt.Errorf("close audit file for rotation: %w", closeErr), al.openLogFile())
	}

	rotateErr := al.rotateAuditPaths()
	reopenErr := al.openLogFile()
	if err := errors.Join(rotateErr, reopenErr); err != nil {
		return err
	}
	al.stats.LogRotations.Add(1)
	return nil
}

func (al *AuditLogger) rotateAuditPaths() error {
	// Preserve the active file even when no positive retention count was set.
	last := max(al.config.MaxLogFiles, 1)
	if err := os.Remove(al.logFilePath(last)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove oldest audit archive: %w", err)
	}
	for i := last - 1; i >= 0; i-- {
		oldPath := al.logFilePath(i)
		if _, err := os.Stat(oldPath); err != nil {
			if i > 0 && errors.Is(err, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("stat audit file for rotation: %w", err)
		}
		if err := os.Rename(oldPath, al.logFilePath(i+1)); err != nil {
			return fmt.Errorf("rename audit file for rotation: %w", err)
		}
	}
	return nil
}

// logFilePath returns the path for a rotated log file
func (al *AuditLogger) logFilePath(n int) string {
	if n == 0 {
		return al.config.LogFile
	}
	return fmt.Sprintf("%s.%d", al.config.LogFile, n)
}

// asyncWriter writes audit entries asynchronously
func (al *AuditLogger) asyncWriter() {
	defer al.wg.Done()

	for {
		select {
		case entry := <-al.asyncQueue:
			al.writeAsync(entry)
		case <-al.ctx.Done():
			// Drain queue
			for len(al.asyncQueue) > 0 {
				entry := <-al.asyncQueue
				al.writeAsync(entry)
			}
			return
		}
	}
}

func (al *AuditLogger) writeAsync(entry *AuditEntry) {
	if err := al.writeToFile(entry); err != nil && al.asyncErr == nil {
		al.asyncErr = fmt.Errorf("write audit entry: %w", err)
	}
}

// cloneAuditData uses the bounded reflection path for nested containers,
// including data that cannot be encoded as JSON.
func cloneAuditData(data map[string]any) map[string]any {
	if data == nil {
		return nil
	}
	cloned, _ := cloneValue(reflect.ValueOf(data), 0)
	return cloned.Interface().(map[string]any)
}

// Query searches audit logs with filters and returns copies of ordinary data
// containers within the clone depth limit.
func (al *AuditLogger) Query(query AuditQuery) ([]*AuditEntry, error) {
	if al.closed.Load() {
		return nil, ErrAuditClosed
	}

	// Validate time range
	if !query.StartTime.IsZero() && !query.EndTime.IsZero() {
		if query.EndTime.Before(query.StartTime) {
			return nil, ErrInvalidAuditQuery
		}
	}

	results := make([]*AuditEntry, 0)

	al.entriesMu.RLock()
	for _, entry := range al.entries {
		if al.matchesQuery(entry, query) {
			// Copy entry
			entryCopy := *entry
			results = append(results, &entryCopy)
		}
	}
	al.entriesMu.RUnlock()

	// Sort by sequence
	if query.Ascending {
		sort.Slice(results, func(i, j int) bool {
			return results[i].Sequence < results[j].Sequence
		})
	} else {
		sort.Slice(results, func(i, j int) bool {
			return results[i].Sequence > results[j].Sequence
		})
	}

	// Apply offset and limit
	if query.Offset > 0 {
		if query.Offset >= len(results) {
			return []*AuditEntry{}, nil
		}
		results = results[query.Offset:]
	}

	if query.Limit > 0 && len(results) > query.Limit {
		results = results[:query.Limit]
	}

	for _, entry := range results {
		entry.Data = cloneAuditData(entry.Data)
	}
	return results, nil
}

// matchesQuery checks if an entry matches query filters
func (al *AuditLogger) matchesQuery(entry *AuditEntry, query AuditQuery) bool {
	// Time range
	if !query.StartTime.IsZero() && entry.Timestamp.Before(query.StartTime) {
		return false
	}
	if !query.EndTime.IsZero() && entry.Timestamp.After(query.EndTime) {
		return false
	}

	// Operation filter
	if len(query.Operations) > 0 {
		found := false
		for _, op := range query.Operations {
			if entry.Operation == op {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// Topic filter
	if len(query.Topics) > 0 {
		found := false
		for _, topic := range query.Topics {
			if entry.Topic == topic {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// Actor filter
	if len(query.Actors) > 0 {
		found := false
		for _, actor := range query.Actors {
			if entry.Actor == actor {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// Success filter
	if query.Success != nil {
		if *query.Success != entry.Success {
			return false
		}
	}

	return true
}

// Verify checks the audit chain within the retained in-memory window.
func (al *AuditLogger) Verify() error {
	if !al.config.VerifyIntegrity {
		return nil
	}

	al.entriesMu.RLock()
	entries := slices.Clone(al.entries)
	prevHash := al.retainedAnchor
	al.entriesMu.RUnlock()

	for _, entry := range entries {
		// Verify previous hash
		if entry.PreviousHash != prevHash {
			al.stats.VerifyFailures.Add(1)
			return fmt.Errorf("%w: sequence %d", ErrAuditCorrupted, entry.Sequence)
		}

		// Verify entry hash
		expectedHash := al.calculateHash(entry, fmt.Sprint(entry.Data))
		if entry.Hash != expectedHash {
			al.stats.VerifyFailures.Add(1)
			return fmt.Errorf("%w: sequence %d hash mismatch", ErrAuditCorrupted, entry.Sequence)
		}

		prevHash = entry.Hash
	}

	return nil
}

// Stats returns audit log statistics.
func (al *AuditLogger) Stats() *AuditStats {
	stats := &AuditStats{}
	stats.TotalEntries.Store(al.stats.TotalEntries.Load())
	stats.SuccessfulOps.Store(al.stats.SuccessfulOps.Load())
	stats.FailedOps.Store(al.stats.FailedOps.Load())
	stats.BytesWritten.Store(al.stats.BytesWritten.Load())
	stats.LogRotations.Store(al.stats.LogRotations.Load())
	stats.VerifyFailures.Store(al.stats.VerifyFailures.Load())

	al.logFileMu.Lock()
	stats.CurrentLogSize = al.stats.CurrentLogSize
	al.logFileMu.Unlock()

	return stats
}

// Close stops admitting records, waits for accepted calls, and drains pending
// writes before closing the log file. It joins the first background write
// error with any file close error.
func (al *AuditLogger) Close() error {
	al.admissionMu.Lock()
	wasClosed := al.closed.Swap(true)
	al.admissionMu.Unlock()
	if wasClosed {
		return ErrAuditClosed
	}

	al.activeLogs.Wait()
	al.cancel()
	al.wg.Wait()

	var closeErr error
	al.logFileMu.Lock()
	if al.logFile != nil {
		if err := al.logFile.Close(); err != nil {
			closeErr = fmt.Errorf("close audit log: %w", err)
		}
	}
	al.logFileMu.Unlock()

	return errors.Join(al.asyncErr, closeErr)
}
