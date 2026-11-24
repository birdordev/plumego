package scheduler

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/birdordev/plumego/log"
	kvstore "github.com/birdordev/plumego/store/kv"
)

func TestKVStoreReportsUnreadableJobsAndRestoresReadableJobs(t *testing.T) {
	base, err := kvstore.NewKVStore(kvstore.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = base.Close() })
	store := NewKVStore(base, "jobs")
	const badPayload = "{private test fixture"
	if err := base.Set("jobs:broken", []byte(badPayload), 0); err != nil {
		t.Fatal(err)
	}
	if got, err := store.List(); len(got) != 0 || !errors.Is(err, errInvalidStoredJob) {
		t.Fatalf("malformed record only: jobs = %+v, err = %v", got, err)
	}

	job := StoredJob{ID: "valid", Kind: "delay", RunAt: time.Now().Add(time.Hour), TaskName: "example"}
	if err := store.Save(job); err != nil {
		t.Fatal(err)
	}
	if got, err := store.List(); len(got) != 1 || got[0].ID != job.ID || !errors.Is(err, errInvalidStoredJob) {
		t.Fatalf("mixed records: jobs = %+v, err = %v", got, err)
	}

	var output bytes.Buffer
	logger := log.NewLogger(log.LoggerConfig{Format: log.LoggerFormatJSON, Output: &output, ErrorOutput: &output})
	scheduler := New(WithStore(store), WithLogger(logger))
	if err := scheduler.RegisterTask(job.TaskName, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	scheduler.Start()
	_, restored := scheduler.Status(job.ID)
	if err := scheduler.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !restored || !strings.Contains(output.String(), "scheduler load persisted jobs failed") ||
		!strings.Contains(output.String(), "invalid_stored_job") || strings.Contains(output.String(), badPayload) {
		t.Errorf("valid restored = %v, recovery logs = %s", restored, output.String())
	}

	if err := base.Close(); err != nil {
		t.Fatal(err)
	}
	if got, err := store.List(); len(got) != 0 || !errors.Is(err, kvstore.ErrStoreClosed) {
		t.Errorf("closed store: jobs = %+v, err = %v", got, err)
	}
}

func TestKVStoreListDuringDelete(t *testing.T) {
	base, err := kvstore.NewKVStore(kvstore.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	store := NewKVStore(base, "jobs")
	ids := []JobID{"a", "b", "c"}
	for _, id := range ids {
		if err := store.Save(StoredJob{ID: id}); err != nil {
			t.Fatal(err)
		}
	}
	deleted := make(chan error, 1)
	go func() {
		var errs []error
		for _, id := range ids {
			errs = append(errs, store.Delete(id))
		}
		deleted <- errors.Join(errs...)
	}()
	_, listErr := store.List()
	if err := <-deleted; err != nil || listErr != nil {
		t.Fatalf("list error = %v, delete error = %v", listErr, err)
	}
}

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

func TestKVStoreRejectsMismatchedStoredJobIDs(t *testing.T) {
	base, err := kvstore.NewKVStore(kvstore.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	store := NewKVStore(base, "jobs")
	if err := store.Save(StoredJob{}); !errors.Is(err, errInvalidStoredJob) {
		t.Errorf("empty job save=%v", err)
	}
	if err := store.Save(StoredJob{ID: "valid"}); err != nil {
		t.Fatal(err)
	}
	if err := base.Set("jobs:wrong", []byte(`{"id":"different","task_name":"work"}`), 0); err != nil {
		t.Fatal(err)
	}
	if err := base.Set("jobs:empty", []byte("null"), 0); err != nil {
		t.Fatal(err)
	}
	jobs, err := store.List()
	if len(jobs) != 1 || jobs[0].ID != "valid" || !errors.Is(err, errInvalidStoredJob) {
		t.Errorf("jobs=%d err=%v", len(jobs), err)
	}
}
