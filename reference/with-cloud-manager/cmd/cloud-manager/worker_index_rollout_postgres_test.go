package main

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/operations"
)

func TestAcceptanceWorkerTenantIndexesDuringWrites(t *testing.T) {
	db := dbtest.Open(t)
	if err := migrateCloudManagerSchema(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	target := priorityRolloutTargets()[0]
	fixture := target.fixture()
	seedPowerDrillData(t, db, fixture, target.parked)
	app := http.TimeoutHandler(pitrAPI(t, db), 5*time.Second, "request timed out")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture.serve(t, w, r)
	}))
	defer server.Close()
	endpoint, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	profile := pitrWorkerProfile(t, fixture)
	stopWorker := startPITRWorker(t, db, profile, "dispatch", cloudFixtureTransport{endpoint, server.Client().Transport})
	defer stopWorker()

	blocker, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback() }()
	if _, err := blocker.ExecContext(t.Context(), `LOCK TABLE cmp_operation_tasks IN ROW EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	ctx, interrupt := context.WithTimeout(t.Context(), 20*time.Second)
	defer interrupt()
	first := startTenantWorkerIndexMigration(ctx, db)
	awaitTenantWorkerIndexBuild(t, ctx, db, first)
	assertTenantWorkerIndexVersion(t, db, 0, 0)
	if err := runWorker(t.Context(), db, "", "observe-only", "tenant"); err == nil ||
		err.Error() != "tenant worker indexes are unavailable; run cloud-manager migrate" {
		t.Fatalf("dedicated worker started during index migration: %v", err)
	}
	accepted := submitPITRPower(t, app, fixture, "start", "online-index-start")
	waitPITRDone(t, db, accepted.ID)
	if got := fixture.writes.Load(); got != 1 {
		t.Fatalf("cloud fixture received %d dispatches during index migration", got)
	}
	interrupt()
	if err := <-first; err == nil {
		t.Fatal("interrupted build was reported successful")
	}
	assertTenantWorkerIndexVersion(t, db, 0, 0)
	var invalidOID int64
	if err := db.QueryRowContext(t.Context(), `SELECT indexrelid::bigint FROM pg_index
		WHERE indexrelid=to_regclass('cmp_operation_tasks_tenant_ready') AND NOT indisvalid`).Scan(&invalidOID); err != nil {
		t.Fatalf("expected an invalid index from the interrupted build: %v", err)
	}
	if err := blocker.Rollback(); err != nil {
		t.Fatal(err)
	}

	restart, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	a, b := startTenantWorkerIndexMigration(restart, db), startTenantWorkerIndexMigration(restart, db)
	if err := <-a; err != nil {
		t.Fatal(err)
	}
	if err := <-b; err != nil {
		t.Fatal(err)
	}
	assertTenantWorkerIndexVersion(t, db, 1, 2)
	var readyOID int64
	if err := db.QueryRowContext(t.Context(), `SELECT to_regclass('cmp_operation_tasks_tenant_ready')::bigint`).Scan(&readyOID); err != nil || readyOID == invalidOID {
		t.Fatalf("the interrupted index was not replaced: %d, %v", readyOID, err)
	}
	if replay := submitPITRPower(t, app, fixture, "start", "online-index-start"); replay.ID != accepted.ID || fixture.writes.Load() != 1 {
		t.Fatalf("original operation did not survive migration: %+v", replay)
	}
}

func startTenantWorkerIndexMigration(ctx context.Context, db *sql.DB) <-chan error {
	done := make(chan error, 1)
	go func() { done <- operations.MigrateWorkerTenantIndexes(ctx, db) }()
	return done
}

func awaitTenantWorkerIndexBuild(t *testing.T, ctx context.Context, db *sql.DB, done <-chan error) {
	t.Helper()
	ticks := time.NewTicker(25 * time.Millisecond)
	defer ticks.Stop()
	for {
		var waiting bool
		if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_progress_create_index
			WHERE relid=to_regclass('cmp_operation_tasks') AND phase='waiting for writers before build')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		select {
		case err := <-done:
			t.Fatalf("index migration completed while the first writer held the table: %v", err)
		case <-ctx.Done():
			t.Fatalf("index migration did not enter concurrent build: %v", ctx.Err())
		case <-ticks.C:
		}
	}
}

func assertTenantWorkerIndexVersion(t *testing.T, db *sql.DB, versions, indexes int) {
	t.Helper()
	var recorded, valid int
	err := db.QueryRowContext(t.Context(), `SELECT
		(SELECT count(*) FROM cmp_migrations WHERE version=$1),
		(SELECT count(*) FROM pg_index WHERE indexrelid IN (to_regclass('cmp_operation_tasks_tenant_ready'),
			to_regclass('cmp_operation_tasks_tenant_lease')) AND indisvalid AND indisready AND indislive)`,
		operations.WorkerTenantMigration).Scan(&recorded, &valid)
	if err != nil || recorded != versions || valid != indexes {
		t.Fatalf("index migration state versions=%d indexes=%d: %v", recorded, valid, err)
	}
}
