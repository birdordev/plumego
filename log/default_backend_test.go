package log

import (
	"flag"
	"fmt"
	stdlog "log"
	"os"
	"strings"
)

type initConfig struct {
	LogDir          string
	AlsoLogToStderr bool
	LogToStderr     bool
	Verbosity       int
	VModule         string
	LogBacktraceAt  string
}

var (
	logDir          = flag.String("log_dir", "", "If non-empty, write log files in this directory")
	alsoLogToStderr = flag.Bool("alsologtostderr", false, "log to standard error as well as files")
	logToStderr     = flag.Bool("logtostderr", false, "log to standard error instead of files")
	verbosity       = flag.Int("v", 0, "log level for V logs")
	vmodule         = flag.String("vmodule", "", "comma-separated list of pattern=N settings for file-filtered logging")
	logBacktraceAt  = flag.String("log_backtrace_at", "", "when logging hits line file:N, emit a stack trace")
	std             = newGLogger()
)

func initDefaultFromFlags() {
	if !flag.Parsed() {
		flag.Parse()
	}

	if err := initDefaultWithConfig(initConfig{
		LogDir:          *logDir,
		AlsoLogToStderr: *alsoLogToStderr,
		LogToStderr:     *logToStderr,
		Verbosity:       *verbosity,
		VModule:         *vmodule,
		LogBacktraceAt:  *logBacktraceAt,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
	}
}

func initDefaultWithConfig(cfg initConfig) error {
	std.mu.Lock()
	defer std.mu.Unlock()

	std.logDir = cfg.LogDir
	if std.logDir != "" {
		if err := std.initLogFiles(); err != nil {
			return err
		}
	}

	std.toStderr = cfg.LogToStderr
	std.alsoToStderr = cfg.AlsoLogToStderr
	std.verbosity = cfg.Verbosity
	std.logBacktraceAt = cfg.LogBacktraceAt

	if cfg.VModule != "" {
		std.parseVmodule(cfg.VModule)
	} else {
		std.vmodulePatterns = nil
	}

	if std.toStderr {
		std.output = os.Stderr
	}
	std.rebuildWriterCache()
	return nil
}

func vDefault(level int) bool {
	return std.vAt(level, 2)
}

func infoDefault(args ...any) {
	std.log(INFO, 2, args...)
}

func infofDefault(format string, args ...any) {
	std.logf(INFO, 2, format, args...)
}

func infolnDefault(args ...any) {
	std.log(INFO, 2, fmt.Sprintln(args...))
}

func warningDefault(args ...any) {
	std.log(WARNING, 2, args...)
}

func warningfDefault(format string, args ...any) {
	std.logf(WARNING, 2, format, args...)
}

func warninglnDefault(args ...any) {
	std.log(WARNING, 2, fmt.Sprintln(args...))
}

func errorDefault(args ...any) {
	std.log(ERROR, 2, args...)
}

func errorfDefault(format string, args ...any) {
	std.logf(ERROR, 2, format, args...)
}

func errorlnDefault(args ...any) {
	std.log(ERROR, 2, fmt.Sprintln(args...))
}

func vlogDefault(level int, args ...any) {
	if std.vAt(level, 2) {
		std.log(INFO, 2, args...)
	}
}

func flushDefault() {
	std.Flush()
}

func closeDefault() {
	std.Close()
}

func copyStandardLogTo(level Level) {
	stdlog.SetOutput(&logWriter{level: level})
	stdlog.SetFlags(0)
}

type logWriter struct {
	level Level
}

func (w *logWriter) Write(p []byte) (n int, err error) {
	msg := strings.TrimRight(string(p), "\n")
	std.log(w.level, 3, msg)
	return len(p), nil
}
