//go:build unix

package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	mqstore "github.com/birdordev/plumego/x/messaging/mq/store"
)

func TestPostgresScheduleTimeZoneSnapshotAcrossReplicaRollout(t *testing.T) {
	_, _, db := postgresTestQueues(t)
	ctx := t.Context()
	repo := repository{db, mqstore.NewSQL(db, mqstore.DefaultSQLConfig())}
	app, err := newApp("", "test-token", "tenant", repo)
	if err != nil {
		t.Fatal(err)
	}
	oldArchive, updatedArchive := zoneArchive(t, 0), zoneArchive(t, 7200)
	t.Setenv("SCHEDULER_TZDATA_ZIP", oldArchive)
	now := time.Now().UTC()
	due := now.Add(-3 * time.Minute).Truncate(time.Minute)
	expression := fmt.Sprintf("0,30 %d %d * * *", due.Minute(), due.Hour())
	definition := fmt.Sprintf(`"expression":%q,"time_zone":%q,"topic":"demo.record","payload":{},"misfire_policy":"coalesce"`, expression, exampleZone)
	response := auditHTTPRequest(ctx, app, "POST", "/schedules", "", "", `{"id":"rollout",`+definition+`}`)
	if response.Code != 201 {
		t.Fatalf("create: %d %s", response.Code, response.Body)
	}
	state, err := repo.scheduleStatus(ctx, "tenant", "rollout")
	if err != nil || state.TimeZone != exampleZone || len(state.ZoneRulesDigest) != 64 {
		t.Fatalf("initial zone state %+v: %v", state, err)
	}
	oldDigest := state.ZoneRulesDigest
	oldRules, err := loadScheduleZone(exampleZone)
	if err != nil {
		t.Fatal(err)
	}
	oldSpec, err := parseSchedule(expression, exampleZone, oldRules)
	if err != nil {
		t.Fatal(err)
	}
	oldReplica := startSchedulerChild(t, db, "scheduler")
	t.Setenv("SCHEDULER_TZDATA_ZIP", updatedArchive)
	newReplica := startSchedulerChild(t, db, "scheduler")
	killSchedulerChild(t, oldReplica)

	setCursor := func(at time.Time) {
		t.Helper()
		if _, err := db.ExecContext(ctx, "UPDATE schedules SET next_fire_at = $1 WHERE id = 'rollout'", at); err != nil {
			t.Fatal(err)
		}
	}
	awaitOccurrence := func(at, following time.Time) {
		t.Helper()
		awaitSchedulerState(t, func(ctx context.Context) (bool, error) {
			var count int
			if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM scheduled_runs WHERE tenant_id = 'tenant' AND schedule_id = 'rollout' AND scheduled_at = $1", at).Scan(&count); err != nil {
				return false, err
			}
			state, err := repo.scheduleStatus(ctx, "tenant", "rollout")
			return count == 1 && state.NextFireAt != nil && state.NextFireAt.Equal(following), err
		})
	}
	setCursor(due)
	awaitOccurrence(due, oldSpec.Next(now))
	killSchedulerChild(t, newReplica)

	response = auditHTTPRequest(ctx, app, "PUT", "/schedules/rollout", "", "", `{"version":1,`+definition+`}`)
	if response.Code != 200 {
		t.Fatalf("ordinary replace: %d %s", response.Code, response.Body)
	}
	state, err = repo.scheduleStatus(ctx, "tenant", "rollout")
	if err != nil || state.ZoneRulesDigest != oldDigest || state.Version != 2 {
		t.Fatalf("ordinary update repinned rules: %+v %v", state, err)
	}

	promised := due.Add(30 * time.Second)
	setCursor(promised)
	response = auditHTTPRequest(ctx, app, "PUT", "/schedules/rollout", "", "", `{"version":2,"refresh_time_zone":true,`+definition+`}`)
	if response.Code != 200 {
		t.Fatalf("refresh: %d %s", response.Code, response.Body)
	}
	state, err = repo.scheduleStatus(ctx, "tenant", "rollout")
	if err != nil || state.ZoneRulesDigest == oldDigest || state.Version != 3 ||
		state.NextFireAt == nil || !state.NextFireAt.Equal(promised) {
		t.Fatalf("refresh did not preserve UTC cursor: %+v %v", state, err)
	}
	newRules, err := loadScheduleZone(exampleZone)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := parseSchedule(expression, exampleZone, newRules)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SCHEDULER_TZDATA_ZIP", oldArchive)
	startSchedulerChild(t, db, "scheduler")
	awaitOccurrence(promised, spec.Next(now))
}
