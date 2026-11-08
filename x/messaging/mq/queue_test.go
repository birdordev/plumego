package mq_test

import (
	"testing"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"
	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

func TestTaskQueueEnqueueReserveAck(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store := mqstore.NewMemory(mqstore.MemConfig{Now: func() time.Time { return now }})
	queue := mq.NewTaskQueue(store, mq.WithQueueNowFunc(func() time.Time { return now }))

	task := mq.Task{
		ID:       "t-1",
		Topic:    "sms.send",
		TenantID: "tenant-a",
		Payload:  []byte("hello"),
	}
	if err := queue.Enqueue(t.Context(), task, mq.EnqueueOptions{}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	reserved, err := queue.Reserve(t.Context(), mq.ReserveOptions{
		Topics:     []string{"sms.send"},
		Limit:      1,
		Lease:      10 * time.Second,
		ConsumerID: "c-1",
		Now:        now,
	})
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if len(reserved) != 1 {
		t.Fatalf("expected 1 task, got %d", len(reserved))
	}
	if reserved[0].Attempts != 1 {
		t.Fatalf("expected attempts=1, got %d", reserved[0].Attempts)
	}

	if err := queue.Ack(t.Context(), reserved[0].ID, "c-1", now); err != nil {
		t.Fatalf("ack: %v", err)
	}

	stats, err := queue.Stats(t.Context())
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.Leased != 0 || stats.Queued != 0 {
		t.Fatalf("expected empty queue stats, got %+v", stats)
	}
}

func TestTaskQueueReleaseAndRetry(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store := mqstore.NewMemory(mqstore.MemConfig{Now: func() time.Time { return now }})
	queue := mq.NewTaskQueue(store, mq.WithQueueNowFunc(func() time.Time { return now }))

	task := mq.Task{ID: "t-2", Topic: "sms.send", TenantID: "tenant-a", Payload: []byte("hello")}
	if err := queue.Enqueue(t.Context(), task, mq.EnqueueOptions{}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	reserved, err := queue.Reserve(t.Context(), mq.ReserveOptions{
		Topics:     []string{"sms.send"},
		Limit:      1,
		Lease:      5 * time.Second,
		ConsumerID: "c-1",
		Now:        now,
	})
	if err != nil || len(reserved) != 1 {
		t.Fatalf("reserve: %v len=%d", err, len(reserved))
	}

	retryAt := now.Add(30 * time.Second)
	if err := queue.Release(t.Context(), reserved[0].ID, "c-1", mq.ReleaseOptions{
		RetryAt: retryAt,
		Reason:  "fail",
		Now:     now,
	}); err != nil {
		t.Fatalf("release: %v", err)
	}

	reserved, err = queue.Reserve(t.Context(), mq.ReserveOptions{
		Topics:     []string{"sms.send"},
		Limit:      1,
		Lease:      5 * time.Second,
		ConsumerID: "c-1",
		Now:        now.Add(10 * time.Second),
	})
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if len(reserved) != 0 {
		t.Fatalf("expected 0 tasks before retry, got %d", len(reserved))
	}

	reserved, err = queue.Reserve(t.Context(), mq.ReserveOptions{
		Topics:     []string{"sms.send"},
		Limit:      1,
		Lease:      5 * time.Second,
		ConsumerID: "c-1",
		Now:        retryAt,
	})
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if len(reserved) != 1 {
		t.Fatalf("expected 1 task after retry, got %d", len(reserved))
	}
}

func TestTaskQueueDedupe(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store := mqstore.NewMemory(mqstore.MemConfig{Now: func() time.Time { return now }})
	queue := mq.NewTaskQueue(store, mq.WithQueueNowFunc(func() time.Time { return now }))

	opts := mq.EnqueueOptions{DedupeKey: "dup-1"}
	if err := queue.Enqueue(t.Context(), mq.Task{ID: "t-3", Topic: "sms.send"}, opts); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if err := queue.Enqueue(t.Context(), mq.Task{ID: "t-4", Topic: "sms.send"}, opts); err != mq.ErrDuplicateTask {
		t.Fatalf("expected duplicate error, got %v", err)
	}
}

func TestTaskQueueLeaseLost(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store := mqstore.NewMemory(mqstore.MemConfig{Now: func() time.Time { return now }})
	queue := mq.NewTaskQueue(store, mq.WithQueueNowFunc(func() time.Time { return now }))

	if err := queue.Enqueue(t.Context(), mq.Task{ID: "t-5", Topic: "sms.send"}, mq.EnqueueOptions{}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	reserved, err := queue.Reserve(t.Context(), mq.ReserveOptions{
		Topics:     []string{"sms.send"},
		Limit:      1,
		Lease:      1 * time.Second,
		ConsumerID: "c-1",
		Now:        now,
	})
	if err != nil || len(reserved) != 1 {
		t.Fatalf("reserve: %v len=%d", err, len(reserved))
	}

	if err := queue.ExtendLease(t.Context(), reserved[0].ID, "c-1", 1*time.Second, now.Add(2*time.Second)); err != mq.ErrLeaseLost {
		t.Fatalf("expected lease lost, got %v", err)
	}
}
