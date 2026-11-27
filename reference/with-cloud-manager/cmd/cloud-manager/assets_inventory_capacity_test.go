package main

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	"with-cloud-manager/internal/controller"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

func TestCapacityProjectAssetsWithInventoryWrites(t *testing.T) {
	requireAssetCapacity(t)
	server, cases, db := startAssetCapacity(t)
	seedAssetCapacityIsolation(t, db)
	cases = append(cases,
		assetCapacityCase{name: "shared", query: "&account=account-48", count: 50, wantAccount: "account-48"},
		assetCapacityCase{name: "private", query: "&account=account-49"},
	)
	for _, sample := range cases {
		if err := readCapacityAssets(t.Context(), server.Client(), server.URL, sample); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now().Add(time.Second)
	_, err := db.ExecContext(t.Context(), `INSERT INTO cmp_inventory_schedules
			(tenant_id,account_id,region,kind,next_at,interval_seconds)
		SELECT tenant_id,account_id,region,kind,
			$1::timestamptz+(row_number() OVER (ORDER BY region,account_id,tenant_id)-1)*interval '125 milliseconds',60
		FROM cmp_inventory_scopes`, start)
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(t.Context())
	const controllers = 4
	errs := make(chan error, controllers)
	var workers sync.WaitGroup
	defer func() {
		stop()
		workers.Wait()
	}()
	for range controllers {
		workers.Go(func() {
			errs <- controller.NewStore(db).Run(ctx, assetCapacityScan(inventory.NewStore(db)))
		})
	}
	runAssetCapacityRate(t, server, cases, start)
	stop()
	workers.Wait()
	for range controllers {
		if err := <-errs; err != nil {
			t.Errorf("inventory controller: %v", err)
		}
	}
	checkAssetCapacityInventory(t, db, start.Add(assetCapacityWindow))
}

func seedAssetCapacityIsolation(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.ExecContext(t.Context(), `INSERT INTO cmp_tenants VALUES ('outside');
		INSERT INTO cmp_projects VALUES ('bench','shared'),('bench','private'),('outside','outside');
		UPDATE cmp_cloud_accounts SET project_id='shared' WHERE account_id IN
			('account-41','account-42','account-43','account-44','account-45','account-46','account-47','account-48');
		UPDATE cmp_cloud_accounts SET project_id='private' WHERE account_id='account-49';
		INSERT INTO cmp_cloud_account_grants (tenant_id,account_id,project_id)
			SELECT tenant_id,account_id,'ops' FROM cmp_cloud_accounts WHERE project_id='shared';
		INSERT INTO cmp_cloud_accounts (tenant_id,account_id,project_id,provider,remote_identity,credential_ref,mode)
			VALUES ('outside','account-50','outside','tencent-cvm','outside-50','fixture-profile','read_only');
		INSERT INTO cmp_inventory_scopes (tenant_id,account_id,region,kind,credential_revision,running,lease_until)
			SELECT 'outside',account_id,region,kind,1,false,now() FROM cmp_inventory_scopes WHERE account_id='account-50';
		INSERT INTO cmp_inventory_objects (scope_id,external_id,name,status,seen_generation)
			SELECT scope_id,'i-'||lpad(g::text,5,'0'),'instance '||g,'running',1
			FROM cmp_inventory_scopes CROSS JOIN generate_series(1,500) AS g WHERE tenant_id='outside';
		UPDATE cmp_inventory_objects AS o SET name='end-of-fleet needle'
		FROM cmp_inventory_scopes AS s WHERE s.scope_id=o.scope_id AND s.region='region-4' AND o.external_id='i-00500'
			AND (s.tenant_id='outside' OR s.account_id='account-49');
		ANALYZE cmp_inventory_scopes;
		ANALYZE cmp_cloud_accounts;
		ANALYZE cmp_cloud_account_grants;
		ANALYZE cmp_inventory_objects;`)
	if err != nil {
		t.Fatal(err)
	}
}

func assetCapacityScan(store inventory.Store) controller.Execute {
	return func(ctx context.Context, task controller.Task) (controller.Publish, error) {
		claim, err := store.ClaimScheduled(ctx, task.Key, 10*time.Minute, task.CredentialRevision, task.Lease)
		if err != nil {
			return nil, err
		}
		marker := task.Key.Region == "region-4" && (task.Key.Account == "account-50" || task.Key.Account == "account-49")
		return nil, scanInventoryPages(ctx, store, claim, assetCapacityDiscovery{marker: marker})
	}
}

type assetCapacityDiscovery struct{ marker bool }

func (d assetCapacityDiscovery) Discover(ctx context.Context, _ provider.Scope, cursor string) (provider.Page, error) {
	timer := time.NewTimer(50 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return provider.Page{}, ctx.Err()
	case <-timer.C:
	}
	first, next := 1, "second"
	switch cursor {
	case "":
	case "second":
		first, next = 251, ""
	default:
		return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	page := provider.Page{Objects: make([]provider.Object, 250), Next: next}
	for i := range page.Objects {
		n := first + i
		item := provider.Object{ID: fmt.Sprintf("i-%05d", n), Name: fmt.Sprintf("instance %d", n), Status: "running"}
		if n%5 == 0 {
			item.Status = "stopped"
		}
		if d.marker && n == 500 {
			item.Name = "end-of-fleet needle"
		}
		page.Objects[i] = item
	}
	return page, nil
}

func checkAssetCapacityInventory(t *testing.T, db *sql.DB, end time.Time) {
	t.Helper()
	var total, scheduled, completed, failed int
	err := db.QueryRowContext(t.Context(), `SELECT count(*),
		count(*) FILTER (WHERE p.last_outcome='success' AND p.last_finished_at<=$1),
		count(*) FILTER (WHERE s.completed_at<=$1 AND s.completed_credential_revision=s.credential_revision
			AND NOT s.running AND s.pages=1 AND o.total=500 AND o.changed=100),
		count(*) FILTER (WHERE p.failures<>0)
		FROM cmp_inventory_schedules AS p JOIN cmp_inventory_scopes AS s USING (tenant_id,account_id,region,kind)
		CROSS JOIN LATERAL (SELECT count(*) AS total,count(*) FILTER (WHERE status='stopped') AS changed
			FROM cmp_inventory_objects WHERE scope_id=s.scope_id AND removed_at IS NULL AND seen_generation=s.generation) AS o`, end).
		Scan(&total, &scheduled, &completed, &failed)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("inventory scopes=%d scheduled=%d verified_complete=%d failed=%d", total, scheduled, completed, failed)
	if total != 204 || failed != 0 || scheduled*100 < 95*total || scheduled != completed {
		t.Errorf("expected at least 95%% of %d inventory scopes to finish within the read window and persist both pages", total)
	}
}
