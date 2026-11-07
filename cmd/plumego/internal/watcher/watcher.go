package watcher

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const defaultPollInterval = 500 * time.Millisecond

// Options configures the dependency-free polling watcher.
type Options struct {
	PollInterval time.Duration
}

// Watcher watches for file changes
type Watcher struct {
	dir          string
	include      []string
	exclude      []string
	debounce     time.Duration
	pollInterval time.Duration

	events chan string
	errors chan error
	done   chan struct{}

	lastChange time.Time
	pending    map[string]struct{}
	closeOnce  sync.Once
}

// NewWatcher creates a new file watcher
func NewWatcher(dir string, include, exclude []string, debounce time.Duration) (*Watcher, error) {
	return NewWatcherWithOptions(dir, include, exclude, debounce, Options{})
}

// NewWatcherWithOptions creates a new file watcher with explicit polling options.
func NewWatcherWithOptions(dir string, include, exclude []string, debounce time.Duration, opts Options) (*Watcher, error) {
	if debounce <= 0 {
		return nil, fmt.Errorf("debounce must be positive")
	}
	pollInterval := opts.PollInterval
	if pollInterval == 0 {
		pollInterval = defaultPollInterval
	}
	if pollInterval <= 0 {
		return nil, fmt.Errorf("poll interval must be positive")
	}

	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(absDir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("watch path is not a directory: %s", absDir)
	}

	w := &Watcher{
		dir:          absDir,
		include:      include,
		exclude:      exclude,
		debounce:     debounce,
		pollInterval: pollInterval,
		events:       make(chan string, 10),
		errors:       make(chan error, 10),
		done:         make(chan struct{}),
		pending:      make(map[string]struct{}),
	}

	// Build the initial file snapshot synchronously so that a file changed
	// immediately after construction is detected on the next poll. Scanning
	// inside the watch goroutine would race the caller's first modification.
	fileModTimes := make(map[string]time.Time)
	w.scanFiles(fileModTimes)

	go w.watch(fileModTimes)

	return w, nil
}

// Events returns the events channel
func (w *Watcher) Events() <-chan string {
	return w.events
}

// Errors returns the errors channel
func (w *Watcher) Errors() <-chan error {
	return w.errors
}

// Close stops the watcher
func (w *Watcher) Close() error {
	w.closeOnce.Do(func() {
		close(w.done)
	})
	return nil
}

func (w *Watcher) watch(fileModTimes map[string]time.Time) {
	defer close(w.events)
	defer close(w.errors)

	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			w.scanFiles(fileModTimes)

			// Check if we should emit a pending event
			if len(w.pending) > 0 && time.Since(w.lastChange) > w.debounce {
				w.emitPending()
			}

		case <-w.done:
			return
		}
	}
}

func (w *Watcher) scanFiles(modTimes map[string]time.Time) {
	seen := make(map[string]struct{})

	filepath.Walk(w.dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if path != w.dir && os.IsNotExist(err) {
				return nil
			}
			w.reportError(err)
			return nil
		}

		// Skip directories
		if info.IsDir() {
			// Check if we should skip this directory
			relPath, _ := filepath.Rel(w.dir, path)
			if w.shouldExclude(relPath) {
				return filepath.SkipDir
			}
			return nil
		}

		// Get relative path
		relPath, err := filepath.Rel(w.dir, path)
		if err != nil {
			return nil
		}

		// Check if file matches patterns
		if !w.shouldWatch(relPath) {
			return nil
		}
		seen[path] = struct{}{}

		// Check modification time
		modTime := info.ModTime()
		lastModTime, exists := modTimes[path]

		if !exists {
			// New file
			modTimes[path] = modTime
		} else if modTime.After(lastModTime) {
			// File modified
			modTimes[path] = modTime
			w.queueChange(relPath)
		}

		return nil
	})

	for path := range modTimes {
		if _, ok := seen[path]; ok {
			continue
		}
		relPath, err := filepath.Rel(w.dir, path)
		if err != nil {
			delete(modTimes, path)
			continue
		}
		if w.shouldWatch(relPath) {
			w.queueChange(relPath)
		}
		delete(modTimes, path)
	}
}

func (w *Watcher) queueChange(path string) {
	w.lastChange = time.Now()
	w.pending[path] = struct{}{}
}

func (w *Watcher) emitPending() {
	paths := make([]string, 0, len(w.pending))
	for path := range w.pending {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	w.pending = make(map[string]struct{})

	for _, path := range paths {
		select {
		case w.events <- path:
		case <-w.done:
			return
		}
	}
}

func (w *Watcher) reportError(err error) {
	select {
	case w.errors <- err:
	default:
	}
}

func (w *Watcher) shouldWatch(path string) bool {
	// Check includes
	if len(w.include) > 0 {
		matched := false
		for _, pattern := range w.include {
			if matchPattern(pattern, path) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	// Check excludes
	if w.shouldExclude(path) {
		return false
	}

	return true
}

func (w *Watcher) shouldExclude(path string) bool {
	for _, pattern := range w.exclude {
		if matchPattern(pattern, path) {
			return true
		}
	}
	return false
}

func matchPattern(pattern, path string) bool {
	pattern = filepath.ToSlash(pattern)
	path = filepath.ToSlash(path)

	// Simple pattern matching
	// Supports:
	//   - **/*.go (recursive)
	//   - *.go (single level)
	//   - dir/file.go (exact match)

	// Handle ** (recursive match)
	if strings.Contains(pattern, "**") {
		if strings.HasPrefix(pattern, "**/") && strings.HasSuffix(pattern, "/**") {
			segment := strings.TrimSuffix(strings.TrimPrefix(pattern, "**/"), "/**")
			return path == segment || strings.HasPrefix(path, segment+"/") || strings.Contains(path, "/"+segment+"/")
		}

		parts := strings.Split(pattern, "**")
		if len(parts) == 2 {
			prefix := strings.TrimSuffix(parts[0], "/")
			suffix := strings.TrimPrefix(parts[1], "/")

			if prefix != "" && !strings.HasPrefix(path, prefix) {
				return false
			}

			if suffix == "" {
				return true
			}

			// Match suffix
			if strings.HasPrefix(suffix, "*") {
				ext := strings.TrimPrefix(suffix, "*")
				return strings.HasSuffix(path, ext)
			}

			return strings.HasSuffix(path, suffix)
		}
	}

	// Handle * (single level)
	if strings.Contains(pattern, "*") {
		if strings.HasPrefix(pattern, "*.") {
			ext := strings.TrimPrefix(pattern, "*")
			return strings.HasSuffix(path, ext)
		}

		// Simple glob matching
		return simpleGlob(pattern, path)
	}

	// Exact match
	return pattern == path
}

func simpleGlob(pattern, str string) bool {
	// Very simple glob implementation
	if pattern == "*" {
		return true
	}

	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == str
	}

	// Check prefix
	if parts[0] != "" && !strings.HasPrefix(str, parts[0]) {
		return false
	}

	// Check suffix
	if parts[len(parts)-1] != "" && !strings.HasSuffix(str, parts[len(parts)-1]) {
		return false
	}

	// Check middle parts
	currentPos := len(parts[0])
	for i := 1; i < len(parts)-1; i++ {
		part := parts[i]
		if part == "" {
			continue
		}

		idx := strings.Index(str[currentPos:], part)
		if idx == -1 {
			return false
		}
		currentPos += idx + len(part)
	}

	return true
}
