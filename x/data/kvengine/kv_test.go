package kvengine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spcent/plumego/metrics"
)

// TestMain handles test setup and cleanup
func TestMain(m *testing.M) {
	// Run tests
	code := m.Run()

	// Cleanup test directories
	cleanupTestDirs()

	os.Exit(code)
}

func cleanupTestDirs() {
	testDirs, _ := filepath.Glob("testdata_*")
	for _, dir := range testDirs {
		os.RemoveAll(dir)
	}
}

// Test utilities
func createTestStore(t *testing.T) (*KVStore, func()) {
	dataDir := fmt.Sprintf("testdata_%d", time.Now().UnixNano())

	opts := Options{
		DataDir:       dataDir,
		MaxEntries:    100000, // Increased from 1000 to 100000 to reduce evict frequency
		MaxMemoryMB:   100,    // Increased from 10 to 100 to reduce evict frequency
		FlushInterval: 10 * time.Millisecond,
		CleanInterval: 100 * time.Millisecond,
		ShardCount:    4,
		WALSyncMode:   WALSyncInterval,
	}

	kv, err := NewKVStore(opts)
	if err != nil {
		t.Fatalf("Failed to create test store: %v", err)
	}

	cleanup := func() {
		kv.Close()
		os.RemoveAll(dataDir)
	}

	return kv, cleanup
}

// Basic functionality tests

func TestNewKVStore(t *testing.T) {
	tests := []struct {
		name    string
		opts    Options
		wantErr bool
	}{
		{
			name: "valid options",
			opts: Options{
				DataDir:     "testdata",
				MaxEntries:  1000,
				MaxMemoryMB: 10,
				ShardCount:  4,
			},
			wantErr: false,
		},
		{
			name: "invalid shard count",
			opts: Options{
				DataDir:     "testdata",
				MaxEntries:  1000,
				MaxMemoryMB: 10,
				ShardCount:  3, // Not power of 2
			},
			wantErr: true,
		},
		{
			name: "zero max entries",
			opts: Options{
				DataDir:     "testdata",
				MaxEntries:  0,
				MaxMemoryMB: 10,
				ShardCount:  4,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kv, err := NewKVStore(tt.opts)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewKVStore() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if kv != nil {
				kv.Close()
				os.RemoveAll(tt.opts.DataDir)
			}
		})
	}
}

func TestNewKVStoreRequiresDataDir(t *testing.T) {
	_, err := NewKVStore(Options{})
	if err == nil || !strings.Contains(err.Error(), "data dir is required") {
		t.Fatalf("NewKVStore without DataDir error = %v, want data dir required", err)
	}
}

func TestBasicOperations(t *testing.T) {
	kv, cleanup := createTestStore(t)
	defer cleanup()

	// Test Set and Get
	key := "test_key"
	value := []byte("test_value")

	err := kv.Set(key, value, 0)
	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	retrieved, err := kv.Get(key)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if !bytes.Equal(value, retrieved) {
		t.Errorf("Expected %s, got %s", value, retrieved)
	}

	// Test Exists
	if !kv.Exists(key) {
		t.Error("Key should exist")
	}

	// Test Delete
	err = kv.Delete(key)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Test Get after delete
	_, err = kv.Get(key)
	if err != ErrKeyNotFound {
		t.Errorf("Expected ErrKeyNotFound, got %v", err)
	}

	// Test Exists after delete
	if kv.Exists(key) {
		t.Error("Key should not exist after delete")
	}
}

func TestTTL(t *testing.T) {
	kv, cleanup := createTestStore(t)
	defer cleanup()

	key := "ttl_key"
	value := []byte("ttl_value")
	ttl := 100 * time.Millisecond

	// Set with TTL
	err := kv.Set(key, value, ttl)
	if err != nil {
		t.Fatalf("Set with TTL failed: %v", err)
	}

	// Should exist immediately
	if !kv.Exists(key) {
		t.Error("Key should exist immediately after set")
	}

	// Wait for expiration
	time.Sleep(ttl + 50*time.Millisecond)

	// Should not exist after TTL
	if kv.Exists(key) {
		t.Error("Key should not exist after TTL")
	}

	// Get should return expired error
	_, err = kv.Get(key)
	if err != ErrKeyExpired && err != ErrKeyNotFound {
		t.Errorf("Expected ErrKeyExpired or ErrKeyNotFound, got %v", err)
	}
}

func TestConcurrentOperations(t *testing.T) {
	kv, cleanup := createTestStore(t)
	defer cleanup()

	numWorkers := 10
	numOps := 100
	var wg sync.WaitGroup

	// Concurrent writes
	wg.Add(numWorkers)
	for i := 0; i < numWorkers; i++ {
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < numOps; j++ {
				key := fmt.Sprintf("key_%d_%d", workerID, j)
				value := []byte(fmt.Sprintf("value_%d_%d", workerID, j))
				if err := kv.Set(key, value, 0); err != nil {
					t.Errorf("Set failed: %v", err)
				}
			}
		}(i)
	}
	wg.Wait()

	// Verify all keys exist
	expectedKeys := numWorkers * numOps
	if kv.Size() != expectedKeys {
		t.Errorf("Expected %d keys, got %d", expectedKeys, kv.Size())
	}

	// Concurrent reads
	wg.Add(numWorkers)
	for i := 0; i < numWorkers; i++ {
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < numOps; j++ {
				key := fmt.Sprintf("key_%d_%d", workerID, j)
				expectedValue := []byte(fmt.Sprintf("value_%d_%d", workerID, j))

				value, err := kv.Get(key)
				if err != nil {
					t.Errorf("Get failed for key %s: %v", key, err)
					continue
				}

				if !bytes.Equal(value, expectedValue) {
					t.Errorf("Value mismatch for key %s", key)
				}
			}
		}(i)
	}
	wg.Wait()
}

func TestLRUEviction(t *testing.T) {
	// Create store with very small limits
	dataDir := fmt.Sprintf("testdata_%d", time.Now().UnixNano())
	defer os.RemoveAll(dataDir)

	opts := Options{
		DataDir:     dataDir,
		MaxEntries:  5, // Very small limit
		MaxMemoryMB: 1,
		ShardCount:  2,
	}

	kv, err := NewKVStore(opts)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer kv.Close()

	// Add more entries than the limit
	for i := 0; i < 10; i++ {
		key := fmt.Sprintf("key_%d", i)
		value := []byte(fmt.Sprintf("value_%d", i))
		kv.Set(key, value, 0)
	}

	// Should have evicted some entries
	if kv.Size() > 5 {
		t.Errorf("Expected max 5 entries due to eviction, got %d", kv.Size())
	}

	// Check that evictions were recorded
	stats := kv.GetStats()
	if stats.Evictions == 0 {
		t.Error("Expected some evictions to be recorded")
	}
}

func TestSetEvictsAcrossShardsWhenMaxEntriesReached(t *testing.T) {
	opts := Options{
		DataDir:     t.TempDir(),
		MaxEntries:  1,
		MaxMemoryMB: 1,
		ShardCount:  2,
	}
	kv, err := NewKVStore(opts)
	if err != nil {
		t.Fatalf("NewKVStore() error = %v", err)
	}
	defer kv.Close()

	first, second := keysOnDifferentShards(t, kv)
	if err := kv.Set(first, []byte("first"), 0); err != nil {
		t.Fatalf("Set(%q) error = %v", first, err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- kv.Set(second, []byte("second"), 0)
	}()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Set(%q) error = %v", second, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("Set(%q) did not return under cross-shard entry pressure", second)
	}

	if _, err := kv.Get(second); err != nil {
		t.Fatalf("Get(%q) error = %v", second, err)
	}
}

func TestSetEvictsAcrossShardsWhenMaxMemoryReached(t *testing.T) {
	opts := Options{
		DataDir:     t.TempDir(),
		MaxEntries:  10,
		MaxMemoryMB: 1,
		ShardCount:  2,
	}
	kv, err := NewKVStore(opts)
	if err != nil {
		t.Fatalf("NewKVStore() error = %v", err)
	}
	defer kv.Close()

	first, second := keysOnDifferentShards(t, kv)
	maxMemory := opts.MaxMemoryMB * 1024 * 1024
	largeValue := bytes.Repeat([]byte("a"), maxMemory-len(first)-64-32)
	if err := kv.Set(first, largeValue, 0); err != nil {
		t.Fatalf("Set(%q) error = %v", first, err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- kv.Set(second, bytes.Repeat([]byte("b"), 128), 0)
	}()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Set(%q) error = %v", second, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("Set(%q) did not return under cross-shard memory pressure", second)
	}

	if _, err := kv.Get(second); err != nil {
		t.Fatalf("Get(%q) error = %v", second, err)
	}
}

func keysOnDifferentShards(t *testing.T, kv *KVStore) (string, string) {
	t.Helper()

	first := "capacity_key_0"
	firstShard := kv.getShard(first)
	for i := 1; i < 1000; i++ {
		candidate := fmt.Sprintf("capacity_key_%d", i)
		if kv.getShard(candidate) != firstShard {
			return first, candidate
		}
	}
	t.Fatal("could not find keys on different shards")
	return "", ""
}

func TestKeys(t *testing.T) {
	kv, cleanup := createTestStore(t)
	defer cleanup()

	// Add some keys
	expectedKeys := []string{"apple", "banana", "cherry"}
	for _, key := range expectedKeys {
		kv.Set(key, []byte("value"), 0)
	}

	// Get all keys
	keys := kv.Keys()
	sort.Strings(keys)
	sort.Strings(expectedKeys)

	if len(keys) != len(expectedKeys) {
		t.Errorf("Expected %d keys, got %d", len(expectedKeys), len(keys))
	}

	for i, key := range keys {
		if key != expectedKeys[i] {
			t.Errorf("Expected key %s, got %s", expectedKeys[i], key)
		}
	}
}

func TestStats(t *testing.T) {
	kv, cleanup := createTestStore(t)
	defer cleanup()

	// Perform some operations
	kv.Set("key1", []byte("value1"), 0)
	kv.Set("key2", []byte("value2"), 0)
	_, _ = kv.Get("key1") // Hit
	_, _ = kv.Get("key3") // Miss

	stats := kv.GetStats()

	if stats.Entries != 2 {
		t.Errorf("Expected 2 entries, got %d", stats.Entries)
	}

	if stats.Hits != 1 {
		t.Errorf("Expected 1 hit, got %d", stats.Hits)
	}

	if stats.Misses != 1 {
		t.Errorf("Expected 1 miss, got %d", stats.Misses)
	}

	expectedHitRatio := 0.5
	if stats.HitRatio != expectedHitRatio {
		t.Errorf("Expected hit ratio %f, got %f", expectedHitRatio, stats.HitRatio)
	}
}

func TestPersistence(t *testing.T) {
	dataDir := fmt.Sprintf("testdata_%d", time.Now().UnixNano())
	defer os.RemoveAll(dataDir)

	opts := Options{
		DataDir:     dataDir,
		MaxEntries:  1000,
		MaxMemoryMB: 10,
		ShardCount:  4,
	}

	// Create store and add data
	kv1, err := NewKVStore(opts)
	if err != nil {
		t.Fatalf("Failed to create first store: %v", err)
	}

	testData := map[string][]byte{
		"persistent1": []byte("value1"),
		"persistent2": []byte("value2"),
		"persistent3": []byte("value3"),
	}

	for k, v := range testData {
		if err = kv1.Set(k, v, 0); err != nil {
			t.Fatalf("Failed to set %s: %v", k, err)
		}
	}

	// Force WAL flush
	time.Sleep(50 * time.Millisecond)
	kv1.Close()

	// Create new store with same data directory
	kv2, err := NewKVStore(opts)
	if err != nil {
		t.Fatalf("Failed to create second store: %v", err)
	}
	defer kv2.Close()

	// Verify data persisted
	for k, expectedV := range testData {
		v, err := kv2.Get(k)
		if err != nil {
			t.Errorf("Failed to get %s: %v", k, err)
			continue
		}

		if !bytes.Equal(v, expectedV) {
			t.Errorf("Value mismatch for %s: expected %s, got %s", k, expectedV, v)
		}
	}
}

func TestSnapshotLoadUsesDistinctEntries(t *testing.T) {
	dataDir := fmt.Sprintf("testdata_%d", time.Now().UnixNano())

	opts := Options{
		DataDir:       dataDir,
		MaxEntries:    100,
		MaxMemoryMB:   10,
		FlushInterval: 10 * time.Millisecond,
		CleanInterval: time.Second,
		ShardCount:    2,
	}

	kv, err := NewKVStore(opts)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer func() {
		kv.Close()
		os.RemoveAll(dataDir)
	}()

	entries := map[string][]byte{
		"alpha": []byte("one"),
		"beta":  []byte("two"),
	}

	for k, v := range entries {
		if err = kv.Set(k, v, 0); err != nil {
			t.Fatalf("Set %s failed: %v", k, err)
		}
	}

	if err = kv.Snapshot(); err != nil {
		t.Fatalf("Snapshot failed: %v", err)
	}

	kv.Close()

	kvReloaded, err := NewKVStore(opts)
	if err != nil {
		t.Fatalf("Failed to reload store: %v", err)
	}
	defer kvReloaded.Close()

	for k, expected := range entries {
		value, err := kvReloaded.Get(k)
		if err != nil {
			t.Fatalf("Get %s after reload failed: %v", k, err)
		}

		if !bytes.Equal(value, expected) {
			t.Errorf("Value mismatch for %s: expected %s, got %s", k, expected, value)
		}
	}
}

func TestSnapshot(t *testing.T) {
	kv, cleanup := createTestStore(t)
	defer cleanup()

	// Add some data
	for i := 0; i < 10; i++ {
		key := fmt.Sprintf("snap_key_%d", i)
		value := []byte(fmt.Sprintf("snap_value_%d", i))
		kv.Set(key, value, 0)
	}

	// Create snapshot
	err := kv.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot failed: %v", err)
	}

	// Verify snapshot file exists
	snapshotPath := filepath.Join(kv.opts.DataDir, "snapshot.bin")
	if _, err := os.Stat(snapshotPath); os.IsNotExist(err) {
		t.Error("Snapshot file should exist")
	}

	// WAL should be reset after snapshot
	stats := kv.GetStats()
	if stats.WALSize > 0 {
		t.Error("WAL should be reset after snapshot")
	}
}

func TestSnapshotPreservesConcurrentAcknowledgedWrites(t *testing.T) {
	dataDir := t.TempDir()
	opts := Options{
		DataDir:       dataDir,
		MaxEntries:    1000,
		MaxMemoryMB:   10,
		FlushInterval: time.Hour,
		CleanInterval: time.Hour,
		ShardCount:    4,
	}

	kv, err := NewKVStore(opts)
	if err != nil {
		t.Fatalf("NewKVStore() error = %v", err)
	}

	acknowledged := make(map[string][]byte)
	var ackMu sync.Mutex
	var count atomic.Int64
	stop := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}

			key := fmt.Sprintf("concurrent_%04d", i)
			value := []byte(fmt.Sprintf("value_%04d", i))
			if err := kv.Set(key, value, 0); err != nil {
				return
			}

			ackMu.Lock()
			acknowledged[key] = append([]byte(nil), value...)
			ackMu.Unlock()
			count.Add(1)
			time.Sleep(100 * time.Microsecond)
		}
	}()

	for count.Load() < 10 {
		time.Sleep(100 * time.Microsecond)
	}

	if err := kv.Snapshot(); err != nil {
		close(stop)
		<-done
		_ = kv.Close()
		t.Fatalf("Snapshot() error = %v", err)
	}
	close(stop)
	<-done

	ackMu.Lock()
	expected := make(map[string][]byte, len(acknowledged))
	for k, v := range acknowledged {
		expected[k] = append([]byte(nil), v...)
	}
	ackMu.Unlock()

	if err := kv.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := NewKVStore(opts)
	if err != nil {
		t.Fatalf("reopen NewKVStore() error = %v", err)
	}
	defer reopened.Close()

	for key, want := range expected {
		got, err := reopened.Get(key)
		if err != nil {
			t.Fatalf("Get(%q) after reopen error = %v", key, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("Get(%q) = %q, want %q", key, got, want)
		}
	}
}

func TestCleanup(t *testing.T) {
	dataDir := fmt.Sprintf("testdata_%d", time.Now().UnixNano())
	defer os.RemoveAll(dataDir)

	opts := Options{
		DataDir:       dataDir,
		MaxEntries:    1000,
		MaxMemoryMB:   10,
		CleanInterval: 50 * time.Millisecond, // Fast cleanup for testing
		ShardCount:    4,
		WALSyncMode:   WALSyncInterval,
	}

	kv, err := NewKVStore(opts)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer kv.Close()

	// Add keys with short TTL
	ttl := 100 * time.Millisecond
	numKeys := 10

	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("cleanup_key_%d", i)
		value := []byte(fmt.Sprintf("cleanup_value_%d", i))
		kv.Set(key, value, ttl)
	}

	// Verify keys exist
	if kv.Size() != numKeys {
		t.Errorf("Expected %d keys, got %d", numKeys, kv.Size())
	}

	// Wait for TTL + cleanup cycle
	time.Sleep(ttl + 100*time.Millisecond)

	// Keys should be cleaned up
	if kv.Size() != 0 {
		t.Errorf("Expected 0 keys after cleanup, got %d", kv.Size())
	}
}

func TestGetExpiredIncrementsMisses(t *testing.T) {
	dataDir := fmt.Sprintf("testdata_%d", time.Now().UnixNano())
	defer os.RemoveAll(dataDir)

	opts := Options{
		DataDir:       dataDir,
		MaxEntries:    10,
		MaxMemoryMB:   1,
		FlushInterval: 10 * time.Millisecond,
		CleanInterval: time.Hour,
		ShardCount:    2,
	}

	kv, err := NewKVStore(opts)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer kv.Close()

	key := "ephemeral"
	if err = kv.Set(key, []byte("value"), 20*time.Millisecond); err != nil {
		t.Fatalf("Failed to set key with TTL: %v", err)
	}

	statsBefore := kv.GetStats()
	time.Sleep(40 * time.Millisecond)

	_, err = kv.Get(key)
	if err != ErrKeyExpired {
		t.Fatalf("Expected ErrKeyExpired, got %v", err)
	}

	statsAfter := kv.GetStats()
	if statsAfter.Misses != statsBefore.Misses+1 {
		t.Fatalf("Expected misses to increment after expired get: before=%d after=%d", statsBefore.Misses, statsAfter.Misses)
	}
}

func TestErrorConditions(t *testing.T) {
	kv, cleanup := createTestStore(t)
	defer cleanup()

	// Test operations on non-existent key
	_, err := kv.Get("nonexistent")
	if err != ErrKeyNotFound {
		t.Errorf("Expected ErrKeyNotFound, got %v", err)
	}

	err = kv.Delete("nonexistent")
	if err != ErrKeyNotFound {
		t.Errorf("Expected ErrKeyNotFound, got %v", err)
	}

	// Test operations on closed store
	kv.Close()

	err = kv.Set("key", []byte("value"), 0)
	if err != ErrStoreClosed {
		t.Errorf("Expected ErrStoreClosed, got %v", err)
	}

	_, err = kv.Get("key")
	if err != ErrStoreClosed {
		t.Errorf("Expected ErrStoreClosed, got %v", err)
	}

	err = kv.Delete("key")
	if err != ErrStoreClosed {
		t.Errorf("Expected ErrStoreClosed, got %v", err)
	}
}

// Benchmark tests

func BenchmarkSet(b *testing.B) {
	kv, cleanup := createTestStore(&testing.T{})
	defer cleanup()

	value := []byte("benchmark_value")

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := fmt.Sprintf("bench_key_%d", i)
			kv.Set(key, value, 0)
			i++
		}
	})
}

func BenchmarkGet(b *testing.B) {
	kv, cleanup := createTestStore(&testing.T{})
	defer cleanup()

	// Pre-populate with data
	numKeys := 10000
	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("bench_key_%d", i)
		value := []byte(fmt.Sprintf("bench_value_%d", i))
		kv.Set(key, value, 0)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := fmt.Sprintf("bench_key_%d", i%numKeys)
			_, _ = kv.Get(key)
			i++
		}
	})
}

func BenchmarkMixed(b *testing.B) {
	kv, cleanup := createTestStore(&testing.T{})
	defer cleanup()

	// Pre-populate
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("key_%d", i)
		value := []byte(fmt.Sprintf("value_%d", i))
		kv.Set(key, value, 0)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := fmt.Sprintf("key_%d", i%1000)

			// 70% reads, 30% writes
			if i%10 < 7 {
				_, _ = kv.Get(key)
			} else {
				value := []byte(fmt.Sprintf("new_value_%d", i))
				kv.Set(key, value, 0)
			}
			i++
		}
	})
}

// Stress tests

func TestStressLargeValues(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	kv, cleanup := createTestStore(t)
	defer cleanup()

	// Test with 1MB values
	largeValue := make([]byte, 1024*1024)
	for i := range largeValue {
		largeValue[i] = byte(i % 256)
	}

	numKeys := 10
	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("large_key_%d", i)
		if err := kv.Set(key, largeValue, 0); err != nil {
			t.Fatalf("Failed to set large value: %v", err)
		}
	}

	// Verify all values
	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("large_key_%d", i)
		value, err := kv.Get(key)
		if err != nil {
			t.Fatalf("Failed to get large value: %v", err)
		}

		if !bytes.Equal(value, largeValue) {
			t.Errorf("Large value corrupted for key %s", key)
		}
	}
}

func TestStressHighConcurrency(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	kv, cleanup := createTestStore(t)
	defer cleanup()

	numWorkers := 50
	opsPerWorker := 1000
	var wg sync.WaitGroup

	// Track operations
	var sets, gets, deletes int64

	wg.Add(numWorkers)
	for i := 0; i < numWorkers; i++ {
		go func(workerID int) {
			defer wg.Done()

			for j := 0; j < opsPerWorker; j++ {
				key := fmt.Sprintf("stress_%d_%d", workerID, j)
				value := []byte(fmt.Sprintf("value_%d_%d", workerID, j))

				op := j % 3
				switch op {
				case 0: // Set
					kv.Set(key, value, 0)
					atomic.AddInt64(&sets, 1)
				case 1: // Get
					_, _ = kv.Get(key)
					atomic.AddInt64(&gets, 1)
				case 2: // Delete
					_ = kv.Delete(key)
					atomic.AddInt64(&deletes, 1)
				}
			}
		}(i)
	}

	wg.Wait()

	t.Logf("Completed stress test: %d sets, %d gets, %d deletes",
		atomic.LoadInt64(&sets),
		atomic.LoadInt64(&gets),
		atomic.LoadInt64(&deletes))

	// Verify store is still functional
	testKey := "post_stress_test"
	testValue := []byte("post_stress_value")

	if err := kv.Set(testKey, testValue, 0); err != nil {
		t.Fatalf("Store not functional after stress test: %v", err)
	}

	retrievedValue, err := kv.Get(testKey)
	if err != nil {
		t.Fatalf("Failed to get value after stress test: %v", err)
	}

	if !bytes.Equal(testValue, retrievedValue) {
		t.Error("Value corrupted after stress test")
	}
}

// Memory usage test
func TestMemoryUsage(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping memory test in short mode")
	}

	kv, cleanup := createTestStore(t)
	defer cleanup()

	initialStats := kv.GetStats()
	initialMemory := initialStats.MemoryUsage

	// Add known amount of data
	numEntries := 1000
	valueSize := 1024
	value := make([]byte, valueSize)

	for i := 0; i < numEntries; i++ {
		key := fmt.Sprintf("mem_key_%d", i)
		kv.Set(key, value, 0)
	}

	finalStats := kv.GetStats()
	finalMemory := finalStats.MemoryUsage

	memoryIncrease := finalMemory - initialMemory
	t.Logf("Memory increase: %d bytes for %d entries", memoryIncrease, numEntries)

	// Memory usage should have increased
	if memoryIncrease <= 0 {
		t.Error("Memory usage should have increased")
	}

	// Rough check - should be somewhat proportional to data added
	expectedMinIncrease := int64(numEntries * valueSize / 2) // Allow for overhead
	if memoryIncrease < expectedMinIncrease {
		t.Errorf("Memory increase too small: got %d, expected at least %d",
			memoryIncrease, expectedMinIncrease)
	}
}

// Default store test
func TestDefault(t *testing.T) {
	kv, err := Default(t.TempDir())
	if err != nil {
		t.Fatalf("Failed to create default store: %v", err)
	}
	defer kv.Close()

	// Basic functionality test
	err = kv.Set("test", []byte("value"), 0)
	if err != nil {
		t.Fatalf("Default store Set failed: %v", err)
	}

	value, err := kv.Get("test")
	if err != nil {
		t.Fatalf("Default store Get failed: %v", err)
	}

	if string(value) != "value" {
		t.Errorf("Expected 'value', got '%s'", string(value))
	}
}

func TestDefaultRequiresExplicitDataDir(t *testing.T) {
	_, err := Default("")
	if err == nil || !strings.Contains(err.Error(), "data dir is required") {
		t.Fatalf("Default empty data dir error = %v, want data dir required", err)
	}
}

// Test metrics collector integration
func TestMetricsCollector(t *testing.T) {
	kv, cleanup := createTestStore(t)
	defer cleanup()

	// Test GetMetricsCollector returns nil initially
	if collector := kv.GetMetricsCollector(); collector != nil {
		t.Errorf("Expected nil collector, got %v", collector)
	}

	// Test SetMetricsCollector
	mockCollector := &mockMetricsCollector{
		NoopCollector: metrics.NewNoopCollector(),
	}
	kv.SetMetricsCollector(mockCollector)

	// Verify collector was set
	if collector := kv.GetMetricsCollector(); collector == nil {
		t.Error("Collector not set correctly")
	}

	// Test recordMetrics doesn't panic with nil collector
	kv.SetMetricsCollector(nil)
	kv.recordMetrics("test", "key", time.Millisecond, nil, true)

	// Test recordMetrics with valid collector
	kv.SetMetricsCollector(mockCollector)
	kv.recordMetrics("test", "key", time.Millisecond, nil, true)

	if err := kv.Set("metrics-key", []byte("value"), 0); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if _, err := kv.Get("metrics-key"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, err := kv.Get("missing-metrics-key"); err != ErrKeyNotFound {
		t.Fatalf("missing Get = %v, want ErrKeyNotFound", err)
	}
	if err := kv.Delete("metrics-key"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	mockCollector.mu.Lock()
	defer mockCollector.mu.Unlock()
	wantOps := []string{"test", "set", "get", "get", "delete"}
	if len(mockCollector.records) != len(wantOps) {
		t.Fatalf("recorded operations = %d, want %d: %+v", len(mockCollector.records), len(wantOps), mockCollector.records)
	}
	for i, want := range wantOps {
		if got := mockCollector.records[i].operation; got != want {
			t.Fatalf("operation[%d] = %q, want %q", i, got, want)
		}
	}
	if !mockCollector.records[2].hit {
		t.Fatalf("successful get should record hit")
	}
	if mockCollector.records[3].err == nil || mockCollector.records[3].hit {
		t.Fatalf("missing get should record error miss: %+v", mockCollector.records[3])
	}
}

// mockMetricsCollector embeds NoopCollector for cleaner mock implementation.
// The local KV observer method is defined below; NoopCollector provides the
// stable metrics no-op surface used by shared test helpers.
type mockMetricsCollector struct {
	*metrics.NoopCollector
	mu      sync.Mutex
	records []kvObservation
}

type kvObservation struct {
	operation string
	key       string
	err       error
	hit       bool
}

func (m *mockMetricsCollector) ObserveKV(ctx context.Context, operation, key string, duration time.Duration, err error, hit bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.records = append(m.records, kvObservation{
		operation: operation,
		key:       key,
		err:       err,
		hit:       hit,
	})
}

// Test validate options with more edge cases
func TestValidateOptionsExtended(t *testing.T) {
	tests := []struct {
		name    string
		opts    Options
		wantErr bool
		errMsg  string
	}{
		{
			name: "negative max entries",
			opts: Options{
				DataDir:     "testdata",
				MaxEntries:  -1,
				MaxMemoryMB: 10,
				ShardCount:  4,
			},
			wantErr: true,
			errMsg:  "max entries must be positive",
		},
		{
			name: "negative max memory",
			opts: Options{
				DataDir:     "testdata",
				MaxEntries:  1000,
				MaxMemoryMB: -1,
				ShardCount:  4,
			},
			wantErr: true,
			errMsg:  "max memory must be positive",
		},
		{
			name: "zero shard count",
			opts: Options{
				DataDir:     "testdata",
				MaxEntries:  1000,
				MaxMemoryMB: 10,
				ShardCount:  0,
			},
			wantErr: true,
			errMsg:  "shard count must be power of 2",
		},
		{
			name: "non-power-of-2 shard count",
			opts: Options{
				DataDir:     "testdata",
				MaxEntries:  1000,
				MaxMemoryMB: 10,
				ShardCount:  7,
			},
			wantErr: true,
			errMsg:  "shard count must be power of 2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateOptions(&tt.opts)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateOptions() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil && err.Error() != tt.errMsg {
				t.Errorf("validateOptions() error message = %v, want %v", err.Error(), tt.errMsg)
			}
		})
	}
}

// Test ReadOnly mode
func TestReadOnlyMode(t *testing.T) {
	dataDir := fmt.Sprintf("testdata_%d", time.Now().UnixNano())
	defer os.RemoveAll(dataDir)

	// First create a store and add some data
	opts := Options{
		DataDir:     dataDir,
		MaxEntries:  1000,
		MaxMemoryMB: 10,
		ShardCount:  4,
	}

	kv1, err := NewKVStore(opts)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	testData := map[string][]byte{
		"key1": []byte("value1"),
		"key2": []byte("value2"),
	}

	for k, v := range testData {
		if err := kv1.Set(k, v, 0); err != nil {
			t.Fatalf("Failed to set %s: %v", k, err)
		}
	}

	time.Sleep(50 * time.Millisecond)
	kv1.Close()

	// Now open in readonly mode
	opts.ReadOnly = true
	kv2, err := NewKVStore(opts)
	if err != nil {
		t.Fatalf("Failed to create readonly store: %v", err)
	}
	defer kv2.Close()

	// Should be able to read existing data
	for k, expectedV := range testData {
		v, err := kv2.Get(k)
		if err != nil {
			t.Errorf("Failed to get %s in readonly mode: %v", k, err)
			continue
		}
		if !bytes.Equal(v, expectedV) {
			t.Errorf("Value mismatch for %s", k)
		}
	}

	// Should not be able to write
	err = kv2.Set("newkey", []byte("newvalue"), 0)
	if err != ErrStoreClosed {
		t.Errorf("Expected ErrStoreClosed for Set in readonly mode, got %v", err)
	}

	// Should not be able to delete
	err = kv2.Delete("key1")
	if err != ErrStoreClosed {
		t.Errorf("Expected ErrStoreClosed for Delete in readonly mode, got %v", err)
	}

	// Should not be able to snapshot
	err = kv2.Snapshot()
	if err != ErrStoreClosed {
		t.Errorf("Expected ErrStoreClosed for Snapshot in readonly mode, got %v", err)
	}
}

// Test snapshot with compression
func TestSnapshotWithCompression(t *testing.T) {
	dataDir := fmt.Sprintf("testdata_%d", time.Now().UnixNano())
	defer os.RemoveAll(dataDir)

	opts := Options{
		DataDir:           dataDir,
		MaxEntries:        1000,
		MaxMemoryMB:       10,
		ShardCount:        4,
		EnableCompression: true,
	}

	kv, err := NewKVStore(opts)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Add data
	for i := 0; i < 100; i++ {
		key := fmt.Sprintf("compressed_key_%d", i)
		value := []byte(fmt.Sprintf("compressed_value_%d", i))
		if err := kv.Set(key, value, 0); err != nil {
			t.Fatalf("Failed to set key: %v", err)
		}
	}

	// Create snapshot
	if err := kv.Snapshot(); err != nil {
		t.Fatalf("Snapshot failed: %v", err)
	}

	kv.Close()

	// Reload from snapshot
	kv2, err := NewKVStore(opts)
	if err != nil {
		t.Fatalf("Failed to reload store: %v", err)
	}
	defer kv2.Close()

	// Verify data
	for i := 0; i < 100; i++ {
		key := fmt.Sprintf("compressed_key_%d", i)
		expected := []byte(fmt.Sprintf("compressed_value_%d", i))

		value, err := kv2.Get(key)
		if err != nil {
			t.Errorf("Failed to get %s: %v", key, err)
			continue
		}

		if !bytes.Equal(value, expected) {
			t.Errorf("Value mismatch for %s", key)
		}
	}
}

// Test Keys with TTL expiration
func TestKeysWithExpiredEntries(t *testing.T) {
	kv, cleanup := createTestStore(t)
	defer cleanup()

	// Add keys with different TTLs
	kv.Set("permanent", []byte("value"), 0)
	kv.Set("short_ttl", []byte("value"), 50*time.Millisecond)
	kv.Set("long_ttl", []byte("value"), 1*time.Hour)

	// Wait for short TTL to expire
	time.Sleep(100 * time.Millisecond)

	keys := kv.Keys()
	sort.Strings(keys)

	expected := []string{"long_ttl", "permanent"}
	if len(keys) != len(expected) {
		t.Errorf("Expected %d keys, got %d", len(expected), len(keys))
	}

	for i, key := range keys {
		if i < len(expected) && key != expected[i] {
			t.Errorf("Expected key %s, got %s", expected[i], key)
		}
	}
}

// Test Exists with expired key
func TestExistsWithExpiredKey(t *testing.T) {
	kv, cleanup := createTestStore(t)
	defer cleanup()

	key := "expire_test"
	kv.Set(key, []byte("value"), 50*time.Millisecond)

	if !kv.Exists(key) {
		t.Error("Key should exist immediately")
	}

	time.Sleep(100 * time.Millisecond)

	if kv.Exists(key) {
		t.Error("Key should not exist after expiration")
	}
}

// Test Exists on closed store
func TestExistsOnClosedStore(t *testing.T) {
	kv, cleanup := createTestStore(t)
	kv.Set("key", []byte("value"), 0)
	kv.Close()
	defer cleanup()

	if kv.Exists("key") {
		t.Error("Exists should return false on closed store")
	}
}

// Test Keys on closed store
func TestKeysOnClosedStore(t *testing.T) {
	kv, cleanup := createTestStore(t)
	kv.Set("key", []byte("value"), 0)
	kv.Close()
	defer cleanup()

	keys := kv.Keys()
	if keys != nil {
		t.Errorf("Keys should return nil on closed store, got %v", keys)
	}
}

// Test close timeout
func TestCloseTimeout(t *testing.T) {
	dataDir := fmt.Sprintf("testdata_%d", time.Now().UnixNano())
	defer os.RemoveAll(dataDir)

	opts := Options{
		DataDir:      dataDir,
		MaxEntries:   1000,
		MaxMemoryMB:  10,
		ShardCount:   4,
		CloseTimeout: 1 * time.Millisecond, // Very short timeout
	}

	kv, err := NewKVStore(opts)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Close should complete even with short timeout
	// (in this case it should succeed because workers should finish quickly)
	err = kv.Close()
	if err != nil && err != ErrCloseTimeout {
		t.Errorf("Unexpected error on close: %v", err)
	}
}

func TestCloseTimeoutStillClosesWAL(t *testing.T) {
	dataDir := fmt.Sprintf("testdata_%d", time.Now().UnixNano())
	defer os.RemoveAll(dataDir)

	opts := Options{
		DataDir:       dataDir,
		MaxEntries:    1000,
		MaxMemoryMB:   10,
		ShardCount:    4,
		FlushInterval: time.Hour,
		CleanInterval: time.Hour,
		CloseTimeout:  1 * time.Millisecond,
		WALSyncMode:   WALSyncInterval,
	}
	kv, err := NewKVStore(opts)
	if err != nil {
		t.Fatalf("NewKVStore: %v", err)
	}
	if err := kv.Set("timeout-key", []byte("timeout-value"), 0); err != nil {
		t.Fatalf("Set: %v", err)
	}

	kv.wg.Add(1)
	err = kv.Close()
	kv.wg.Done()
	if !errors.Is(err, ErrCloseTimeout) {
		t.Fatalf("Close error = %v, want ErrCloseTimeout", err)
	}

	if _, err := kv.walFile.Write([]byte("after close")); err == nil {
		t.Fatal("WAL file accepted write after Close timeout; want closed file")
	}

	reloaded, err := NewKVStore(opts)
	if err != nil {
		t.Fatalf("reload after close timeout: %v", err)
	}
	defer reloaded.Close()

	got, err := reloaded.Get("timeout-key")
	if err != nil {
		t.Fatalf("Get after reload: %v", err)
	}
	if !bytes.Equal(got, []byte("timeout-value")) {
		t.Fatalf("Get after reload = %q, want timeout-value", got)
	}
}

// Test double close
func TestDoubleClose(t *testing.T) {
	kv, cleanup := createTestStore(t)
	defer cleanup()

	// First close
	err := kv.Close()
	if err != nil {
		t.Fatalf("First close failed: %v", err)
	}

	// Second close should return the original close result.
	err = kv.Close()
	if err != nil {
		t.Errorf("Expected nil on second close, got %v", err)
	}
}

func TestMetricsCollectorConcurrentAccess(t *testing.T) {
	kv, cleanup := createTestStore(t)
	defer cleanup()

	collector := &mockMetricsCollector{
		NoopCollector: metrics.NewNoopCollector(),
	}

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if (id+j)%2 == 0 {
					kv.SetMetricsCollector(collector)
				} else {
					kv.SetMetricsCollector(nil)
				}
				_ = kv.GetMetricsCollector()
				kv.recordMetrics("test", "key", time.Millisecond, nil, true)
			}
		}(i)
	}
	wg.Wait()
}

// Test WAL replay with delete operations
func TestWALReplayWithDeletes(t *testing.T) {
	dataDir := fmt.Sprintf("testdata_%d", time.Now().UnixNano())
	defer os.RemoveAll(dataDir)

	opts := Options{
		DataDir:     dataDir,
		MaxEntries:  1000,
		MaxMemoryMB: 10,
		ShardCount:  4,
	}

	// Create store and perform operations
	kv1, err := NewKVStore(opts)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	kv1.Set("key1", []byte("value1"), 0)
	kv1.Set("key2", []byte("value2"), 0)
	kv1.Set("key3", []byte("value3"), 0)
	_ = kv1.Delete("key2")

	time.Sleep(50 * time.Millisecond)
	kv1.Close()

	// Reload and verify
	kv2, err := NewKVStore(opts)
	if err != nil {
		t.Fatalf("Failed to reload store: %v", err)
	}
	defer kv2.Close()

	// key1 and key3 should exist
	if _, err := kv2.Get("key1"); err != nil {
		t.Errorf("key1 should exist after reload: %v", err)
	}
	if _, err := kv2.Get("key3"); err != nil {
		t.Errorf("key3 should exist after reload: %v", err)
	}

	// key2 should not exist (was deleted)
	if _, err := kv2.Get("key2"); err != ErrKeyNotFound {
		t.Errorf("key2 should not exist after reload, got error: %v", err)
	}
}

// Test set defaults with various configurations
func TestSetDefaults(t *testing.T) {
	tests := []struct {
		name     string
		opts     Options
		expected Options
	}{
		{
			name: "all defaults except data dir",
			opts: Options{},
			expected: Options{
				MaxEntries:    defaultMaxEntries,
				MaxMemoryMB:   defaultMaxMemoryMB,
				FlushInterval: defaultFlushInterval,
				CleanInterval: defaultCleanInterval,
				ShardCount:    defaultShardCount,
				CloseTimeout:  defaultCloseTimeout,
				WALSyncMode:   WALSyncImmediate,
			},
		},
		{
			name: "partial defaults",
			opts: Options{
				DataDir:    "custom",
				MaxEntries: 5000,
			},
			expected: Options{
				DataDir:       "custom",
				MaxEntries:    5000,
				MaxMemoryMB:   defaultMaxMemoryMB,
				FlushInterval: defaultFlushInterval,
				CleanInterval: defaultCleanInterval,
				ShardCount:    defaultShardCount,
				CloseTimeout:  defaultCloseTimeout,
				WALSyncMode:   WALSyncImmediate,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := setDefaults(&tt.opts)
			if err != nil {
				t.Fatalf("setDefaults failed: %v", err)
			}

			if tt.opts.DataDir != tt.expected.DataDir {
				t.Errorf("DataDir: got %v, want %v", tt.opts.DataDir, tt.expected.DataDir)
			}
			if tt.opts.MaxEntries != tt.expected.MaxEntries {
				t.Errorf("MaxEntries: got %v, want %v", tt.opts.MaxEntries, tt.expected.MaxEntries)
			}
			if tt.opts.MaxMemoryMB != tt.expected.MaxMemoryMB {
				t.Errorf("MaxMemoryMB: got %v, want %v", tt.opts.MaxMemoryMB, tt.expected.MaxMemoryMB)
			}
			if tt.opts.ShardCount != tt.expected.ShardCount {
				t.Errorf("ShardCount: got %v, want %v", tt.opts.ShardCount, tt.expected.ShardCount)
			}
			if tt.opts.WALSyncMode != tt.expected.WALSyncMode {
				t.Errorf("WALSyncMode: got %v, want %v", tt.opts.WALSyncMode, tt.expected.WALSyncMode)
			}
		})
	}
}

func TestWALSyncModeImmediateFlushesAcknowledgedSet(t *testing.T) {
	dataDir := t.TempDir()
	kv, err := NewKVStore(Options{
		DataDir:       dataDir,
		MaxEntries:    100,
		MaxMemoryMB:   10,
		FlushInterval: time.Hour,
		CleanInterval: time.Hour,
		ShardCount:    4,
		WALSyncMode:   WALSyncImmediate,
	})
	if err != nil {
		t.Fatalf("NewKVStore() error = %v", err)
	}
	defer kv.Close()

	if err := kv.Set("synced", []byte("value"), 0); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	info, err := os.Stat(filepath.Join(dataDir, "store.wal"))
	if err != nil {
		t.Fatalf("stat WAL: %v", err)
	}
	if info.Size() == 0 {
		t.Fatalf("WAL size after acknowledged Set = 0, want flushed content")
	}
}

func TestValidateOptionsRejectsInvalidWALSyncMode(t *testing.T) {
	opts := Options{
		DataDir:     t.TempDir(),
		MaxEntries:  1,
		MaxMemoryMB: 1,
		ShardCount:  1,
		WALSyncMode: "sometimes",
	}
	if err := validateOptions(&opts); err == nil || !strings.Contains(err.Error(), "invalid WAL sync mode") {
		t.Fatalf("validateOptions() error = %v, want invalid WAL sync mode", err)
	}
}

// Test update existing key
func TestUpdateExistingKey(t *testing.T) {
	kv, cleanup := createTestStore(t)
	defer cleanup()

	key := "update_test"

	// Set initial value
	kv.Set(key, []byte("value1"), 0)

	// Update with new value
	kv.Set(key, []byte("value2"), 0)

	// Verify new value
	value, err := kv.Get(key)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if string(value) != "value2" {
		t.Errorf("Expected 'value2', got '%s'", string(value))
	}

	// Should only have one entry
	if kv.Size() != 1 {
		t.Errorf("Expected 1 entry, got %d", kv.Size())
	}
}

// Test snapshot with expired entries
func TestSnapshotWithExpiredEntries(t *testing.T) {
	dataDir := fmt.Sprintf("testdata_%d", time.Now().UnixNano())
	defer os.RemoveAll(dataDir)

	opts := Options{
		DataDir:     dataDir,
		MaxEntries:  1000,
		MaxMemoryMB: 10,
		ShardCount:  4,
	}

	kv, err := NewKVStore(opts)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Add permanent and expiring entries
	kv.Set("permanent", []byte("value"), 0)
	kv.Set("expiring", []byte("value"), 50*time.Millisecond)

	// Wait for expiration
	time.Sleep(100 * time.Millisecond)

	// Create snapshot - should only include non-expired entries
	if err := kv.Snapshot(); err != nil {
		t.Fatalf("Snapshot failed: %v", err)
	}

	kv.Close()

	// Reload
	kv2, err := NewKVStore(opts)
	if err != nil {
		t.Fatalf("Failed to reload: %v", err)
	}
	defer kv2.Close()

	// Permanent entry should exist
	if _, err := kv2.Get("permanent"); err != nil {
		t.Errorf("Permanent entry should exist: %v", err)
	}

	// Expired entry should not exist
	if _, err := kv2.Get("expiring"); err != ErrKeyNotFound {
		t.Errorf("Expired entry should not exist, got error: %v", err)
	}
}

// Test Get with concurrent expiration cleanup
func TestGetWithConcurrentExpiration(t *testing.T) {
	kv, cleanup := createTestStore(t)
	defer cleanup()

	key := "concurrent_expire"
	kv.Set(key, []byte("value"), 50*time.Millisecond)

	// Start concurrent gets
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			time.Sleep(60 * time.Millisecond)
			// This should handle concurrent expiration gracefully
			_, _ = kv.Get(key)
		}()
	}

	wg.Wait()
}

// Test WAL replay with corrupted entries
func TestWALReplayWithCorruptedEntries(t *testing.T) {
	dataDir := fmt.Sprintf("testdata_%d", time.Now().UnixNano())
	defer os.RemoveAll(dataDir)

	opts := Options{
		DataDir:     dataDir,
		MaxEntries:  1000,
		MaxMemoryMB: 10,
		ShardCount:  4,
	}

	// Create store and add data
	kv1, err := NewKVStore(opts)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	kv1.Set("key1", []byte("value1"), 0)
	kv1.Set("key2", []byte("value2"), 0)

	time.Sleep(50 * time.Millisecond)
	kv1.Close()

	// Append corrupted data to WAL
	walPath := filepath.Join(dataDir, "store.wal")
	f, err := os.OpenFile(walPath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("Failed to open WAL: %v", err)
	}
	f.WriteString("{invalid json}\n")
	f.Close()

	// Reload should fail closed on WAL corruption.
	_, err = NewKVStore(opts)
	if !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("reload error = %v, want ErrInvalidEntry", err)
	}
}

func TestWALAutoDetectModeCanBeDisabled(t *testing.T) {
	dataDir := fmt.Sprintf("testdata_%d", time.Now().UnixNano())
	defer os.RemoveAll(dataDir)

	opts := Options{
		DataDir:          dataDir,
		MaxEntries:       1000,
		MaxMemoryMB:      10,
		ShardCount:       4,
		SerializerFormat: FormatBinary,
	}

	kv1, err := NewKVStore(opts)
	if err != nil {
		t.Fatalf("create binary store: %v", err)
	}
	if err := kv1.Set("key1", []byte("value1"), 0); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := kv1.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	autoOpts := opts
	autoOpts.SerializerFormat = FormatJSON
	kv2, err := NewKVStore(autoOpts)
	if err != nil {
		t.Fatalf("auto-detect reload: %v", err)
	}
	_ = kv2.Close()

	disabledOpts := autoOpts
	disabledOpts.AutoDetectMode = AutoDetectDisabled
	_, err = NewKVStore(disabledOpts)
	if !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("disabled auto-detect reload error = %v, want ErrInvalidEntry", err)
	}
}

func TestInvalidAutoDetectModeRejected(t *testing.T) {
	dataDir := fmt.Sprintf("testdata_%d", time.Now().UnixNano())
	defer os.RemoveAll(dataDir)

	opts := Options{
		DataDir:        dataDir,
		MaxEntries:     1000,
		MaxMemoryMB:    10,
		ShardCount:     4,
		AutoDetectMode: FormatAutoDetectMode("sometimes"),
	}

	_, err := NewKVStore(opts)
	if err == nil || !strings.Contains(err.Error(), "invalid auto-detect mode") {
		t.Fatalf("NewKVStore error = %v, want invalid auto-detect mode", err)
	}
}

// Test snapshot on empty store
func TestSnapshotEmptyStore(t *testing.T) {
	kv, cleanup := createTestStore(t)
	defer cleanup()

	// Snapshot empty store
	if err := kv.Snapshot(); err != nil {
		t.Fatalf("Snapshot of empty store failed: %v", err)
	}

	// Verify snapshot file was created
	snapshotPath := filepath.Join(kv.opts.DataDir, "snapshot.bin")
	if _, err := os.Stat(snapshotPath); os.IsNotExist(err) {
		t.Error("Snapshot file should exist even for empty store")
	}
}

func TestCompressedSnapshotInvalidGzipFailsClosed(t *testing.T) {
	dataDir := fmt.Sprintf("testdata_%d", time.Now().UnixNano())
	defer os.RemoveAll(dataDir)

	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatalf("mkdir data dir: %v", err)
	}
	snapshotPath := filepath.Join(dataDir, "snapshot.bin")
	if err := os.WriteFile(snapshotPath, []byte("not gzip snapshot data"), 0644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}

	opts := Options{
		DataDir:           dataDir,
		MaxEntries:        1000,
		MaxMemoryMB:       10,
		ShardCount:        4,
		EnableCompression: true,
	}

	_, err := NewKVStore(opts)
	if err == nil || !strings.Contains(err.Error(), "open compressed snapshot") {
		t.Fatalf("NewKVStore error = %v, want compressed snapshot error", err)
	}
}

func TestCompressedJSONSnapshotAutoDetectsAfterDecompression(t *testing.T) {
	dataDir := fmt.Sprintf("testdata_%d", time.Now().UnixNano())
	defer os.RemoveAll(dataDir)

	writeOpts := Options{
		DataDir:           dataDir,
		MaxEntries:        1000,
		MaxMemoryMB:       10,
		ShardCount:        4,
		EnableCompression: true,
		SerializerFormat:  FormatJSON,
	}
	kv, err := NewKVStore(writeOpts)
	if err != nil {
		t.Fatalf("create JSON store: %v", err)
	}
	if err := kv.Set("compressed-json-key", []byte("compressed-json-value"), 0); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := kv.Snapshot(); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if err := kv.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	readOpts := writeOpts
	readOpts.SerializerFormat = FormatBinary
	readOpts.AutoDetectMode = AutoDetectEnabled
	reloaded, err := NewKVStore(readOpts)
	if err != nil {
		t.Fatalf("reload compressed JSON snapshot with auto-detect: %v", err)
	}
	defer reloaded.Close()

	got, err := reloaded.Get("compressed-json-key")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Equal(got, []byte("compressed-json-value")) {
		t.Fatalf("Get = %q, want compressed-json-value", got)
	}
}

func TestUnknownSnapshotFormatFailsClosed(t *testing.T) {
	dataDir := fmt.Sprintf("testdata_%d", time.Now().UnixNano())
	defer os.RemoveAll(dataDir)

	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatalf("mkdir data dir: %v", err)
	}
	snapshotPath := filepath.Join(dataDir, "snapshot.bin")
	if err := os.WriteFile(snapshotPath, []byte("???? invalid snapshot"), 0644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}

	opts := Options{
		DataDir:     dataDir,
		MaxEntries:  1000,
		MaxMemoryMB: 10,
		ShardCount:  4,
	}

	_, err := NewKVStore(opts)
	if err == nil || !strings.Contains(err.Error(), "unknown snapshot format") {
		t.Fatalf("NewKVStore error = %v, want unknown snapshot format", err)
	}
}

// Test resetWAL error handling
func TestResetWALErrorHandling(t *testing.T) {
	dataDir := fmt.Sprintf("testdata_%d", time.Now().UnixNano())
	defer os.RemoveAll(dataDir)

	opts := Options{
		DataDir:     dataDir,
		MaxEntries:  1000,
		MaxMemoryMB: 10,
		ShardCount:  4,
	}

	kv, err := NewKVStore(opts)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer kv.Close()

	kv.Set("key", []byte("value"), 0)

	// Create snapshot which triggers resetWAL
	if err := kv.Snapshot(); err != nil {
		t.Fatalf("Snapshot failed: %v", err)
	}

	// Verify WAL was reset
	stats := kv.GetStats()
	if stats.WALSize > 0 {
		t.Error("WAL should be empty after reset")
	}
}

// Test evictLRU with nil tail
func TestEvictLRUWithNilTail(t *testing.T) {
	dataDir := fmt.Sprintf("testdata_%d", time.Now().UnixNano())
	defer os.RemoveAll(dataDir)

	opts := Options{
		DataDir:     dataDir,
		MaxEntries:  1,
		MaxMemoryMB: 1,
		ShardCount:  2,
	}

	kv, err := NewKVStore(opts)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer kv.Close()

	// Add entry to trigger eviction on the shard with nil tail
	kv.Set("key1", []byte("value1"), 0)
	kv.Set("key2", []byte("value2"), 0) // This might trigger eviction
}

// Test Get with retry path (concurrent expiration and recreation)
func TestGetWithRetryPath(t *testing.T) {
	kv, cleanup := createTestStore(t)
	defer cleanup()

	key := "retry_test"

	// Set with short TTL
	kv.Set(key, []byte("value1"), 50*time.Millisecond)

	// Wait for near expiration
	time.Sleep(55 * time.Millisecond)

	// Concurrent operations: one might trigger expiration, another might recreate
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		_, _ = kv.Get(key) // Might trigger expiration
	}()

	go func() {
		defer wg.Done()
		time.Sleep(5 * time.Millisecond)
		kv.Set(key, []byte("value2"), 0) // Recreate
	}()

	wg.Wait()
}

// Test cleanExpired with various scenarios
func TestCleanExpiredComprehensive(t *testing.T) {
	dataDir := fmt.Sprintf("testdata_%d", time.Now().UnixNano())
	defer os.RemoveAll(dataDir)

	opts := Options{
		DataDir:       dataDir,
		MaxEntries:    1000,
		MaxMemoryMB:   10,
		CleanInterval: 30 * time.Millisecond,
		ShardCount:    4,
		WALSyncMode:   WALSyncInterval,
	}

	kv, err := NewKVStore(opts)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer kv.Close()

	// Add mix of permanent and expiring keys across shards
	for i := 0; i < 50; i++ {
		key := fmt.Sprintf("perm_%d", i)
		kv.Set(key, []byte("value"), 0)
	}

	for i := 0; i < 50; i++ {
		key := fmt.Sprintf("expire_%d", i)
		kv.Set(key, []byte("value"), 50*time.Millisecond)
	}

	initialSize := kv.Size()
	if initialSize != 100 {
		t.Errorf("Expected 100 entries, got %d", initialSize)
	}

	// Wait for cleanup cycles
	time.Sleep(150 * time.Millisecond)

	// Should have cleaned up expired entries
	finalSize := kv.Size()
	if finalSize >= initialSize {
		t.Errorf("Expected entries to be cleaned up, initial=%d final=%d", initialSize, finalSize)
	}

	if finalSize != 50 {
		t.Logf("Warning: Expected 50 permanent entries, got %d (cleanup might be in progress)", finalSize)
	}
}

// Test loadSnapshot with non-existent file
func TestLoadSnapshotNonExistent(t *testing.T) {
	dataDir := fmt.Sprintf("testdata_%d", time.Now().UnixNano())
	defer os.RemoveAll(dataDir)

	opts := Options{
		DataDir:     dataDir,
		MaxEntries:  1000,
		MaxMemoryMB: 10,
		ShardCount:  4,
	}

	// Creating a new store with no snapshot file should succeed
	kv, err := NewKVStore(opts)
	if err != nil {
		t.Fatalf("Failed to create store without snapshot: %v", err)
	}
	defer kv.Close()

	// Should start empty
	if kv.Size() != 0 {
		t.Errorf("Expected empty store, got %d entries", kv.Size())
	}
}

// Test replayWAL with non-existent file
func TestReplayWALNonExistent(t *testing.T) {
	dataDir := fmt.Sprintf("testdata_%d", time.Now().UnixNano())
	defer os.RemoveAll(dataDir)

	opts := Options{
		DataDir:     dataDir,
		MaxEntries:  1000,
		MaxMemoryMB: 10,
		ShardCount:  4,
	}

	// Creating store without WAL should succeed
	kv, err := NewKVStore(opts)
	if err != nil {
		t.Fatalf("Failed to create store without WAL: %v", err)
	}
	defer kv.Close()
}

// Test Get with TryLock failure path
func TestGetWithLRUContentions(t *testing.T) {
	kv, cleanup := createTestStore(t)
	defer cleanup()

	key := "contention_test"
	kv.Set(key, []byte("value"), 0)

	// Create heavy contention on the shard
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				// Mix of reads and writes to create lock contention
				if j%2 == 0 {
					_, _ = kv.Get(key)
				} else {
					testKey := fmt.Sprintf("contention_%d_%d", id, j)
					kv.Set(testKey, []byte("value"), 0)
				}
			}
		}(i)
	}

	wg.Wait()
}

// Test encodeWALEntry coverage
func TestEncodeWALEntry(t *testing.T) {
	kv, cleanup := createTestStore(t)
	defer cleanup()

	entry := WALEntry{
		Op:      opSet,
		Key:     "test",
		Value:   []byte("value"),
		Version: 1,
	}
	entry.CRC = kv.calculateCRC(entry)

	// This is tested implicitly through Set operations,
	// but let's ensure it works
	encoded, err := kv.encodeWALEntry(entry)
	if err != nil {
		t.Fatalf("encodeWALEntry failed: %v", err)
	}

	if len(encoded) == 0 {
		t.Error("Encoded entry should not be empty")
	}
}

// Test WAL entry validation
func TestValidateWALEntry(t *testing.T) {
	kv, cleanup := createTestStore(t)
	defer cleanup()

	// Valid entry
	validEntry := WALEntry{
		Op:      opSet,
		Key:     "test",
		Value:   []byte("value"),
		Version: 1,
	}
	validEntry.CRC = kv.calculateCRC(validEntry)

	if !kv.validateWALEntry(validEntry) {
		t.Error("Valid entry should pass validation")
	}

	// Invalid entry (wrong CRC)
	invalidEntry := validEntry
	invalidEntry.CRC = 12345

	if kv.validateWALEntry(invalidEntry) {
		t.Error("Invalid entry should fail validation")
	}
}

// Test Delete with WAL entry writing
func TestDeleteNonExistent(t *testing.T) {
	kv, cleanup := createTestStore(t)
	defer cleanup()

	// Delete should still write to WAL even if key doesn't exist locally
	// but will return error
	err := kv.Delete("nonexistent")
	if err != ErrKeyNotFound {
		t.Errorf("Expected ErrKeyNotFound, got %v", err)
	}
}

// Test memory-based eviction
func TestMemoryBasedEviction(t *testing.T) {
	dataDir := fmt.Sprintf("testdata_%d", time.Now().UnixNano())
	defer os.RemoveAll(dataDir)

	opts := Options{
		DataDir:     dataDir,
		MaxEntries:  1000,
		MaxMemoryMB: 1, // Very small memory limit
		ShardCount:  2,
	}

	kv, err := NewKVStore(opts)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer kv.Close()

	// Add large values to trigger memory-based eviction
	largeValue := make([]byte, 100*1024) // 100KB
	for i := 0; i < 20; i++ {
		key := fmt.Sprintf("large_%d", i)
		kv.Set(key, largeValue, 0)
	}

	// Should have evicted some entries
	stats := kv.GetStats()
	if stats.Evictions == 0 {
		t.Error("Expected some memory-based evictions")
	}
}

// Test CRC calculation
func TestCRCCalculation(t *testing.T) {
	kv, cleanup := createTestStore(t)
	defer cleanup()

	entry1 := WALEntry{
		Op:      opSet,
		Key:     "test",
		Value:   []byte("value"),
		Version: 1,
	}

	entry2 := WALEntry{
		Op:      opSet,
		Key:     "test",
		Value:   []byte("value"),
		Version: 1,
	}

	// Same entries should produce same CRC
	crc1 := kv.calculateCRC(entry1)
	crc2 := kv.calculateCRC(entry2)

	if crc1 != crc2 {
		t.Errorf("Same entries should have same CRC: %d vs %d", crc1, crc2)
	}

	// Different entries should produce different CRC
	entry3 := WALEntry{
		Op:      opSet,
		Key:     "different",
		Value:   []byte("value"),
		Version: 1,
	}

	crc3 := kv.calculateCRC(entry3)
	if crc1 == crc3 {
		t.Error("Different entries should have different CRC")
	}

	entry4 := entry1
	entry4.ExpireAt = time.Now().Add(time.Hour)
	crc4 := kv.calculateCRC(entry4)
	if crc1 == crc4 {
		t.Error("Different expiry metadata should produce different CRC")
	}
}

// Test replay WAL with Set operations
func TestReplayWALSetOperations(t *testing.T) {
	dataDir := fmt.Sprintf("testdata_%d", time.Now().UnixNano())
	defer os.RemoveAll(dataDir)

	opts := Options{
		DataDir:     dataDir,
		MaxEntries:  1000,
		MaxMemoryMB: 10,
		ShardCount:  4,
	}

	// Create store and perform set with update
	kv1, err := NewKVStore(opts)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	kv1.Set("key", []byte("value1"), 0)
	kv1.Set("key", []byte("value2"), 0) // Update same key

	time.Sleep(50 * time.Millisecond)
	kv1.Close()

	// Reload and verify latest value
	kv2, err := NewKVStore(opts)
	if err != nil {
		t.Fatalf("Failed to reload store: %v", err)
	}
	defer kv2.Close()

	value, err := kv2.Get("key")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if string(value) != "value2" {
		t.Errorf("Expected 'value2', got '%s'", string(value))
	}
}

// Test Get with expiration and retry edge case
func TestGetExpirationRetryEdgeCase(t *testing.T) {
	kv, cleanup := createTestStore(t)
	defer cleanup()

	key := "edge_case"

	// Create many goroutines competing on the same key
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			switch id % 3 {
			case 0:
				// Set with short TTL
				kv.Set(key, []byte(fmt.Sprintf("value_%d", id)), 10*time.Millisecond)
			case 1:
				// Try to get
				_, _ = kv.Get(key)
			default:
				// Wait a bit then get
				time.Sleep(15 * time.Millisecond)
				_, _ = kv.Get(key)
			}
		}(i)
	}

	wg.Wait()
}

// Test move to front LRU logic
func TestMoveToFrontLRU(t *testing.T) {
	kv, cleanup := createTestStore(t)
	defer cleanup()

	// Just test that Get moves entries to front (implicitly tested by LRU eviction tests)
	// Create some entries
	for i := 0; i < 10; i++ {
		key := fmt.Sprintf("lru_%d", i)
		kv.Set(key, []byte("value"), 0)
	}

	// Access some keys multiple times
	for i := 0; i < 5; i++ {
		_, _ = kv.Get("lru_0")
		_, _ = kv.Get("lru_5")
	}

	// Verify entries still exist
	if _, err := kv.Get("lru_0"); err != nil {
		t.Error("Frequently accessed entry should still exist")
	}
}

// Test stats with no hits/misses
func TestStatsNoActivity(t *testing.T) {
	kv, cleanup := createTestStore(t)
	defer cleanup()

	stats := kv.GetStats()

	if stats.HitRatio != 0 {
		t.Errorf("Expected 0 hit ratio with no activity, got %f", stats.HitRatio)
	}
}

// Test isClosed
func TestIsClosed(t *testing.T) {
	kv, cleanup := createTestStore(t)
	defer cleanup()

	if kv.isClosed() {
		t.Error("Store should not be closed initially")
	}

	kv.Close()

	if !kv.isClosed() {
		t.Error("Store should be closed after Close()")
	}
}

// Test Size method
func TestSizeMethod(t *testing.T) {
	kv, cleanup := createTestStore(t)
	defer cleanup()

	if kv.Size() != 0 {
		t.Errorf("Expected size 0, got %d", kv.Size())
	}

	kv.Set("key1", []byte("value1"), 0)
	kv.Set("key2", []byte("value2"), 0)

	if kv.Size() != 2 {
		t.Errorf("Expected size 2, got %d", kv.Size())
	}

	_ = kv.Delete("key1")

	if kv.Size() != 1 {
		t.Errorf("Expected size 1 after delete, got %d", kv.Size())
	}
}

// Test load snapshot and WAL together
func TestLoadSnapshotAndWALCombined(t *testing.T) {
	dataDir := fmt.Sprintf("testdata_%d", time.Now().UnixNano())
	defer os.RemoveAll(dataDir)

	opts := Options{
		DataDir:     dataDir,
		MaxEntries:  1000,
		MaxMemoryMB: 10,
		ShardCount:  4,
	}

	// Create store and add data
	kv1, err := NewKVStore(opts)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	kv1.Set("snap1", []byte("value1"), 0)
	kv1.Set("snap2", []byte("value2"), 0)

	// Create snapshot
	if err := kv1.Snapshot(); err != nil {
		t.Fatalf("Snapshot failed: %v", err)
	}

	// Add more data after snapshot (will be in WAL)
	kv1.Set("wal1", []byte("value3"), 0)
	kv1.Set("wal2", []byte("value4"), 0)

	time.Sleep(50 * time.Millisecond)
	kv1.Close()

	// Reload - should load both snapshot and WAL
	kv2, err := NewKVStore(opts)
	if err != nil {
		t.Fatalf("Failed to reload: %v", err)
	}
	defer kv2.Close()

	// Verify snapshot data
	if _, err := kv2.Get("snap1"); err != nil {
		t.Errorf("snap1 should exist: %v", err)
	}
	if _, err := kv2.Get("snap2"); err != nil {
		t.Errorf("snap2 should exist: %v", err)
	}

	// Verify WAL data
	if _, err := kv2.Get("wal1"); err != nil {
		t.Errorf("wal1 should exist: %v", err)
	}
	if _, err := kv2.Get("wal2"); err != nil {
		t.Errorf("wal2 should exist: %v", err)
	}
}

// Test Get with expired entry edge cases
func TestGetExpiredEntryDoubleCheck(t *testing.T) {
	kv, cleanup := createTestStore(t)
	defer cleanup()

	key := "double_check"
	kv.Set(key, []byte("value"), 30*time.Millisecond)

	// Wait until just expired
	time.Sleep(35 * time.Millisecond)

	// Concurrent gets on expired key
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = kv.Get(key)
		}()
	}

	wg.Wait()

	// Key should be gone
	if _, err := kv.Get(key); err == nil {
		t.Error("Expired key should not exist")
	}
}

// Test evictLRU edge cases
func TestEvictLRUEdgeCases(t *testing.T) {
	kv, cleanup := createTestStore(t)
	defer cleanup()

	// Add entries and trigger some evictions through limits
	for i := 0; i < 100; i++ {
		key := fmt.Sprintf("evict_%d", i)
		value := make([]byte, 1024) // 1KB each
		kv.Set(key, value, 0)
	}

	stats := kv.GetStats()
	// Should have some entries
	if stats.Entries == 0 {
		t.Error("Should have some entries")
	}
}

// Test getShard hashing
func TestGetShardHashing(t *testing.T) {
	kv, cleanup := createTestStore(t)
	defer cleanup()

	// Add many keys to ensure they're distributed across shards
	keyCount := 100
	shardMap := make(map[*Shard]int)

	for i := 0; i < keyCount; i++ {
		key := fmt.Sprintf("shard_test_%d", i)
		kv.Set(key, []byte("value"), 0)

		shard := kv.getShard(key)
		shardMap[shard]++
	}

	// Should use multiple shards
	if len(shardMap) < 2 {
		t.Error("Keys should be distributed across multiple shards")
	}
}

// Test WAL flush explicitly
func TestWALFlushExplicitly(t *testing.T) {
	kv, cleanup := createTestStore(t)
	defer cleanup()

	// Add some data
	for i := 0; i < 10; i++ {
		key := fmt.Sprintf("flush_%d", i)
		kv.Set(key, []byte("value"), 0)
	}

	// Call flushWAL directly (it's a method, but called by background worker)
	if err := kv.flushWAL(); err != nil {
		t.Fatalf("flushWAL() error = %v", err)
	}

	// Verify WAL has content
	stats := kv.GetStats()
	if stats.WALSize == 0 {
		t.Error("WAL should have content after flush")
	}
}

// Test write WAL entry with large data
func TestWriteWALEntryLargeData(t *testing.T) {
	kv, cleanup := createTestStore(t)
	defer cleanup()

	// Set large value
	largeValue := make([]byte, 10*1024) // 10KB
	for i := range largeValue {
		largeValue[i] = byte(i % 256)
	}

	if err := kv.Set("large", largeValue, 0); err != nil {
		t.Fatalf("Failed to set large value: %v", err)
	}

	// Force flush
	time.Sleep(50 * time.Millisecond)

	// Verify it's in WAL
	stats := kv.GetStats()
	if stats.WALSize == 0 {
		t.Error("WAL should contain large entry")
	}
}

// Test delete from shard with entry details
func TestDeleteFromShardDetails(t *testing.T) {
	kv, cleanup := createTestStore(t)
	defer cleanup()

	// Add entry
	key := "delete_detail"
	kv.Set(key, []byte("value"), 0)

	// Verify it exists
	if !kv.Exists(key) {
		t.Fatal("Key should exist")
	}

	// Delete it
	if err := kv.Delete(key); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Verify it's gone
	if kv.Exists(key) {
		t.Error("Key should not exist after delete")
	}
}

// Test Set with TTL update
func TestSetWithTTLUpdate(t *testing.T) {
	kv, cleanup := createTestStore(t)
	defer cleanup()

	key := "ttl_update"

	// Set with long TTL
	kv.Set(key, []byte("value1"), 1*time.Hour)

	// Update with short TTL
	kv.Set(key, []byte("value2"), 50*time.Millisecond)

	// Wait for short TTL to expire
	time.Sleep(100 * time.Millisecond)

	// Should be expired
	if kv.Exists(key) {
		t.Error("Key should have expired")
	}
}

// Test cleanExpired with low percentage
func TestCleanExpiredLowPercentage(t *testing.T) {
	dataDir := fmt.Sprintf("testdata_%d", time.Now().UnixNano())
	defer os.RemoveAll(dataDir)

	opts := Options{
		DataDir:       dataDir,
		MaxEntries:    1000,
		MaxMemoryMB:   10,
		CleanInterval: 20 * time.Millisecond,
		ShardCount:    4,
		WALSyncMode:   WALSyncInterval,
	}

	kv, err := NewKVStore(opts)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer kv.Close()

	// Add many permanent keys
	for i := 0; i < 100; i++ {
		key := fmt.Sprintf("perm_%d", i)
		kv.Set(key, []byte("value"), 0)
	}

	// Add few expiring keys (low percentage)
	for i := 0; i < 5; i++ {
		key := fmt.Sprintf("temp_%d", i)
		kv.Set(key, []byte("value"), 40*time.Millisecond)
	}

	initialSize := kv.Size()

	// Wait for cleanup
	time.Sleep(100 * time.Millisecond)

	// Should have cleaned up the few expired keys
	finalSize := kv.Size()
	if finalSize >= initialSize {
		t.Errorf("Expected some cleanup, initial=%d final=%d", initialSize, finalSize)
	}
}
