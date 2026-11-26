package main

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"with-cloud-manager/internal/controller"
)

func runClusterSchedule(t *testing.T, db *sql.DB, id int64, execute controller.Execute, want string, failures int) {
	t.Helper()
	runClusterScheduleWith(t, db, id, execute, want, failures, controller.NewStore(db).Run)
}

func runClusterScheduleWith(t *testing.T, db *sql.DB, id int64, execute controller.Execute, want string, failures int, run func(context.Context, controller.Execute) error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if _, err := db.ExecContext(ctx, `UPDATE cmp_inventory_schedules
		SET next_at=clock_timestamp(),last_finished_at=NULL WHERE schedule_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- run(ctx, execute) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		var outcome sql.NullString
		var retries int
		var released bool
		err := db.QueryRowContext(ctx, `SELECT CASE WHEN last_finished_at IS NOT NULL THEN last_outcome END, failures, lease_until IS NULL
			FROM cmp_inventory_schedules WHERE schedule_id=$1`, id).Scan(&outcome, &retries, &released)
		if err != nil {
			t.Fatal(err)
		}
		if outcome.Valid {
			if outcome.String != want || retries != failures || !released {
				t.Fatalf("schedule outcome=%s count=%d released=%t", outcome.String, retries, released)
			}
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("cluster schedule did not finish")
		case <-ticker.C:
		}
	}
}
