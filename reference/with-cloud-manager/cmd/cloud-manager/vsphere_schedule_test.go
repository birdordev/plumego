package main

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"with-cloud-manager/internal/controller"
	"with-cloud-manager/internal/inventory"
)

func runVSphereScheduledScan(t *testing.T, db *sql.DB, key inventory.Key, path string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 7*time.Second)
	defer cancel()
	var id int64
	err := db.QueryRowContext(ctx, `INSERT INTO cmp_inventory_schedules(tenant_id,account_id,region,kind,interval_seconds)
		VALUES ($1,$2,$3,$4,300) RETURNING schedule_id`, key.Tenant, key.Account, key.Region, key.Kind).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- controller.NewStore(db).Run(ctx, inventoryScheduleExecutor(db, map[string]string{vSphereFixtureRef: path}, nil))
	}()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		var outcome sql.NullString
		if err := db.QueryRowContext(ctx, `SELECT last_outcome FROM cmp_inventory_schedules WHERE schedule_id=$1`, id).Scan(&outcome); err != nil {
			t.Fatal(err)
		}
		if outcome.Valid {
			if outcome.String != "success" {
				t.Fatalf("vSphere schedule result %s", outcome.String)
			}
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("vSphere scheduled scan did not complete")
		case <-tick.C:
		}
	}
}
