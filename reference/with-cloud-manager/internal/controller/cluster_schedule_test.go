package controller

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

func TestAcceptanceClusterSchedulePublication(t *testing.T) {
	for _, kind := range []string{NodePoolScheduleKind, WorkloadScheduleKind, WorkloadWatchScheduleKind} {
		t.Run(kind, func(t *testing.T) {
			testClusterSchedulePublication(t, kind)
		})
	}
}

func testClusterSchedulePublication(t *testing.T, kind string) {
	t.Helper()
	ctx, db := t.Context(), dbtest.Open(t)
	for _, migrate := range []func(context.Context, *sql.DB) error{identity.Migrate, inventory.Migrate, Migrate} {
		if err := migrate(ctx, db); err != nil {
			t.Fatal(err)
		}
	}
	exec := func(query string) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	exec(`CREATE TABLE test_cluster_publications (id text PRIMARY KEY);
		INSERT INTO cmp_tenants VALUES ('tenant');
		INSERT INTO cmp_projects VALUES ('tenant','project');
		INSERT INTO cmp_cloud_accounts (tenant_id,account_id,project_id,provider,credential_ref,remote_identity,mode)
			VALUES ('tenant','account','project','tencent-cvm','profiles/reader','100000000001','read_only');
		INSERT INTO cmp_inventory_schedules (tenant_id,account_id,region,kind,interval_seconds,enabled)
			VALUES ('tenant','account','ap-guangzhou','kubernetes-cluster',300,false);`)
	if _, err := db.ExecContext(ctx, `INSERT INTO cmp_inventory_schedules (tenant_id,account_id,region,kind,target_id,interval_seconds,enabled) VALUES
			('tenant','account','ap-guangzhou',$1,'cls-first',300,true),
			('tenant','account','ap-guangzhou',$1,'cls-second',300,false)`, kind); err != nil {
		t.Fatal(err)
	}
	for _, args := range []struct{ kind, target string }{
		{kind, "cls-first"},
		{kind, ""},
		{kind, "../cls-first"},
		{"kubernetes-cluster", "cls-first"},
	} {
		if _, err := db.ExecContext(ctx, `INSERT INTO cmp_inventory_schedules (tenant_id,account_id,region,kind,target_id,interval_seconds)
			VALUES ('tenant','account','ap-guangzhou',$1,$2,300)`, args.kind, args.target); err == nil {
			t.Fatalf("invalid plan accepted: %+v", args)
		}
	}

	store := NewStore(db)
	pool := inventoryPool
	if kind == WorkloadWatchScheduleKind {
		pool = workloadWatchPool
	}
	step := func(execTask Execute) {
		t.Helper()
		if worked, err := store.step(ctx, execTask, pool); err != nil || !worked {
			t.Fatalf("scheduler step worked=%t err=%v", worked, err)
		}
	}
	publish := func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO test_cluster_publications VALUES ('current')")
		return err
	}
	var replacement job
	step(func(ctx context.Context, task Task) (Publish, error) {
		if task.ScheduleKind != kind || task.Key.Kind != provider.KubernetesCluster || task.TargetID != "cls-first" ||
			task.Provider != provider.TencentCVM || task.CredentialRef != "profiles/reader" || task.CredentialRevision != 1 {
			t.Fatalf("wrong child task: %+v", task)
		}
		exec(`UPDATE cmp_inventory_schedules SET lease_until=clock_timestamp()-interval '1 second' WHERE target_id='cls-first'`)
		var err error
		replacement, err = NewStore(db).claim(ctx, time.Minute, pool)
		if err != nil {
			t.Fatal(err)
		}
		return func(context.Context, *sql.Tx) error {
			t.Error("replaced lease ran its publication")
			return nil
		}, nil
	})
	if err := store.finish(ctx, replacement, "success", time.Minute, 0, publish); err != nil {
		t.Fatalf("current owner could not publish: %v", err)
	}
	check := func(outcome string, failures, published int) {
		t.Helper()
		var state string
		var count, rows int
		var noLease bool
		err := db.QueryRowContext(ctx, `SELECT last_outcome,failures,lease_until IS NULL,
			(SELECT count(*) FROM test_cluster_publications)
			FROM cmp_inventory_schedules WHERE target_id='cls-first'`).Scan(&state, &count, &noLease, &rows)
		if err != nil || !noLease || state != outcome || failures != count || rows != published {
			t.Fatalf("result=%s failures=%d released=%t publications=%d err=%v", state, count, noLease, rows, err)
		}
	}
	check("success", 0, 1)

	const due = `UPDATE cmp_inventory_schedules SET next_at=clock_timestamp() WHERE target_id='cls-first'`
	exec(due)
	step(func(context.Context, Task) (Publish, error) {
		return func(ctx context.Context, tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, "INSERT INTO test_cluster_publications VALUES ('rolled-back')"); err != nil {
				return err
			}
			return inventory.ErrStale
		}, nil
	})
	check("failure", 1, 1)

	exec(due)
	step(func(context.Context, Task) (Publish, error) {
		exec(`UPDATE cmp_inventory_schedules SET enabled=false WHERE target_id='cls-first'`)
		return func(context.Context, *sql.Tx) error {
			t.Error("disabled plan ran its publication")
			return nil
		}, nil
	})
	check("failure", 1, 1)

	exec(`UPDATE cmp_inventory_schedules SET enabled=true WHERE target_id='cls-first'`)
	step(func(context.Context, Task) (Publish, error) {
		exec(`UPDATE cmp_inventory_schedules SET region='ap-beijing' WHERE target_id='cls-first'`)
		return func(context.Context, *sql.Tx) error {
			t.Error("reconfigured plan ran its previous publication")
			return nil
		}, nil
	})
	check("failure", 1, 1)

	ctxStop, stop := context.WithCancel(ctx)
	defer stop()
	if worked, err := store.step(ctxStop, func(context.Context, Task) (Publish, error) {
		stop()
		return func(context.Context, *sql.Tx) error {
			t.Error("shutdown ran its publication")
			return nil
		}, nil
	}, pool); err != nil || !worked {
		t.Fatalf("shutdown step worked=%t err=%v", worked, err)
	}
	check("failure", 1, 1)
	if _, err := store.claim(ctx, time.Minute, pool); errors.Is(err, sql.ErrNoRows) {
		t.Fatal("released plan was unavailable")
	} else if err != nil {
		t.Fatal(err)
	}
}
