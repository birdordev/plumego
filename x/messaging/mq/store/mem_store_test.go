package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/spcent/plumego/x/messaging/mq"
)

func newTask(id, topic string) mq.Task {
	now := time.Now()
	return mq.Task{
		ID:        id,
		Topic:     topic,
		Payload:   []byte("payload"),
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// --- Insert ---

func TestMemStore_Insert_OK(t *testing.T) {
	s := NewMemory(DefaultMemConfig())
	task := newTask("t1", "orders")
	if err := s.Insert(t.Context(), task); err != nil {
		t.Fatalf("Insert: %v", err)
	}
}

func TestMemStore_Insert_DuplicateID(t *testing.T) {
	s := NewMemory(DefaultMemConfig())
	task := newTask("t1", "orders")
	_ = s.Insert(t.Context(), task)
	err := s.Insert(t.Context(), task)
	if !errors.Is(err, mq.ErrDuplicateTask) {
		t.Errorf("expected ErrDuplicateTask, got %v", err)
	}
}

func TestMemStore_Insert_DedupeKey(t *testing.T) {
	s := NewMemory(DefaultMemConfig())

	t1 := newTask("t1", "orders")
	t1.DedupeKey = "order-abc"
	_ = s.Insert(t.Context(), t1)

	t2 := newTask("t2", "orders")
	t2.DedupeKey = "order-abc"
	err := s.Insert(t.Context(), t2)
	if !errors.Is(err, mq.ErrDuplicateTask) {
		t.Errorf("expected ErrDuplicateTask for duplicate dedupe key, got %v", err)
	}
}

// --- Reserve ---

func TestMemStore_Reserve_ReturnsQueued(t *testing.T) {
	s := NewMemory(DefaultMemConfig())
	_ = s.Insert(t.Context(), newTask("t1", "orders"))
	_ = s.Insert(t.Context(), newTask("t2", "orders"))

	tasks, err := s.Reserve(t.Context(), mq.ReserveOptions{
		Topics:     []string{"orders"},
		Limit:      10,
		Lease:      30 * time.Second,
		ConsumerID: "worker-1",
		Now:        time.Now(),
	})
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if len(tasks) != 2 {
		t.Errorf("expected 2 tasks, got %d", len(tasks))
	}
}

func TestMemStore_Reserve_ZeroLimit_ReturnsNil(t *testing.T) {
	s := NewMemory(DefaultMemConfig())
	_ = s.Insert(t.Context(), newTask("t1", "orders"))

	tasks, err := s.Reserve(t.Context(), mq.ReserveOptions{Limit: 0})
	if err != nil {
		t.Fatalf("Reserve with limit=0: %v", err)
	}
	if tasks != nil {
		t.Errorf("expected nil, got %v", tasks)
	}
}

func TestMemStore_Reserve_TopicFilter(t *testing.T) {
	s := NewMemory(DefaultMemConfig())
	_ = s.Insert(t.Context(), newTask("t1", "orders"))
	_ = s.Insert(t.Context(), newTask("t2", "payments"))

	tasks, err := s.Reserve(t.Context(), mq.ReserveOptions{
		Topics:     []string{"payments"},
		Limit:      10,
		Lease:      10 * time.Second,
		ConsumerID: "w1",
		Now:        time.Now(),
	})
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Topic != "payments" {
		t.Errorf("expected 1 payments task, got %v", tasks)
	}
}

func TestMemStore_Reserve_ExpiresTask(t *testing.T) {
	s := NewMemory(DefaultMemConfig())
	task := newTask("t1", "orders")
	task.ExpiresAt = time.Now().Add(-time.Hour) // already expired
	_ = s.Insert(t.Context(), task)

	tasks, err := s.Reserve(t.Context(), mq.ReserveOptions{
		Topics:     []string{"orders"},
		Limit:      10,
		ConsumerID: "w1",
		Now:        time.Now(),
	})
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("expected 0 tasks (expired), got %d", len(tasks))
	}
}

// --- Ack ---

func TestMemStore_Ack_OK(t *testing.T) {
	s := NewMemory(DefaultMemConfig())
	_ = s.Insert(t.Context(), newTask("t1", "orders"))
	now := time.Now()

	tasks, _ := s.Reserve(t.Context(), mq.ReserveOptions{
		Topics:     []string{"orders"},
		Limit:      1,
		Lease:      time.Minute,
		ConsumerID: "w1",
		Now:        now,
	})

	if err := s.Ack(t.Context(), tasks[0].ID, "w1", now); err != nil {
		t.Fatalf("Ack: %v", err)
	}

	stats, _ := s.Stats(t.Context())
	if stats.Queued != 0 {
		t.Errorf("expected 0 queued after ack, got %d", stats.Queued)
	}
}

func TestMemStore_Ack_NotFound(t *testing.T) {
	s := NewMemory(DefaultMemConfig())
	err := s.Ack(t.Context(), "nonexistent", "w1", time.Now())
	if !errors.Is(err, mq.ErrTaskNotFound) {
		t.Errorf("expected ErrTaskNotFound, got %v", err)
	}
}

func TestMemStore_Ack_WrongConsumer(t *testing.T) {
	s := NewMemory(DefaultMemConfig())
	_ = s.Insert(t.Context(), newTask("t1", "orders"))
	now := time.Now()
	_, _ = s.Reserve(t.Context(), mq.ReserveOptions{
		Topics: []string{"orders"}, Limit: 1, Lease: time.Minute, ConsumerID: "w1", Now: now,
	})

	err := s.Ack(t.Context(), "t1", "wrong-consumer", now)
	if !errors.Is(err, mq.ErrLeaseLost) {
		t.Errorf("expected ErrLeaseLost, got %v", err)
	}
}

// --- Release ---

func TestMemStore_Release_OK(t *testing.T) {
	s := NewMemory(DefaultMemConfig())
	_ = s.Insert(t.Context(), newTask("t1", "orders"))
	now := time.Now()
	_, _ = s.Reserve(t.Context(), mq.ReserveOptions{
		Topics: []string{"orders"}, Limit: 1, Lease: time.Minute, ConsumerID: "w1", Now: now,
	})

	if err := s.Release(t.Context(), "t1", "w1", mq.ReleaseOptions{Now: now}); err != nil {
		t.Fatalf("Release: %v", err)
	}

	stats, _ := s.Stats(t.Context())
	if stats.Queued != 1 {
		t.Errorf("expected task back in queued, got queued=%d", stats.Queued)
	}
}

func TestMemStore_Release_NotFound(t *testing.T) {
	s := NewMemory(DefaultMemConfig())
	err := s.Release(t.Context(), "nope", "w1", mq.ReleaseOptions{})
	if !errors.Is(err, mq.ErrTaskNotFound) {
		t.Errorf("expected ErrTaskNotFound, got %v", err)
	}
}

// --- MoveToDLQ ---

func TestMemStore_MoveToDLQ_OK(t *testing.T) {
	s := NewMemory(DefaultMemConfig())
	_ = s.Insert(t.Context(), newTask("t1", "orders"))
	now := time.Now()
	_, _ = s.Reserve(t.Context(), mq.ReserveOptions{
		Topics: []string{"orders"}, Limit: 1, Lease: time.Minute, ConsumerID: "w1", Now: now,
	})

	if err := s.MoveToDLQ(t.Context(), "t1", "w1", "too many failures", now); err != nil {
		t.Fatalf("MoveToDLQ: %v", err)
	}

	stats, _ := s.Stats(t.Context())
	if stats.Dead != 1 {
		t.Errorf("expected 1 dead, got %d", stats.Dead)
	}
}

func TestMemStore_MoveToDLQ_NotFound(t *testing.T) {
	s := NewMemory(DefaultMemConfig())
	err := s.MoveToDLQ(t.Context(), "nope", "w1", "reason", time.Now())
	if !errors.Is(err, mq.ErrTaskNotFound) {
		t.Errorf("expected ErrTaskNotFound, got %v", err)
	}
}

// --- ExtendLease ---

func TestMemStore_ExtendLease_OK(t *testing.T) {
	s := NewMemory(DefaultMemConfig())
	_ = s.Insert(t.Context(), newTask("t1", "orders"))
	now := time.Now()
	_, _ = s.Reserve(t.Context(), mq.ReserveOptions{
		Topics: []string{"orders"}, Limit: 1, Lease: time.Minute, ConsumerID: "w1", Now: now,
	})

	if err := s.ExtendLease(t.Context(), "t1", "w1", 2*time.Minute, now); err != nil {
		t.Fatalf("ExtendLease: %v", err)
	}
}

func TestMemStore_ExtendLease_NotFound(t *testing.T) {
	s := NewMemory(DefaultMemConfig())
	err := s.ExtendLease(t.Context(), "nope", "w1", time.Minute, time.Now())
	if !errors.Is(err, mq.ErrTaskNotFound) {
		t.Errorf("expected ErrTaskNotFound, got %v", err)
	}
}

// --- Stats ---

func TestMemStore_Stats_AllStatuses(t *testing.T) {
	s := NewMemory(DefaultMemConfig())
	now := time.Now()

	// Insert one per topic to keep topics isolated.

	// Leased: insert into "leased-topic", reserve it.
	_ = s.Insert(t.Context(), newTask("leased", "leased-topic"))
	_, _ = s.Reserve(t.Context(), mq.ReserveOptions{Topics: []string{"leased-topic"}, Limit: 1, Lease: time.Minute, ConsumerID: "w1", Now: now})

	// Dead: insert into "dead-topic", reserve, then move to DLQ.
	_ = s.Insert(t.Context(), newTask("dead", "dead-topic"))
	_, _ = s.Reserve(t.Context(), mq.ReserveOptions{Topics: []string{"dead-topic"}, Limit: 1, Lease: time.Minute, ConsumerID: "w2", Now: now})
	_ = s.MoveToDLQ(t.Context(), "dead", "w2", "fail", now)

	// Queued: just insert into "queued-topic", do not reserve.
	_ = s.Insert(t.Context(), newTask("queued", "queued-topic"))

	stats, err := s.Stats(t.Context())
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Queued != 1 {
		t.Errorf("Queued = %d, want 1", stats.Queued)
	}
	if stats.Leased != 1 {
		t.Errorf("Leased = %d, want 1", stats.Leased)
	}
	if stats.Dead != 1 {
		t.Errorf("Dead = %d, want 1", stats.Dead)
	}
}

// --- ReplayDLQ ---

func TestMemStore_ReplayDLQ_MovesDeadToQueued(t *testing.T) {
	s := NewMemory(DefaultMemConfig())
	now := time.Now()

	_ = s.Insert(t.Context(), newTask("d1", "orders"))
	_, _ = s.Reserve(t.Context(), mq.ReserveOptions{Topics: []string{"orders"}, Limit: 1, Lease: time.Minute, ConsumerID: "w1", Now: now})
	_ = s.MoveToDLQ(t.Context(), "d1", "w1", "fail", now)

	result, err := s.ReplayDLQ(t.Context(), ReplayOptions{Max: 10})
	if err != nil {
		t.Fatalf("ReplayDLQ: %v", err)
	}
	if result.Replayed != 1 {
		t.Errorf("Replayed = %d, want 1", result.Replayed)
	}

	stats, _ := s.Stats(t.Context())
	if stats.Dead != 0 {
		t.Errorf("expected 0 dead after replay, got %d", stats.Dead)
	}
	if stats.Queued != 1 {
		t.Errorf("expected 1 queued after replay, got %d", stats.Queued)
	}
}

func TestMemStore_ReplayDLQ_NilStore_Error(t *testing.T) {
	var s *MemStore
	_, err := s.ReplayDLQ(context.Background(), ReplayOptions{})
	if err == nil {
		t.Error("expected error for nil MemStore")
	}
}
