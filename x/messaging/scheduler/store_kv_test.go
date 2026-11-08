package scheduler

import (
	"context"
	"testing"
	"time"

	kvstore "github.com/birdordev/plumego/store/kv"
)

func TestKVStorePersistence(t *testing.T) {
	kv, err := kvstore.NewKVStore(kvstore.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("kv store: %v", err)
	}
	defer func() { _ = kv.Close() }()

	store := NewKVStore(kv, "")
	job := StoredJob{ID: "kv-1", Kind: "delay", RunAt: time.Now(), TaskName: "task"}
	if err := store.Save(job); err != nil {
		t.Fatalf("save: %v", err)
	}

	items, err := store.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	if err := store.Delete("kv-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// Ensure scheduler can load from KV store with registry.
	s := New(WithStore(store))
	if err := s.RegisterTask("task", func(ctx context.Context) error { return nil }); err != nil {
		t.Fatalf("register task: %v", err)
	}
	s.Start()
	_ = s.Stop(t.Context())
}
