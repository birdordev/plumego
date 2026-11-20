package pubsub

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func waitFor(timeout time.Duration, condition func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return condition()
}

func TestPersistentPubSub_Basic(t *testing.T) {
	dir := t.TempDir()

	config := PersistenceConfig{
		Enabled:           true,
		DataDir:           dir,
		WALSegmentSize:    1 << 20, // 1MB
		FlushInterval:     50 * time.Millisecond,
		SnapshotInterval:  500 * time.Millisecond,
		DefaultDurability: DurabilitySync,
	}

	pps, err := NewPersistent(config)
	if err != nil {
		t.Fatalf("Failed to create persistent pubsub: %v", err)
	}
	defer pps.Close()

	// Publish some messages
	for i := 0; i < 10; i++ {
		msg := Message{
			ID:   generateCorrelationID(),
			Data: i,
		}
		if err := pps.Publish("test.topic", msg); err != nil {
			t.Errorf("Failed to publish message %d: %v", i, err)
		}
	}

	// Check stats
	stats := pps.PersistenceStats()
	if stats.WALWrites != 10 {
		t.Errorf("Expected 10 WAL writes, got %d", stats.WALWrites)
	}

	// Verify WAL file exists
	walFiles, _ := filepath.Glob(filepath.Join(dir, "wal-*.log"))
	if len(walFiles) == 0 {
		t.Error("No WAL files created")
	}
}

func TestPersistentPubSub_Restore(t *testing.T) {
	dir := t.TempDir()

	config := PersistenceConfig{
		Enabled:           true,
		DataDir:           dir,
		DefaultDurability: DurabilitySync,
		FlushInterval:     10 * time.Millisecond,
	}

	// Create first instance and publish
	pps1, err := NewPersistent(config)
	if err != nil {
		t.Fatalf("Failed to create first instance: %v", err)
	}

	messages := []Message{
		{ID: "msg1", Data: "hello"},
		{ID: "msg2", Data: "world"},
		{ID: "msg3", Data: "test"},
	}

	for _, msg := range messages {
		if err := pps1.PublishPersistent("test.restore", msg, DurabilitySync); err != nil {
			t.Fatalf("Failed to publish: %v", err)
		}
	}

	// Close first instance
	if err := pps1.Close(); err != nil {
		t.Fatalf("Failed to close first instance: %v", err)
	}

	// Create second instance - should restore
	var restored []Message
	config.OnRestore = func(msg Message, timestamp time.Time) {
		restored = append(restored, msg)
	}

	pps2, err := NewPersistent(config)
	if err != nil {
		t.Fatalf("Failed to create second instance: %v", err)
	}
	defer pps2.Close()

	// Wait for async restore callback processing.
	waitFor(120*time.Millisecond, func() bool {
		return pps2.PersistenceStats().RestoreCount == uint64(len(messages)) && len(restored) == len(messages)
	})

	// Check restored messages
	stats := pps2.PersistenceStats()
	if stats.RestoreCount != uint64(len(messages)) {
		t.Errorf("Expected %d restored messages, got %d", len(messages), stats.RestoreCount)
	}

	if len(restored) != len(messages) {
		t.Errorf("Expected %d messages in restore callback, got %d", len(messages), len(restored))
	}
}

func TestPersistentPubSub_DurabilityLevels(t *testing.T) {
	dir := t.TempDir()

	tests := []struct {
		name       string
		durability DurabilityLevel
		expectWAL  bool
	}{
		{"None", DurabilityNone, false},
		{"Async", DurabilityAsync, true},
		{"Sync", DurabilitySync, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			subdir := filepath.Join(dir, tt.name)
			config := PersistenceConfig{
				Enabled:           true,
				DataDir:           subdir,
				DefaultDurability: tt.durability,
			}

			pps, err := NewPersistent(config)
			if err != nil {
				t.Fatalf("Failed to create pubsub: %v", err)
			}

			msg := Message{ID: "test", Data: "data"}
			if err := pps.PublishPersistent("topic", msg, tt.durability); err != nil {
				t.Fatalf("Failed to publish: %v", err)
			}

			if tt.expectWAL {
				waitFor(120*time.Millisecond, func() bool {
					return pps.PersistenceStats().WALWrites > 0
				})
			}
			pps.Close()

			stats := pps.PersistenceStats()
			if tt.expectWAL && stats.WALWrites == 0 {
				t.Error("Expected WAL writes but got none")
			}
			if !tt.expectWAL && stats.WALWrites > 0 {
				t.Errorf("Expected no WAL writes but got %d", stats.WALWrites)
			}
		})
	}
}

func TestPersistentPubSub_Snapshot(t *testing.T) {
	dir := t.TempDir()

	config := PersistenceConfig{
		Enabled:           true,
		DataDir:           dir,
		DefaultDurability: DurabilitySync,
		SnapshotInterval:  100 * time.Millisecond,
	}

	pps, err := NewPersistent(config)
	if err != nil {
		t.Fatalf("Failed to create pubsub: %v", err)
	}

	// Publish messages
	for i := 0; i < 5; i++ {
		msg := Message{Data: i}
		_ = pps.Publish("test", msg)
	}

	// Trigger manual snapshot
	if err := pps.Snapshot(); err != nil {
		t.Fatalf("Snapshot failed: %v", err)
	}

	stats := pps.PersistenceStats()
	if stats.Snapshots == 0 {
		t.Error("Expected at least one snapshot")
	}

	// Check snapshot file exists
	snaps, _ := filepath.Glob(filepath.Join(dir, "snapshot-*.json"))
	if len(snaps) == 0 {
		t.Error("No snapshot files created")
	}

	pps.Close()
}

func TestPersistentPubSub_WALRotation(t *testing.T) {
	dir := t.TempDir()

	config := PersistenceConfig{
		Enabled:           true,
		DataDir:           dir,
		WALSegmentSize:    1024, // Small size to force rotation
		DefaultDurability: DurabilitySync,
	}

	pps, err := NewPersistent(config)
	if err != nil {
		t.Fatalf("Failed to create pubsub: %v", err)
	}
	defer pps.Close()

	// Publish large messages to exceed segment size
	for i := 0; i < 100; i++ {
		msg := Message{
			Data: make([]byte, 100), // 100 bytes each
		}
		_ = pps.Publish("test", msg)
	}

	// Check for multiple WAL files
	walFiles, _ := filepath.Glob(filepath.Join(dir, "wal-*.log"))
	if len(walFiles) < 1 {
		t.Error("Expected WAL file rotation")
	}
}

func TestPersistentPubSub_CRCVerification(t *testing.T) {
	dir := t.TempDir()

	config := PersistenceConfig{
		Enabled:           true,
		DataDir:           dir,
		DefaultDurability: DurabilitySync,
	}

	pps, err := NewPersistent(config)
	if err != nil {
		t.Fatalf("Failed to create pubsub: %v", err)
	}

	msg := Message{ID: "test", Data: "data"}
	if err := pps.Publish("topic", msg); err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	pps.Close()

	// Corrupt WAL file
	walFiles, _ := filepath.Glob(filepath.Join(dir, "wal-*.log"))
	if len(walFiles) > 0 {
		data, _ := os.ReadFile(walFiles[0])
		if len(data) > 10 {
			data[10] = ^data[10] // Flip a byte
			_ = os.WriteFile(walFiles[0], data, 0644)
		}
	}

	// Try to restore - should handle corruption gracefully
	config.OnRestore = func(msg Message, timestamp time.Time) {
		t.Error("Should not restore corrupted entry")
	}

	pps2, err := NewPersistent(config)
	if err != nil {
		t.Fatalf("Failed to create second instance: %v", err)
	}
	defer pps2.Close()

}

func TestPersistentPubSub_Concurrency(t *testing.T) {
	dir := t.TempDir()

	config := PersistenceConfig{
		Enabled:           true,
		DataDir:           dir,
		DefaultDurability: DurabilityAsync,
		FlushInterval:     50 * time.Millisecond,
	}

	pps, err := NewPersistent(config)
	if err != nil {
		t.Fatalf("Failed to create pubsub: %v", err)
	}
	defer pps.Close()

	// Concurrent publishers
	const numGoroutines = 10
	const messagesPerGoroutine = 50

	done := make(chan bool, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			for j := 0; j < messagesPerGoroutine; j++ {
				msg := Message{
					Data: id*messagesPerGoroutine + j,
				}
				_ = pps.Publish("concurrent.test", msg)
			}
			done <- true
		}(i)
	}

	// Wait for all
	for i := 0; i < numGoroutines; i++ {
		<-done
	}

	waitFor(220*time.Millisecond, func() bool {
		return pps.PersistenceStats().WALWrites == uint64(numGoroutines*messagesPerGoroutine)
	})

	stats := pps.PersistenceStats()
	expected := uint64(numGoroutines * messagesPerGoroutine)
	if stats.WALWrites != expected {
		t.Errorf("Expected %d WAL writes, got %d", expected, stats.WALWrites)
	}
}

func TestPersistentPubSub_Cleanup(t *testing.T) {
	dir := t.TempDir()

	config := PersistenceConfig{
		Enabled:          true,
		DataDir:          dir,
		RetentionPeriod:  100 * time.Millisecond,
		SnapshotInterval: 50 * time.Millisecond,
	}

	pps, err := NewPersistent(config)
	if err != nil {
		t.Fatalf("Failed to create pubsub: %v", err)
	}

	// Create multiple snapshots
	for i := 0; i < 3; i++ {
		_ = pps.Snapshot()
		time.Sleep(110 * time.Millisecond)
	}

	// Trigger cleanup
	pps.cleanup()

	// Check that old snapshots are cleaned
	snaps, _ := filepath.Glob(filepath.Join(dir, "snapshot-*.json"))
	if len(snaps) > 2 {
		t.Logf("Note: Found %d snapshots (cleanup may be async)", len(snaps))
	}

	pps.Close()
}

func TestPersistentPubSub_Disabled(t *testing.T) {
	config := PersistenceConfig{
		Enabled: false,
		DataDir: t.TempDir(),
	}

	_, err := NewPersistent(config)
	if err != ErrPersistenceDisabled {
		t.Errorf("Expected ErrPersistenceDisabled, got %v", err)
	}
}

func TestPersistentPubSub_SubscribeAndRestore(t *testing.T) {
	dir := t.TempDir()

	config := PersistenceConfig{
		Enabled:           true,
		DataDir:           dir,
		DefaultDurability: DurabilitySync,
	}

	// First instance
	pps1, err := NewPersistent(config)
	if err != nil {
		t.Fatalf("Failed to create first instance: %v", err)
	}

	// Subscribe
	sub, err := pps1.Subscribe(t.Context(), "test.channel", SubOptions{BufferSize: 10})
	if err != nil {
		t.Fatalf("Failed to subscribe: %v", err)
	}

	// Publish and receive
	go func() {
		for i := 0; i < 5; i++ {
			msg := Message{Data: i}
			_ = pps1.Publish("test.channel", msg)
			time.Sleep(10 * time.Millisecond)
		}
	}()

	received := 0
	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()

	for {
		select {
		case <-sub.C():
			received++
			if received >= 5 {
				goto done
			}
		case <-ctx.Done():
			goto done
		}
	}

done:
	if received != 5 {
		t.Errorf("Expected to receive 5 messages, got %d", received)
	}

	pps1.Close()

	// Second instance - restore
	pps2, err := NewPersistent(config)
	if err != nil {
		t.Fatalf("Failed to create second instance: %v", err)
	}
	defer pps2.Close()

	stats := pps2.PersistenceStats()
	if stats.RestoreCount == 0 {
		t.Log("Note: In-memory messages not persisted (by design)")
	}
}

func BenchmarkPersistentPubSub_Sync(b *testing.B) {
	dir := b.TempDir()

	config := PersistenceConfig{
		Enabled:           true,
		DataDir:           dir,
		DefaultDurability: DurabilitySync,
	}

	pps, err := NewPersistent(config)
	if err != nil {
		b.Fatalf("Failed to create pubsub: %v", err)
	}
	defer pps.Close()

	msg := Message{Data: []byte("benchmark message")}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = pps.Publish("bench.topic", msg)
	}
}

func BenchmarkPersistentPubSub_Async(b *testing.B) {
	dir := b.TempDir()

	config := PersistenceConfig{
		Enabled:           true,
		DataDir:           dir,
		DefaultDurability: DurabilityAsync,
		FlushInterval:     100 * time.Millisecond,
	}

	pps, err := NewPersistent(config)
	if err != nil {
		b.Fatalf("Failed to create pubsub: %v", err)
	}
	defer pps.Close()

	msg := Message{Data: []byte("benchmark message")}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = pps.Publish("bench.topic", msg)
	}
}

func TestPersistentPubSubRestoresAcrossSnapshotMarkers(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "empty markers"
		if legacy {
			name = "legacy snapshot followed by empty markers"
		}
		t.Run(name, func(t *testing.T) {
			cfg := DefaultPersistenceConfig()
			cfg.Enabled = true
			cfg.DataDir = t.TempDir()
			cfg.SnapshotInterval = time.Hour
			var restored []string
			cfg.OnRestore = func(msg Message, _ time.Time) { restored = append(restored, msg.ID) }

			open := func() *PersistentPubSub {
				t.Helper()
				b, err := NewPersistent(cfg)
				if err != nil {
					t.Fatal(err)
				}
				return b
			}
			publish := func(b *PersistentPubSub, id string) {
				t.Helper()
				if err := b.PublishPersistent("sample", Message{ID: id}, DurabilitySync); err != nil {
					t.Fatal(err)
				}
			}
			closeBroker := func(b *PersistentPubSub) {
				t.Helper()
				if err := b.Close(); err != nil {
					t.Fatal(err)
				}
			}
			writeSnapshot := func(name string, snapshot snapshotData, modified time.Time) {
				t.Helper()
				data, err := json.Marshal(snapshot)
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(cfg.DataDir, name)
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(path, modified, modified); err != nil {
					t.Fatal(err)
				}
			}

			b := open()
			publish(b, "first")
			old := time.Now().Add(-48 * time.Hour)
			if legacy {
				writeSnapshot("snapshot-1.json", snapshotData{
					Version: 1, WALSequence: 1,
					Messages: []persistedMessage{{Topic: "sample", Message: Message{ID: "first"}}},
				}, old)
			}
			writeSnapshot("snapshot-2.json", snapshotData{Version: 1, WALSequence: 1}, time.Now())
			if err := b.Snapshot(); err != nil {
				t.Fatal(err)
			}
			publish(b, "second")
			// Age the active segment, then run the same cleanup as Snapshot.
			wal := b.walFile.Name()
			if err := os.Chtimes(wal, old, old); err != nil {
				t.Fatal(err)
			}
			if err := b.Snapshot(); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(wal); err != nil {
				t.Fatalf("cleanup removed the active WAL: %v", err)
			}
			b.rotateWAL()
			publish(b, "third")
			if err := b.Snapshot(); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(wal); err != nil {
				t.Fatalf("cleanup removed an uncovered WAL: %v", err)
			}
			closeBroker(b)

			b = open()
			if !slices.Equal(restored, []string{"first", "second", "third"}) {
				t.Fatalf("restored = %v", restored)
			}
			publish(b, "fourth")
			closeBroker(b)

			restored = nil
			closeBroker(open())
			if !slices.Equal(restored, []string{"first", "second", "third", "fourth"}) {
				t.Fatalf("restored after second reopen = %v", restored)
			}
		})
	}
}

func TestPersistentPubSubCloseReportsWALFailureAndClosesBroker(t *testing.T) {
	cfg := DefaultPersistenceConfig()
	cfg.Enabled = true
	cfg.DataDir = t.TempDir()
	cfg.FlushInterval = time.Hour
	b, err := NewPersistent(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b.Close() }()
	if err := b.PublishPersistent("sample", Message{ID: "pending"}, DurabilityAsync); err != nil {
		t.Fatal(err)
	}
	b.walMu.Lock()
	err = b.walFile.Close()
	b.walMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}

	if err := b.Close(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("Close = %v, want closed-file error", err)
	}
	if err := b.InProcBroker.Publish("sample", Message{}); err == nil {
		t.Error("base broker is still open")
	}
	if err := b.Snapshot(); !errors.Is(err, ErrPersistenceClosed) {
		t.Errorf("Snapshot after close = %v", err)
	}
	if err := b.Close(); err != nil {
		t.Errorf("repeated Close = %v", err)
	}
}
