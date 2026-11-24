package mq

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

type workerDecisionStore struct {
	TaskStore
	renew   func(context.Context) error
	outcome string
	release ReleaseOptions
	reason  string
}

func (s *workerDecisionStore) ExtendLease(ctx context.Context, _, _ string, _ time.Duration, _ time.Time) error {
	if s.renew != nil {
		return s.renew(ctx)
	}
	return nil
}

func (s *workerDecisionStore) Ack(context.Context, string, string, time.Time) error {
	s.outcome = "ack"
	return nil
}

func (s *workerDecisionStore) Release(_ context.Context, _, _ string, opts ReleaseOptions) error {
	s.outcome, s.release = "release", opts
	return nil
}

func (s *workerDecisionStore) MoveToDLQ(_ context.Context, _, _, reason string, _ time.Time) error {
	s.outcome, s.reason = "dlq", reason
	return nil
}

func newDecisionWorker(store *workerDecisionStore, ctx context.Context, config WorkerConfig, handler TaskHandler) *Worker {
	w := NewWorker(NewTaskQueue(store), config)
	w.config = normalizeWorkerConfig(config)
	w.runCtx = ctx
	w.Register("work", handler)
	return w
}

func TestWorkerRejectsPrefetchWithRevokedClaim(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		now := time.Now()
		checked := false
		store := &workerDecisionStore{renew: func(ctx context.Context) error {
			checked = true
			deadline, ok := ctx.Deadline()
			if !ok || deadline.After(now.Add(3*time.Second)) {
				t.Errorf("renewal deadline: %v (%v)", deadline, ok)
			}
			return ErrLeaseLost
		}}
		w := newDecisionWorker(store, t.Context(), WorkerConfig{LeaseExtendInterval: 3 * time.Second}, func(context.Context, Task) error {
			t.Fatal("revoked claim entered handler")
			return nil
		})
		w.handleTask(Task{ID: "one", Topic: "work", LeaseOwner: "old", LeaseUntil: now.Add(10 * time.Second)})
		if !checked || store.outcome != "" {
			t.Fatalf("confirmation requested=%t, finalization=%q", checked, store.outcome)
		}
	})
}

func TestWorkerClassifiesAndResolvesTaskFailures(t *testing.T) {
	for _, tc := range []struct {
		name     string
		kind     TaskFailureKind
		resolve  func(context.Context, Task) (bool, error)
		outcome  string
		reason   string
		minDelay time.Duration
	}{
		{"permanent", TaskFailurePermanent, nil, "dlq", "permanent task failure", 0},
		{"rate limited", TaskFailureRateLimited, nil, "release", "task rate limited", 8 * time.Second},
		{"unknown", TaskFailureOutcomeUnknown, nil, "dlq", "task outcome unknown", 0},
		{"committed", TaskFailureOutcomeUnknown, func(context.Context, Task) (bool, error) { return true, nil }, "ack", "", 0},
		{"safe to retry", TaskFailureOutcomeUnknown, func(context.Context, Task) (bool, error) { return false, nil }, "release", "task failed", 0},
		{"reconciliation failed", TaskFailureOutcomeUnknown, func(context.Context, Task) (bool, error) {
			return false, errors.New("unavailable")
		}, "dlq", "task outcome unknown", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				now := time.Now()
				store := &workerDecisionStore{}
				w := newDecisionWorker(store, t.Context(), WorkerConfig{ResolveOutcome: tc.resolve}, func(context.Context, Task) error {
					return &TaskFailure{Kind: tc.kind, RetryAfter: 8 * time.Second, Cause: errors.New("sensitive")}
				})
				w.handleTask(Task{ID: "one", Topic: "work", CreatedAt: now, Attempts: 1, MaxAttempts: 5})
				reason := store.reason
				if store.outcome == "release" {
					reason = store.release.Reason
					if delay := store.release.RetryAt.Sub(now); delay < tc.minDelay {
						t.Errorf("retry delay = %v, want at least %v", delay, tc.minDelay)
					}
				}
				if store.outcome != tc.outcome || reason != tc.reason {
					t.Fatalf("outcome=%q reason=%q, want %q %q", store.outcome, reason, tc.outcome, tc.reason)
				}
			})
		})
	}
}

func TestWorkerEnforcesSeparateTaskTimeLimits(t *testing.T) {
	for _, tc := range []struct {
		name        string
		cfg         WorkerConfig
		queueAge    time.Duration
		age         time.Duration
		outcome     string
		reason      string
		handlerRuns bool
	}{
		{"queue", WorkerConfig{MaxQueueWait: time.Second}, 2 * time.Second, 3 * time.Second, "dlq", "task queue wait limit exceeded", false},
		{"total already elapsed", WorkerConfig{MaxTaskAge: time.Second}, 0, 2 * time.Second, "dlq", "task age limit exceeded", false},
		{"execution", WorkerConfig{TaskTimeout: time.Second}, 0, 0, "release", "task execution timed out", true},
		{"total while running", WorkerConfig{MaxTaskAge: time.Second, TaskTimeout: 2 * time.Second}, 0, 0, "dlq", "task age limit exceeded", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				now := time.Now()
				started := false
				store := &workerDecisionStore{}
				w := newDecisionWorker(store, t.Context(), tc.cfg, func(ctx context.Context, _ Task) error {
					started = true
					<-ctx.Done()
					return ctx.Err()
				})
				w.handleTask(Task{ID: "one", Topic: "work", Attempts: 1, MaxAttempts: 5, AvailableAt: now.Add(-tc.queueAge), CreatedAt: now.Add(-tc.age)})
				reason := store.reason
				if store.outcome == "release" {
					reason = store.release.Reason
				}
				if started != tc.handlerRuns || store.outcome != tc.outcome || reason != tc.reason {
					t.Fatalf("started=%t, outcome=%q reason=%q", started, store.outcome, reason)
				}
			})
		})
	}
}

type workerIdentityStore struct {
	TaskStore
	owner chan string
}

func (s workerIdentityStore) Reserve(ctx context.Context, opts ReserveOptions) ([]Task, error) {
	s.owner <- opts.ConsumerID
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestSameNamedWorkersUseDifferentStartupIdentities(t *testing.T) {
	cfg := WorkerConfig{ConsumerID: "worker", Pool: "exports", Version: "v2"}
	var ids []string
	for range 2 {
		store := workerIdentityStore{owner: make(chan string, 1)}
		worker := NewWorker(NewTaskQueue(store), cfg)
		identity := worker.Identity()
		if identity.ConsumerID != "worker" || identity.Pool != "exports" || identity.Version != "v2" || identity.InstanceID == "" {
			t.Fatalf("identity = %+v", identity)
		}
		worker.Register("work", func(context.Context, Task) error { return nil })
		worker.Start(t.Context())
		select {
		case owner := <-store.owner:
			if !strings.HasPrefix(owner, "worker/"+identity.InstanceID+"/") {
				t.Fatalf("claim owner = %q, identity = %+v", owner, identity)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("worker did not reserve")
		}
		if err := worker.Stop(t.Context()); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, worker.Identity().InstanceID)
	}
	if ids[0] == ids[1] {
		t.Fatal("worker start identities collide")
	}
}

func TestWorkerDoesNotRepeatUncertainSuccessAfterTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &workerDecisionStore{}
		w := newDecisionWorker(store, t.Context(), WorkerConfig{TaskTimeout: time.Second}, func(ctx context.Context, _ Task) error {
			<-ctx.Done()
			return nil
		})
		w.handleTask(Task{ID: "one", Topic: "work", Attempts: 1, MaxAttempts: 5})
		if store.outcome != "dlq" || store.reason != "task outcome unknown" {
			t.Fatalf("outcome=%s reason=%q", store.outcome, store.reason)
		}
	})
}
