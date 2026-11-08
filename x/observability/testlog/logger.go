package testlog

import (
	"context"
	"math/bits"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/birdordev/plumego/contract"
	plog "github.com/birdordev/plumego/log"
)

const maxMapCapacity = int(^uint(0) >> 1)

const (
	DEBUG   = plog.DEBUG
	INFO    = plog.INFO
	WARNING = plog.WARNING
	ERROR   = plog.ERROR
	FATAL   = plog.FATAL
)

type (
	Fields = plog.Fields
	Level  = plog.Level
)

// Entry holds a single captured log entry.
type Entry struct {
	Level   Level
	Message string
	Fields  Fields
	Time    time.Time
}

type sharedEntries struct {
	mu      sync.RWMutex
	entries []Entry
}

// Logger is an in-memory StructuredLogger designed for tests.
// All loggers derived via WithFields share the same entry store.
type Logger struct {
	shared     *sharedEntries
	level      Level
	baseFields Fields
	// FatalHook overrides os.Exit(1) so tests can assert on fatal logging.
	FatalHook func(msg string)
}

var _ plog.StructuredLogger = (*Logger)(nil)

// New creates a Logger that captures all levels.
func New() *Logger {
	return &Logger{
		shared:     &sharedEntries{},
		level:      DEBUG,
		baseFields: Fields{},
	}
}

func (l *Logger) With(key string, value any) plog.StructuredLogger {
	return l.WithFields(Fields{key: value})
}

func (l *Logger) WithFields(fields Fields) plog.StructuredLogger {
	return &Logger{
		shared:     l.shared,
		level:      l.level,
		baseFields: mergeFields(l.baseFields, fields),
		FatalHook:  l.FatalHook,
	}
}

func (l *Logger) Debug(msg string, fields ...Fields) { l.capture(DEBUG, msg, firstFields(fields)) }
func (l *Logger) Info(msg string, fields ...Fields)  { l.capture(INFO, msg, firstFields(fields)) }
func (l *Logger) Warn(msg string, fields ...Fields)  { l.capture(WARNING, msg, firstFields(fields)) }
func (l *Logger) Error(msg string, fields ...Fields) { l.capture(ERROR, msg, firstFields(fields)) }
func (l *Logger) Fatal(msg string, fields ...Fields) {
	l.capture(FATAL, msg, firstFields(fields))
	l.handleFatal(msg)
}

func (l *Logger) DebugCtx(ctx context.Context, msg string, fields ...Fields) {
	l.capture(DEBUG, msg, l.withRequestID(ctx, firstFields(fields)))
}
func (l *Logger) InfoCtx(ctx context.Context, msg string, fields ...Fields) {
	l.capture(INFO, msg, l.withRequestID(ctx, firstFields(fields)))
}
func (l *Logger) WarnCtx(ctx context.Context, msg string, fields ...Fields) {
	l.capture(WARNING, msg, l.withRequestID(ctx, firstFields(fields)))
}
func (l *Logger) ErrorCtx(ctx context.Context, msg string, fields ...Fields) {
	l.capture(ERROR, msg, l.withRequestID(ctx, firstFields(fields)))
}
func (l *Logger) FatalCtx(ctx context.Context, msg string, fields ...Fields) {
	l.capture(FATAL, msg, l.withRequestID(ctx, firstFields(fields)))
	l.handleFatal(msg)
}

// Entries returns a snapshot of all captured log entries.
func (l *Logger) Entries() []Entry {
	l.shared.mu.RLock()
	defer l.shared.mu.RUnlock()
	cp := make([]Entry, len(l.shared.entries))
	copy(cp, l.shared.entries)
	return cp
}

// Clear removes all captured entries.
func (l *Logger) Clear() {
	l.shared.mu.Lock()
	defer l.shared.mu.Unlock()
	l.shared.entries = l.shared.entries[:0]
}

// Count returns the total number of captured entries.
func (l *Logger) Count() int {
	l.shared.mu.RLock()
	defer l.shared.mu.RUnlock()
	return len(l.shared.entries)
}

// CountByLevel returns the number of entries at the given level.
func (l *Logger) CountByLevel(level Level) int {
	l.shared.mu.RLock()
	defer l.shared.mu.RUnlock()
	n := 0
	for _, e := range l.shared.entries {
		if e.Level == level {
			n++
		}
	}
	return n
}

// HasEntry reports whether any entry at level contains substr.
func (l *Logger) HasEntry(level Level, substr string) bool {
	l.shared.mu.RLock()
	defer l.shared.mu.RUnlock()
	for _, e := range l.shared.entries {
		if e.Level == level && strings.Contains(e.Message, substr) {
			return true
		}
	}
	return false
}

// LastEntry returns the most recently captured entry, if any.
func (l *Logger) LastEntry() (Entry, bool) {
	l.shared.mu.RLock()
	defer l.shared.mu.RUnlock()
	if len(l.shared.entries) == 0 {
		return Entry{}, false
	}
	return l.shared.entries[len(l.shared.entries)-1], true
}

func (l *Logger) handleFatal(msg string) {
	if l.FatalHook != nil {
		l.FatalHook(msg)
		return
	}
	os.Exit(1)
}

func (l *Logger) capture(level Level, msg string, fields Fields) {
	if level < l.level {
		return
	}
	entry := Entry{
		Level:   level,
		Message: msg,
		Fields:  mergeFields(l.baseFields, fields),
		Time:    time.Now(),
	}
	l.shared.mu.Lock()
	defer l.shared.mu.Unlock()
	l.shared.entries = append(l.shared.entries, entry)
}

func (l *Logger) withRequestID(ctx context.Context, fields Fields) Fields {
	if requestID := contract.RequestIDFromContext(ctx); requestID != "" {
		return mergeFields(fields, Fields{"request_id": requestID})
	}
	return fields
}

func firstFields(extra []Fields) Fields {
	if len(extra) > 0 {
		return extra[0]
	}
	return nil
}

func safeMapCapacity(lengths ...int) int {
	total := 0
	for _, length := range lengths {
		next, ok := addMapCapacity(total, length)
		if !ok {
			return 0
		}
		total = next
	}
	return total
}

func addMapCapacity(base, extra int) (int, bool) {
	if base < 0 || extra < 0 {
		return 0, false
	}
	sum, carry := bits.Add(uint(base), uint(extra), 0)
	if carry != 0 || sum > uint(maxMapCapacity) {
		return 0, false
	}
	return int(sum), true
}

func mergeFields(base, extra Fields) Fields {
	if len(base) == 0 && len(extra) == 0 {
		return Fields{}
	}
	merged := make(Fields, safeMapCapacity(len(base), len(extra)))
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range extra {
		merged[key] = value
	}
	return merged
}
