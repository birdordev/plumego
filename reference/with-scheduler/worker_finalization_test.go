package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"
	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestPostgresWorkerRecoversFailedFinalizeWrites(t *testing.T) {
	for _, tc := range []struct {
		operation, target, reason string
		failure                   mq.TaskFailureKind
		dlq                       int
	}{
		{"release", "queued", "task failed", mq.TaskFailureTransient, 0},
		{"dlq", "dead", "permanent task failure", mq.TaskFailurePermanent, 1},
	} {
		t.Run(tc.operation, func(t *testing.T) {
			first, second, db := postgresTestQueues(t)
			ctx := t.Context()
			repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
			id, err := repo.submit(ctx, "tenant", "failed-finalization", submission{Topic: "demo.record", Payload: json.RawMessage(`{}`)})
			if err != nil {
				t.Fatal(err)
			}
			// Use a sequence to count rejected statements across transaction rollbacks.
			if _, err := db.ExecContext(ctx, `CREATE SEQUENCE failed_finalizations;
				CREATE FUNCTION reject_finalization() RETURNS trigger LANGUAGE plpgsql AS $$
				BEGIN
					IF OLD.status = 'leased' AND NEW.status IN ('queued', 'dead') THEN
						PERFORM nextval('failed_finalizations');
						RAISE EXCEPTION 'injected finalization failure';
					END IF;
					RETURN NEW;
				END $$;
				CREATE TRIGGER reject_finalization BEFORE UPDATE ON mq_tasks
					FOR EACH ROW EXECUTE FUNCTION reject_finalization()`); err != nil {
				t.Fatal(err)
			}
			type confirmation struct {
				id, operation string
				err           error
			}
			unconfirmed := make(chan confirmation, 2)
			cfg := mq.WorkerConfig{
				Concurrency:  1,
				MaxInflight:  1,
				PollInterval: 10 * time.Millisecond,
				RetryPolicy:  mq.ExponentialBackoff{Base: time.Hour, Max: time.Hour},
				FinalizationErrorHook: func(_ context.Context, taskID, operation string, err error) {
					unconfirmed <- confirmation{taskID, operation, err}
				},
			}
			handler := func(context.Context, mq.Task) error { return &mq.TaskFailure{Kind: tc.failure} }
			worker := mq.NewWorker(first, cfg)
			worker.Register("demo.record", handler)
			worker.Start(ctx)
			stop := func(w *mq.Worker) {
				t.Helper()
				if err := w.Stop(ctx); err != nil {
					t.Error(err)
				}
			}
			defer stop(worker)
			select {
			case got := <-unconfirmed:
				var pgErr *pgconn.PgError
				if got.id != id || got.operation != tc.operation || !errors.As(got.err, &pgErr) || pgErr.Code != "P0001" {
					t.Fatalf("unconfirmed operation = %+v", got)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("worker did not report unconfirmed finalization")
			}
			stop(worker)

			var status, oldOwner string
			var failed, dead, finished int
			if err := db.QueryRowContext(ctx, `SELECT status, lease_owner,
				(SELECT last_value FROM failed_finalizations),
				(SELECT count(*) FROM mq_task_dlq WHERE task_id = $1),
				(SELECT count(*) FROM mq_task_attempts WHERE task_id = $1 AND finished_at IS NOT NULL)
				FROM mq_tasks WHERE id = $1`, id).Scan(&status, &oldOwner, &failed, &dead, &finished); err != nil {
				t.Fatal(err)
			}
			if status != mq.TaskStatusLeased || oldOwner == "" || failed != 2 || dead != 0 || finished != 0 {
				t.Fatalf("before recovery: state=%s writes=%d dead=%d finished=%d", status, failed, dead, finished)
			}
			if _, err := db.ExecContext(ctx, "DROP TRIGGER reject_finalization ON mq_tasks"); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, "UPDATE mq_tasks SET lease_until = clock_timestamp() - interval '1 second' WHERE id = $1", id); err != nil {
				t.Fatal(err)
			}
			replacement := mq.NewWorker(second, cfg)
			replacement.Register("demo.record", handler)
			replacement.Start(ctx)
			defer stop(replacement)
			awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
				var attempts, withReason int
				var leaseCleared, delayed bool
				err := db.QueryRowContext(ctx, `SELECT status, attempts, lease_owner IS NULL AND lease_until IS NULL,
					available_at > clock_timestamp() + interval '50 minutes',
					(SELECT count(*) FROM mq_task_dlq WHERE task_id = $1 AND reason = $2),
					(SELECT count(*) FROM mq_task_attempts WHERE task_id = $1 AND attempt_no = 2 AND finished_at IS NOT NULL AND reason = $2)
					FROM mq_tasks WHERE id = $1`, id, tc.reason).Scan(&status, &attempts, &leaseCleared, &delayed, &dead, &withReason)
				return status == tc.target && attempts == 2 && leaseCleared && dead == tc.dlq && withReason == 1 &&
					(tc.operation != "release" || delayed), err
			})
			if err := first.Ack(ctx, id, oldOwner, time.Now()); !errors.Is(err, mq.ErrLeaseLost) {
				t.Fatalf("expired claim unexpectedly ACKed after recovery: %v", err)
			}
		})
	}
}

type lostAckReply struct{ mq.TaskStore }

func (q lostAckReply) Ack(ctx context.Context, id, owner string, now time.Time) error {
	if err := q.TaskStore.Ack(ctx, id, owner, now); err != nil {
		return err
	}
	return io.ErrUnexpectedEOF
}

func TestPostgresFinalizerKnowsCommittedAckAfterLostReply(t *testing.T) {
	first, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	id, err := repo.submit(ctx, "tenant", "lost-reply", submission{Topic: recordTaskType, Payload: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := first.ReserveFenced(ctx, mq.ReserveOptions{Limit: 1, ConsumerID: "worker"})
	if err != nil || len(claims) != 1 {
		t.Fatalf("claims %+v: %v", claims, err)
	}
	task := claims[0]
	if _, err := repo.beginExecution(ctx, task); err != nil {
		t.Fatal(err)
	}
	if err := repo.recordResult(ctx, task); err != nil {
		t.Fatal(err)
	}
	queue := &journalQueue{TaskStore: lostAckReply{repo.queue}, repo: repo}
	if err := queue.Ack(ctx, id, task.LeaseOwner, time.Now()); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("lost response: %v", err)
	}
	if got, err := repo.reconcileFinalization(ctx); err != nil || got != "" {
		t.Fatalf("confirmed operation selected for retry: %q %v", got, err)
	}
	page, err := repo.runAttempts(ctx, "tenant", id, 0)
	if err != nil || len(page.Items) != 1 || page.Items[0].FinalizationState != "confirmed" ||
		page.Items[0].ConfirmationError != "commit_unknown" || page.Items[0].ExecutionOutcome != "succeeded" {
		t.Fatalf("commit uncertainty history: %+v %v", page, err)
	}
}

type expireBeforeRelease struct {
	mq.TaskStore
	repo repository
}

func (q expireBeforeRelease) Release(ctx context.Context, id, owner string, opts mq.ReleaseOptions) error {
	if _, err := q.repo.db.ExecContext(ctx, `UPDATE mq_tasks SET lease_until = clock_timestamp() - interval '1 second',
		expires_at = clock_timestamp() - interval '1 second' WHERE id = $1`, id); err != nil {
		return err
	}
	opts.Now = time.Now()
	return q.TaskStore.Release(ctx, id, owner, opts)
}

func TestPostgresFinalizerReportsExpiredClaimAndHonorsCancellation(t *testing.T) {
	first, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	id, err := repo.submit(ctx, "tenant", "cancel-pending", submission{Topic: recordTaskType, Payload: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := first.ReserveFenced(ctx, mq.ReserveOptions{Limit: 1, ConsumerID: "worker"})
	if err != nil || len(claims) != 1 {
		t.Fatalf("claims %+v: %v", claims, err)
	}
	task := claims[0]
	if _, err := repo.beginExecution(ctx, task); err != nil {
		t.Fatal(err)
	}
	queue := &journalQueue{TaskStore: expireBeforeRelease{repo.queue, repo}, repo: repo}
	err = queue.Release(ctx, id, task.LeaseOwner, mq.ReleaseOptions{RetryAt: time.Now().Add(time.Hour), Reason: "task failed"})
	if !errors.Is(err, mq.ErrLeaseLost) {
		t.Fatalf("expired release: %v", err)
	}
	if changed, err := repo.expireLeasesWithoutFinalization(ctx, 128, time.Now()); err != nil || changed != 0 {
		t.Fatalf("expiry touched pending confirmation: %d %v", changed, err)
	}
	if err := repo.cancelRun(ctx, "tenant", id); err != nil {
		t.Fatal(err)
	}
	if got, err := repo.reconcileFinalization(ctx); err != nil || got != "" {
		t.Fatalf("canceled operation returned for retry: %q %v", got, err)
	}
	page, err := repo.runAttempts(ctx, "tenant", id, 0)
	if err != nil || len(page.Items) != 1 || page.Items[0].FinalizationState != "superseded" ||
		page.Items[0].ConfirmationError != "lease_lost" || page.Items[0].ExecutionOutcome != "failed" ||
		page.Items[0].FailureCode != "canceled" {
		t.Fatalf("superseded confirmation %+v %v", page, err)
	}
}
