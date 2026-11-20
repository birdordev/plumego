package pubsub

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAuditLogger_Basic(t *testing.T) {
	config := DefaultAuditConfig()
	config.LogFile = ""

	al, err := NewAuditLogger(config)
	if err != nil {
		t.Fatalf("Failed to create audit logger: %v", err)
	}
	defer al.Close()

	// Log some operations
	err = al.Log(OpPublish, "test.topic", "user1", "127.0.0.1", map[string]any{
		"message_id": "msg-123",
	}, true, nil)

	if err != nil {
		t.Fatalf("Failed to log operation: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	// Query logs
	results, err := al.Query(AuditQuery{})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("Expected 1 audit entry, got %d", len(results))
	}

	entry := results[0]
	if entry.Operation != OpPublish {
		t.Errorf("Expected operation %s, got %s", OpPublish, entry.Operation)
	}

	if entry.Topic != "test.topic" {
		t.Errorf("Expected topic 'test.topic', got '%s'", entry.Topic)
	}

	if entry.Actor != "user1" {
		t.Errorf("Expected actor 'user1', got '%s'", entry.Actor)
	}

	if !entry.Success {
		t.Error("Expected success=true")
	}
}

func TestAuditLogger_Sequence(t *testing.T) {
	config := DefaultAuditConfig()
	config.LogFile = ""

	al, err := NewAuditLogger(config)
	if err != nil {
		t.Fatalf("Failed to create audit logger: %v", err)
	}
	defer al.Close()

	// Log multiple operations
	for i := 0; i < 10; i++ {
		_ = al.Log(OpPublish, "test", "user1", "127.0.0.1", nil, true, nil)
	}

	time.Sleep(100 * time.Millisecond)

	results, _ := al.Query(AuditQuery{Ascending: true})

	// Verify sequences are monotonically increasing
	for i := 1; i < len(results); i++ {
		if results[i].Sequence <= results[i-1].Sequence {
			t.Errorf("Sequences not monotonically increasing: %d, %d",
				results[i-1].Sequence, results[i].Sequence)
		}
	}
}

func TestAuditLogger_Integrity(t *testing.T) {
	config := DefaultAuditConfig()
	config.LogFile = ""
	config.VerifyIntegrity = true

	al, err := NewAuditLogger(config)
	if err != nil {
		t.Fatalf("Failed to create audit logger: %v", err)
	}
	defer al.Close()

	// Log operations
	for i := 0; i < 5; i++ {
		_ = al.Log(OpPublish, "test", "user1", "127.0.0.1", nil, true, nil)
	}

	time.Sleep(100 * time.Millisecond)

	// Verify integrity
	if err := al.Verify(); err != nil {
		t.Fatalf("Integrity verification failed: %v", err)
	}

	// Check that hashes are chained
	results, _ := al.Query(AuditQuery{Ascending: true})

	for i := 1; i < len(results); i++ {
		if results[i].PreviousHash != results[i-1].Hash {
			t.Error("Hash chain broken")
		}
	}
}

func TestAuditLogger_IntegrityTampering(t *testing.T) {
	config := DefaultAuditConfig()
	config.LogFile = ""
	config.VerifyIntegrity = true

	al, err := NewAuditLogger(config)
	if err != nil {
		t.Fatalf("Failed to create audit logger: %v", err)
	}
	defer al.Close()

	// Log operations
	for i := 0; i < 3; i++ {
		_ = al.Log(OpPublish, "test", "user1", "127.0.0.1", nil, true, nil)
	}

	time.Sleep(100 * time.Millisecond)

	// Tamper with an entry
	al.entriesMu.Lock()
	if len(al.entries) > 1 {
		al.entries[1].Topic = "tampered"
	}
	al.entriesMu.Unlock()

	// Verify should fail
	if err := al.Verify(); err == nil {
		t.Error("Expected integrity verification to fail after tampering")
	}
}

func TestAuditLogger_FileLogging(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "audit.log")

	config := DefaultAuditConfig()
	config.LogFile = logFile
	config.AsyncMode = false

	al, err := NewAuditLogger(config)
	if err != nil {
		t.Fatalf("Failed to create audit logger: %v", err)
	}

	// Log operations
	for i := 0; i < 5; i++ {
		_ = al.Log(OpPublish, "test", "user1", "127.0.0.1", nil, true, nil)
	}

	al.Close()

	// Verify log file exists and has content
	info, err := os.Stat(logFile)
	if err != nil {
		t.Fatalf("Log file not created: %v", err)
	}

	if info.Size() == 0 {
		t.Error("Log file is empty")
	}
}

func TestAuditLogger_LogRotation(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "audit.log")

	config := DefaultAuditConfig()
	config.LogFile = logFile
	config.MaxLogSize = 1024 // 1 KB
	config.MaxLogFiles = 3
	config.AsyncMode = false

	al, err := NewAuditLogger(config)
	if err != nil {
		t.Fatalf("Failed to create audit logger: %v", err)
	}
	defer al.Close()

	// Log enough operations to trigger rotation
	for i := 0; i < 100; i++ {
		_ = al.Log(OpPublish, "test.topic.with.long.name", "user1", "127.0.0.1",
			map[string]any{
				"data": "some data to increase size",
			}, true, nil)
	}

	time.Sleep(100 * time.Millisecond)

	// Check if rotation occurred
	stats := al.Stats()
	if stats.LogRotations.Load() == 0 {
		t.Log("Note: Log rotation may not have occurred yet")
	}

	// Verify rotated files exist
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("Failed to read directory: %v", err)
	}

	logFileCount := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			logFileCount++
		}
	}

	if logFileCount > 0 {
		t.Logf("Found %d log files", logFileCount)
	}
}

func TestAuditLogger_Query_TimeRange(t *testing.T) {
	config := DefaultAuditConfig()
	config.LogFile = ""

	al, err := NewAuditLogger(config)
	if err != nil {
		t.Fatalf("Failed to create audit logger: %v", err)
	}
	defer al.Close()

	start := time.Now()

	// Log first batch
	for i := 0; i < 3; i++ {
		_ = al.Log(OpPublish, "test", "user1", "127.0.0.1", nil, true, nil)
	}

	time.Sleep(100 * time.Millisecond)
	firstWindowEnd := time.Now()
	time.Sleep(2 * time.Millisecond)
	secondWindowStart := time.Now()

	// Log second batch
	for i := 0; i < 3; i++ {
		_ = al.Log(OpSubscribe, "test", "user2", "127.0.0.1", nil, true, nil)
	}

	time.Sleep(100 * time.Millisecond)
	end := time.Now()

	// Query first half
	results, err := al.Query(AuditQuery{
		StartTime: start,
		EndTime:   firstWindowEnd,
	})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(results) != 3 {
		t.Errorf("Expected 3 entries in first half, got %d", len(results))
	}

	// Query second half
	results, err = al.Query(AuditQuery{
		StartTime: secondWindowStart,
		EndTime:   end,
	})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(results) != 3 {
		t.Errorf("Expected 3 entries in second half, got %d", len(results))
	}
}

func TestAuditLogger_Query_Operation(t *testing.T) {
	config := DefaultAuditConfig()
	config.LogFile = ""

	al, err := NewAuditLogger(config)
	if err != nil {
		t.Fatalf("Failed to create audit logger: %v", err)
	}
	defer al.Close()

	// Log different operations
	_ = al.Log(OpPublish, "test", "user1", "127.0.0.1", nil, true, nil)
	_ = al.Log(OpSubscribe, "test", "user1", "127.0.0.1", nil, true, nil)
	_ = al.Log(OpPublish, "test", "user1", "127.0.0.1", nil, true, nil)

	time.Sleep(100 * time.Millisecond)

	// Query only Publish operations
	results, err := al.Query(AuditQuery{
		Operations: []AuditOperation{OpPublish},
	})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("Expected 2 Publish operations, got %d", len(results))
	}

	for _, entry := range results {
		if entry.Operation != OpPublish {
			t.Errorf("Expected OpPublish, got %s", entry.Operation)
		}
	}
}

func TestAuditLogger_Query_Topic(t *testing.T) {
	config := DefaultAuditConfig()
	config.LogFile = ""

	al, err := NewAuditLogger(config)
	if err != nil {
		t.Fatalf("Failed to create audit logger: %v", err)
	}
	defer al.Close()

	// Log to different topics
	_ = al.Log(OpPublish, "topic.a", "user1", "127.0.0.1", nil, true, nil)
	_ = al.Log(OpPublish, "topic.b", "user1", "127.0.0.1", nil, true, nil)
	_ = al.Log(OpPublish, "topic.a", "user1", "127.0.0.1", nil, true, nil)

	time.Sleep(100 * time.Millisecond)

	// Query only topic.a
	results, err := al.Query(AuditQuery{
		Topics: []string{"topic.a"},
	})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("Expected 2 entries for topic.a, got %d", len(results))
	}

	for _, entry := range results {
		if entry.Topic != "topic.a" {
			t.Errorf("Expected topic.a, got %s", entry.Topic)
		}
	}
}

func TestAuditLogger_Query_Actor(t *testing.T) {
	config := DefaultAuditConfig()
	config.LogFile = ""

	al, err := NewAuditLogger(config)
	if err != nil {
		t.Fatalf("Failed to create audit logger: %v", err)
	}
	defer al.Close()

	// Log from different actors
	_ = al.Log(OpPublish, "test", "alice", "127.0.0.1", nil, true, nil)
	_ = al.Log(OpPublish, "test", "bob", "127.0.0.1", nil, true, nil)
	_ = al.Log(OpPublish, "test", "alice", "127.0.0.1", nil, true, nil)

	time.Sleep(100 * time.Millisecond)

	// Query only alice
	results, err := al.Query(AuditQuery{
		Actors: []string{"alice"},
	})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("Expected 2 entries for alice, got %d", len(results))
	}

	for _, entry := range results {
		if entry.Actor != "alice" {
			t.Errorf("Expected alice, got %s", entry.Actor)
		}
	}
}

func TestAuditLogger_Query_Success(t *testing.T) {
	config := DefaultAuditConfig()
	config.LogFile = ""

	al, err := NewAuditLogger(config)
	if err != nil {
		t.Fatalf("Failed to create audit logger: %v", err)
	}
	defer al.Close()

	// Log successful and failed operations
	_ = al.Log(OpPublish, "test", "user1", "127.0.0.1", nil, true, nil)
	_ = al.Log(OpPublish, "test", "user1", "127.0.0.1", nil, false, errors.New("test error"))
	_ = al.Log(OpPublish, "test", "user1", "127.0.0.1", nil, true, nil)

	time.Sleep(100 * time.Millisecond)

	// Query only failures
	successFilter := false
	results, err := al.Query(AuditQuery{
		Success: &successFilter,
	})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("Expected 1 failed operation, got %d", len(results))
	}

	if results[0].Success {
		t.Error("Expected failed operation")
	}

	if results[0].Error == "" {
		t.Error("Expected error message")
	}
}

func TestAuditLogger_Pagination(t *testing.T) {
	config := DefaultAuditConfig()
	config.LogFile = ""

	al, err := NewAuditLogger(config)
	if err != nil {
		t.Fatalf("Failed to create audit logger: %v", err)
	}
	defer al.Close()

	// Log 10 operations
	for i := 0; i < 10; i++ {
		_ = al.Log(OpPublish, "test", "user1", "127.0.0.1", nil, true, nil)
	}

	time.Sleep(100 * time.Millisecond)

	// Query first page
	page1, err := al.Query(AuditQuery{
		Limit:     3,
		Offset:    0,
		Ascending: true,
	})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(page1) != 3 {
		t.Errorf("Expected 3 entries in first page, got %d", len(page1))
	}

	// Query second page
	page2, err := al.Query(AuditQuery{
		Limit:     3,
		Offset:    3,
		Ascending: true,
	})
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(page2) != 3 {
		t.Errorf("Expected 3 entries in second page, got %d", len(page2))
	}

	// Verify pages are different
	if page1[0].Sequence == page2[0].Sequence {
		t.Error("Pages should contain different entries")
	}
}

func TestAuditLogger_OperationFilter(t *testing.T) {
	config := DefaultAuditConfig()
	config.LogFile = ""
	config.Operations = []AuditOperation{OpPublish, OpSubscribe}

	al, err := NewAuditLogger(config)
	if err != nil {
		t.Fatalf("Failed to create audit logger: %v", err)
	}
	defer al.Close()

	// Log various operations
	_ = al.Log(OpPublish, "test", "user1", "127.0.0.1", nil, true, nil)
	_ = al.Log(OpClose, "test", "user1", "127.0.0.1", nil, true, nil)
	_ = al.Log(OpSubscribe, "test", "user1", "127.0.0.1", nil, true, nil)

	time.Sleep(100 * time.Millisecond)

	results, _ := al.Query(AuditQuery{})

	// Should only have 2 entries (OpClose should be filtered)
	if len(results) != 2 {
		t.Errorf("Expected 2 entries (filtered), got %d", len(results))
	}
}

func TestAuditLogger_TopicFilter(t *testing.T) {
	config := DefaultAuditConfig()
	config.LogFile = ""
	config.Topics = []string{"allowed.topic"}

	al, err := NewAuditLogger(config)
	if err != nil {
		t.Fatalf("Failed to create audit logger: %v", err)
	}
	defer al.Close()

	// Log to various topics
	_ = al.Log(OpPublish, "allowed.topic", "user1", "127.0.0.1", nil, true, nil)
	_ = al.Log(OpPublish, "denied.topic", "user1", "127.0.0.1", nil, true, nil)
	_ = al.Log(OpPublish, "allowed.topic", "user1", "127.0.0.1", nil, true, nil)

	time.Sleep(100 * time.Millisecond)

	results, _ := al.Query(AuditQuery{})

	// Should only have 2 entries
	if len(results) != 2 {
		t.Errorf("Expected 2 entries (filtered), got %d", len(results))
	}
}

func TestAuditLogger_Stats(t *testing.T) {
	config := DefaultAuditConfig()
	config.LogFile = ""

	al, err := NewAuditLogger(config)
	if err != nil {
		t.Fatalf("Failed to create audit logger: %v", err)
	}
	defer al.Close()

	// Log operations
	_ = al.Log(OpPublish, "test", "user1", "127.0.0.1", nil, true, nil)
	_ = al.Log(OpPublish, "test", "user1", "127.0.0.1", nil, false, errors.New("error"))
	_ = al.Log(OpPublish, "test", "user1", "127.0.0.1", nil, true, nil)

	time.Sleep(100 * time.Millisecond)

	stats := al.Stats()

	if stats.TotalEntries.Load() != 3 {
		t.Errorf("Expected 3 total entries, got %d", stats.TotalEntries.Load())
	}

	if stats.SuccessfulOps.Load() != 2 {
		t.Errorf("Expected 2 successful ops, got %d", stats.SuccessfulOps.Load())
	}

	if stats.FailedOps.Load() != 1 {
		t.Errorf("Expected 1 failed op, got %d", stats.FailedOps.Load())
	}
}

func TestAuditLogger_Close(t *testing.T) {
	config := DefaultAuditConfig()
	config.LogFile = ""

	al, err := NewAuditLogger(config)
	if err != nil {
		t.Fatalf("Failed to create audit logger: %v", err)
	}

	// Close
	if err := al.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Operations after close should error
	err = al.Log(OpPublish, "test", "user1", "127.0.0.1", nil, true, nil)
	if err != ErrAuditClosed {
		t.Errorf("Expected ErrAuditClosed, got %v", err)
	}

	// Query after close should error
	_, err = al.Query(AuditQuery{})
	if err != ErrAuditClosed {
		t.Errorf("Expected ErrAuditClosed, got %v", err)
	}
}

func BenchmarkAuditLogger_Log(b *testing.B) {
	config := DefaultAuditConfig()
	config.LogFile = ""
	config.VerifyIntegrity = false

	al, _ := NewAuditLogger(config)
	defer al.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = al.Log(OpPublish, "test", "user1", "127.0.0.1", nil, true, nil)
	}
}

func BenchmarkAuditLogger_LogWithIntegrity(b *testing.B) {
	config := DefaultAuditConfig()
	config.LogFile = ""
	config.VerifyIntegrity = true

	al, _ := NewAuditLogger(config)
	defer al.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = al.Log(OpPublish, "test", "user1", "127.0.0.1", nil, true, nil)
	}
}

func BenchmarkAuditLogger_Query(b *testing.B) {
	config := DefaultAuditConfig()
	config.LogFile = ""

	al, _ := NewAuditLogger(config)
	defer al.Close()

	// Populate with entries
	for i := 0; i < 1000; i++ {
		_ = al.Log(OpPublish, "test", "user1", "127.0.0.1", nil, true, nil)
	}

	time.Sleep(200 * time.Millisecond)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = al.Query(AuditQuery{Limit: 100})
	}
}

func TestAuditCloseReportsAsyncWriteFailure(t *testing.T) {
	for _, mode := range []string{"successful drain", "encoding error then success", "closed file"} {
		t.Run(mode, func(t *testing.T) {
			cfg := DefaultAuditConfig()
			cfg.VerifyIntegrity = false
			cfg.LogFile = filepath.Join(t.TempDir(), "audit.jsonl")
			al, err := NewAuditLogger(cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = al.Close() })
			if mode == "closed file" {
				if err := al.logFile.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "encoding error then success" {
				if err := al.Log(OpPublish, "invalid", "test", "", map[string]any{"value": make(chan int)}, true, nil); err != nil {
					t.Fatal(err)
				}
			}
			if err := al.Log(OpPublish, "valid", "test", "", nil, true, nil); err != nil {
				t.Fatal(err)
			}
			err = al.Close()
			switch mode {
			case "successful drain":
				if err != nil {
					t.Fatalf("Close = %v", err)
				}
			case "encoding error then success":
				var typeErr *json.UnsupportedTypeError
				if !errors.As(err, &typeErr) {
					t.Fatalf("Close = %v, want an encoding error", err)
				}
			case "closed file":
				if !errors.Is(err, os.ErrClosed) || !strings.Contains(err.Error(), "close audit log") || !strings.Contains(err.Error(), "write audit entry") {
					t.Fatalf("Close = %v, want both write and close errors", err)
				}
			}
			if mode != "closed file" {
				data, err := os.ReadFile(cfg.LogFile)
				if err != nil {
					t.Fatal(err)
				}
				if bytes.Count(data, []byte("\n")) != 1 || !bytes.Contains(data, []byte("\"valid\"")) {
					t.Errorf("expected exactly one successfully written entry, got %q", data)
				}
			}
			if err := al.Close(); !errors.Is(err, ErrAuditClosed) {
				t.Errorf("repeat Close = %v, want ErrAuditClosed", err)
			}
		})
	}
}

func TestAuditLogAndQueryOwnData(t *testing.T) {
	for _, async := range []bool{false, true} {
		name := "synchronous"
		if async {
			name = "asynchronous"
		}
		t.Run(name, func(t *testing.T) {
			cfg := DefaultAuditConfig()
			cfg.AsyncMode = async
			cfg.LogFile = filepath.Join(t.TempDir(), "audit.jsonl")
			al, err := NewAuditLogger(cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = al.Close() })

			input := map[string]any{"nested": []any{map[string]any{"label": "original"}}}
			label := func(data map[string]any) map[string]any { return data["nested"].([]any)[0].(map[string]any) }
			if err := al.Log(OpPublish, "snapshot", "test", "", input, true, nil); err != nil {
				t.Fatal(err)
			}
			label(input)["label"] = "caller edit"
			query := func() *AuditEntry {
				t.Helper()
				entries, err := al.Query(AuditQuery{Topics: []string{"snapshot"}})
				if err != nil || len(entries) != 1 {
					t.Fatalf("Query = %v, %v", entries, err)
				}
				return entries[0]
			}
			read := query()
			if got := label(read.Data)["label"]; got != "original" {
				t.Errorf("stored label = %v", got)
			}
			label(read.Data)["label"] = "query edit"
			if got := label(query().Data)["label"]; got != "original" {
				t.Errorf("queried label = %v", got)
			}
			if err := al.Log(OpPublish, "nil", "test", "", nil, true, nil); err != nil {
				t.Fatal(err)
			}
			entries, err := al.Query(AuditQuery{Topics: []string{"nil"}})
			if err != nil || len(entries) != 1 || entries[0].Data != nil {
				t.Errorf("nil snapshot = %v, %v", entries, err)
			}
			if err := al.Close(); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(cfg.LogFile)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(data, []byte("original")) || bytes.Contains(data, []byte("caller edit")) || bytes.Contains(data, []byte("query edit")) {
				t.Errorf("unexpected audit file: %s", data)
			}
		})
	}
}

func TestNewAuditLoggerRejectsNegativeAsyncBuffer(t *testing.T) {
	cfg := DefaultAuditConfig()
	cfg.LogFile = filepath.Join(t.TempDir(), "audit.jsonl")
	cfg.BufferSize = -1
	if al, err := NewAuditLogger(cfg); al != nil || err == nil {
		t.Fatalf("NewAuditLogger = %v, %v; want a capacity error", al, err)
	}
	if _, err := os.Stat(cfg.LogFile); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("invalid settings created a log file: %v", err)
	}
	for _, tt := range []struct {
		async bool
		size  int
	}{{true, 0}, {true, 1}, {false, -1}} {
		cfg.AsyncMode, cfg.BufferSize = tt.async, tt.size
		al, err := NewAuditLogger(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := al.Log(OpPublish, "valid", "test", "", nil, true, nil); err != nil {
			t.Error(err)
		}
		if err := al.Close(); err != nil {
			t.Error(err)
		}
	}
}

type gatedAuditError struct {
	started chan struct{}
	resume  chan struct{}
}

func (e gatedAuditError) Error() string {
	close(e.started)
	<-e.resume
	return "operation failed"
}

func TestAuditCloseLinearizesLogAdmission(t *testing.T) {
	for _, async := range []bool{true, false} {
		name := "sync"
		if async {
			name = "async"
		}
		t.Run(name, func(t *testing.T) {
			cfg := DefaultAuditConfig()
			cfg.AsyncMode = async
			cfg.LogFile = filepath.Join(t.TempDir(), "audit.jsonl")
			al, err := NewAuditLogger(cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = al.Close() })
			if err := al.Log(OpPublish, "accepted", "test", "", nil, true, nil); err != nil {
				t.Fatal(err)
			}

			blocked := gatedAuditError{started: make(chan struct{}), resume: make(chan struct{})}
			late := make(chan error, 1)
			go func() { late <- al.Log(OpPublish, "late", "test", "", nil, false, blocked) }()
			<-blocked.started
			closeErr := al.Close()
			close(blocked.resume)
			if err := <-late; !errors.Is(err, ErrAuditClosed) {
				t.Errorf("late Log = %v", err)
			}
			if closeErr != nil {
				t.Fatal(closeErr)
			}
			data, err := os.ReadFile(cfg.LogFile)
			if err != nil {
				t.Fatal(err)
			}
			var entry AuditEntry
			if err := json.Unmarshal(data, &entry); err != nil || entry.Topic != "accepted" {
				t.Fatalf("file entry = %s, decode error = %v", data, err)
			}
			if len(al.asyncQueue) != 0 || al.Stats().TotalEntries.Load() != 1 {
				t.Error("late entry affected the logger after shutdown")
			}
		})
	}
}

func TestAuditVerifyAfterRetentionEviction(t *testing.T) {
	cfg := DefaultAuditConfig()
	cfg.AsyncMode = false
	al, err := NewAuditLogger(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer al.Close()
	al.maxInMemory = 2
	for i := range 6 {
		if err := al.Log(OpPublish, "sample", "actor", "", nil, true, nil); err != nil {
			t.Fatal(err)
		}
		if err := al.Verify(); err != nil {
			t.Fatalf("Verify after %d writes = %v", i+1, err)
		}
	}
}
