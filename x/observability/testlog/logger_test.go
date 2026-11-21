package testlog

import (
	"reflect"
	"testing"

	"github.com/birdordev/plumego/contract"
)

func TestLoggerCapturesEntries(t *testing.T) {
	logger := New()
	logger.Info("hello", Fields{"component": "test"})

	if got := logger.Count(); got != 1 {
		t.Fatalf("Count() = %d, want 1", got)
	}

	entry, ok := logger.LastEntry()
	if !ok {
		t.Fatal("LastEntry() = none, want entry")
	}
	if entry.Level != INFO {
		t.Fatalf("entry.Level = %v, want %v", entry.Level, INFO)
	}
	if entry.Fields["component"] != "test" {
		t.Fatalf("entry.Fields[component] = %v, want test", entry.Fields["component"])
	}
}

func TestLoggerWithFieldsSharesEntries(t *testing.T) {
	logger := New()
	child := logger.WithFields(Fields{"scope": "child"})
	child.Warn("warn")

	if got := logger.CountByLevel(WARNING); got != 1 {
		t.Fatalf("CountByLevel(WARNING) = %d, want 1", got)
	}

	entry, ok := logger.LastEntry()
	if !ok {
		t.Fatal("LastEntry() = none, want entry")
	}
	if entry.Fields["scope"] != "child" {
		t.Fatalf("entry.Fields[scope] = %v, want child", entry.Fields["scope"])
	}
}

func TestLoggerContextAddsRequestID(t *testing.T) {
	logger := New()
	ctx := contract.WithRequestID(t.Context(), "req_123")

	logger.InfoCtx(ctx, "request received")

	entry, ok := logger.LastEntry()
	if !ok {
		t.Fatal("LastEntry() = none, want entry")
	}
	if entry.Fields["request_id"] != "req_123" {
		t.Fatalf("entry.Fields[request_id] = %v, want req_123", entry.Fields["request_id"])
	}
}

func TestLoggerClear(t *testing.T) {
	logger := New()
	logger.Info("one")
	logger.Clear()

	if got := logger.Count(); got != 0 {
		t.Fatalf("Count() after Clear = %d, want 0", got)
	}
	if _, ok := logger.LastEntry(); ok {
		t.Fatal("LastEntry() after Clear = entry, want none")
	}
}

func TestLoggerFatalHook(t *testing.T) {
	logger := New()
	var captured string
	logger.FatalHook = func(msg string) {
		captured = msg
	}

	logger.Fatal("boom")

	if captured != "boom" {
		t.Fatalf("FatalHook captured %q, want boom", captured)
	}
	if !logger.HasEntry(FATAL, "boom") {
		t.Fatal("HasEntry(FATAL, boom) = false, want true")
	}
}

func TestSafeMapCapacity(t *testing.T) {
	tests := []struct {
		name    string
		lengths []int
		want    int
	}{
		{
			name:    "sums lengths",
			lengths: []int{2, 3},
			want:    5,
		},
		{
			name:    "rejects negative length",
			lengths: []int{1, -1},
			want:    0,
		},
		{
			name:    "falls back on overflow",
			lengths: []int{maxMapCapacity, 1},
			want:    0,
		},
		{
			name:    "allows max capacity",
			lengths: []int{maxMapCapacity - 1, 1},
			want:    maxMapCapacity,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := safeMapCapacity(tt.lengths...); got != tt.want {
				t.Fatalf("safeMapCapacity(%v) = %d, want %d", tt.lengths, got, tt.want)
			}
		})
	}
}

func TestMergeFieldsKeepsExtraPrecedence(t *testing.T) {
	got := mergeFields(Fields{
		"base":     "yes",
		"override": "base",
	}, Fields{
		"extra":    "yes",
		"override": "extra",
	})

	for key, want := range (Fields{
		"base":     "yes",
		"extra":    "yes",
		"override": "extra",
	}) {
		if got[key] != want {
			t.Fatalf("mergeFields()[%s] = %v, want %v in %#v", key, got[key], want, got)
		}
	}
}

func TestEntriesOwnCapturedFields(t *testing.T) {
	l := New()
	l.Info("message", Fields{"key": "original"})
	l.Entries()[0].Fields["key"] = "modified"
	if got := l.Entries()[0].Fields["key"]; got != "original" {
		t.Errorf("stored field = %v", got)
	}
}

func TestLastEntryOwnsCapturedFields(t *testing.T) {
	l := New()
	if _, ok := l.LastEntry(); ok {
		t.Fatal("empty logger returned an entry")
	}
	l.Info("no fields", nil)
	if e, ok := l.LastEntry(); !ok || e.Message != "no fields" || len(e.Fields) != 0 {
		t.Fatalf("fieldless entry = %+v, %t", e, ok)
	}
	l.Info("message", Fields{"key": "original", "other": "kept"})
	e, ok := l.LastEntry()
	if !ok {
		t.Fatal("missing last entry")
	}
	e.Fields["key"] = "changed"
	delete(e.Fields, "other")
	if got := l.Entries()[1].Fields; got["key"] != "original" || got["other"] != "kept" {
		t.Error("last entry aliases stored fields")
	}
}

func TestClearReleasesCapturedEntries(t *testing.T) {
	l := New()
	l.Info("message", Fields{"key": "payload"})
	previous := l.shared.entries
	l.Clear()
	if !reflect.DeepEqual(previous[0], Entry{}) {
		t.Error("cleared slot retains data")
	}
	l.Info("after clear", nil)
	if entries := l.Entries(); len(entries) != 1 || entries[0].Message != "after clear" {
		t.Errorf("entries after reuse = %+v", entries)
	}
}

func TestScopedLoggerCapturesContextError(t *testing.T) {
	root := New()
	child := root.With("scope", "child").WithFields(Fields{"region": "original"})
	ctx := contract.WithRequestID(t.Context(), "req_fixture")
	fields := Fields{"region": "override", "request_id": "fallback"}
	child.ErrorCtx(ctx, "operation failed", fields)
	fields["region"] = "changed"
	root.Info("parent entry")

	entries := root.Entries()
	if len(entries) != 2 {
		t.Fatalf("root entries = %v", entries)
	}
	entry := entries[0]
	if entry.Level != ERROR || entry.Message != "operation failed" ||
		entry.Fields["scope"] != "child" || entry.Fields["region"] != "override" ||
		entry.Fields["request_id"] != "req_fixture" {
		t.Errorf("child error = %+v", entry)
	}
	if len(entries[1].Fields) != 0 {
		t.Errorf("root entry inherited child fields: %+v", entries[1])
	}
}
