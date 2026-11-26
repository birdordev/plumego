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

func TestAcceptanceIdleScheduleFencing(t *testing.T) {
	db, ctx := dbtest.Open(t), t.Context()
	for _, migrate := range []func(context.Context, *sql.DB) error{identity.Migrate, inventory.Migrate, Migrate} {
		if err := migrate(ctx, db); err != nil {
			t.Fatal(err)
		}
	}
	exec := func(sqlText string) {
		t.Helper()
		if _, err := db.ExecContext(ctx, sqlText); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO cmp_tenants VALUES ('t');
		INSERT INTO cmp_projects VALUES ('t','p');
		INSERT INTO cmp_cloud_accounts(tenant_id,account_id,project_id,provider,remote_identity,credential_ref,mode)
			VALUES ('t','a','p','tencent-cvm','123','fixture','read_only');
		INSERT INTO cmp_inventory_scopes(tenant_id,account_id,region,kind,credential_revision,lease_until)
			VALUES ('t','a','ap-guangzhou','instance',1,now());
		INSERT INTO cmp_inventory_objects(scope_id,external_id,name,status,seen_generation)
			SELECT scope_id,'ins-one','one','running',1 FROM cmp_inventory_scopes;
		CREATE TABLE fixture_idle_publish (id integer NOT NULL);`)
	store := NewStore(db)
	key := inventory.Key{Tenant: "t", Account: "a", Region: "ap-guangzhou", Kind: provider.Instance}
	if err := store.ConfigureIdle(ctx, key, "ins-one", 24*time.Hour); err != nil {
		t.Fatal(err)
	}
	for _, pool := range []schedulePool{inventoryPool, workloadWatchPool} {
		if _, err := store.claim(ctx, time.Minute, pool); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("non-idle pool claimed the idle job: %v", err)
		}
	}
	first, err := store.claim(ctx, time.Minute, idleAnalysisPool)
	if err != nil || first.task.TargetID != "ins-one" || first.task.Key != key {
		t.Fatalf("idle scope not bound: %+v %v", first, err)
	}
	if err := store.ConfigureIdle(ctx, key, "ins-one", time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := store.claim(ctx, time.Minute, idleAnalysisPool); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("rescheduling invalidated a live lease: %v", err)
	}
	exec("UPDATE cmp_inventory_schedules SET lease_until=clock_timestamp()-interval '1 second'")
	second, err := store.claim(ctx, time.Minute, idleAnalysisPool)
	if err != nil || second.fence <= first.fence {
		t.Fatalf("expired lease was not fenced: %+v %v", second, err)
	}
	publish := func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO fixture_idle_publish VALUES (1)")
		return err
	}
	if err := store.finish(ctx, first, "success", time.Hour, 0, publish); !errors.Is(err, ErrStale) {
		t.Fatalf("replaced lease could publish: %v", err)
	}
	if err := store.ConfigureIdle(ctx, key, "ins-one", 0); err != nil {
		t.Fatal(err)
	}
	if err := store.finish(ctx, second, "success", time.Hour, 0, publish); !errors.Is(err, ErrStale) {
		t.Fatalf("disabled schedule could publish: %v", err)
	}
	var published int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM fixture_idle_publish").Scan(&published); err != nil || published != 0 {
		t.Fatalf("stale publication count=%d error=%v", published, err)
	}
}
