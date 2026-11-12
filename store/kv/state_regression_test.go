package kvstore

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
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
			reopened, err := NewKVStore(Options{DataDir: dir, MaxEntries: 1})
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			for _, key := range []string{"original", "replacement"} {
				got, gotErr := store.Get(key)
				disk, diskErr := reopened.Get(key)
				if !bytes.Equal(got, disk) || !errors.Is(gotErr, diskErr) {
					t.Fatalf("%s: live value %q (%v), reopened value %q (%v)", key, got, gotErr, disk, diskErr)
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
			reopened, err := NewKVStore(Options{DataDir: dir})
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			for _, current := range []*KVStore{store, reopened} {
				got, err := current.Get("original")
				if operation == "set" {
					if err != nil || string(got) != "new" {
						t.Fatalf("committed value = %q, %v", got, err)
					}
				} else if !errors.Is(err, ErrKeyNotFound) {
					t.Fatalf("committed delete error = %v", err)
				}
			}
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
