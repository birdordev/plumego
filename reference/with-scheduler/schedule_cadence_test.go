package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"
	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

func TestPostgresScheduleCadence(t *testing.T) {
	t.Run("fixed rate preserves phase during overlap and retry", func(t *testing.T) {
		queue, _, db := postgresTestQueues(t)
		ctx := t.Context()
		repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
		if err := repo.addSchedule(ctx, scheduleDefinition{Tenant: "tenant", ID: "rate", Expression: "@every 1s",
			TimeZone: "UTC", Topic: "demo.record", Payload: json.RawMessage(`{}`), Mode: "fixed_rate"}); err != nil {
			t.Fatal(err)
		}
		var anchor time.Time
		if err := db.QueryRowContext(ctx, `UPDATE schedules SET next_fire_at = clock_timestamp() - interval '3 seconds'
			WHERE id = 'rate' RETURNING next_fire_at`).Scan(&anchor); err != nil {
			t.Fatal(err)
		}
		materialize := func(step int) string {
			t.Helper()
			id, err := repo.materializeNext(ctx)
			if err != nil || id == "" {
				t.Fatalf("materialize step %d: id=%q err=%v", step, id, err)
			}
			status, err := repo.runStatus(ctx, "tenant", id)
			if want := anchor.Add(time.Duration(step) * time.Second); err != nil || !status.ScheduledAt.Equal(want) {
				t.Fatalf("step %d: scheduled=%s want=%s err=%v", step, status.ScheduledAt, want, err)
			}
			return id
		}
		a := materialize(0)
		b := materialize(1)
		rows, err := queue.ReserveFenced(ctx, mq.ReserveOptions{Limit: 2, ConsumerID: "worker"})
		if err != nil || len(rows) != 2 {
			t.Fatalf("initial claims: count=%d err=%v", len(rows), err)
		}
		claims := map[string]string{rows[0].ID: rows[0].LeaseOwner, rows[1].ID: rows[1].LeaseOwner}
		if err := queue.Ack(ctx, b, claims[b], time.Now()); err != nil {
			t.Fatal(err)
		}
		if err := queue.Release(ctx, a, claims[a], mq.ReleaseOptions{RetryAt: time.Now().Add(time.Minute), Reason: "task failed"}); err != nil {
			t.Fatal(err)
		}
		materialize(2)
		status, err := repo.scheduleStatus(ctx, "tenant", "rate")
		if err != nil || status.Mode != "fixed_rate" || status.WaitingRunID != nil || status.NextFireAt == nil ||
			!status.NextFireAt.Equal(anchor.Add(3*time.Second)) {
			t.Fatalf("fixed-rate cursor: %+v %v", status, err)
		}
	})

	t.Run("fixed delay starts at each durable terminal transition", func(t *testing.T) {
		queue, nextQueue, db := postgresTestQueues(t)
		ctx := t.Context()
		repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
		replacement := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
		app, err := newApp("127.0.0.1:0", "test-token", "tenant", repo)
		if err != nil {
			t.Fatal(err)
		}
		for _, sample := range []struct {
			expression string
			status     int
		}{{"0 * * * *", 400}, {"@every 250ms", 201}} {
			raw, err := json.Marshal(scheduleDefinition{ID: "delay", Expression: sample.expression, TimeZone: "UTC",
				Topic: "demo.record", Payload: json.RawMessage(`{}`), Mode: "fixed_delay"})
			if err != nil {
				t.Fatal(err)
			}
			w := auditHTTPRequest(ctx, app, http.MethodPost, "/schedules", "", "", string(raw))
			if w.Code != sample.status {
				t.Fatalf("create %s: %d %s", sample.expression, w.Code, w.Body)
			}
		}
		if _, err := db.ExecContext(ctx, "UPDATE schedules SET next_fire_at = clock_timestamp() WHERE id = 'delay'"); err != nil {
			t.Fatal(err)
		}
		reserve := func(q *mq.TaskQueue, id string) mq.Task {
			t.Helper()
			rows, err := q.ReserveFenced(ctx, mq.ReserveOptions{Limit: 1, ConsumerID: "worker"})
			if err != nil || len(rows) != 1 || rows[0].ID != id {
				t.Fatalf("reserve %s: count=%d err=%v", id, len(rows), err)
			}
			return rows[0]
		}
		noWork := func() {
			t.Helper()
			if id, err := replacement.materializeNext(ctx); err != nil || id != "" {
				t.Fatalf("unexpected occurrence %q: %v", id, err)
			}
		}
		first, err := repo.materializeNext(ctx)
		if err != nil || first == "" {
			t.Fatalf("initial occurrence %q: %v", first, err)
		}
		task := reserve(queue, first)
		if err := queue.Release(ctx, first, task.LeaseOwner, mq.ReleaseOptions{Reason: "task failed"}); err != nil {
			t.Fatal(err)
		}
		noWork()
		task = reserve(nextQueue, first)
		manual, err := repo.triggerSchedule(ctx, "tenant", "delay", "manual")
		if err != nil {
			t.Fatal(err)
		}
		manualTask := reserve(queue, manual)
		if err := queue.Ack(ctx, manual, manualTask.LeaseOwner, time.Now()); err != nil {
			t.Fatal(err)
		}
		// 首次自动运行的耗时超过配置的间隔。
		select {
		case <-time.After(300 * time.Millisecond):
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		noWork()
		status, err := replacement.scheduleStatus(ctx, "tenant", "delay")
		if err != nil || status.Mode != "fixed_delay" || status.WaitingRunID == nil || *status.WaitingRunID != first || status.NextFireAt != nil {
			t.Fatalf("waiting cadence: %+v %v", status, err)
		}
		if err := nextQueue.Ack(ctx, first, task.LeaseOwner, time.Now()); err != nil {
			t.Fatal(err)
		}
		completedAt := func(id string) time.Time {
			t.Helper()
			var result time.Time
			if err := db.QueryRowContext(ctx, "SELECT delay_completed_at FROM scheduled_runs WHERE id = $1", id).Scan(&result); err != nil {
				t.Fatal(err)
			}
			return result
		}
		nextAt := completedAt(first).Add(250 * time.Millisecond)
		noWork()
		status, err = replacement.scheduleStatus(ctx, "tenant", "delay")
		if err != nil || status.WaitingRunID != nil || status.NextFireAt == nil || !status.NextFireAt.Equal(nextAt) {
			t.Fatalf("recovered cadence: %+v %v", status, err)
		}
		var second string
		awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
			var err error
			second, err = replacement.materializeNext(ctx)
			return second != "", err
		})
		run, err := repo.runStatus(ctx, "tenant", second)
		if err != nil || !run.ScheduledAt.Equal(nextAt) {
			t.Fatalf("second occurrence: %+v %v", run, err)
		}
		// 短任务永久失败，新的间隔仍从持久化的终态时间开始。
		task = reserve(queue, second)
		if err := queue.MoveToDLQ(ctx, second, task.LeaseOwner, "permanent task failure", time.Now()); err != nil {
			t.Fatal(err)
		}
		thirdAt := completedAt(second).Add(250 * time.Millisecond)
		noWork()
		status, err = replacement.scheduleStatus(ctx, "tenant", "delay")
		if err != nil || status.NextFireAt == nil || !status.NextFireAt.Equal(thirdAt) {
			t.Fatalf("post-failure cadence: %+v %v", status, err)
		}
	})
}

func TestPostgresFixedDelayIgnoresSupersededRun(t *testing.T) {
	_, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	d := scheduleDefinition{Tenant: "tenant", ID: "replaced", Expression: "@every 1h",
		TimeZone: "UTC", Topic: "demo.record", Payload: json.RawMessage(`{}`), Mode: "fixed_delay"}
	if err := repo.addSchedule(ctx, d); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE schedules SET next_fire_at = clock_timestamp() WHERE id = 'replaced'"); err != nil {
		t.Fatal(err)
	}
	id, err := repo.materializeNext(ctx)
	if err != nil || id == "" {
		t.Fatalf("initial occurrence %q: %v", id, err)
	}
	d.Payload = json.RawMessage(`{"new":true}`)
	if _, err := repo.replaceSchedule(ctx, 1, d); err != nil {
		t.Fatal(err)
	}
	unchanged, err := repo.scheduleStatus(ctx, "tenant", d.ID)
	if err != nil || unchanged.WaitingRunID == nil || *unchanged.WaitingRunID != id {
		t.Fatalf("payload change interrupted cadence: %+v %v", unchanged, err)
	}
	d.Mode = "fixed_rate"
	if _, err := repo.replaceSchedule(ctx, 2, d); err != nil {
		t.Fatal(err)
	}
	changed, err := repo.scheduleStatus(ctx, "tenant", d.ID)
	if err != nil || changed.NextFireAt == nil || changed.WaitingRunID != nil {
		t.Fatalf("new cadence: %+v %v", changed, err)
	}
	if err := repo.cancelRun(ctx, "tenant", id); err != nil {
		t.Fatal(err)
	}
	if err := advanceCompletedDelays(ctx, repo.db, "", nil); err != nil {
		t.Fatal(err)
	}
	final, err := repo.scheduleStatus(ctx, "tenant", d.ID)
	if err != nil || final.Mode != "fixed_rate" || final.NextFireAt == nil || !final.NextFireAt.Equal(*changed.NextFireAt) {
		t.Fatalf("superseded run moved the new cadence: %+v %v", final, err)
	}
	var consumed bool
	if err := db.QueryRowContext(ctx, `SELECT delay_completed_at IS NOT NULL AND delay_applied
		FROM scheduled_runs WHERE id = $1`, id).Scan(&consumed); err != nil || !consumed {
		t.Fatalf("superseded completion not consumed: %v, %v", consumed, err)
	}
}
