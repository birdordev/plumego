//go:build unix

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

func TestPostgresSchedulerDispersesSimultaneousPlans(t *testing.T) {
	_, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	const (
		planned = 100
		strict  = 4
		window  = 5000
	)
	for i := range planned {
		tenant := "hot-tenant"
		if i >= 64 {
			tenant = fmt.Sprintf("quiet-%d", i%3)
		}
		jitter := window
		if i >= planned-strict {
			jitter = 0
		}
		if err := repo.addSchedule(ctx, scheduleDefinition{
			Tenant: tenant, ID: fmt.Sprintf("simultaneous-%d", i), Expression: "@every 1h",
			TimeZone: "UTC", Topic: recordTaskType, Payload: json.RawMessage(`{}`), JitterWindow: jitter,
		}); err != nil {
			t.Fatal(err)
		}
	}
	startSchedulerChild(t, db, "scheduler")
	startSchedulerChild(t, db, "scheduler")
	awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
		var owners int
		err := db.QueryRowContext(ctx, "SELECT count(DISTINCT owner_id) FROM scheduler_shards").Scan(&owners)
		return owners == 2, err
	})
	var scheduled time.Time
	if err := db.QueryRowContext(ctx, "SELECT clock_timestamp() + interval '1 second'").Scan(&scheduled); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE schedules SET next_fire_at = $1", scheduled); err != nil {
		t.Fatal(err)
	}
	awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
		var available int
		err := db.QueryRowContext(ctx, "SELECT count(*) FROM scheduled_runs WHERE scheduled_at = $1", scheduled).Scan(&available)
		return available == planned, err
	})
	visibleAt := time.Now()
	var slots, hotShards, wrongIdentity int
	var minJitter, maxJitter, maxCreation, maxStrict float64
	err := db.QueryRowContext(ctx, `SELECT
		count(DISTINCT floor(extract(epoch FROM q.available_at - r.scheduled_at) * 10)),
		count(DISTINCT s.schedule_shard) FILTER (WHERE s.tenant_id = 'hot-tenant'),
		count(*) FILTER (WHERE q.available_at <> r.scheduled_at +
			scheduler_jitter_milliseconds(s.tenant_id, s.id, s.jitter_window_ms) * interval '1 millisecond'),
		min(extract(epoch FROM q.available_at - r.scheduled_at)),
		max(extract(epoch FROM q.available_at - r.scheduled_at)),
		max(extract(epoch FROM q.created_at - r.scheduled_at)),
		max(extract(epoch FROM q.created_at - r.scheduled_at)) FILTER (WHERE s.jitter_window_ms = 0)
		FROM scheduled_runs r JOIN mq_tasks q ON q.id = r.id
		JOIN schedules s ON s.id = r.schedule_id AND s.tenant_id = r.tenant_id WHERE r.scheduled_at = $1`,
		scheduled).Scan(&slots, &hotShards, &wrongIdentity, &minJitter, &maxJitter, &maxCreation, &maxStrict)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("simultaneous plans=%d, 100ms slots=%d, hot tenant shards=%d, max jitter=%.3fs, latest creation=%.3fs, last commit observed=%.3fs, strict latest=%.3fs",
		planned, slots, hotShards, maxJitter, maxCreation, visibleAt.Sub(scheduled).Seconds(), maxStrict)
	if slots < 20 || hotShards < 16 || wrongIdentity != 0 || minJitter != 0 || maxJitter >= float64(window)/1000 ||
		maxStrict > 1 || visibleAt.Sub(scheduled) > time.Duration(window)*time.Millisecond {
		t.Fatalf("invalid distribution: slots=%d hot=%d identity=%d jitter=%.3f..%.3f strict=%.3f latest=%s",
			slots, hotShards, wrongIdentity, minJitter, maxJitter, maxStrict, visibleAt.Sub(scheduled))
	}
}
