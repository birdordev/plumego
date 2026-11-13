package kvstore

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDirectoryLock(t *testing.T) {
	dir := t.TempDir()
	opts := Options{DataDir: dir}
	store, err := NewKVStore(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if other, err := NewKVStore(opts); err == nil {
		other.Close()
		t.Fatal("opened a directory already owned by a live store")
	} else if !strings.Contains(err.Error(), "directory already open") {
		t.Fatalf("second open = %v, want directory contention", err)
	}
	if err := store.Set("first", []byte("preserved"), 0); err != nil {
		t.Fatal(err)
	}
	runLockProcess(t, dir, "contended")
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	runLockProcess(t, dir, "owner-exits")
	reopened, err := NewKVStore(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	for key, want := range map[string]string{"first": "preserved", "second": "child"} {
		if got, err := reopened.Get(key); err != nil || string(got) != want {
			t.Fatalf("%s = %q, %v", key, got, err)
		}
	}
}

func TestDirectoryLockReleasedAfterLoadFailure(t *testing.T) {
	dir := t.TempDir()
	opts := Options{DataDir: dir}
	statePath := filepath.Join(dir, stateFileName)
	if err := os.WriteFile(statePath, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewKVStore(opts); !errors.Is(err, ErrCorruptState) {
		t.Fatalf("open with corrupt state = %v", err)
	}
	if err := os.Remove(statePath); err != nil {
		t.Fatal(err)
	}
	store, err := NewKVStore(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
}

func runLockProcess(t *testing.T, dir, mode string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), executable, "-test.run=^TestDirectoryLockChild$")
	cmd.Env = append(os.Environ(), "PLUMEGO_KV_TEST_DIR="+dir, "PLUMEGO_KV_TEST_MODE="+mode, "GORACE=atexit_sleep_ms=0")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child %s: %v\n%s", mode, err, output)
	}
}

func TestDirectoryLockChild(t *testing.T) {
	dir := os.Getenv("PLUMEGO_KV_TEST_DIR")
	if dir == "" {
		t.Skip("requires a parent lock test")
	}
	store, err := NewKVStore(Options{DataDir: dir})
	if os.Getenv("PLUMEGO_KV_TEST_MODE") == "contended" {
		if err == nil {
			store.Close()
			t.Fatal("child acquired a lock held by its parent")
		}
		if !strings.Contains(err.Error(), "directory already open") {
			t.Fatalf("child open = %v, want directory contention", err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set("second", []byte("child"), 0); err != nil {
		t.Fatal(err)
	}
	// The child process exits without Close, exercising OS handle cleanup.
}
