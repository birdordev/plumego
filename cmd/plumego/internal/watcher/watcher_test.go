package watcher

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func TestShouldWatchIncludeAndExcludePatterns(t *testing.T) {
	w := &Watcher{
		include: []string{"**/*.go"},
		exclude: []string{"**/vendor/**", "**/*_test.go"},
	}

	tests := []struct {
		path string
		want bool
	}{
		{path: "main.go", want: true},
		{path: "internal/app/routes.go", want: true},
		{path: "internal/app/routes_test.go", want: false},
		{path: "vendor/example/main.go", want: false},
		{path: "README.md", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := w.shouldWatch(tt.path); got != tt.want {
				t.Fatalf("shouldWatch(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestMatchPatternRecursiveIncludesAndExcludes(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		path    string
		want    bool
	}{
		{name: "recursive go root", pattern: "**/*.go", path: "main.go", want: true},
		{name: "recursive go nested", pattern: "**/*.go", path: "internal/app/routes.go", want: true},
		{name: "recursive dir exact", pattern: "**/vendor/**", path: "vendor", want: true},
		{name: "recursive dir nested", pattern: "**/vendor/**", path: "internal/vendor/pkg/file.go", want: true},
		{name: "suffix test", pattern: "**/*_test.go", path: "internal/app/routes_test.go", want: true},
		{name: "suffix nonmatch", pattern: "**/*_test.go", path: "internal/app/routes.go", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchPattern(tt.pattern, tt.path); got != tt.want {
				t.Fatalf("matchPattern(%q, %q) = %v, want %v", tt.pattern, tt.path, got, tt.want)
			}
		})
	}
}

func TestNewWatcherRejectsInvalidDurations(t *testing.T) {
	tmp := t.TempDir()
	if _, err := NewWatcher(tmp, []string{"**/*.go"}, nil, 0); err == nil {
		t.Fatal("expected zero debounce to fail")
	}
	if _, err := NewWatcherWithOptions(tmp, []string{"**/*.go"}, nil, 10*time.Millisecond, Options{PollInterval: -time.Millisecond}); err == nil {
		t.Fatal("expected negative poll interval to fail")
	}
}

func TestWatcherEmitsDebouncedModifyEvent(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0644); err != nil {
		t.Fatalf("write initial file: %v", err)
	}

	w, err := newFastWatcher(tmp, []string{"**/*.go"}, nil, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("new watcher: %v", err)
	}
	defer w.Close()

	if err := os.WriteFile(path, []byte("package main\n\nfunc main() {}\n"), 0644); err != nil {
		t.Fatalf("modify file: %v", err)
	}

	select {
	case got := <-w.Events():
		if got != "main.go" {
			t.Fatalf("event = %q, want main.go", got)
		}
	case err := <-w.Errors():
		t.Fatalf("watcher error: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for watcher event")
	}
}

func TestWatcherCloseIsIdempotentAndClosesChannels(t *testing.T) {
	w, err := newFastWatcher(t.TempDir(), []string{"**/*.go"}, nil, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("new watcher: %v", err)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}

	select {
	case _, ok := <-w.Events():
		if ok {
			t.Fatal("events channel should be closed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for events channel close")
	}
}

func TestWatcherEmitsMultipleDebouncedModifyEvents(t *testing.T) {
	tmp := t.TempDir()
	paths := []string{
		filepath.Join(tmp, "a.go"),
		filepath.Join(tmp, "b.go"),
	}
	for _, path := range paths {
		if err := os.WriteFile(path, []byte("package main\n"), 0644); err != nil {
			t.Fatalf("write initial file: %v", err)
		}
	}

	w, err := newFastWatcher(tmp, []string{"**/*.go"}, nil, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("new watcher: %v", err)
	}
	defer w.Close()

	for _, path := range paths {
		if err := os.WriteFile(path, []byte("package main\n\nvar changed = true\n"), 0644); err != nil {
			t.Fatalf("modify file: %v", err)
		}
	}

	got := []string{readWatcherEvent(t, w), readWatcherEvent(t, w)}
	sort.Strings(got)
	if got[0] != "a.go" || got[1] != "b.go" {
		t.Fatalf("events = %#v, want a.go and b.go", got)
	}
}

func TestWatcherEmitsDeleteEvent(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0644); err != nil {
		t.Fatalf("write initial file: %v", err)
	}

	w, err := newFastWatcher(tmp, []string{"**/*.go"}, nil, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("new watcher: %v", err)
	}
	defer w.Close()

	if err := os.Remove(path); err != nil {
		t.Fatalf("remove file: %v", err)
	}

	if got := readWatcherEvent(t, w); got != "main.go" {
		t.Fatalf("event = %q, want main.go", got)
	}
}

func TestWatcherReportsWalkErrors(t *testing.T) {
	tmp := t.TempDir()
	w, err := newFastWatcher(tmp, []string{"**/*.go"}, nil, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("new watcher: %v", err)
	}
	defer w.Close()

	if err := os.RemoveAll(tmp); err != nil {
		t.Fatalf("remove watched dir: %v", err)
	}

	select {
	case err := <-w.Errors():
		if err == nil {
			t.Fatal("expected watcher error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for watcher error")
	}
}

func readWatcherEvent(t *testing.T, w *Watcher) string {
	t.Helper()
	select {
	case got, ok := <-w.Events():
		if !ok {
			t.Fatal("events channel closed")
		}
		return got
	case err := <-w.Errors():
		t.Fatalf("watcher error: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for watcher event")
	}
	return ""
}

func newFastWatcher(dir string, include, exclude []string, debounce time.Duration) (*Watcher, error) {
	return NewWatcherWithOptions(dir, include, exclude, debounce, Options{PollInterval: 10 * time.Millisecond})
}
