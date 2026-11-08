package log

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// jsonLogger implements StructuredLogger with JSON output format.
// It's thread-safe and suitable for structured logging in production environments.
type jsonLogger struct {
	mu               sync.Mutex
	writeMu          *sync.Mutex
	writeErrOnce     *sync.Once
	output           io.Writer
	errorOutput      io.Writer // optional; Error/Fatal go here when non-nil
	level            Level
	verbosity        int
	fields           Fields
	respectVerbosity bool
}

func newJSONLogger(config LoggerConfig) *jsonLogger {
	if config.Output == nil {
		config.Output = os.Stdout
	}
	return &jsonLogger{
		writeMu:          &sync.Mutex{},
		writeErrOnce:     &sync.Once{},
		output:           config.Output,
		errorOutput:      config.ErrorOutput,
		level:            config.Level,
		verbosity:        config.Verbosity,
		fields:           cloneFields(config.Fields),
		respectVerbosity: config.RespectVerbosity,
	}
}

// WithFields returns a new logger with additional fields merged in.
// Derived loggers share the writer lock and write-error tracker.
// The fields, outputs, level and verbosity are snapshotted at call time.
func (l *jsonLogger) WithFields(fields Fields) StructuredLogger {
	// These fields are immutable after construction, so a single lock snapshot
	// is sufficient; no risk of partial reads.
	l.mu.Lock()
	merged := mergeFields(l.fields, fields)
	child := &jsonLogger{
		writeMu:          l.writeMu,
		output:           l.output,
		errorOutput:      l.errorOutput,
		level:            l.level,
		verbosity:        l.verbosity,
		fields:           merged,
		respectVerbosity: l.respectVerbosity,
		writeErrOnce:     l.writeErrOnce,
	}
	l.mu.Unlock()
	return child
}

// Debug logs a debug message with optional fields.
// When RespectVerbosity is enabled in LoggerConfig, Debug is gated on the
// local Verbosity field.
func (l *jsonLogger) Debug(msg string, fields ...Fields) {
	if l.respectVerbosity && !l.vAt(1) {
		return
	}
	l.log(DEBUG, msg, mergeFieldArgs(fields))
}

// Info logs an info message with optional fields.
func (l *jsonLogger) Info(msg string, fields ...Fields) {
	l.log(INFO, msg, mergeFieldArgs(fields))
}

// Warn logs a warning message with optional fields.
func (l *jsonLogger) Warn(msg string, fields ...Fields) {
	l.log(WARNING, msg, mergeFieldArgs(fields))
}

// Error logs an error message with optional fields.
func (l *jsonLogger) Error(msg string, fields ...Fields) {
	l.log(ERROR, msg, mergeFieldArgs(fields))
}

// Fatal logs a fatal message then calls os.Exit(1).
func (l *jsonLogger) Fatal(msg string, fields ...Fields) {
	l.log(FATAL, msg, mergeFieldArgs(fields))
	os.Exit(1)
}

// DebugCtx logs a debug message with context and optional fields.
func (l *jsonLogger) DebugCtx(ctx context.Context, msg string, fields ...Fields) {
	_ = ctx
	if l.respectVerbosity && !l.vAt(1) {
		return
	}
	l.log(DEBUG, msg, mergeFieldArgs(fields))
}

// InfoCtx logs an info message with context and optional fields.
func (l *jsonLogger) InfoCtx(ctx context.Context, msg string, fields ...Fields) {
	_ = ctx
	l.log(INFO, msg, mergeFieldArgs(fields))
}

// WarnCtx logs a warning message with context and optional fields.
func (l *jsonLogger) WarnCtx(ctx context.Context, msg string, fields ...Fields) {
	_ = ctx
	l.log(WARNING, msg, mergeFieldArgs(fields))
}

// ErrorCtx logs an error message with context and optional fields.
func (l *jsonLogger) ErrorCtx(ctx context.Context, msg string, fields ...Fields) {
	_ = ctx
	l.log(ERROR, msg, mergeFieldArgs(fields))
}

// FatalCtx logs a fatal message with context then calls os.Exit(1).
func (l *jsonLogger) FatalCtx(ctx context.Context, msg string, fields ...Fields) {
	_ = ctx
	l.log(FATAL, msg, mergeFieldArgs(fields))
	os.Exit(1)
}

func (l *jsonLogger) log(level Level, msg string, fields Fields) {
	// Snapshot the minimum level under the lock so a concurrent SetLevel()
	// cannot race with this read (go race detector would flag the plain read).
	l.mu.Lock()
	minLevel := l.level
	l.mu.Unlock()
	if level < minLevel {
		return
	}

	// Build the entry and marshal it outside the lock to reduce contention.
	entry := l.buildEntry(level, msg, fields)
	data, err := json.Marshal(entry)
	if err != nil {
		data = []byte(`{"level":"ERROR","msg":"failed to marshal log entry"}`)
	}

	writeMu := l.writeMu
	if writeMu == nil {
		writeMu = &sync.Mutex{}
		l.mu.Lock()
		if l.writeMu == nil {
			l.writeMu = writeMu
		} else {
			writeMu = l.writeMu
		}
		l.mu.Unlock()
	}
	writeMu.Lock()
	defer writeMu.Unlock()
	l.writeRaw(level, data)
}

// buildEntry constructs the log entry map. Called without the mutex held.
func (l *jsonLogger) buildEntry(level Level, msg string, fields Fields) map[string]any {
	// Snapshot base fields under lock so WithFields-derived loggers are safe.
	l.mu.Lock()
	baseFields := l.fields
	l.mu.Unlock()

	combined := mergeFields(baseFields, fields)
	entry := make(map[string]any, safeMapCapacity(len(combined), 4))
	for k, v := range combined {
		entry[k] = normalizeJSONFieldValue(v)
	}

	// Reserved keys are always controlled by logger internals.
	entry["time"] = time.Now().UTC().Format(time.RFC3339Nano)
	entry["level"] = levelName(level)
	entry["msg"] = msg

	return entry
}

const (
	maxJSONFieldDepth = 32
	maxJSONFieldNodes = 1024
)

func normalizeJSONFieldValue(value any) any {
	remaining := maxJSONFieldNodes
	return normalizeJSONFieldValueAt(value, 0, &remaining)
}

func normalizeJSONFieldValueAt(value any, depth int, remaining *int) any {
	if *remaining <= 0 {
		return "[truncated]"
	}
	*remaining -= 1
	if depth >= maxJSONFieldDepth {
		return "[truncated]"
	}
	switch v := value.(type) {
	case Fields:
		return normalizeJSONFieldMap(map[string]any(v), depth, remaining)
	case map[string]any:
		return normalizeJSONFieldMap(v, depth, remaining)
	case map[string]string:
		normalized := make(map[string]any, min(len(v), *remaining))
		for key, nested := range v {
			if *remaining <= 0 {
				normalized["[truncated]"] = "[truncated]"
				break
			}
			*remaining -= 1
			normalized[key] = nested
		}
		return normalized
	case []any:
		normalized := make([]any, 0, min(len(v), *remaining)+1)
		for _, nested := range v {
			if *remaining <= 0 {
				normalized = append(normalized, "[truncated]")
				break
			}
			normalized = append(normalized, normalizeJSONFieldValueAt(nested, depth+1, remaining))
		}
		return normalized
	}
	data, err := json.Marshal(value)
	if err != nil {
		// Only numeric formatting is safe here. Generic formatting can reveal
		// fields deliberately excluded from JSON or invoke user String methods.
		switch v := value.(type) {
		case float32:
			return fmt.Sprint(v)
		case float64:
			return fmt.Sprint(v)
		}
		return "[unsupported]"
	}
	// Keep validated JSON bytes instead of decoding numbers through float64.
	return json.RawMessage(data)
}

func normalizeJSONFieldMap(fields map[string]any, depth int, remaining *int) map[string]any {
	normalized := make(map[string]any, min(len(fields), *remaining))
	for key, value := range fields {
		if *remaining <= 0 {
			normalized["[truncated]"] = "[truncated]"
			break
		}
		normalized[key] = normalizeJSONFieldValueAt(value, depth+1, remaining)
	}
	return normalized
}

// writeRaw writes pre-marshalled JSON data to the appropriate output.
// Must be called with the shared writer mutex held.
func (l *jsonLogger) writeRaw(level Level, data []byte) {
	w := l.writerFor(level)
	if err := writeFull(w, data); err != nil {
		l.reportWriteError(err)
		return
	}
	if err := writeFull(w, []byte("\n")); err != nil {
		l.reportWriteError(err)
	}
}

// writerFor returns the appropriate writer for the given level.
// Error and Fatal go to errorOutput when configured; everything else uses output.
func (l *jsonLogger) writerFor(level Level) io.Writer {
	if l.errorOutput != nil && (level == ERROR || level == FATAL) {
		return l.errorOutput
	}
	return l.output
}

// SetLevel changes the minimum log level at runtime.
// It is safe to call concurrently with logging.
func (l *jsonLogger) SetLevel(level Level) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = level
}

// Level returns the current minimum log level.
func (l *jsonLogger) Level() Level {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.level
}

func (l *jsonLogger) vAt(level int) bool {
	return level <= l.verbosity
}

func (l *jsonLogger) reportWriteError(_ error) {
	if l.writeErrOnce == nil {
		l.writeErrOnce = &sync.Once{}
	}
	l.writeErrOnce.Do(func() {
		_, _ = os.Stderr.WriteString("json logger: failed to write log output\n")
	})
}
