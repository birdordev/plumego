package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"
	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

func TestOverlapOptions(t *testing.T) {
	for _, options := range []overlapOptions{
		{Policy: "skip", Scope: "key"},
		{Policy: "serial", Scope: "tenant", Key: "unexpected"},
		{Policy: "allow", Scope: "tenant"},
		{Policy: "unsupported"},
	} {
		input := submission{Topic: recordTaskType, Payload: json.RawMessage(`{}`), taskOptions: taskOptions{Overlap: &options}}
		if _, err := prepareSubmission("tenant-a", "request", input); !errors.Is(err, errInvalidTask) {
			t.Errorf("invalid overlap %+v: %v", options, err)
		}
	}
	input := submission{Topic: recordTaskType, Payload: json.RawMessage(`{}`),
		taskOptions: taskOptions{Overlap: &overlapOptions{Policy: "skip", Scope: "schedule"}}}
	if _, err := prepareSubmission("tenant-a", "request", input); !errors.Is(err, errInvalidTask) {
		t.Errorf("direct run with schedule scope: %v", err)
	}
}

func TestPostgresWorkerExecutionOverlap(t *testing.T) {
	for _, tc := range []struct {
		name, policy, scope, secondTenant string
	}{
		{"allow", "allow", "", "tenant-a"},
		{"key skip", "skip", "key", "tenant-a"},
		{"tenant serial", "serial", "tenant", "tenant-a"},
		{"schedule serial", "serial", "schedule", "tenant-a"},
		{"automatic fixed delay skip", "skip", "schedule", "tenant-a"},
		{"same key different tenants", "skip", "key", "tenant-b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first, second, db := postgresTestQueues(t)
			repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
			options := taskOptions{Overlap: &overlapOptions{Policy: tc.policy, Scope: tc.scope}}
			if tc.scope == "key" {
				options.Overlap.Key = "same-business-key"
			}
			payload := json.RawMessage(`{}`)
			fixedDelay := tc.scope == "schedule" && tc.policy == "skip"
			if tc.scope == "schedule" {
				definition := scheduleDefinition{Tenant: "tenant-a", ID: "repeat", Topic: recordTaskType,
					Expression: "@hourly", TimeZone: "UTC", Payload: payload, taskOptions: options}
				if fixedDelay {
					definition.Mode, definition.Expression = "fixed_delay", "@every 1h"
				}
				if err := repo.addSchedule(t.Context(), definition); err != nil {
					t.Fatal(err)
				}
				status, err := repo.scheduleStatus(t.Context(), "tenant-a", "repeat")
				if err != nil || status.Overlap == nil || *status.Overlap != *options.Overlap {
					t.Fatalf("schedule overlap = %+v, %v", status.Overlap, err)
				}
			}
			submit := func(tenant, key string) string {
				t.Helper()
				var id string
				var err error
				if tc.scope == "schedule" {
					id, err = repo.triggerSchedule(t.Context(), tenant, "repeat", key)
				} else {
					id, err = repo.submit(t.Context(), tenant, key, submission{Topic: recordTaskType, Payload: payload, taskOptions: options})
				}
				if err != nil {
					t.Fatal(err)
				}
				return id
			}
			start := func(queue *mq.TaskQueue, handler mq.TaskHandler) {
				worker := mq.NewWorker(queue, mq.WorkerConfig{Concurrency: 1, PollInterval: 10 * time.Millisecond,
					LeaseDuration: 10 * time.Second, BeginExecution: repo.beginExecution})
				worker.Register(recordTaskType, handler)
				worker.Start(t.Context())
				t.Cleanup(func() {
					ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
					defer cancel()
					if err := worker.Stop(ctx); err != nil {
						t.Error(err)
					}
				})
			}

			firstID := submit("tenant-a", "first")
			firstStarted := make(chan mq.Task, 1)
			releaseFirst := make(chan struct{})
			start(first, func(ctx context.Context, task mq.Task) error {
				firstStarted <- task
				select {
				case <-releaseFirst:
					return repo.recordResult(ctx, task)
				case <-ctx.Done():
					return ctx.Err()
				}
			})
			waitStart := func(started <-chan mq.Task, id string) {
				t.Helper()
				select {
				case task := <-started:
					if task.ID != id || task.ExecutionAttempts != 1 {
						t.Fatalf("unexpected start %s (execution %d)", task.ID, task.ExecutionAttempts)
					}
				case <-time.After(5 * time.Second):
					t.Fatalf("worker did not start %s", id)
				}
			}
			waitStart(firstStarted, firstID)
			var secondID string
			if fixedDelay {
				if _, err := db.ExecContext(t.Context(), "UPDATE schedules SET next_fire_at = clock_timestamp() WHERE id = 'repeat'"); err != nil {
					t.Fatal(err)
				}
				var err error
				secondID, err = repo.materializeNext(t.Context())
				if err != nil || secondID == "" {
					t.Fatalf("materialize automatic run %q: %v", secondID, err)
				}
			} else {
				secondID = submit(tc.secondTenant, "second")
			}
			secondStarted := make(chan mq.Task, 1)
			start(second, func(ctx context.Context, task mq.Task) error {
				secondStarted <- task
				return repo.recordResult(ctx, task)
			})
			switch {
			case tc.policy == "allow" || tc.secondTenant != "tenant-a":
				waitStart(secondStarted, secondID)
			default:
				expectedCode := "overlap_deferred"
				if tc.policy == "skip" {
					expectedCode = "overlap_skipped"
				}
				awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
					page, err := repo.runAttempts(ctx, tc.secondTenant, secondID, 0)
					for _, attempt := range page.Items {
						if attempt.FailureCode == expectedCode && attempt.ExecutionNo == nil {
							return true, nil
						}
					}
					return false, err
				})
				select {
				case task := <-secondStarted:
					t.Fatalf("overlapping task started before release: %s", task.ID)
				default:
				}
				if tc.policy == "skip" {
					state, err := repo.runStatus(t.Context(), tc.secondTenant, secondID)
					if err != nil || state.QueueStatus != "skipped" || state.ExecutionAttempts != 0 || state.ResultReady {
						t.Fatalf("skip state = %+v, %v", state, err)
					}
					query, err := parseRunListQuery("status=skipped")
					if err != nil {
						t.Fatal(err)
					}
					page, err := repo.listRuns(t.Context(), tc.secondTenant, query)
					if err != nil || len(page.Items) != 1 || page.Items[0].ID != secondID {
						t.Fatalf("skipped list = %+v, %v", page, err)
					}
					if fixedDelay {
						if id, err := repo.materializeNext(t.Context()); id != "" || err != nil {
							t.Fatalf("advance skipped occurrence %q: %v", id, err)
						}
						definition, err := repo.scheduleStatus(t.Context(), tc.secondTenant, "repeat")
						if err != nil || definition.WaitingRunID != nil || definition.NextFireAt == nil {
							t.Fatalf("fixed-delay cursor after skip = %+v, %v", definition, err)
						}
					}
				}
			}
			close(releaseFirst)
			if tc.policy == "serial" {
				// 第一个 Worker 在释放名额后也可能领取第二个实例。
				awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
					state, err := repo.runStatus(ctx, tc.secondTenant, secondID)
					return state.QueueStatus == "done" && state.ExecutionAttempts == 1, err
				})
			}
			awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
				state, err := repo.runStatus(ctx, "tenant-a", firstID)
				return state.QueueStatus == "done", err
			})
		})
	}
}

func TestPostgresExecutionSlotCompetitionAndRecovery(t *testing.T) {
	first, second, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	submitClaim := func(queue *mq.TaskQueue, key string, lease time.Duration) mq.Task {
		t.Helper()
		id, err := repo.submit(ctx, "tenant", key, submission{Topic: recordTaskType, Payload: json.RawMessage(`{}`),
			taskOptions: taskOptions{Overlap: &overlapOptions{Policy: "skip", Scope: "tenant"}}})
		if err != nil {
			t.Fatal(err)
		}
		tasks, err := queue.ReserveFenced(ctx, mq.ReserveOptions{ConsumerID: key, Limit: 1, Lease: lease})
		if err != nil || len(tasks) != 1 || tasks[0].ID != id {
			t.Fatalf("reserve %s = %+v, %v", key, tasks, err)
		}
		return tasks[0]
	}
	a := submitClaim(first, "first", 2*time.Second)
	b := submitClaim(second, "second", 2*time.Second)
	barrier := make(chan struct{})
	type admission struct {
		task mq.Task
		n    int
		err  error
	}
	results := make(chan admission, 2)
	for _, task := range []mq.Task{a, b} {
		go func() {
			<-barrier
			n, err := repo.beginExecution(ctx, task)
			results <- admission{task, n, err}
		}()
	}
	close(barrier)
	var winner mq.Task
	skips := 0
	for range 2 {
		select {
		case result := <-results:
			switch {
			case result.n == 1 && result.err == nil:
				winner = result.task
			case result.n == 0 && errors.Is(result.err, mq.ErrExecutionHandled):
				skips++
			default:
				t.Fatalf("competing admission = %d, %v", result.n, result.err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("competing admission timed out")
		}
	}
	if winner.ID == "" || skips != 1 {
		t.Fatalf("winner %q, skips %d", winner.ID, skips)
	}
	if err := first.ExtendLease(ctx, winner.ID, winner.LeaseOwner, time.Minute, time.Now()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-time.After(time.Until(winner.LeaseUntil) + 20*time.Millisecond):
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	duringRenewal := submitClaim(second, "after-renewal", time.Minute)
	if n, err := repo.beginExecution(ctx, duringRenewal); n != 0 || !errors.Is(err, mq.ErrExecutionHandled) {
		t.Fatalf("renewed lease admission = %d, %v", n, err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE mq_tasks SET lease_until = clock_timestamp() - interval '1 second' WHERE id = $1", winner.ID); err != nil {
		t.Fatal(err)
	}
	// 过期任务会重新参与领取。已有代次仍无法提交业务结果。
	claims, err := second.ReserveFenced(ctx, mq.ReserveOptions{ConsumerID: "replacement", Limit: 1, Lease: time.Minute})
	if err != nil || len(claims) != 1 || claims[0].ID != winner.ID || claims[0].LeaseOwner == winner.LeaseOwner {
		t.Fatalf("replacement claim = %+v, %v", claims, err)
	}
	replacement := claims[0]
	if n, err := repo.beginExecution(ctx, replacement); n != 2 || err != nil {
		t.Fatalf("replacement admission = %d, %v", n, err)
	}
	if err := repo.recordResult(ctx, winner); !errors.Is(err, mq.ErrLeaseLost) {
		t.Fatalf("old claim result = %v", err)
	}
	if err := repo.recordResult(ctx, replacement); err != nil {
		t.Fatal(err)
	}
}
