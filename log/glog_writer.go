package log

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// logBufferPool recycles log header buffers to reduce allocations.
// The pool stores *[]byte (not []byte) so Put/Get avoid boxing the slice
// header into an interface allocation on every log line.
var logBufferPool = sync.Pool{
	New: func() any {
		b := make([]byte, 0, 1024) // Preallocate 1KB buffer
		return &b
	},
}

// appendDecimal writes v as a base-10 integer into b, right-aligned to width
// digits with the given pad byte (typically '0'). Values wider than width are
// written in full. The result appends without allocating.
func appendDecimal(b []byte, v int, width int, pad byte) []byte {
	digits := 1
	for tmp := v / 10; tmp > 0; tmp /= 10 {
		digits++
	}
	for i := digits; i < width; i++ {
		b = append(b, pad)
	}
	return strconv.AppendInt(b, int64(v), 10)
}

// formatHeader builds the log header into a pooled buffer and returns the
// pool entry pointer. The caller must return it via releaseHeaderBuffer once
// the header (and any slice appended onto it) is no longer needed.
func (l *gLogger) formatHeader(level Level, file string, line int) *[]byte {
	bufp := logBufferPool.Get().(*[]byte)
	buf := (*bufp)[:0] // Reset buffer to empty

	now := time.Now()
	if idx := strings.LastIndex(file, "/"); idx >= 0 {
		file = file[idx+1:]
	}

	// Build header directly into the pooled buffer. Format:
	// MMDD HH:MM:SS.microseconds %7d [file:line]
	buf = append(buf, levelInitial(level)) // First letter of level
	buf = appendDecimal(buf, int(now.Month()), 2, '0')
	buf = appendDecimal(buf, now.Day(), 2, '0')
	buf = append(buf, ' ')
	buf = appendDecimal(buf, now.Hour(), 2, '0')
	buf = append(buf, ':')
	buf = appendDecimal(buf, now.Minute(), 2, '0')
	buf = append(buf, ':')
	buf = appendDecimal(buf, now.Second(), 2, '0')
	buf = append(buf, '.')
	buf = appendDecimal(buf, now.Nanosecond()/1000, 6, '0')
	buf = append(buf, ' ')
	buf = appendDecimal(buf, pid, 7, ' ')
	buf = append(buf, ' ')
	buf = append(buf, '[')
	buf = append(buf, file...)
	buf = append(buf, ':')
	buf = strconv.AppendInt(buf, int64(line), 10)
	buf = append(buf, ']')
	buf = append(buf, ' ')

	*bufp = buf
	return bufp
}

// releaseHeaderBuffer resets a header buffer obtained from formatHeader and
// returns it to the pool.
func releaseHeaderBuffer(bufp *[]byte) {
	// Do not pool buffers that grew far beyond the preallocated size; a single
	// huge log line would otherwise pin the memory in the pool.
	if cap(*bufp) > 64*1024 {
		return
	}
	*bufp = (*bufp)[:0]
	logBufferPool.Put(bufp)
}

type logFileDestination struct {
	level  Level
	writer io.Writer
}

type logDestinations struct {
	files  []logFileDestination
	output io.Writer
}

// rebuildWriterCache publishes the ordered destinations for each log level.
// Call under l.mu write lock whenever logFiles, alsoToStderr, toStderr, or
// output changes.
func (l *gLogger) rebuildWriterCache() {
	if l.toStderr {
		// All levels go to stderr; no need to cache per-level.
		l.cachedWriters = nil
		return
	}
	cache := make(map[Level]logDestinations, 4)
	for level := INFO; level <= FATAL; level++ {
		var destinations logDestinations
		if file := l.logFiles[level]; l.logDir != "" && file != nil {
			destinations.files = append(destinations.files, logFileDestination{level, file})
			for lv := INFO; lv < level; lv++ {
				if file := l.logFiles[lv]; file != nil {
					destinations.files = append(destinations.files, logFileDestination{lv, file})
				}
			}
		}
		if l.alsoToStderr || len(destinations.files) == 0 {
			destinations.output = l.stderrWriter()
		}
		cache[level] = destinations
	}
	l.cachedWriters = cache
}

// getLogDestinations returns the effective targets for the given level.
// Must be called with at least l.mu read lock held.
func (l *gLogger) getLogDestinations(level Level) logDestinations {
	if level >= ERROR && l.errorOutput != nil {
		return logDestinations{output: l.errorOutput}
	}
	if !l.toStderr {
		if destinations, ok := l.cachedWriters[level]; ok {
			return destinations
		}
	}
	return logDestinations{output: l.stderrWriter()}
}

func (d logDestinations) write(data []byte, fileBytes *[FATAL + 1]int64) error {
	for _, file := range d.files {
		n, err := writeBytes(file.writer, data)
		fileBytes[file.level] += int64(n)
		if err != nil {
			return err
		}
	}
	if d.output != nil {
		return writeFull(d.output, data)
	}
	return nil
}

func (l *gLogger) stderrWriter() io.Writer {
	if l.output != nil {
		return l.output
	}
	return os.Stderr
}

func (l *gLogger) log(level Level, calldepth int, args ...any) {
	l.logInternal(level, calldepth+1, func() string {
		return fmt.Sprint(args...)
	})
}

func (l *gLogger) logf(level Level, calldepth int, format string, args ...any) {
	l.logInternal(level, calldepth+1, func() string {
		return fmt.Sprintf(format, args...)
	})
}

func (l *gLogger) logInternal(level Level, calldepth int, messageBuilder func() string) {
	// First check if we should log at this level to avoid unnecessary work
	l.mu.RLock()
	shouldLog := level >= l.level
	l.mu.RUnlock()

	if !shouldLog {
		if level == FATAL {
			l.Flush()
			os.Exit(1)
		}
		return
	}

	// Get caller information
	_, file, line, ok := runtime.Caller(calldepth)
	if !ok {
		file = "???"
		line = 1
	}

	// Format header and message
	headerBuf := l.formatHeader(level, file, line)
	message := escapeLogMessage(strings.TrimSuffix(messageBuilder(), "\n"))
	logLine := append(*headerBuf, message...)
	logLine = append(logLine, '\n')

	l.writeMu.Lock()
	defer l.writeMu.Unlock()

	// Snapshot writer state under writeMu so Close cannot close files between
	// selecting a writer and writing through it.
	l.mu.RLock()
	destinations := l.getLogDestinations(level)
	shouldBacktrace := l.shouldLogBacktrace(file, line)
	l.mu.RUnlock()

	var fileBytes [FATAL + 1]int64
	if err := destinations.write(logLine, &fileBytes); err != nil {
		l.reportWriteError(err)
	}

	// Return buffer to pool. logLine may alias the header buffer's backing
	// array, so this must stay after logLine's last use.
	releaseHeaderBuffer(headerBuf)

	// Handle backtrace if needed
	if shouldBacktrace {
		stack := make([]byte, 4096)
		stack = stack[:runtime.Stack(stack, false)]
		if err := destinations.write(stack, &fileBytes); err != nil {
			l.reportWriteError(err)
		}
	}

	// Rotate only after the entire record has used the current writer.
	for _, file := range destinations.files {
		if size := fileBytes[file.level]; size > 0 {
			_ = l.checkLogRotation(file.level, size)
		}
	}

	// Handle fatal errors
	if level == FATAL {
		l.Flush()
		os.Exit(1)
	}
}

func (l *gLogger) reportWriteError(_ error) {
	l.writeErrOnce.Do(func() {
		_, _ = os.Stderr.WriteString("glog: failed to write log output\n")
	})
}

func writeFull(w io.Writer, data []byte) error {
	_, err := writeBytes(w, data)
	return err
}

func writeBytes(w io.Writer, data []byte) (int, error) {
	n, err := w.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	return n, err
}

func escapeLogMessage(message string) string {
	var escaped strings.Builder
	start := 0
	for offset := 0; offset < len(message); {
		r, width := utf8.DecodeRuneInString(message[offset:])
		next := offset + width
		invalid := r == utf8.RuneError && width == 1
		if invalid || unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			escaped.WriteString(message[start:offset])
			if invalid {
				escaped.WriteRune(utf8.RuneError)
			} else {
				quoted := strconv.QuoteRune(r)
				escaped.WriteString(quoted[1 : len(quoted)-1])
			}
			start = next
		}
		offset = next
	}
	if start == 0 {
		return message
	}
	escaped.WriteString(message[start:])
	return escaped.String()
}
