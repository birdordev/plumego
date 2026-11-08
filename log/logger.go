package log

import (
	"context"
	"fmt"
	"io"
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
	text := fmt.Sprint(value)
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
