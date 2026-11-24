//go:build unix

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

func TestPostgresSchedulerShardTakeoverWithContendedRows(t *testing.T) {
	_, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	first := startSchedulerChild(t, db, "scheduler")
	var firstOwner, secondOwner string
	awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
		var assigned int
		err := db.QueryRowContext(ctx, "SELECT count(*), COALESCE(min(owner_id), '') FROM scheduler_shards WHERE owner_id IS NOT NULL").
			Scan(&assigned, &firstOwner)
		return assigned == 64 && firstOwner != "", err
	})
	startSchedulerChild(t, db, "scheduler")
	awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
		err := db.QueryRowContext(ctx, "SELECT COALESCE(min(owner_id), '') FROM scheduler_shards WHERE owner_id <> $1", firstOwner).
			Scan(&secondOwner)
		return secondOwner != "", err
	})

	const tenant = "tenant"
	pick := func(owner, prefix string, avoid int) (string, int) {
		t.Helper()
		var id string
		var shard int
		err := db.QueryRowContext(ctx, `SELECT candidate.id, s.shard FROM (
			SELECT $2 || i::text AS id FROM generate_series(1, 512) i
		) candidate JOIN scheduler_shards s ON s.shard = scheduler_plan_shard($1, candidate.id)
		WHERE s.owner_id = $3 AND s.shard <> $4 ORDER BY candidate.id LIMIT 1`, tenant, prefix, owner, avoid).
			Scan(&id, &shard)
		if err != nil {
			t.Fatal(err)
		}
		return id, shard
	}
	blocked, blockedShard := pick(firstOwner, "blocked", -1)
	neighbor, neighborShard := pick(firstOwner, "neighbor", blockedShard)
	other, _ := pick(secondOwner, "other", -1)
	for _, id := range []string{blocked, neighbor, other} {
		if err := repo.addSchedule(ctx, scheduleDefinition{Tenant: tenant, ID: id, Expression: "@every 1s",
			TimeZone: "UTC", Topic: recordTaskType, Payload: json.RawMessage(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	var seed time.Time
	if err := db.QueryRowContext(ctx, "SELECT date_trunc('second', clock_timestamp()) + interval '2 seconds'").Scan(&seed); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE schedules SET next_fire_at = $1", seed); err != nil {
		t.Fatal(err)
	}
	planLock, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = planLock.Rollback() }()
	if _, err := planLock.ExecContext(ctx, "SELECT id FROM schedules WHERE tenant_id = $1 AND id = $2 FOR UPDATE", tenant, blocked); err != nil {
		t.Fatal(err)
	}
	awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
		var committed int
		err := db.QueryRowContext(ctx, `SELECT count(*) FROM scheduled_runs WHERE tenant_id = $1
			AND schedule_id IN ($2, $3) AND scheduled_at = $4`, tenant, neighbor, other, seed).Scan(&committed)
		return committed == 2, err
	})
	shardLock, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = shardLock.Rollback() }()
	if _, err := shardLock.ExecContext(ctx, "SELECT shard FROM scheduler_shards WHERE shard = $1 FOR SHARE", blockedShard); err != nil {
		t.Fatal(err)
	}
	killAt := time.Now()
	killSchedulerChild(t, first)
	assignedTo := func(ctx context.Context, shard int) (string, error) {
		var owner sql.NullString
		err := db.QueryRowContext(ctx, "SELECT owner_id FROM scheduler_shards WHERE shard = $1", shard).Scan(&owner)
		return owner.String, err
	}
	awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
		owner, err := assignedTo(ctx, neighborShard)
		return owner == secondOwner, err
	})
	if elapsed := time.Since(killAt); elapsed > shardLease+4*shardHeartbeat {
		t.Fatalf("uncontended shard takeover took %v", elapsed)
	} else {
		t.Logf("uncontended shard takeover took %v", elapsed)
	}
	if owner, err := assignedTo(ctx, blockedShard); err != nil || owner != firstOwner {
		t.Fatalf("locked ownership changed early: %q %v", owner, err)
	}
	if err := shardLock.Rollback(); err != nil {
		t.Fatal(err)
	}
	unlockAt := time.Now()
	awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
		owner, err := assignedTo(ctx, blockedShard)
		return owner == secondOwner, err
	})
	if elapsed := time.Since(unlockAt); elapsed > 4*shardHeartbeat {
		t.Fatalf("unlocked shard takeover took %v", elapsed)
	} else {
		t.Logf("locked shard transferred %v after unlock", elapsed)
	}
	if _, err := repo.plannedOccurrence(ctx, tenant, blocked, seed); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("locked plan materialized before release: %v", err)
	}
	if err := planLock.Rollback(); err != nil {
		t.Fatal(err)
	}
	if id, err := repo.materializeOwned(ctx, nil, shardClaim{owner: firstOwner, shard: blockedShard, regionEpoch: 1}); err != nil || id != "" {
		t.Fatalf("expired owner entered shard after transfer: %q %v", id, err)
	}
	const checkedOccurrences = 4
	awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
		var count int
		err := db.QueryRowContext(ctx, `SELECT count(*) FROM scheduled_runs WHERE tenant_id = $1 AND schedule_id = $2
			AND scheduled_at IN (SELECT $3::timestamptz + delta * interval '1 second' FROM generate_series(0, $4 - 1) delta)`,
			tenant, blocked, seed, checkedOccurrences).Scan(&count)
		return count == checkedOccurrences, err
	})
	state, err := repo.scheduleStatus(ctx, tenant, blocked)
	if err != nil || state.NextFireAt == nil || state.NextFireAt.Before(seed.Add(checkedOccurrences*time.Second)) {
		t.Fatalf("cursor after catch-up %+v: %v", state, err)
	}
}
