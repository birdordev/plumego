package log

import (
	"context"
	"encoding"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"reflect"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"
)

var errJSONFieldPanic = errors.New("log field encoder panicked")

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
	l.log(DEBUG, msg, fields)
}

// Info logs an info message with optional fields.
func (l *jsonLogger) Info(msg string, fields ...Fields) {
	l.log(INFO, msg, fields)
}

// Warn logs a warning message with optional fields.
func (l *jsonLogger) Warn(msg string, fields ...Fields) {
	l.log(WARNING, msg, fields)
}

// Error logs an error message with optional fields.
func (l *jsonLogger) Error(msg string, fields ...Fields) {
	l.log(ERROR, msg, fields)
}

// Fatal logs a fatal message then calls os.Exit(1).
func (l *jsonLogger) Fatal(msg string, fields ...Fields) {
	l.log(FATAL, msg, fields)
	os.Exit(1)
}

// DebugCtx logs a debug message with context and optional fields.
func (l *jsonLogger) DebugCtx(ctx context.Context, msg string, fields ...Fields) {
	_ = ctx
	if l.respectVerbosity && !l.vAt(1) {
		return
	}
	l.log(DEBUG, msg, fields)
}

// InfoCtx logs an info message with context and optional fields.
func (l *jsonLogger) InfoCtx(ctx context.Context, msg string, fields ...Fields) {
	_ = ctx
	l.log(INFO, msg, fields)
}

// WarnCtx logs a warning message with context and optional fields.
func (l *jsonLogger) WarnCtx(ctx context.Context, msg string, fields ...Fields) {
	_ = ctx
	l.log(WARNING, msg, fields)
}

// ErrorCtx logs an error message with context and optional fields.
func (l *jsonLogger) ErrorCtx(ctx context.Context, msg string, fields ...Fields) {
	_ = ctx
	l.log(ERROR, msg, fields)
}

// FatalCtx logs a fatal message with context then calls os.Exit(1).
func (l *jsonLogger) FatalCtx(ctx context.Context, msg string, fields ...Fields) {
	_ = ctx
	l.log(FATAL, msg, fields)
	os.Exit(1)
}

func (l *jsonLogger) log(level Level, msg string, fields []Fields) {
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
func (l *jsonLogger) buildEntry(level Level, msg string, fields []Fields) map[string]any {
	// Snapshot base fields under lock so WithFields-derived loggers are safe.
	l.mu.Lock()
	baseFields := l.fields
	l.mu.Unlock()

	capacity := safeMapCapacity(fieldSetCapacity(len(baseFields), fields), 3)
	entry := mergeFieldsWithCapacity(baseFields, fields, capacity)
	for k, v := range entry {
		if !utf8.ValidString(k) {
			delete(entry, k)
			continue
		}
		switch k {
		case "time", "level", "msg":
			continue
		}
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
	case nil, string, bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, uintptr:
		return v
	case Fields:
		return normalizeJSONFieldMap(map[string]any(v), depth, remaining)
	case map[string]any:
		return normalizeJSONFieldMap(v, depth, remaining)
	case map[string]string:
		if v == nil {
			return nil
		}
		normalized := make(map[string]any, min(len(v), *remaining))
		for key, nested := range v {
			if *remaining <= 0 {
				normalized["[truncated]"] = "[truncated]"
				break
			}
			*remaining -= 1
			if utf8.ValidString(key) {
				normalized[key] = nested
			}
		}
		return normalized
	case []any:
		if v == nil {
			return nil
		}
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
	switch value.(type) {
	case json.Marshaler, encoding.TextMarshaler:
		// Keep explicit encoding methods on the standard JSON path.
	default:
		if normalized, ok := normalizeTypedJSONContainer(reflect.ValueOf(value), depth, remaining); ok {
			return normalized
		}
	}
	data, err := marshalJSONField(value)
	if err != nil {
		// Only numeric formatting is safe here. Generic formatting can reveal
		// fields deliberately excluded from JSON or invoke user String methods.
		if _, ok := err.(*json.UnsupportedValueError); ok {
			// The encoder may report a float nested inside a composite value.
			scalar := reflect.ValueOf(value)
			switch scalar.Kind() {
			case reflect.Float32, reflect.Float64:
				f := scalar.Float()
				if math.IsNaN(f) || math.IsInf(f, 0) {
					return strconv.FormatFloat(f, 'g', -1, scalar.Type().Bits())
				}
			}
		}
		return "[unsupported]"
	}
	// Keep validated JSON bytes instead of decoding numbers through float64.
	return json.RawMessage(data)
}

// Marshal only this field so a caller-provided encoder cannot drop the entry.
func marshalJSONField(value any) (data []byte, err error) {
	defer func() {
		if recover() != nil {
			data = nil
			err = errJSONFieldPanic
		}
	}()
	return json.Marshal(value)
}

func normalizeTypedJSONContainer(value reflect.Value, depth int, remaining *int) (any, bool) {
	switch value.Kind() {
	case reflect.Pointer, reflect.Interface:
		if value.IsNil() {
			return nil, true
		}
		elem := value.Elem()
		if hasJSONEncodingMethod(elem.Type()) {
			return nil, false
		}
		switch elem.Kind() {
		case reflect.Pointer, reflect.Interface, reflect.Slice, reflect.Array, reflect.Map:
			if *remaining <= 0 || depth+1 >= maxJSONFieldDepth {
				return "[truncated]", true
			}
			*remaining -= 1
			return normalizeTypedJSONContainer(elem, depth+1, remaining)
		default:
			return nil, false
		}
	case reflect.Slice, reflect.Array:
		element := value.Type().Elem()
		encodeElementAddress := hasJSONEncodingMethod(reflect.PointerTo(element))
		if value.Kind() == reflect.Slice {
			if element.Kind() == reflect.Uint8 && !encodeElementAddress {
				return nil, false // encoding/json encodes byte slices as base64.
			}
			if value.IsNil() {
				return nil, true
			}
		}
		items := make([]any, 0, min(value.Len(), *remaining)+1)
		for i := range value.Len() {
			if *remaining <= 0 {
				return append(items, "[truncated]"), true
			}
			item := value.Index(i)
			if encodeElementAddress && item.CanAddr() {
				item = item.Addr()
			}
			items = append(items, normalizeJSONFieldValueAt(item.Interface(), depth+1, remaining))
		}
		return items, true
	case reflect.Map:
		if value.Type().Key().Kind() != reflect.String {
			return nil, false
		}
		if value.IsNil() {
			return nil, true
		}
		fields := make(map[string]any, min(value.Len(), *remaining))
		for key, item := range value.Seq2() {
			if *remaining <= 0 {
				fields["[truncated]"] = "[truncated]"
				break
			}
			name := key.String()
			if !utf8.ValidString(name) {
				*remaining -= 1
				continue
			}
			fields[name] = normalizeJSONFieldValueAt(item.Interface(), depth+1, remaining)
		}
		return fields, true
	default:
		return nil, false
	}
}

func hasJSONEncodingMethod(typ reflect.Type) bool {
	return typ.Implements(reflect.TypeFor[json.Marshaler]()) ||
		typ.Implements(reflect.TypeFor[encoding.TextMarshaler]())
}

func normalizeJSONFieldMap(fields map[string]any, depth int, remaining *int) map[string]any {
	if fields == nil {
		return nil
	}
	normalized := make(map[string]any, min(len(fields), *remaining))
	for key, value := range fields {
		if *remaining <= 0 {
			normalized["[truncated]"] = "[truncated]"
			break
		}
		if !utf8.ValidString(key) {
			*remaining -= 1
			continue
		}
		normalized[key] = normalizeJSONFieldValueAt(value, depth+1, remaining)
	}
	return normalized
}

// writeRaw writes pre-marshalled JSON data to the appropriate output.
// Must be called with the shared writer mutex held.
func (l *jsonLogger) writeRaw(level Level, data []byte) {
	w := l.writerFor(level)
	if err := writeFull(w, append(data, '\n')); err != nil {
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
