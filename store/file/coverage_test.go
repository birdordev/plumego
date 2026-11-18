package file

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func assertStringContains(t *testing.T, value string, expected string) {
	t.Helper()

	if !strings.Contains(value, expected) {
		t.Fatalf("expected %q to contain %q", value, expected)
	}
}

func TestMetadataCloneSharesUnsupportedValueTypes(t *testing.T) {
	tags := map[string]int{"count": 1}
	metadata := map[string]any{"tags": tags}
	for _, clone := range []map[string]any{
		(File{Metadata: metadata}).Clone().Metadata,
		(PutOptions{Metadata: metadata}).CloneMetadata(),
	} {
		clone["tags"].(map[string]int)["count"]++
		delete(clone, "tags")
	}
	if _, ok := metadata["tags"]; !ok || tags["count"] != 3 {
		t.Fatalf("metadata map = %v, want original map with a shared tags value", metadata)
	}
}

// --- Error type tests ---

func TestError_Error_WithPath(t *testing.T) {
	e := &Error{Op: "Put", Path: "files/report.txt", Err: ErrNotFound}
	msg := e.Error()
	assertStringContains(t, msg, "Put")
	assertStringContains(t, msg, "files/report.txt")
}

func TestError_Error_WithoutPath(t *testing.T) {
	e := &Error{Op: "Delete", Err: ErrNotFound}
	msg := e.Error()
	assertStringContains(t, msg, "Delete")
}

func TestError_Error_NilReceiver(t *testing.T) {
	var e *Error
	if got := e.Error(); got != "file: <nil>" {
		t.Fatalf("unexpected nil receiver error string: %q", got)
	}
}

func TestError_Error_WithoutCause(t *testing.T) {
	e := &Error{Op: "Stat", Path: "file.txt"}
	msg := e.Error()
	assertStringContains(t, msg, "Stat")
	assertStringContains(t, msg, "file.txt")
	if strings.Contains(msg, "<nil>") {
		t.Fatalf("error string should not expose nil cause: %q", msg)
	}
}

func TestError_Unwrap(t *testing.T) {
	e := &Error{Op: "Get", Err: ErrNotFound}
	if !errors.Is(e, ErrNotFound) {
		t.Error("Unwrap should allow errors.Is to match ErrNotFound")
	}
}

func TestError_Unwrap_NilReceiver(t *testing.T) {
	var e *Error
	if err := e.Unwrap(); err != nil {
		t.Fatalf("expected nil unwrap from nil receiver, got %v", err)
	}
}

func TestFileStat_Zero(t *testing.T) {
	var stat FileStat
	if stat.Size != 0 {
		t.Error("expected zero size")
	}
}

func TestFile_Zero(t *testing.T) {
	var f File
	if !f.CreatedAt.IsZero() {
		t.Error("expected zero CreatedAt")
	}
}

func TestFileCloneCopiesMutableFields(t *testing.T) {
	accessed := time.Now()
	deleted := accessed.Add(time.Hour)
	wantAccessed := accessed
	wantDeleted := deleted
	file := File{
		ID:           "file-1",
		Metadata:     map[string]any{"owner": "api"},
		LastAccessAt: &accessed,
		DeletedAt:    &deleted,
	}

	clone := file.Clone()
	file.Metadata["owner"] = "mutated"
	*file.LastAccessAt = accessed.Add(2 * time.Hour)
	*file.DeletedAt = deleted.Add(2 * time.Hour)

	if clone.ID != "file-1" {
		t.Fatalf("clone ID = %q, want file-1", clone.ID)
	}
	if got := clone.Metadata["owner"]; got != "api" {
		t.Fatalf("clone metadata owner = %v, want api", got)
	}
	if clone.LastAccessAt == file.LastAccessAt || clone.DeletedAt == file.DeletedAt {
		t.Fatal("clone should copy time pointers")
	}
	if !clone.LastAccessAt.Equal(wantAccessed) {
		t.Fatalf("clone LastAccessAt = %v, want %v", clone.LastAccessAt, wantAccessed)
	}
	if !clone.DeletedAt.Equal(wantDeleted) {
		t.Fatalf("clone DeletedAt = %v, want %v", clone.DeletedAt, wantDeleted)
	}
}

func TestMetadataClonesPreserveSliceShapeAndOwnership(t *testing.T) {
	metadata := map[string]any{
		"nil bytes": []byte(nil), "empty bytes": []byte{}, "bytes": []byte("data"),
		"nil strings": []string(nil), "empty strings": []string{}, "strings": []string{"item"},
	}
	want, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	for _, clone := range []map[string]any{
		(File{Metadata: metadata}).Clone().Metadata,
		(PutOptions{Metadata: metadata}).CloneMetadata(),
	} {
		got, err := json.Marshal(clone)
		if err != nil || string(got) != string(want) {
			t.Fatalf("clone JSON = %s, err = %v, want %s", got, err, want)
		}
		clone["bytes"].([]byte)[0] = 'X'
		clone["strings"].([]string)[0] = "changed"
		if string(metadata["bytes"].([]byte)) != "data" || metadata["strings"].([]string)[0] != "item" {
			t.Fatal("clone mutation changed original metadata")
		}
	}
}

func TestQuery_Zero(t *testing.T) {
	var q Query
	if q.MimeType != "" || q.Page != 0 || q.PageSize != 0 || q.OrderBy != "" {
		t.Fatalf("unexpected zero query: %+v", q)
	}
	if !q.StartTime.IsZero() || !q.EndTime.IsZero() {
		t.Fatalf("zero query should have zero time filters: %+v", q)
	}
}

// --- PutOptions coverage ---

func TestPutOptions_AllFields(t *testing.T) {
	opts := PutOptions{
		FileName:    "file.txt",
		ContentType: "text/plain",
		Reader:      strings.NewReader("data"),
		Size:        -1,
		Metadata:    map[string]any{"key": "val"},
	}
	if opts.FileName != "file.txt" {
		t.Error("FileName not set")
	}
	if opts.Size != -1 {
		t.Fatalf("Size = %d, want -1 for unknown size", opts.Size)
	}
}

func TestPutOptionsCloneMetadata(t *testing.T) {
	opts := PutOptions{Metadata: map[string]any{"key": "value"}}

	clone := opts.CloneMetadata()
	opts.Metadata["key"] = "mutated"
	clone["extra"] = true

	if got := clone["key"]; got != "value" {
		t.Fatalf("clone metadata key = %v, want value", got)
	}
	if _, ok := opts.Metadata["extra"]; ok {
		t.Fatal("mutating cloned metadata should not affect original")
	}
	if (PutOptions{}).CloneMetadata() != nil {
		t.Fatal("nil metadata should clone to nil")
	}
}

func TestCloneMetadataDetachesNestedMutableValues(t *testing.T) {
	opts := PutOptions{Metadata: map[string]any{
		"nested": map[string]any{
			"labels": []any{"a", map[string]any{"inner": "b"}},
		},
		"strings":     []string{"one"},
		"bytes":       []byte("abc"),
		"attrs":       map[string]string{"tier": "gold"},
		"nil attrs":   map[string]string(nil),
		"empty attrs": map[string]string{},
	}}

	clones := []map[string]any{
		opts.CloneMetadata(),
		(File{Metadata: opts.Metadata}).Clone().Metadata,
	}

	opts.Metadata["nested"].(map[string]any)["labels"].([]any)[0] = "mutated"
	opts.Metadata["nested"].(map[string]any)["labels"].([]any)[1].(map[string]any)["inner"] = "mutated"
	opts.Metadata["strings"].([]string)[0] = "mutated"
	opts.Metadata["bytes"].([]byte)[0] = 'z'
	opts.Metadata["attrs"].(map[string]string)["tier"] = "mutated"

	for _, clone := range clones {
		labels := clone["nested"].(map[string]any)["labels"].([]any)
		if labels[0] != "a" {
			t.Fatalf("nested label = %v, want a", labels[0])
		}
		if got := labels[1].(map[string]any)["inner"]; got != "b" {
			t.Fatalf("nested inner = %v, want b", got)
		}
		if got := clone["strings"].([]string)[0]; got != "one" {
			t.Fatalf("string slice value = %v, want one", got)
		}
		if got := string(clone["bytes"].([]byte)); got != "abc" {
			t.Fatalf("byte slice value = %q, want abc", got)
		}
		if got := clone["attrs"].(map[string]string)["tier"]; got != "gold" {
			t.Fatalf("attrs tier = %v, want gold", got)
		}
		if clone["nil attrs"].(map[string]string) != nil {
			t.Fatal("nil attrs should clone to nil")
		}
		empty := clone["empty attrs"].(map[string]string)
		if empty == nil || len(empty) != 0 {
			t.Fatalf("empty attrs clone = %#v, want non-nil empty map", empty)
		}
		empty["tier"] = "silver"
		clone["attrs"].(map[string]string)["tier"] = "silver"
	}
	if len(opts.Metadata["empty attrs"].(map[string]string)) != 0 ||
		opts.Metadata["attrs"].(map[string]string)["tier"] != "mutated" {
		t.Fatal("mutating cloned attrs changed source metadata")
	}
}

func TestQuery_AllFields(t *testing.T) {
	start := time.Now().Add(-time.Hour)
	end := time.Now()
	query := Query{
		MimeType:  "text/plain",
		StartTime: start,
		EndTime:   end,
		Page:      2,
		PageSize:  25,
		OrderBy:   "created_at desc",
	}

	if query.MimeType != "text/plain" || query.Page != 2 || query.PageSize != 25 || query.OrderBy != "created_at desc" {
		t.Fatalf("unexpected query fields: %+v", query)
	}
	if !query.StartTime.Equal(start) || !query.EndTime.Equal(end) {
		t.Fatalf("unexpected query time filters: %+v", query)
	}
}

// --- time field zero check ---

func TestFileStat_ModifiedTime(t *testing.T) {
	stat := FileStat{
		Path:         "some/path.txt",
		Size:         100,
		ModifiedTime: time.Now(),
		IsDir:        false,
	}
	if stat.ModifiedTime.IsZero() {
		t.Error("ModifiedTime should not be zero")
	}
}
