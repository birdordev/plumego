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

	// BufferSize for async writes
	BufferSize int

	// IncludeMessageData includes full message data in audit logs
	IncludeMessageData bool

	// IncludeStackTrace includes stack traces for errors
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
	entries      []*AuditEntry
	entriesMu    sync.RWMutex
	maxInMemory  int
	nextSequence atomic.Uint64

	// Last entry hash for chaining
	lastHash   string
	lastHashMu sync.RWMutex

	// File logging
	logFile   *os.File
	logFileMu sync.Mutex

	// Async writing
	asyncQueue chan *AuditEntry

	// Statistics
	stats AuditStats

	// Control
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	closed atomic.Bool
}

// NewAuditLogger creates a new audit logger
func NewAuditLogger(config AuditConfig) (*AuditLogger, error) {
	if !config.Enabled {
		return nil, errors.New("audit logging is not enabled")
	}

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

// Log records an audit entry
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
		Sequence:  al.nextSequence.Add(1),
		Timestamp: time.Now().UTC(),
		Operation: operation,
		Topic:     topic,
		Actor:     actor,
		Source:    source,
		Data:      data,
		Success:   success,
	}

	if err != nil {
		entry.Error = err.Error()
	}

	// Add integrity hash if enabled
	if al.config.VerifyIntegrity {
		al.lastHashMu.RLock()
		entry.PreviousHash = al.lastHash
		al.lastHashMu.RUnlock()

		entry.Hash = al.calculateHash(entry)

		al.lastHashMu.Lock()
		al.lastHash = entry.Hash
		al.lastHashMu.Unlock()
	}

	// Update stats
	al.stats.TotalEntries.Add(1)
	if success {
		al.stats.SuccessfulOps.Add(1)
	} else {
		al.stats.FailedOps.Add(1)
	}

	// Store in memory
	al.storeInMemory(entry)

	// Write to file
	if al.config.AsyncMode {
		select {
		case al.asyncQueue <- entry:
		case <-al.ctx.Done():
			return ErrAuditClosed
		default:
			// Queue full, write synchronously
			return al.writeToFile(entry)
		}
	} else if al.logFile != nil {
		return al.writeToFile(entry)
	}

	return nil
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
func (al *AuditLogger) calculateHash(entry *AuditEntry) string {
	h := sha256.New()

	// Hash components
	_, _ = fmt.Fprintf(h, "%d", entry.Sequence)
	_, _ = fmt.Fprintf(h, "%s", entry.Timestamp.Format(time.RFC3339Nano))
	_, _ = fmt.Fprintf(h, "%s", entry.Operation)
	_, _ = fmt.Fprintf(h, "%s", entry.Topic)
	_, _ = fmt.Fprintf(h, "%s", entry.Actor)
	_, _ = fmt.Fprintf(h, "%s", entry.Source)
	_, _ = fmt.Fprintf(h, "%s", entry.MessageID)
	_, _ = fmt.Fprintf(h, "%v", entry.Data)
	_, _ = fmt.Fprintf(h, "%t", entry.Success)
	_, _ = fmt.Fprintf(h, "%s", entry.Error)
	_, _ = fmt.Fprintf(h, "%s", entry.PreviousHash)

	return hex.EncodeToString(h.Sum(nil))
}

// storeInMemory stores entry in memory (circular buffer)
func (al *AuditLogger) storeInMemory(entry *AuditEntry) {
	al.entriesMu.Lock()
	defer al.entriesMu.Unlock()

	al.entries = append(al.entries, entry)

	// Keep only last N entries
	if len(al.entries) > al.maxInMemory {
		al.entries = al.entries[len(al.entries)-al.maxInMemory:]
	}
}

// writeToFile writes entry to log file
func (al *AuditLogger) writeToFile(entry *AuditEntry) error {
	if al.logFile == nil {
		return nil
	}

	al.logFileMu.Lock()
	defer al.logFileMu.Unlock()

	// Check if rotation is needed
	if al.stats.CurrentLogSize >= al.config.MaxLogSize {
		if err := al.rotateLogFile(); err != nil {
			return err
		}
	}

	// Write as JSON line
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}

	data = append(data, '\n')

	n, err := al.logFile.Write(data)
	if err != nil {
		return err
	}

	al.stats.CurrentLogSize += int64(n)
	al.stats.BytesWritten.Add(uint64(n))

	return nil
}

// rotateLogFile rotates the current log file
func (al *AuditLogger) rotateLogFile() error {
	// Close current file
	if err := al.logFile.Close(); err != nil {
		return err
	}

	// Rotate existing files
	for i := al.config.MaxLogFiles - 1; i >= 0; i-- {
		oldPath := al.logFilePath(i)
		newPath := al.logFilePath(i + 1)

		if i == al.config.MaxLogFiles-1 {
			// Delete oldest file
			_ = os.Remove(newPath)
		}

		if _, err := os.Stat(oldPath); err == nil {
			_ = os.Rename(oldPath, newPath)
		}
	}

	// Rename current to .1
	_ = os.Rename(al.config.LogFile, al.logFilePath(1))

	// Create new log file
	if err := al.openLogFile(); err != nil {
		return err
	}

	al.stats.LogRotations.Add(1)
	al.stats.CurrentLogSize = 0

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
			_ = al.writeToFile(entry)
		case <-al.ctx.Done():
			// Drain queue
			for len(al.asyncQueue) > 0 {
				entry := <-al.asyncQueue
				_ = al.writeToFile(entry)
			}
			return
		}
	}
}

// Query searches audit logs with filters
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

// Verify checks the integrity of the audit log chain
func (al *AuditLogger) Verify() error {
	if !al.config.VerifyIntegrity {
		return nil
	}

	al.entriesMu.RLock()
	defer al.entriesMu.RUnlock()

	prevHash := ""
	for _, entry := range al.entries {
		// Verify previous hash
		if entry.PreviousHash != prevHash {
			al.stats.VerifyFailures.Add(1)
			return fmt.Errorf("%w: sequence %d", ErrAuditCorrupted, entry.Sequence)
		}

		// Verify entry hash
		expectedHash := al.calculateHash(entry)
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

// Close stops the audit logger and flushes pending writes
func (al *AuditLogger) Close() error {
	if !al.closed.CompareAndSwap(false, true) {
		return ErrAuditClosed
	}

	al.cancel()
	al.wg.Wait()

	// Close log file
	al.logFileMu.Lock()
	if al.logFile != nil {
		_ = al.logFile.Close()
	}
	al.logFileMu.Unlock()

	return nil
}
