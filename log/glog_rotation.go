package log

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type rotationConfig struct {
	MaxSize    int // Maximum size in MB before rotation
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
		_ = os.Remove(linkPath)
		if err := os.Symlink(filename, linkPath); err != nil {
			return fmt.Errorf("failed to create symlink %s: %w", linkPath, err)
		}
	}

	cleanupOldLogs(l.logDir, l.program, l.rotationConfig, currentLogFiles(l.logFiles))
	l.rebuildWriterCache()
	initialized = true
	return nil
}

func (l *gLogger) fileLevelsForLog(level Level) []Level {
	l.mu.RLock()
	defer l.mu.RUnlock()

	if l.logDir == "" || l.toStderr {
		return nil
	}

	levels := make([]Level, 0, 4)
	if l.logFiles[level] != nil {
		levels = append(levels, level)
	}
	for lv := INFO; lv < level; lv++ {
		if l.logFiles[lv] != nil {
			levels = append(levels, lv)
		}
	}
	return levels
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
	if config.MaxSize <= 0 {
		return
	}
	l.currentSize[level] += logSize
	if l.currentSize[level] < int64(config.MaxSize)*1024*1024 {
		return
	}
	l.currentSize[level] = 0
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
	// Close the current log file
	if file, ok := l.logFiles[level]; ok {
		_ = file.Close()
	}

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

	// Update log file map
	l.logFiles[level] = file
	l.currentSize[level] = 0

	// The log file changed, so cached writers for this level are stale.
	l.rebuildWriterCache()

	// Update symlink to point to the newest log file.
	// Symlink creation is best-effort: failures are logged to stderr but do not
	// abort the rotation because the log file itself was opened successfully.
	linkName := fmt.Sprintf("%s.%s", l.program, lname)
	linkPath := filepath.Join(l.logDir, linkName)
	_ = os.Remove(linkPath) // ignore removal error; the next Symlink may still succeed
	if err := os.Symlink(filename, linkPath); err != nil {
		// Write directly to stderr to avoid recursion through the logger.
		fmt.Fprintf(os.Stderr, "log: failed to update symlink %s → %s: %v\n", linkPath, filename, err)
	}

	return nil
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

		if config.MaxAge > 0 {
			maxAge := time.Duration(config.MaxAge) * 24 * time.Hour
			if now.Sub(info.ModTime()) > maxAge {
				if err := os.Remove(filepath.Join(logDir, name)); err != nil && !os.IsNotExist(err) {
					fmt.Fprintf(os.Stderr, "log: failed to remove aged log %s: %v\n", name, err)
				}
				continue
			}
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
	for level := INFO; level <= ERROR; level++ {
		if strings.Contains(name, "."+levelName(level)+".") {
			return level, true
		}
	}
	return INFO, false
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
