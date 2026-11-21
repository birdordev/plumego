package kvstore

import (
	"bytes"
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
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
