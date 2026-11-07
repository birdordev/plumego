package mq

import (
	"os"
	"testing"
	"time"

	kvstore "github.com/spcent/plumego/store/kv"
)

func TestKVDeduperLifecycle(t *testing.T) {
	dir, err := os.MkdirTemp("", "plumego-mq-dedupe")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	kv, err := kvstore.NewKVStore(kvstore.Config{DataDir: dir})
	if err != nil {
		t.Fatalf("kv store: %v", err)
	}
	defer func() { _ = kv.Close() }()

	deduper := NewKVDeduper(kv, KVDeduperConfig{
		Prefix:     "mq",
		DefaultTTL: time.Second,
	})

	key := "tenant-1:task-1"
	completed, err := deduper.IsCompleted(t.Context(), key)
	if err != nil {
		t.Fatalf("IsCompleted err: %v", err)
	}
	if completed {
		t.Fatalf("expected not completed")
	}

	if err := deduper.MarkCompleted(t.Context(), key, 0); err != nil {
		t.Fatalf("MarkCompleted: %v", err)
	}

	completed, err = deduper.IsCompleted(t.Context(), key)
	if err != nil {
		t.Fatalf("IsCompleted after mark: %v", err)
	}
	if !completed {
		t.Fatalf("expected completed")
	}

	time.Sleep(1200 * time.Millisecond)

	completed, err = deduper.IsCompleted(t.Context(), key)
	if err != nil {
		t.Fatalf("IsCompleted after ttl: %v", err)
	}
	if completed {
		t.Fatalf("expected expired entry")
	}
}
