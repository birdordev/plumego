package log

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// Level represents a log severity. Higher values denote more severe events.
type Level int

// Log severity levels in ascending order. DEBUG is the least severe and FATAL
// the most; log calls below the configured level are suppressed.
const (
	DEBUG   Level = -1
	INFO    Level = 0
	WARNING Level = 1
	ERROR   Level = 2
	FATAL   Level = 3
)

var levelNames = map[Level]string{
	DEBUG:   "DEBUG",
	INFO:    "INFO",
	WARNING: "WARN",
	ERROR:   "ERROR",
	FATAL:   "FATAL",
}

type vmodulePattern struct {
	pattern string
	level   int
}

type gLogger struct {
	mu              sync.RWMutex
	writeMu         sync.Mutex
	level           Level
	output          io.Writer
	errorOutput     io.Writer
	toStderr        bool
	alsoToStderr    bool
	verbosity       int
	vmodulePatterns []vmodulePattern
	logBacktraceAt  string
	logFiles        map[Level]*os.File
	logDir          string
	program         string
	rotationConfig  rotationConfig
	currentSize     map[Level]int64
	writeErrOnce    sync.Once
	// cachedWriters caches the per-level io.Writer (possibly an io.MultiWriter)
	// so getLogWriter does not allocate on every log call.
	// Invalidated whenever logFiles, alsoToStderr, toStderr, or output changes.
	cachedWriters map[Level]io.Writer
}

// pid is cached at package init to avoid a getpid syscall on every log line.
var pid = os.Getpid()

func newGLogger() *gLogger {
	return &gLogger{
		level:        INFO,
		output:       os.Stderr,
		toStderr:     true,
		alsoToStderr: false,
		verbosity:    0,
		logFiles:     make(map[Level]*os.File),
		currentSize:  make(map[Level]int64),
		program:      filepath.Base(os.Args[0]),
	}
}

func (l *gLogger) parseVmodule(vmodule string) {
	l.vmodulePatterns = nil
	for _, item := range strings.Split(vmodule, ",") {
		if item == "" {
			continue
		}
		parts := strings.Split(item, "=")
		if len(parts) != 2 {
			continue
		}
		pattern := strings.TrimSpace(parts[0])
		levelStr := strings.TrimSpace(parts[1])
		level, err := strconv.Atoi(levelStr)
		if err != nil {
			continue
		}
		l.vmodulePatterns = append(l.vmodulePatterns, vmodulePattern{
			pattern: pattern,
			level:   level,
		})
	}
}

func (l *gLogger) getVerbosityForFile(file string) int {
	base := filepath.Base(file)
	base = strings.TrimSuffix(base, ".go")
	for _, pattern := range l.vmodulePatterns {
		if matched, _ := filepath.Match(pattern.pattern, base); matched {
			return pattern.level
		}
	}

	return l.verbosity
}

func (l *gLogger) shouldLogBacktrace(file string, line int) bool {
	if l.logBacktraceAt == "" {
		return false
	}

	target := fmt.Sprintf("%s:%d", filepath.Base(file), line)
	return target == l.logBacktraceAt
}

func (l *gLogger) SetLevel(level Level) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = level
}

func (l *gLogger) SetOutput(w io.Writer) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if w == nil {
		w = os.Stderr
	}
	l.output = w
	l.rebuildWriterCache()
}

func (l *gLogger) SetErrorOutput(w io.Writer) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.errorOutput = w
}

func (l *gLogger) SetVerbose(v int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.verbosity = v
}

func (l *gLogger) vAt(level int, calldepth int) bool {
	_, file, _, ok := runtime.Caller(calldepth)
	if !ok {
		return level <= l.verbosity
	}

	l.mu.RLock()
	verbosity := l.getVerbosityForFile(file)
	l.mu.RUnlock()

	return level <= verbosity
}

func (l *gLogger) V(level int) bool {
	return l.vAt(level, 2)
}

func (l *gLogger) Info(args ...any) {
	l.log(INFO, 2, args...)
}

func (l *gLogger) Infof(format string, args ...any) {
	l.logf(INFO, 2, format, args...)
}

func (l *gLogger) Infoln(args ...any) {
	l.log(INFO, 2, fmt.Sprintln(args...))
}

func (l *gLogger) Warning(args ...any) {
	l.log(WARNING, 2, args...)
}

func (l *gLogger) Warningf(format string, args ...any) {
	l.logf(WARNING, 2, format, args...)
}

func (l *gLogger) Warningln(args ...any) {
	l.log(WARNING, 2, fmt.Sprintln(args...))
}

func (l *gLogger) Error(args ...any) {
	l.log(ERROR, 2, args...)
}

func (l *gLogger) Errorf(format string, args ...any) {
	l.logf(ERROR, 2, format, args...)
}

func (l *gLogger) Errorln(args ...any) {
	l.log(ERROR, 2, fmt.Sprintln(args...))
}

func (l *gLogger) Fatal(args ...any) {
	l.log(FATAL, 2, args...)
}

func (l *gLogger) Fatalf(format string, args ...any) {
	l.logf(FATAL, 2, format, args...)
}

func (l *gLogger) Fatalln(args ...any) {
	l.log(FATAL, 2, fmt.Sprintln(args...))
}

func (l *gLogger) VLog(level int, args ...any) {
	if l.vAt(level, 2) {
		l.log(INFO, 2, args...)
	}
}

func (l *gLogger) VLogf(level int, format string, args ...any) {
	if l.vAt(level, 2) {
		l.logf(INFO, 2, format, args...)
	}
}
