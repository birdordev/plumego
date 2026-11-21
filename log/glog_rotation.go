package log

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type rotationConfig struct {
	MaxSize    int // Maximum size in MiB before rotation
	MaxAge     int // Maximum days to retain old log files
	MaxBackups int // Maximum number of old log files to retain
}

func (l *gLogger) initLogFiles() error {
	if err := os.MkdirAll(l.logDir, 0755); err != nil {
		return err
	}
	initialized := false
	openedLevels := make([]Level, 0, 3)
	defer func() {
		if initialized {
			return
		}
		for _, level := range openedLevels {
			if file := l.logFiles[level]; file != nil {
				_ = file.Close()
				delete(l.logFiles, level)
				delete(l.currentSize, level)
			}
		}
		l.rebuildWriterCache()
	}()

	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "unknown"
	}

	currentPID := pid
	now := time.Now()

	for level := INFO; level <= ERROR; level++ {
		lname := levelName(level)
		filename := logFilename(l.program, hostname, lname, now, currentPID)

		logPath := filepath.Join(l.logDir, filename)
		file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return err
		}
		l.logFiles[level] = file
		openedLevels = append(openedLevels, level)
		if info, err := file.Stat(); err == nil {
			l.currentSize[level] = info.Size()
		} else {
			l.currentSize[level] = 0
		}

		linkName := fmt.Sprintf("%s.%s", l.program, lname)
		linkPath := filepath.Join(l.logDir, linkName)
		if err := replaceLogSymlink(filename, linkPath); err != nil {
			return fmt.Errorf("failed to create symlink %s: %w", linkPath, err)
		}
	}

	cleanupOldLogs(l.logDir, l.program, l.rotationConfig, currentLogFiles(l.logFiles))
	l.rebuildWriterCache()
	initialized = true
	return nil
}

// checkLogRotation checks if the log file needs rotation based on size.
func (l *gLogger) checkLogRotation(level Level, logSize int64) error {
	logDir, program, config, current, rotated, err := l.rotateIfNeeded(level, logSize)
	if err != nil || !rotated {
		return err
	}
	cleanupOldLogs(logDir, program, config, current)
	return nil
}

// rotateIfNeeded checks rotation under a single deferred lock and reports
// whether a rotation actually happened (rotated=true) along with the data
// needed to run cleanupOldLogs outside the lock.
func (l *gLogger) rotateIfNeeded(level Level, logSize int64) (logDir, program string, config rotationConfig, current map[string]struct{}, rotated bool, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	config = l.rotationConfig
	l.currentSize[level] += logSize
	if config.MaxSize <= 0 {
		return
	}
	// Convert bytes to whole MiB so a large configured threshold cannot overflow.
	if l.currentSize[level]/(1<<20) < int64(config.MaxSize) {
		return
	}
	if err = l.rotateLogFile(level); err != nil {
		return
	}
	logDir = l.logDir
	program = l.program
	current = currentLogFiles(l.logFiles)
	rotated = true
	return
}

// rotateLogFile rotates the log file for the given level
func (l *gLogger) rotateLogFile(level Level) error {
	// Generate new log filename with timestamp
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "unknown"
	}

	currentPID := pid
	now := time.Now()

	lname := levelName(level)
	filename := logFilename(l.program, hostname, lname, now, currentPID)

	logPath := filepath.Join(l.logDir, filename)
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}

	// Keep the current writer usable until its replacement opens successfully.
	previous := l.logFiles[level]
	l.logFiles[level] = file
	l.currentSize[level] = 0

	// The log file changed, so cached writers for this level are stale.
	l.rebuildWriterCache()
	if previous != nil {
		_ = previous.Close()
	}

	// Update symlink to point to the newest log file.
	// Symlink creation is best-effort: failures are logged to stderr but do not
	// abort the rotation because the log file itself was opened successfully.
	linkName := fmt.Sprintf("%s.%s", l.program, lname)
	linkPath := filepath.Join(l.logDir, linkName)
	if err := replaceLogSymlink(filename, linkPath); err != nil {
		// Write directly to stderr to avoid recursion through the logger.
		fmt.Fprintf(os.Stderr, "log: failed to update symlink %s → %s: %v\n", linkPath, filename, err)
	}

	return nil
}

func replaceLogSymlink(target, path string) error {
	// A private directory on the same filesystem keeps the old link in place
	// until the replacement is ready to publish. The relative target resolves
	// against the log directory after the rename.
	dir, err := os.MkdirTemp(filepath.Dir(path), ".log-link-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	staged := filepath.Join(dir, "active")
	if err := os.Symlink(target, staged); err != nil {
		return err
	}
	return os.Rename(staged, path)
}

type logFileInfo struct {
	name    string
	path    string
	modTime time.Time
}

func logFilename(program, hostname, lname string, now time.Time, currentPID int) string {
	return fmt.Sprintf("%s.%s.%s.%04d%02d%02d-%02d%02d%02d.%09d.%d.log",
		program, hostname, lname,
		now.Year(), now.Month(), now.Day(),
		now.Hour(), now.Minute(), now.Second(),
		now.Nanosecond(), currentPID)
}

func currentLogFiles(files map[Level]*os.File) map[string]struct{} {
	current := make(map[string]struct{}, len(files))
	for _, file := range files {
		if file == nil {
			continue
		}
		current[filepath.Base(file.Name())] = struct{}{}
	}
	return current
}

func cleanupOldLogs(logDir, program string, config rotationConfig, current map[string]struct{}) {
	if logDir == "" {
		return
	}
	if config.MaxAge <= 0 && config.MaxBackups <= 0 {
		return
	}

	entries, err := os.ReadDir(logDir)
	if err != nil {
		return
	}

	now := time.Now()
	const day = 24 * time.Hour
	var maxAge time.Duration
	if config.MaxAge > 0 {
		// A retention period beyond Duration's range cannot expire an archive by age.
		maxAge = time.Duration(math.MaxInt64)
		if config.MaxAge <= int(maxAge/day) {
			maxAge = time.Duration(config.MaxAge) * day
		}
	}
	// 活动链接发布失败时可能仍指向上一文件，
	// 即使 writer 已经切换。
	linked := make(map[string]struct{})
	for level := INFO; level <= ERROR; level++ {
		target, err := os.Readlink(filepath.Join(logDir, program+"."+levelName(level)))
		if err == nil && filepath.Base(target) == target {
			linked[target] = struct{}{}
		}
	}
	perLevel := make(map[Level][]logFileInfo)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}

		name := entry.Name()
		if _, ok := current[name]; ok {
			continue
		}
		if _, ok := linked[name]; ok {
			continue
		}
		if !strings.HasSuffix(name, ".log") {
			continue
		}
		if !strings.HasPrefix(name, program+".") {
			continue
		}

		level, ok := levelFromFilename(name)
		if !ok {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		if maxAge > 0 && now.Sub(info.ModTime()) > maxAge {
			if err := os.Remove(filepath.Join(logDir, name)); err != nil && !os.IsNotExist(err) {
				fmt.Fprintf(os.Stderr, "log: failed to remove aged log %s: %v\n", name, err)
			}
			continue
		}

		if config.MaxBackups > 0 {
			perLevel[level] = append(perLevel[level], logFileInfo{
				name:    name,
				path:    filepath.Join(logDir, name),
				modTime: info.ModTime(),
			})
		}
	}

	if config.MaxBackups <= 0 {
		return
	}

	for _, files := range perLevel {
		sort.Slice(files, func(i, j int) bool {
			return files[i].modTime.After(files[j].modTime)
		})
		for i := config.MaxBackups; i < len(files); i++ {
			if err := os.Remove(files[i].path); err != nil && !os.IsNotExist(err) {
				fmt.Fprintf(os.Stderr, "log: failed to remove excess log %s: %v\n", files[i].name, err)
			}
		}
	}
}

func levelFromFilename(name string) (Level, bool) {
	base, ok := strings.CutSuffix(name, ".log")
	if !ok {
		return INFO, false
	}
	prefix, pid, ok := cutLastDot(base)
	if !ok || pid == "" || strings.Trim(pid, "0123456789") != "" {
		return INFO, false
	}
	prefix, timestamp, ok := cutLastDot(prefix)
	if !ok {
		return INFO, false
	}
	// Current filenames include fractional seconds before the PID. Older
	// archives put the PID immediately after the timestamp.
	if fraction := timestamp; len(fraction) > 0 && len(fraction) <= 9 && strings.Trim(fraction, "0123456789") == "" {
		prefix, timestamp, ok = cutLastDot(prefix)
		if !ok {
			return INFO, false
		}
	}
	prefix, label, ok := cutLastDot(prefix)
	// The prefix contains at least the program and host segments.
	if !ok || !strings.Contains(prefix, ".") {
		return INFO, false
	}
	if _, err := time.Parse("20060102-150405", timestamp); err != nil {
		return INFO, false
	}
	for level := INFO; level <= ERROR; level++ {
		if label == levelName(level) {
			return level, true
		}
	}
	return INFO, false
}

func cutLastDot(s string) (string, string, bool) {
	i := strings.LastIndexByte(s, '.')
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+1:], true
}

func levelName(level Level) string {
	if name, ok := levelNames[level]; ok {
		return name
	}
	return "INFO"
}

func levelInitial(level Level) byte {
	switch level {
	case DEBUG:
		return 'D'
	case INFO:
		return 'I'
	case WARNING:
		return 'W'
	case ERROR:
		return 'E'
	case FATAL:
		return 'F'
	default:
		return 'I'
	}
}
