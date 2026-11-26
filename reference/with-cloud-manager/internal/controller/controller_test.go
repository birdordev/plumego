package controller

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"with-cloud-manager/internal/dbschema"
	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

type discoveryFunc func(context.Context, provider.Scope, string) (provider.Page, error)

func (f discoveryFunc) Discover(ctx context.Context, scope provider.Scope, cursor string) (provider.Page, error) {
	return f(ctx, scope, cursor)
}

func TestAcceptanceDurableInventorySchedule(t *testing.T) {
	db := dbtest.Open(t)
	ctx := t.Context()
	for _, migrate := range []func(context.Context, *sql.DB) error{identity.Migrate, inventory.Migrate} {
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
	if err := dbschema.Apply(ctx, db, "0004_inventory_schedules", schema); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO cmp_tenants VALUES ('tenant');
		INSERT INTO cmp_projects VALUES ('tenant','project');
		INSERT INTO cmp_cloud_accounts (tenant_id,account_id,project_id,provider,credential_ref,remote_identity,mode)
			VALUES ('tenant','account','project','openstack','profiles/reader','site:project','read_only');
		INSERT INTO cmp_inventory_schedules (tenant_id,account_id,region,kind,interval_seconds)
			VALUES ('tenant','account','RegionOne','instance',600);`)
	for range 2 {
		if err := Migrate(ctx, db); err != nil {
			t.Fatal(err)
		}
	}
	store := NewStore(db)
	lock, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Rollback() }()
	if _, err := lock.ExecContext(ctx, "SELECT schedule_id FROM cmp_inventory_schedules FOR UPDATE"); err != nil {
		t.Fatal(err)
	}
	bounded, stop := context.WithTimeout(ctx, time.Second)
	_, busy := store.claim(bounded, time.Minute, inventoryPool)
	stop()
	if !errors.Is(busy, sql.ErrNoRows) {
		t.Fatalf("locked row was not skipped: %v", busy)
	}
	if err := lock.Rollback(); err != nil {
		t.Fatal(err)
	}
	first, err := store.claim(ctx, time.Minute, inventoryPool)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(db).claim(ctx, time.Minute, inventoryPool); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("duplicate active claim: %v", err)
	}
	exec("UPDATE cmp_inventory_schedules SET lease_until=clock_timestamp()-interval '1 second'")
	second, err := NewStore(db).claim(ctx, time.Minute, inventoryPool)
	if err != nil || second.id != first.id || second.fence <= first.fence {
		t.Fatalf("failed to recover expired lease: %+v %v", second, err)
	}
	if err := store.finish(ctx, first, "success", time.Minute, 0, nil); !errors.Is(err, ErrStale) {
		t.Fatalf("old owner finished: %v", err)
	}
	if err := store.release(ctx, second); err != nil {
		t.Fatal(err)
	}

	check := func(outcome string, failures int, minimum time.Duration) {
		t.Helper()
		var recorded string
		var count int
		var seconds float64
		var yielded bool
		err := db.QueryRowContext(ctx, `SELECT last_outcome,failures,
			EXTRACT(epoch FROM (next_at-last_finished_at)),lease_until IS NULL FROM cmp_inventory_schedules`).
			Scan(&recorded, &count, &seconds, &yielded)
		if err != nil || recorded != outcome || count != failures || seconds+0.01 < minimum.Seconds() || !yielded {
			t.Fatalf("outcome=%s failures=%d delay=%f released=%t err=%v", recorded, count, seconds, yielded, err)
		}
	}
	worked, err := store.step(ctx, func(_ context.Context, task Task) (Publish, error) {
		key := task.Key
		if key.Tenant != "tenant" || key.Account != "account" || key.Region != "RegionOne" ||
			key.Kind != provider.Instance || task.Provider != "openstack" || task.CredentialRef != "profiles/reader" || task.TargetID != "" {
			t.Errorf("unexpected scan dispatch: %+v %s", key, task.Provider)
		}
		return nil, &provider.Failure{Kind: provider.Throttled, RetryAfter: 90 * time.Second}
	}, inventoryPool)
	if !worked || err != nil {
		t.Fatalf("throttled scan: %t %v", worked, err)
	}
	check("throttled", 1, 90*time.Second)
	if worked, err := store.step(ctx, func(context.Context, Task) (Publish, error) {
		t.Error("scheduled retry ran early")
		return nil, nil
	}, inventoryPool); worked || err != nil {
		t.Fatalf("early retry: %t %v", worked, err)
	}
	exec("UPDATE cmp_inventory_schedules SET next_at=clock_timestamp()")
	worked, err = store.step(ctx, func(ctx context.Context, task Task) (Publish, error) {
		inv := inventory.NewStore(db)
		claim, err := inv.Claim(ctx, task.Key, time.Minute)
		if err != nil {
			return nil, err
		}
		_, done, err := inv.ScanPage(ctx, claim, discoveryFunc(func(context.Context, provider.Scope, string) (provider.Page, error) {
			return provider.Page{Objects: []provider.Object{{ID: "fixture-instance"}}}, nil
		}))
		if !done {
			t.Error("inventory scan did not complete")
		}
		return nil, err
	}, inventoryPool)
	if !worked || err != nil {
		t.Fatalf("scheduled inventory scan: %t %v", worked, err)
	}
	check("success", 0, 10*time.Minute)
	var object string
	if err := db.QueryRowContext(ctx, "SELECT external_id FROM cmp_inventory_objects").Scan(&object); err != nil || object != "fixture-instance" {
		t.Fatalf("scheduled asset not persisted: %q %v", object, err)
	}
	exec("UPDATE cmp_inventory_schedules SET next_at=clock_timestamp()")
	attempt, cancel := context.WithCancel(ctx)
	defer cancel()
	if worked, err := store.step(attempt, func(context.Context, Task) (Publish, error) {
		cancel()
		return nil, context.Canceled
	}, inventoryPool); !worked || err != nil {
		t.Fatalf("shutdown release: %t %v", worked, err)
	}
	if _, err := store.claim(ctx, time.Minute, inventoryPool); err != nil {
		t.Fatalf("shutdown retained lease: %v", err)
	}
}
