package kvstore

import (
	"bytes"
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func TestDirectorySyncFailurePreservesPublishedState(t *testing.T) {
	for _, operation := range []string{"set", "delete", "evict"} {
		t.Run(operation, func(t *testing.T) {
			dir := t.TempDir()
			store, err := NewKVStore(Options{DataDir: dir, MaxEntries: 1})
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if err := store.Set("original", []byte("old"), 0); err != nil {
				t.Fatal(err)
			}
			syncErr := errors.New("directory sync failed")
			store.syncStateDir = func(string) error { return syncErr }
			switch operation {
			case "set":
				err = store.Set("original", []byte("new"), 0)
			case "delete":
				err = store.Delete("original")
			case "evict":
				store.data["original"].UpdatedAt = time.Now().Add(-time.Hour)
				err = store.Set("replacement", []byte("new"), 0)
			}
			if !errors.Is(err, syncErr) {
				t.Fatalf("error = %v, want directory sync error", err)
			}
			var persisted diskState
			readState(t, dir, &persisted)
			for _, key := range []string{"original", "replacement"} {
				got, gotErr := store.Get(key)
				disk, exists := persisted.Entries[key]
				if exists && (gotErr != nil || !bytes.Equal(got, disk.Value)) ||
					!exists && !errors.Is(gotErr, ErrKeyNotFound) {
					t.Fatalf("%s: live value %q (%v), disk entry %+v (exists=%v)", key, got, gotErr, disk, exists)
				}
			}
			matches, err := filepath.Glob(filepath.Join(dir, stateFileName+".*.tmp"))
			if err != nil || len(matches) != 0 {
				t.Fatalf("temporary files = %v, error = %v", matches, err)
			}
			store.syncStateDir = syncDir
			if operation == "delete" {
				if err := store.Delete("original"); !errors.Is(err, ErrKeyNotFound) {
					t.Fatalf("delete retry = %v, want ErrKeyNotFound", err)
				}
			} else {
				key := "original"
				if operation == "evict" {
					key = "replacement"
				}
				if err := store.Set(key, []byte("retried"), 0); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.Set("subsequent", []byte("value"), 0); err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			final, err := NewKVStore(Options{DataDir: dir, MaxEntries: 1})
			if err != nil {
				t.Fatal(err)
			}
			defer final.Close()
			if got, err := final.Get("subsequent"); err != nil || string(got) != "value" {
				t.Fatalf("subsequent write = %q, %v", got, err)
			}
		})
	}
}

func TestSlowPersistenceKeepsOneExpiration(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		store, err := NewKVStore(Options{DataDir: dir})
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		store.syncStateDir = func(dir string) error {
			time.Sleep(2 * time.Minute)
			return syncDir(dir)
		}
		if err := store.Set("ttl", []byte("value"), time.Minute); err != nil {
			t.Fatal(err)
		}
		var disk diskState
		readState(t, dir, &disk)
		if !store.data["ttl"].ExpireAt.Equal(disk.Entries["ttl"].ExpireAt) {
			t.Fatal("persistence extended only the in-memory expiration")
		}
		if exists, err := store.ExistsContext(t.Context(), "ttl"); err != nil || exists {
			t.Fatalf("expired live entry exists=%v, error=%v", exists, err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		reopened, err := NewKVStore(Options{DataDir: dir})
		if err != nil {
			t.Fatal(err)
		}
		defer reopened.Close()
		if exists, err := reopened.ExistsContext(t.Context(), "ttl"); err != nil || exists {
			t.Fatalf("expired reopened entry exists=%v, error=%v", exists, err)
		}
	})
}

func TestWriteFailureRestoresEvictionAndAllowsRetry(t *testing.T) {
	dir := t.TempDir()
	store, err := NewKVStore(Options{DataDir: dir, MaxEntries: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Set("original", []byte("old"), 0); err != nil {
		t.Fatal(err)
	}
	store.data["original"].UpdatedAt = time.Now().Add(-time.Hour)
	original, err := os.ReadFile(filepath.Join(dir, stateFileName))
	if err != nil {
		t.Fatal(err)
	}
	blockStatePath(t, dir)
	if err := store.Set("replacement", []byte("new"), 0); err == nil {
		t.Fatal("expected rename failure")
	}
	if got, err := store.Get("original"); err != nil || string(got) != "old" {
		t.Fatalf("original = %q, %v", got, err)
	}
	if _, err := store.Get("replacement"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("replacement error = %v", err)
	}
	if err := os.Remove(filepath.Join(dir, stateFileName)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, stateFileName), original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := store.Set("replacement", []byte("new"), 0); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewKVStore(Options{DataDir: dir, MaxEntries: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err := reopened.Get("original"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("evicted original error = %v", err)
	}
	if got, err := reopened.Get("replacement"); err != nil || string(got) != "new" {
		t.Fatalf("replacement after retry = %q, %v", got, err)
	}
}

func TestEvictionPrefersWriteAtSameTimestamp(t *testing.T) {
	dir := t.TempDir()
	synctest.Test(t, func(t *testing.T) {
		opts := Options{DataDir: dir, MaxEntries: 2}
		store, err := NewKVStore(opts)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		now := time.Now()
		for _, key := range []string{"b", "c", "a"} {
			if err := store.Set(key, []byte(key), 0); err != nil {
				t.Fatal(err)
			}
		}
		if !time.Now().Equal(now) {
			t.Fatal("writes did not share one timestamp")
		}
		checkKeys := func(store *KVStore) {
			t.Helper()
			if got, err := store.KeysContext(t.Context()); err != nil || !slices.Equal(got, []string{"a", "c"}) {
				t.Fatalf("keys = %v, %v; want [a c]", got, err)
			}
		}
		checkKeys(store)
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		reopened, err := NewKVStore(opts)
		if err != nil {
			t.Fatal(err)
		}
		defer reopened.Close()
		checkKeys(reopened)
		if err := reopened.Set("d", []byte("d"), time.Second); err != nil {
			t.Fatal(err)
		}
		if got := mustKeys(t, reopened); !slices.Equal(got, []string{"c", "d"}) {
			t.Fatalf("keys after reopened eviction = %v; want [c d]", got)
		}
		time.Sleep(2 * time.Second)
		if got := mustKeys(t, reopened); !slices.Equal(got, []string{"c"}) {
			t.Fatalf("keys after expiry = %v; want [c]", got)
		}
	})
}

func TestConcurrentWritesPreserveAllKeysAfterReload(t *testing.T) {
	dir := t.TempDir()
	store, err := NewKVStore(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	const workers = 12
	start := make(chan struct{})
	failures := make(chan error, workers)
	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			key := string(rune('a' + i))
			if err := store.SetContext(context.Background(), key, []byte(key), 0); err != nil {
				failures <- err
				return
			}
			_, err := store.GetContext(context.Background(), key)
			if err != nil {
				failures <- err
			}
		}()
	}
	close(start)
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewKVStore(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	for i := range workers {
		key := string(rune('a' + i))
		if got, err := reopened.Get(key); err != nil || string(got) != key {
			t.Fatalf("reopened %s = %q, %v", key, got, err)
		}
	}
}

// cancelBeforePersistence cancels at the final context check after staging a
// mutation, allowing rollback to be exercised without scheduler timing.
type cancelBeforePersistence struct {
	context.Context
	cancel context.CancelFunc
	checks int
}

func (ctx *cancelBeforePersistence) Err() error {
	ctx.checks++
	if ctx.checks == 3 {
		ctx.cancel()
	}
	return ctx.Context.Err()
}

func TestCanceledStagedMutationRestoresMemoryAndDisk(t *testing.T) {
	for _, operation := range []string{"set", "delete"} {
		t.Run(operation, func(t *testing.T) {
			dir := t.TempDir()
			store, err := NewKVStore(Options{DataDir: dir, MaxEntries: 1})
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if err := store.Set("original", []byte("old"), 0); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(filepath.Join(dir, stateFileName))
			if err != nil {
				t.Fatal(err)
			}
			base, cancel := context.WithCancel(t.Context())
			defer cancel()
			ctx := &cancelBeforePersistence{Context: base, cancel: cancel}
			if operation == "set" {
				err = store.SetContext(ctx, "replacement", []byte("new"), 0)
			} else {
				err = store.DeleteContext(ctx, "original")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("error = %v, want context.Canceled", err)
			}
			if got, err := store.Get("original"); err != nil || string(got) != "old" {
				t.Fatalf("original = %q, %v", got, err)
			}
			if _, err := store.Get("replacement"); !errors.Is(err, ErrKeyNotFound) {
				t.Fatalf("replacement error = %v", err)
			}
			after, err := os.ReadFile(filepath.Join(dir, stateFileName))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("canceled operation changed persisted state")
			}
		})
	}
}

func TestCancellationAfterReplacementDoesNotUndoCommit(t *testing.T) {
	for _, operation := range []string{"set", "delete"} {
		t.Run(operation, func(t *testing.T) {
			dir := t.TempDir()
			store, err := NewKVStore(Options{DataDir: dir})
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if err := store.Set("original", []byte("old"), 0); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			store.syncStateDir = func(dir string) error { cancel(); return syncDir(dir) }
			if operation == "set" {
				err = store.SetContext(ctx, "original", []byte("new"), 0)
			} else {
				err = store.DeleteContext(ctx, "original")
			}
			if err != nil {
				t.Fatalf("committed mutation error = %v", err)
			}
			assertCommitted := func(current *KVStore) {
				t.Helper()
				got, err := current.Get("original")
				if operation == "set" {
					if err != nil || string(got) != "new" {
						t.Fatalf("committed value = %q, %v", got, err)
					}
				} else if !errors.Is(err, ErrKeyNotFound) {
					t.Fatalf("committed delete error = %v", err)
				}
			}
			assertCommitted(store)
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := NewKVStore(Options{DataDir: dir})
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			assertCommitted(reopened)
		})
	}
}

func TestPersistentKeyMustRoundTripThroughJSON(t *testing.T) {
	options := Options{DataDir: t.TempDir()}
	store, err := NewKVStore(options)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	const valid = "键-\uFFFD"
	const invalid = "键-\xff"
	if err := store.Set(valid, []byte("original"), 0); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(invalid, []byte("replacement"), 0); !errors.Is(err, ErrInvalidKey) {
		t.Errorf("invalid key: got %v, want ErrInvalidKey", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewKVStore(options)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.Get(valid)
	if err != nil || string(got) != "original" {
		t.Fatalf("reopened value = %q, %v; want original", got, err)
	}
}

func TestKVRejectsUnrepresentableCapacityBeforeCreatingDirectory(t *testing.T) {
	if strconv.IntSize < 64 {
		t.Skip("int cannot represent an overflowing MiB capacity")
	}
	maxMiB := int64(math.MaxInt64) / (1 << 20)
	path := filepath.Join(t.TempDir(), "state")
	if store, err := NewKVStore(Config{DataDir: path, MaxMemoryMB: int(maxMiB + 1)}); err == nil {
		_ = store.Close()
		t.Fatal("constructor accepted overflowing capacity")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("data directory after rejected construction: %v", err)
	}
	store, err := NewKVStore(Config{DataDir: path, MaxMemoryMB: int(maxMiB)})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Set("key", []byte("value"), 0); err != nil {
		t.Fatal(err)
	}
}

type failedSnapshotTemp struct {
	path               string
	writeErr, closeErr error
	closes             int
}

func (f *failedSnapshotTemp) Name() string              { return f.path }
func (f *failedSnapshotTemp) Write([]byte) (int, error) { return 0, f.writeErr }
func (f *failedSnapshotTemp) Sync() error               { return nil }
func (f *failedSnapshotTemp) Close() error {
	f.closes++
	return f.closeErr
}

func TestSnapshotWritePreservesCleanupErrors(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, stateFileName)
	if err := os.WriteFile(state, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	nonEmpty := filepath.Join(dir, "tmp")
	if err := os.Mkdir(nonEmpty, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nonEmpty, "child"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	writeErr := errors.New("test write failure")
	closeErr := errors.New("test close failure")
	tmp := &failedSnapshotTemp{path: nonEmpty, writeErr: writeErr, closeErr: closeErr}
	err := replaceSnapshot(tmp, state, []byte("replacement"))
	var removeErr *os.PathError
	if !errors.Is(err, writeErr) || !errors.Is(err, closeErr) || !errors.As(err, &removeErr) || removeErr.Op != "remove" || tmp.closes != 1 {
		t.Fatalf("replaceSnapshot = %v, Close calls = %d", err, tmp.closes)
	}
	raw, err := os.ReadFile(state)
	if err != nil || string(raw) != "original" {
		t.Fatalf("snapshot = %q, error = %v", raw, err)
	}
}

type testDirectoryHandle struct {
	syncErr, closeErr error
	calls             []string
}

func (f *testDirectoryHandle) Sync() error {
	f.calls = append(f.calls, "sync")
	return f.syncErr
}

func (f *testDirectoryHandle) Close() error {
	f.calls = append(f.calls, "close")
	return f.closeErr
}

func TestDirectorySyncRetainsCloseErrors(t *testing.T) {
	failedSync := errors.New("sync failure")
	failedClose := errors.New("close failure")
	for _, tc := range []struct{ raw, want error }{
		{failedSync, failedSync}, {os.ErrInvalid, nil}, {nil, nil},
	} {
		handle := &testDirectoryHandle{syncErr: tc.raw, closeErr: failedClose}
		err := syncAndCloseDirectory(handle)
		if !errors.Is(err, failedClose) || (tc.want != nil && !errors.Is(err, tc.want)) ||
			errors.Is(err, os.ErrInvalid) || !slices.Equal(handle.calls, []string{"sync", "close"}) {
			t.Fatalf("sync=%v error=%v calls=%v", tc.raw, err, handle.calls)
		}
	}
}

func TestDeleteCanRemoveExpiredStoredKey(t *testing.T) {
	opts := DefaultConfig(t.TempDir())
	kv, err := NewKVStore(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer kv.Close()
	if err := kv.Set("key", []byte("value"), 0); err != nil {
		t.Fatal(err)
	}
	kv.mu.Lock()
	kv.data["key"].ExpireAt = time.Now().Add(-time.Second)
	kv.mu.Unlock()
	if _, err := kv.Get("key"); !errors.Is(err, ErrKeyExpired) {
		t.Fatalf("Get expired: %v", err)
	}
	if exists, err := kv.ExistsContext(t.Context(), "key"); exists || err != nil {
		t.Fatalf("Exists=%t err=%v", exists, err)
	}
	if err := kv.Delete("key"); err != nil {
		t.Fatal(err)
	}
	if err := kv.Delete("key"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("Delete absent: %v", err)
	}
	if err := kv.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewKVStore(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err := reopened.Get("key"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("reopened: %v", err)
	}
}

func TestSnapshotBudgetErrorDistinguishesReadFailures(t *testing.T) {
	path := filepath.Join(t.TempDir(), stateFileName)
	if err := os.WriteFile(path, []byte("oversized"), 0600); err != nil {
		t.Fatal(err)
	}
	_, byStat := readSnapshot(path, 2)
	_, byRead := readSnapshotBytes(strings.NewReader("oversized"), 2)
	for _, err := range []error{byStat, byRead} {
		var budget snapshotReadBudgetError
		if !errors.As(err, &budget) || budget.limit != 2 || errors.Is(err, ErrCorruptState) {
			t.Fatalf("budget failure = %v", err)
		}
	}
	_, err := readSnapshot(path+".missing", 2)
	var budget snapshotReadBudgetError
	if !errors.Is(err, os.ErrNotExist) || errors.As(err, &budget) {
		t.Fatalf("missing file error = %v", err)
	}
}

func TestExpiredEntryReadsPreserveStoredSnapshot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		kv, err := NewKVStore(DefaultConfig(dir))
		if err != nil {
			t.Fatal(err)
		}
		defer kv.Close()
		const key = "expiry"
		if err := kv.Set(key, []byte("original"), time.Second); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, stateFileName)
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Second)
		if _, err := kv.Get(key); !errors.Is(err, ErrKeyExpired) || mustExists(t, kv, key) ||
			slices.Contains(mustKeys(t, kv), key) {
			t.Fatalf("expired read error = %v", err)
		}
		after, err := os.ReadFile(path)
		if kv.data[key] == nil || err != nil || !bytes.Equal(before, after) {
			t.Fatalf("pure reads changed stored state: exists=%t err=%v", kv.data[key] != nil, err)
		}
	})
}

func TestOversizedSnapshotStartupReleasesDirectoryLock(t *testing.T) {
	dir := t.TempDir()
	options := DefaultConfig(dir)
	path := filepath.Join(dir, stateFileName)
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	maximum := (&KVStore{opts: options}).snapshotReadLimit()
	if err := file.Truncate(maximum + 1); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := NewKVStore(options)
	var budget snapshotReadBudgetError
	if store != nil || !errors.As(err, &budget) || budget.limit != maximum {
		if store != nil {
			_ = store.Close()
		}
		t.Fatalf("oversized open = %v", err)
	}
	if info, err := os.Stat(path); err != nil || info.Size() != maximum+1 {
		t.Fatalf("rejected open changed snapshot: %v, %v", info, err)
	}
	if err := os.WriteFile(path, []byte(`{"entries":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewKVStore(options)
	if err != nil {
		t.Fatalf("retry after correcting snapshot: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestEmptySnapshotShapeAndCorruptEntriesRecovery(t *testing.T) {
	for _, tc := range []struct {
		state string
		valid bool
	}{
		{`{"entries":{}}`, true},
		{`{}`, false},
		{`{"entries":null}`, false},
	} {
		dir := t.TempDir()
		path := filepath.Join(dir, stateFileName)
		if err := os.WriteFile(path, []byte(tc.state), 0600); err != nil {
			t.Fatal(err)
		}
		options := DefaultConfig(dir)
		store, err := NewKVStore(options)
		if tc.valid {
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if !errors.Is(err, ErrCorruptState) {
			if store != nil {
				_ = store.Close()
			}
			t.Fatalf("corrupt fixture returned %v", err)
		}
		if err := os.WriteFile(path, []byte(`{"entries":{}}`), 0600); err != nil {
			t.Fatal(err)
		}
		reopened, err := NewKVStore(options)
		if err != nil {
			t.Fatalf("retry failed: %v", err)
		}
		if err := reopened.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSnapshotBudgetWideningAllowsReadWithoutWriting(t *testing.T) {
	// Verify the historical floor and saturation without opening a large file.
	maxMemoryMB := int64(math.MaxInt64 / bytesPerMiB)
	for _, tc := range []struct {
		mem, want int64
	}{
		{1, defaultMaxMemoryMB * bytesPerMiB * 6},
		{defaultMaxMemoryMB + 1, (defaultMaxMemoryMB + 1) * bytesPerMiB * 6},
		{maxMemoryMB, math.MaxInt64 - 1},
	} {
		if int64(int(tc.mem)) != tc.mem {
			continue
		}
		kv := &KVStore{opts: Options{MaxMemoryMB: int(tc.mem)}}
		if got := kv.snapshotReadLimit(); got != tc.want {
			t.Fatalf("budget(%d)=%d want=%d", tc.mem, got, tc.want)
		}
	}
	path := filepath.Join(t.TempDir(), stateFileName)
	original := []byte(`{"entries":{}}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if data, err := readSnapshot(path, int64(len(original)-1)); data != nil || err == nil {
		t.Fatalf("undersized read=%s err=%v", data, err)
	}
	data, err := readSnapshot(path, int64(len(original)))
	if err != nil || !bytes.Equal(data, original) {
		t.Fatalf("retry=%s err=%v", data, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, original) {
		t.Fatalf("snapshot changed: %s err=%v", after, err)
	}
}

func TestClosedKVReleasesPayloadAndReopensIndependentStats(t *testing.T) {
	config := DefaultConfig(t.TempDir())
	kv, err := NewKVStore(config)
	if err != nil {
		t.Fatal(err)
	}
	defer kv.Close()
	if err := kv.Set("fixture", []byte("value"), 0); err != nil {
		t.Fatal(err)
	}
	retained, err := kv.Get("fixture")
	if err != nil {
		t.Fatal(err)
	}
	if err := kv.Close(); err != nil || kv.data != nil {
		t.Fatalf("Close err=%v data cleared=%t", err, kv.data == nil)
	}
	if _, err := kv.GetStatsContext(t.Context()); !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("closed stats err=%v", err)
	}
	retained[0] = 'X'
	next, err := NewKVStore(config)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	stats, err := next.GetStatsContext(t.Context())
	if err != nil || stats.Entries != 1 || stats.Hits != 0 || stats.Misses != 0 {
		t.Fatalf("reopened stats=%+v err=%v", stats, err)
	}
	if value, err := next.Get("fixture"); string(value) != "value" || err != nil {
		t.Fatalf("reopened value=%q err=%v", value, err)
	}
}

func TestKVSnapshotAcceptsSingleUnknownFieldAndRejectsDuplicates(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	snapshot := filepath.Join(cfg.DataDir, stateFileName)
	for _, tc := range []struct {
		payload string
		invalid bool
	}{
		{`{"entries":{"fixture":{"value":"YQ==","future":{}}},"future":0}`, false},
		{`{"entries":{"fixture":{"value":"YQ==","future":{},"future":{}}}}`, true},
		{`{"entries":{"fixture":{"value":"YQ==","VALUE":"Yg=="}}}`, true},
		{`{"entries":{"fixture":{"value":"YQ=="}}}`, false},
	} {
		if err := os.WriteFile(snapshot, []byte(tc.payload), 0600); err != nil {
			t.Fatal(err)
		}
		kv, err := NewKVStore(cfg)
		if tc.invalid {
			if kv != nil || !errors.Is(err, ErrCorruptState) {
				t.Fatalf("duplicate snapshot=%v err=%v", kv, err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		value, readErr := kv.Get("fixture")
		closeErr := kv.Close()
		if readErr != nil || closeErr != nil || string(value) != "a" {
			t.Fatalf("value=%q read=%v close=%v", value, readErr, closeErr)
		}
	}
}

func TestKVCapacityEvictionAtOpenPersistsOnlyWithMutation(t *testing.T) {
	dir := t.TempDir()
	wide := Options{DataDir: dir, MaxEntries: 3}
	write := func(kv *KVStore, key string) {
		t.Helper()
		if err := kv.Set(key, []byte(key), 0); err != nil {
			t.Fatal(err)
		}
	}
	open := func(cfg Options) *KVStore {
		t.Helper()
		kv, err := NewKVStore(cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = kv.Close() })
		return kv
	}
	stop := func(kv *KVStore) {
		t.Helper()
		if err := kv.Close(); err != nil {
			t.Fatal(err)
		}
	}
	initial := open(wide)
	write(initial, "a")
	write(initial, "b")
	stop(initial)
	narrow := wide
	narrow.MaxEntries = 1
	path := filepath.Join(dir, stateFileName)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	readOnly := open(narrow)
	if keys := mustKeys(t, readOnly); !slices.Equal(keys, []string{"b"}) {
		t.Fatalf("narrow keys=%v", keys)
	}
	stop(readOnly)
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("read-only open changed file: %v", err)
	}
	mutation := open(narrow)
	write(mutation, "c")
	stop(mutation)
	if keys := mustKeys(t, open(wide)); !slices.Equal(keys, []string{"c"}) {
		t.Fatalf("reopened after commit=%v", keys)
	}
}

func TestKVDirectoryAliasSharesSingleLockAndReopens(t *testing.T) {
	dir := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(dir, alias); err != nil {
		if runtime.GOOS == "windows" || runtime.GOOS == "plan9" {
			t.Skipf("directory symlink unavailable: %v", err)
		}
		t.Fatal(err)
	}
	first, err := NewKVStore(DefaultConfig(dir))
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if err := first.Set("fixture", []byte("value"), 0); err != nil {
		t.Fatal(err)
	}
	other, err := NewKVStore(DefaultConfig(alias))
	if other != nil {
		other.Close()
		t.Fatal("alias acquired a second lock")
	}
	if err == nil || !strings.Contains(err.Error(), "lock data dir") {
		t.Fatalf("second open=%v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	last, err := NewKVStore(DefaultConfig(alias))
	if err != nil {
		t.Fatal(err)
	}
	defer last.Close()
	if got, err := last.Get("fixture"); string(got) != "value" || err != nil {
		t.Fatalf("reopen=%q err=%v", got, err)
	}
}

func TestKVFailedSnapshotReplacementDoesNotExtendPreviousTTL(t *testing.T) {
	dir := t.TempDir()
	synctest.Test(t, func(t *testing.T) {
		cfg := DefaultConfig(dir)
		kv, err := NewKVStore(cfg)
		if err != nil {
			t.Fatal(err)
		}
		defer kv.Close()
		if err := kv.Set("fixture", []byte("first"), 3*time.Second); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, stateFileName)
		snapshot, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Second)
		blockStatePath(t, dir)
		if err := kv.Set("fixture", []byte("failed"), time.Hour); err == nil {
			t.Fatal("replacement succeeded")
		}
		if got, err := kv.Get("fixture"); string(got) != "first" || err != nil {
			t.Fatalf("restored=%q err=%v", got, err)
		}
		time.Sleep(2 * time.Second)
		if _, err := kv.Get("fixture"); !errors.Is(err, ErrKeyExpired) {
			t.Fatalf("expiry=%v", err)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, snapshot, 0600); err != nil {
			t.Fatal(err)
		}
		if err := kv.Set("fixture", []byte("retried"), 4*time.Second); err != nil {
			t.Fatal(err)
		}
		if err := kv.Close(); err != nil {
			t.Fatal(err)
		}
		reopened, err := NewKVStore(cfg)
		if err != nil {
			t.Fatal(err)
		}
		defer reopened.Close()
		if got, err := reopened.Get("fixture"); string(got) != "retried" || err != nil {
			t.Fatalf("reopen=%q err=%v", got, err)
		}
		time.Sleep(4 * time.Second)
		if _, err := reopened.Get("fixture"); !errors.Is(err, ErrKeyExpired) {
			t.Fatalf("retried expiry=%v", err)
		}
	})
}

type snapshotBoundaryReader struct {
	data    string
	readErr error
}

func (r snapshotBoundaryReader) Read(p []byte) (int, error) { return copy(p, r.data), r.readErr }

func TestReadSnapshotAtLimitPreservesReaderError(t *testing.T) {
	const limit = 4
	readErr := errors.New("fixture read failure")
	for _, length := range []int{limit - 1, limit, limit + 1} {
		input := strings.Repeat("x", length)
		data, err := readSnapshotBytes(strings.NewReader(input), limit)
		var budget snapshotReadBudgetError
		if length <= limit && (err != nil || string(data) != input) || length > limit && !errors.As(err, &budget) {
			t.Fatalf("length=%d bytes=%q error=%v", length, data, err)
		}
		if _, err := readSnapshotBytes(snapshotBoundaryReader{data: input, readErr: readErr}, limit); !errors.Is(err, readErr) {
			t.Fatalf("length=%d read error=%v", length, err)
		}
	}
}

func TestMissingDeletePreservesSnapshotFileAndReopen(t *testing.T) {
	dir := t.TempDir()
	options := Options{DataDir: dir}
	store, err := NewKVStore(options)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Set("saved", []byte("fixture"), 0); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, stateFileName)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := store.DeleteContext(ctx, "missing"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled delete=%v", err)
	}
	if err := store.DeleteContext(t.Context(), "missing"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("missing delete=%v", err)
	}
	after, err := os.Stat(path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("snapshot changed, error=%v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewKVStore(options)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got, err := reopened.Get("saved"); err != nil || string(got) != "fixture" {
		t.Fatalf("value=%q error=%v", got, err)
	}
}

func TestPreCanceledReplacementLeavesSameRecordAfterReopen(t *testing.T) {
	opts := Options{DataDir: t.TempDir()}
	kv, err := NewKVStore(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer kv.Close()
	if err := kv.Set("fixture", []byte("before"), 0); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := kv.SetContext(ctx, "fixture", []byte("after"), 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled set=%v", err)
	}
	check := func(kv *KVStore) {
		t.Helper()
		got, err := kv.Get("fixture")
		size, sizeErr := kv.SizeContext(t.Context())
		if err != nil || string(got) != "before" || sizeErr != nil || size != 1 {
			t.Fatalf("value=%q size=%d errors=%v %v", got, size, err, sizeErr)
		}
	}
	check(kv)
	if err := kv.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewKVStore(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	check(reopened)
}

func TestKeyListSnapshotSurvivesInsertDeleteAndCallerMutation(t *testing.T) {
	kv, err := NewKVStore(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer kv.Close()
	for _, key := range []string{"zeta", "alpha"} {
		if err := kv.Set(key, []byte(key), 0); err != nil {
			t.Fatal(err)
		}
	}
	first, err := kv.KeysContext(t.Context())
	if err != nil || !slices.Equal(first, []string{"alpha", "zeta"}) {
		t.Fatalf("first=%v err=%v", first, err)
	}
	done := make(chan error, 1)
	go func() {
		done <- errors.Join(kv.Delete("zeta"), kv.Set("beta", []byte("beta"), 0))
	}()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(first, []string{"alpha", "zeta"}) {
		t.Fatalf("old snapshot=%v", first)
	}
	first[0] = "changed"
	next, err := kv.KeysContext(t.Context())
	if err != nil || !slices.Equal(next, []string{"alpha", "beta"}) {
		t.Fatalf("next=%v err=%v", next, err)
	}
}
