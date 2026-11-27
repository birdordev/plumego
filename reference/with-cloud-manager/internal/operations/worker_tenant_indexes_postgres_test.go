package operations

import (
	"errors"
	"testing"

	"with-cloud-manager/internal/dbtest"
)

func TestAcceptanceWorkerTenantIndexesResumeAndRejectConflict(t *testing.T) {
	db := dbtest.Open(t)
	ctx := t.Context()
	exec := func(query string) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	exec(queueSchema)
	exec(`CREATE INDEX cmp_operation_tasks_tenant_ready ON cmp_operation_tasks (tenant_id,topic,available_at) WHERE status='queued';
		CREATE INDEX cmp_operation_tasks_tenant_lease ON cmp_operation_tasks (tenant_id text_pattern_ops,topic,lease_until) WHERE status='leased'`)
	readOIDs := func() (int64, int64) {
		t.Helper()
		var ready, lease int64
		err := db.QueryRowContext(ctx, `SELECT to_regclass('cmp_operation_tasks_tenant_ready')::bigint,
			to_regclass('cmp_operation_tasks_tenant_lease')::bigint`).Scan(&ready, &lease)
		if err != nil {
			t.Fatal(err)
		}
		return ready, lease
	}
	ready, conflicting := readOIDs()
	if err := MigrateWorkerTenantIndexes(ctx, db); !errors.Is(err, ErrWorkerTenantIndexConflict) {
		t.Fatalf("migration accepted a conflicting index definition: %v", err)
	}
	if a, b := readOIDs(); a != ready || b != conflicting {
		t.Fatalf("migration changed existing indexes: %d %d", a, b)
	}
	var recorded bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM cmp_migrations WHERE version=$1)`, WorkerTenantMigration).
		Scan(&recorded); err != nil || recorded {
		t.Fatalf("failed migration was published: recorded=%t error=%v", recorded, err)
	}

	exec(`DROP INDEX cmp_operation_tasks_tenant_lease`)
	if err := MigrateWorkerTenantIndexes(ctx, db); err != nil {
		t.Fatal(err)
	}
	a, b := readOIDs()
	if a != ready || b == conflicting {
		t.Fatalf("migration did not reuse the first successful index: %d %d", a, b)
	}
	if err := MigrateWorkerTenantIndexes(ctx, db); err != nil {
		t.Fatal(err)
	}
	if a2, b2 := readOIDs(); a2 != a || b2 != b {
		t.Fatal("rerunning the completed migration replaced an index")
	}
}
