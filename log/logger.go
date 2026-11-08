package log

import (
	"context"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Compile-time checks that all concrete logger types satisfy StructuredLogger.
var (
	_ StructuredLogger = (*defaultLogger)(nil)
	_ StructuredLogger = (*jsonLogger)(nil)
	_ StructuredLogger = (*discardLogger)(nil)
)

// Fields represents structured log fields to attach to a log entry.
type Fields map[string]any

// LoggerFormat selects the concrete logger implementation behind NewLogger.
type LoggerFormat string

const (
	LoggerFormatText    LoggerFormat = "text"
	LoggerFormatJSON    LoggerFormat = "json"
	LoggerFormatDiscard LoggerFormat = "discard"
)

// LoggerConfig configures the canonical NewLogger constructor.
type LoggerConfig struct {
	Format           LoggerFormat
	Output           io.Writer
	ErrorOutput      io.Writer
	Level            Level
	Fields           Fields
	RespectVerbosity bool
	Verbosity        int
}

// StructuredLogger defines the minimal logging interface used by the application.
//
// All logging methods accept an optional Fields argument. Callers may omit
// it entirely when there are no extra fields to attach:
//
//	logger.Info("server started")
//	logger.Info("server started", log.Fields{"addr": ":8080"})
//
// Implementations must treat a nil or absent Fields the same as an empty one.
// Messages and field keys/values may contain untrusted input. Implementations
// must encode each record safely: escape control characters and line separators
// in text, or use a JSON encoder for JSON output. Do not interpolate raw fields
// into record delimiters. Callers pass original values, not pre-escaped strings,
// and remain responsible for excluding secrets and sensitive error text.
type StructuredLogger interface {
	// WithFields returns a new logger with the given fields merged into every
	// subsequent log entry. The original logger is not modified.
	WithFields(fields Fields) StructuredLogger

	Debug(msg string, fields ...Fields)
	Info(msg string, fields ...Fields)
	Warn(msg string, fields ...Fields)
	Error(msg string, fields ...Fields)
	// Fatal logs a message at FATAL level then calls os.Exit(1).
	Fatal(msg string, fields ...Fields)

	// Context-aware variants preserve the call shape for request-scoped logging.
	// Logger implementations must not infer transport metadata from ctx.
	DebugCtx(ctx context.Context, msg string, fields ...Fields)
	InfoCtx(ctx context.Context, msg string, fields ...Fields)
	WarnCtx(ctx context.Context, msg string, fields ...Fields)
	ErrorCtx(ctx context.Context, msg string, fields ...Fields)
	FatalCtx(ctx context.Context, msg string, fields ...Fields)
}

// mergeFieldArgs merges variadic field maps in call order.
func mergeFieldArgs(extra []Fields) Fields {
	return mergeFieldSets(extra...)
}

// defaultLogger adapts the default text logger backend to StructuredLogger.
type defaultLogger struct {
	backend          *gLogger
	fields           Fields
	respectVerbosity bool
}

// NewLogger creates the canonical structured logger.
//
// With no config it returns the default text logger. Alternate formats such as
// JSON or discard mode are selected through LoggerConfig.Format. Invalid format
// values are treated as a programming error and panic, matching the MustXxx
// convention used elsewhere in the library.
func NewLogger(configs ...LoggerConfig) StructuredLogger {
	cfg := LoggerConfig{}
	if len(configs) > 0 {
		cfg = configs[0]
	}

	switch cfg.Format {
	case LoggerFormatJSON:
		return newJSONLogger(cfg)
	case LoggerFormatDiscard:
		return newDiscardLogger()
	case "", LoggerFormatText:
		return newDefaultLogger(cfg)
	default:
		panic(fmt.Sprintf("log.NewLogger: unsupported format %q", cfg.Format))
	}
}

func (l *defaultLogger) WithFields(fields Fields) StructuredLogger {
	return &defaultLogger{
		backend:          l.getBackend(),
		fields:           mergeFields(l.fields, fields),
		respectVerbosity: l.respectVerbosity,
	}
}

// Debug logs at DEBUG level.
// When RespectVerbosity is enabled, debug logging is gated on V(1).
func (l *defaultLogger) Debug(msg string, fields ...Fields) {
	if l.respectVerbosity && !l.getBackend().vAt(1, 2) {
		return
	}
	l.logWithLevel(DEBUG, msg, mergeFieldArgs(fields))
}

func (l *defaultLogger) Info(msg string, fields ...Fields) {
	l.logWithLevel(INFO, msg, mergeFieldArgs(fields))
}

func (l *defaultLogger) Warn(msg string, fields ...Fields) {
	l.logWithLevel(WARNING, msg, mergeFieldArgs(fields))
}

func (l *defaultLogger) Error(msg string, fields ...Fields) {
	l.logWithLevel(ERROR, msg, mergeFieldArgs(fields))
}

func (l *defaultLogger) Fatal(msg string, fields ...Fields) {
	l.logWithLevel(FATAL, msg, mergeFieldArgs(fields))
}

// DebugCtx logs at DEBUG level with context.
// When RespectVerbosity is enabled, debug logging is gated on V(1).
// Context is accepted for interface compatibility; this backend does not extract
// transport metadata from ctx (see StructuredLogger contract).
func (l *defaultLogger) DebugCtx(_ context.Context, msg string, fields ...Fields) {
	if l.respectVerbosity && !l.getBackend().vAt(1, 2) {
		return
	}
	l.logWithLevel(DEBUG, msg, mergeFieldArgs(fields))
}

func (l *defaultLogger) InfoCtx(_ context.Context, msg string, fields ...Fields) {
	l.logWithLevel(INFO, msg, mergeFieldArgs(fields))
}

func (l *defaultLogger) WarnCtx(_ context.Context, msg string, fields ...Fields) {
	l.logWithLevel(WARNING, msg, mergeFieldArgs(fields))
}

func (l *defaultLogger) ErrorCtx(_ context.Context, msg string, fields ...Fields) {
	l.logWithLevel(ERROR, msg, mergeFieldArgs(fields))
}

func (l *defaultLogger) FatalCtx(_ context.Context, msg string, fields ...Fields) {
	l.logWithLevel(FATAL, msg, mergeFieldArgs(fields))
}

func (l *defaultLogger) logWithLevel(level Level, msg string, fields Fields) {
	combined := mergeFields(l.fields, fields)
	formatted := l.formatFields(combined)
	if formatted != "" {
		msg += " " + formatted
	}

	// calldepth=3: backend.log adds 1 → logInternal calls runtime.Caller(4)
	// Frame 0: logInternal, 1: backend.log, 2: logWithLevel, 3: public method (Info/Error/…), 4: actual caller
	l.getBackend().log(level, 3, msg)
}

func (l *defaultLogger) formatFields(fields Fields) string {
	if len(fields) == 0 {
		return ""
	}

	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, formatTextFieldKey(k)+"="+formatTextFieldValue(fields[k]))
	}

	return strings.Join(parts, " ")
}

func formatTextFieldKey(key string) string {
	if !isSafeTextFieldKey(key) {
		return strconv.Quote(key)
	}
	return key
}

func isSafeTextFieldKey(key string) bool {
	if key == "" {
		return false
	}
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-' || r == '.' || r == '/':
		default:
			return false
		}
	}
	return true
}

func formatTextFieldValue(value any) string {
	remaining := 1024
	text := formatTextValue(reflect.ValueOf(value), 0, &remaining)
	if text == "" || strings.ContainsAny(text, "=\"\\") || strings.IndexFunc(text, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) >= 0 {
		return strconv.Quote(text)
	}
	return text
}

func newDefaultLogger(cfg LoggerConfig) *defaultLogger {
	backend := newGLogger()
	if cfg.Output != nil {
		backend.SetOutput(cfg.Output)
	}
	if cfg.ErrorOutput != nil {
		backend.SetErrorOutput(cfg.ErrorOutput)
	}
	backend.SetLevel(cfg.Level)
	backend.SetVerbose(cfg.Verbosity)
	return &defaultLogger{
		backend:          backend,
		fields:           cloneFields(cfg.Fields),
		respectVerbosity: cfg.RespectVerbosity,
	}
}

func (l *defaultLogger) getBackend() *gLogger {
	if l.backend == nil {
		l.backend = newGLogger()
	}
	return l.backend
}

// formatTextValue bounds container traversal before fmt can recurse into cycles.
// Scalars and custom scalar formatters keep their established representation.
func formatTextValue(value reflect.Value, depth int, remaining *int) string {
	if !value.IsValid() {
		return "<nil>"
	}
	if *remaining <= 0 {
		return "[truncated]"
	}
	*remaining -= 1
	if depth >= 32 {
		return "[truncated]"
	}
	if value.Kind() == reflect.Interface {
		if value.IsNil() {
			return "<nil>"
		}
		return formatTextValue(value.Elem(), depth+1, remaining)
	}
	if value.CanInterface() {
		item := value.Interface()
		// Custom formatters own their execution and must terminate themselves.
		switch item.(type) {
		case fmt.Formatter, fmt.Stringer, error:
			return fmt.Sprint(item)
		}
	}
	switch value.Kind() {
	case reflect.Pointer:
		if value.IsNil() {
			return "<nil>"
		}
		switch value.Elem().Kind() {
		case reflect.Map, reflect.Slice, reflect.Array, reflect.Struct, reflect.Pointer, reflect.Interface:
			return "&" + formatTextValue(value.Elem(), depth+1, remaining)
		default:
			return fmt.Sprintf("%p", value.Interface())
		}
	case reflect.Map:
		type entry struct {
			value reflect.Value
			text  string
		}
		var entries []entry
		iter := value.MapRange()
		truncated := false
		for iter.Next() {
			if *remaining <= 0 {
				truncated = true
				break
			}
			key := iter.Key()
			entries = append(entries, entry{value: iter.Value(), text: formatTextValue(key, depth+1, remaining)})
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].text < entries[j].text })
		var out strings.Builder
		out.WriteString("map[")
		for i, entry := range entries {
			if i > 0 {
				out.WriteByte(' ')
			}
			if *remaining <= 0 {
				truncated = true
				break
			}
			out.WriteString(entry.text)
			out.WriteByte(':')
			out.WriteString(formatTextValue(entry.value, depth+1, remaining))
		}
		if truncated {
			out.WriteString("[truncated]")
		}
		out.WriteByte(']')
		return out.String()
	case reflect.Slice, reflect.Array:
		// Preserve fmt's special treatment of raw byte slices.
		if value.Kind() == reflect.Slice && value.Type().Elem().Kind() == reflect.Uint8 {
			return fmt.Sprint(value.Interface())
		}
		var out strings.Builder
		out.WriteByte('[')
		for i := 0; i < value.Len(); i++ {
			if i > 0 {
				out.WriteByte(' ')
			}
			if *remaining <= 0 {
				out.WriteString("[truncated]")
				break
			}
			out.WriteString(formatTextValue(value.Index(i), depth+1, remaining))
		}
		out.WriteByte(']')
		return out.String()
	case reflect.Struct:
		var out strings.Builder
		out.WriteByte('{')
		for i := 0; i < value.NumField(); i++ {
			if i > 0 {
				out.WriteByte(' ')
			}
			if !value.Field(i).CanInterface() {
				out.WriteString("[unexported]")
				continue
			}
			if *remaining <= 0 {
				out.WriteString("[truncated]")
				break
			}
			out.WriteString(formatTextValue(value.Field(i), depth+1, remaining))
		}
		out.WriteByte('}')
		return out.String()
	default:
		return fmt.Sprint(value.Interface())
	}
}
