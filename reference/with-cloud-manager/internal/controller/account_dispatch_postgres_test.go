package controller

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/provider"
)

func TestAcceptanceInventoryAccountDispatch(t *testing.T) {
	db, store := prepareAccountDispatch(t, 3)
	ctx := t.Context()
	first := claimAccountFixture(t, store)
	second := claimAccountFixture(t, NewStore(db))
	third := claimAccountFixture(t, NewStore(db))
	if first.task.Key.Account == second.task.Key.Account || second.task.Key.Account == third.task.Key.Account ||
		first.task.Key.Account == third.task.Key.Account {
		t.Fatal("distinct replicas shared the same account slot")
	}
	requireNoAccountClaim(t, store)

	dispatchExec(t, db, `UPDATE cmp_inventory_schedules SET fence=fence+1,lease_until=NULL WHERE schedule_id=$1`, first.id)
	requireNoAccountClaim(t, store)
	if err := store.finish(ctx, first, "throttled", time.Minute, 1, nil); !errors.Is(err, ErrStale) {
		t.Fatalf("superseded schedule completed: %v", err)
	}
	if err := store.release(ctx, first, time.Minute); err != nil {
		t.Fatal(err)
	}
	requireNoAccountClaim(t, store)
	if err := store.finish(ctx, second, "success", time.Minute, 0, nil); err != nil {
		t.Fatal(err)
	}
	outside := claimAccountFixture(t, store)
	if outside.task.Key.Account != second.task.Key.Account || outside.id == second.id {
		t.Fatalf("another account was blocked by cooldown: %+v", outside.task.Key)
	}

	dispatchExec(t, db, `UPDATE cmp_inventory_account_dispatch SET cooldown_until=clock_timestamp()-interval '1 second'
		WHERE account_id=$1`, first.task.Key.Account)
	recovered := claimAccountFixture(t, store)
	if recovered.task.Key.Account != first.task.Key.Account {
		t.Fatalf("cooled account did not resume: %+v", recovered.task.Key)
	}
	if err := store.release(ctx, first, time.Hour); err != nil {
		t.Fatal(err)
	}
	requireNoAccountClaim(t, store)
	var fence int64
	if err := db.QueryRowContext(ctx, `SELECT schedule_fence FROM cmp_inventory_account_dispatch
		WHERE (tenant_id,account_id)=($1,$2)`, recovered.task.Key.Tenant, recovered.task.Key.Account).Scan(&fence); err != nil || fence != recovered.fence {
		t.Fatalf("old worker disturbed the live lease: version=%d error=%v", fence, err)
	}
}

func TestAcceptanceInventoryAccountFairness(t *testing.T) {
	_, store := prepareAccountDispatch(t, 3)
	ctx := t.Context()
	first := claimAccountFixture(t, store)
	if first.task.Key.Account != "account-01" {
		t.Fatalf("first claim did not pick the oldest due plan: %s", first.task.Key.Account)
	}
	if err := store.finish(ctx, first, "success", time.Minute, 0, nil); err != nil {
		t.Fatal(err)
	}
	second := claimAccountFixture(t, store)
	if second.task.Key.Tenant == first.task.Key.Tenant {
		t.Fatalf("overdue plans starved an unserved tenant: %+v", second.task.Key)
	}
	if err := store.finish(ctx, second, "success", time.Minute, 0, nil); err != nil {
		t.Fatal(err)
	}
	third := claimAccountFixture(t, store)
	if third.task.Key.Tenant != first.task.Key.Tenant || third.task.Key.Account != "account-02" {
		t.Fatalf("least recently served tenant and account did not resume: %+v", third.task.Key)
	}
	if err := store.finish(ctx, third, "success", time.Minute, 0, nil); err != nil {
		t.Fatal(err)
	}
	fourth := claimAccountFixture(t, store)
	if fourth.task.Key.Tenant != second.task.Key.Tenant {
		t.Fatalf("least recently served tenant did not resume: %+v", fourth.task.Key)
	}
}

func TestAcceptanceInventoryDispatchTenantBurst(t *testing.T) {
	db, store := prepareDispatchFixture(t, 50, 50)
	ctx := t.Context()
	for round := range 5 {
		hot := claimAccountFixture(t, store)
		cold := claimAccountFixture(t, NewStore(db))
		if hot.task.Key.Tenant != "tenant" || cold.task.Key.Tenant != "second" {
			t.Fatalf("round %d allowed the 49-account tenant to delay the one-account tenant: %+v, %+v",
				round, hot.task.Key, cold.task.Key)
		}
		for _, item := range []job{hot, cold} {
			if err := store.finish(ctx, item, "success", time.Minute, 0, nil); err != nil {
				t.Fatal(err)
			}
		}
	}
	busy, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = busy.Rollback() }()
	if _, err := busy.ExecContext(ctx, "SELECT tenant_id FROM cmp_tenants WHERE tenant_id='tenant' FOR NO KEY UPDATE"); err != nil {
		t.Fatal(err)
	}
	if next := claimAccountFixture(t, store); next.task.Key.Tenant != "second" {
		t.Fatalf("a tenant lock blocked another tenant: %+v", next.task.Key)
	}
}

func TestAcceptanceInventoryDispatchFiftyAccounts(t *testing.T) {
	db, store := prepareDispatchFixture(t, 50, 50)
	db.SetMaxOpenConns(14)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	start := time.Now()
	results := make(chan job, 1000)
	failures := make(chan error, 12)
	var replicas sync.WaitGroup
	for range 12 {
		replicas.Go(func() {
			replica := NewStore(db)
			for {
				j, err := replica.claim(ctx, time.Minute, inventoryPool)
				if errors.Is(err, sql.ErrNoRows) {
					break
				}
				if err != nil {
					failures <- err
					break
				}
				results <- j
			}
		})
	}
	replicas.Wait()
	close(results)
	close(failures)
	for err := range failures {
		t.Fatalf("inventory claimant failed: %v", err)
	}
	claimed := make(map[inventoryAccountKey]bool, 50)
	for result := range results {
		key := inventoryAccountKey{result.task.Key.Tenant, result.task.Key.Account}
		if claimed[key] {
			t.Fatalf("account was leased twice: %+v", key)
		}
		claimed[key] = true
	}
	if len(claimed) != 50 {
		t.Fatalf("only %d independent accounts claimed", len(claimed))
	}
	requireNoAccountClaim(t, store)
	t.Logf("12 replicas claimed 50 independent accounts from 1,000 due schedules in %s", time.Since(start))
}

type inventoryAccountKey struct{ tenant, account string }

func prepareAccountDispatch(t *testing.T, accounts int) (*sql.DB, Store) {
	t.Helper()
	return prepareDispatchFixture(t, accounts, 3)
}

func prepareDispatchFixture(t *testing.T, accounts, otherEvery int) (*sql.DB, Store) {
	t.Helper()
	db, ctx := dbtest.Open(t), t.Context()
	for _, migrate := range []func(context.Context, *sql.DB) error{identity.Migrate, Migrate} {
		if err := migrate(ctx, db); err != nil {
			t.Fatal(err)
		}
	}
	dispatchExec(t, db, `INSERT INTO cmp_tenants VALUES ('tenant'),('second');
		INSERT INTO cmp_projects VALUES ('tenant','project'),('second','project');`)
	dispatchExec(t, db, `INSERT INTO cmp_cloud_accounts (tenant_id,account_id,project_id,provider,credential_ref,remote_identity,mode)
		SELECT CASE WHEN n%$2=0 THEN 'second' ELSE 'tenant' END,'account-'||lpad(n::text,2,'0'),'project',
		       (ARRAY['tencent-cvm','alibaba-ecs','huawei-ecs'])[n%3+1],'fixture/reference','fixture:'||n,'read_only'
		FROM generate_series(1,$1) AS n`, accounts, otherEvery)
	dispatchExec(t, db, `INSERT INTO cmp_inventory_schedules(tenant_id,account_id,region,kind,interval_seconds,next_at)
			SELECT tenant_id,account_id,'region-'||r,k,600,statement_timestamp()-interval '2 hours'
		       + (right(account_id,2)::int*100+r)*interval '1 second'
		FROM cmp_cloud_accounts CROSS JOIN generate_series(1,4) AS r
		CROSS JOIN (VALUES ('instance'),('volume'),('image'),('network'),('subnet')) AS kinds(k)`)
	return db, NewStore(db)
}

func claimAccountFixture(t *testing.T, store Store) job {
	t.Helper()
	item, err := store.claim(t.Context(), time.Minute, inventoryPool)
	if err != nil {
		t.Fatalf("inventory claim failed: %v", err)
	}
	return item
}

func requireNoAccountClaim(t *testing.T, store Store) {
	t.Helper()
	if item, err := store.claim(t.Context(), time.Minute, inventoryPool); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("account gate yielded an unexpected job: %+v %v", item.task.Key, err)
	}
}

func dispatchExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatal(err)
	}
}

func TestAcceptanceInventoryAccountThrottle(t *testing.T) {
	_, store := prepareAccountDispatch(t, 3)
	var throttled string
	called, err := store.step(t.Context(), func(_ context.Context, task Task) (Publish, error) {
		throttled = task.Key.Account
		return nil, &provider.Failure{Kind: provider.Throttled, RetryAfter: time.Minute}
	}, inventoryPool)
	if !called || err != nil || throttled == "" {
		t.Fatalf("account throttle was not recorded: called=%t error=%v", called, err)
	}
	for range 2 {
		if result := claimAccountFixture(t, store); result.task.Key.Account == throttled {
			t.Fatalf("sibling plan ignored its account cooldown: %+v", result.task.Key)
		}
	}
	requireNoAccountClaim(t, store)
}

func TestAcceptanceInventoryDispatchTenantBoundary(t *testing.T) {
	db, store := prepareAccountDispatch(t, 1)
	dispatchExec(t, db, `INSERT INTO cmp_cloud_accounts (tenant_id,account_id,project_id,provider,credential_ref,remote_identity,mode)
		VALUES ('second','account-01','project','tencent-cvm','fixture/reference','fixture:second','read_only');
		INSERT INTO cmp_inventory_schedules (tenant_id,account_id,region,kind,interval_seconds)
		VALUES ('second','account-01','region-1','instance',600)`)
	first := claimAccountFixture(t, store)
	second := claimAccountFixture(t, store)
	if first.task.Key.Account != second.task.Key.Account || first.task.Key.Tenant == second.task.Key.Tenant {
		t.Fatalf("same-named accounts crossed tenant boundary: %+v, %+v", first.task.Key, second.task.Key)
	}
	requireNoAccountClaim(t, store)
}
