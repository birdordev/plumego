package cloudtest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cloudmanager "with-cloud-manager/app"
	"with-cloud-manager/app/integration/testsupport"
	"with-cloud-manager/app/routers"
	"with-cloud-manager/app/services/cloud"
	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/publiccloud"
)

func TestAcceptancePublicCloudClusterNodePools(t *testing.T) {
	for _, cluster := range publicCloudClusterFixtures() {
		t.Run(cluster.command, func(t *testing.T) {
			t.Parallel()
			testClusterNodePools(t, cluster)
		})
	}
}

type nodePoolReader func(context.Context, provider.Scope, string) ([]provider.ClusterNodePool, error)

func (read nodePoolReader) ReadClusterNodePools(ctx context.Context, scope provider.Scope, id string) ([]provider.ClusterNodePool, error) {
	return read(ctx, scope, id)
}

func testClusterNodePools(t *testing.T, cluster *clusterFixture) {
	t.Helper()
	ctx, db := t.Context(), dbtest.Open(t)
	if err := cloudmanager.MigrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	const actor, reviewer, ref = "fixture-node-pool-inventory-operator", "fixture-node-pool-inventory-reviewer", "fixture/snapshot-delete"
	seedSnapshotDeletion(t, db, &snapshotDeletionFixture{vendor: cluster.vendor, region: cluster.region,
		volume: "fixture-volume", id: "fixture-snapshot", class: "standard"}, cluster.remoteIdentity(), actor, reviewer)
	f := &nodePoolFixture{cluster: cluster}
	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.serve(t, w, r) }))
	t.Cleanup(remote.Close)
	endpoint, err := url.Parse(remote.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := cloudFixtureTransport{endpoint, remote.Client().Transport}
	config := cloud.PublicCloudProfile{Ref: ref, Config: publiccloud.Config{AccountID: cluster.account, Regions: []string{cluster.region}}}
	if cluster.vendor == provider.HuaweiECS {
		config.ProjectID, config.IAMToken = fixtureHuaweiProject, fixtureHuaweiIAM
	} else {
		config.AccessKeyID, config.AccessKeySecret, config.SecurityToken = fixtureLaunchKey, fixtureLaunchSecret, fixtureLaunchSession
	}
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	run := func(args []string) error {
		t.Helper()
		if !cloudmanager.ValidCommand(args) {
			t.Fatalf("CLI did not accept %s", args[0])
		}
		return cloud.RunPublicCloud(ctx, db, args, cluster.vendor, path, transport)
	}
	scan := []string{"scan-" + cluster.command, "tenant", cluster.vendor, cluster.region, string(provider.KubernetesCluster)}
	refresh := func() error {
		return run([]string{"refresh-" + cluster.command + "-cluster-node-pools", "tenant", cluster.vendor, cluster.region, cluster.id})
	}
	if err := refresh(); !errors.Is(err, inventory.ErrStale) {
		t.Fatalf("refresh without a current parent: %v", err)
	}
	if err := run(scan); err != nil {
		t.Fatal(err)
	}
	app, err := routers.NewAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	q := url.Values{"account": {cluster.vendor}, "region": {cluster.region}, "limit": {"1"}}
	get := func(project, query string, want int) inventory.ClusterNodePoolsPage {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/v1/tenants/tenant/projects/"+project+"/assets/kubernetes-clusters/"+cluster.id+"/node-pools?"+q.Encode()+query, nil)
		r.Header.Set("Authorization", "Bearer "+actor)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if w.Code != want || strings.Contains(w.Body.String(), fixtureNodePoolSecret) ||
			w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("GET node pools %s: status=%d want=%d body=%s", project, w.Code, want, w.Body)
		}
		var result inventory.ClusterNodePoolsPage
		if want == http.StatusOK {
			testsupport.DecodeProjectData(t, w, &result)
		}
		return result
	}
	get("project", "", http.StatusNotFound)
	f.mode.Store(poolsWrongCluster)
	wantScope := provider.InvalidResponse
	if cluster.vendor == provider.HuaweiECS {
		wantScope = provider.Unauthorized
	}
	if err := refresh(); !provider.IsFailure(err, wantScope) {
		t.Fatalf("cross-scope node pool snapshot accepted: %v", err)
	}
	get("project", "", http.StatusNotFound)
	f.mode.Store(poolsHealthy)
	if err := refresh(); err != nil {
		t.Fatal(err)
	}
	first := get("project", "", http.StatusOK)
	if first.Snapshot == "" || first.Total != 51 || first.NextOffset != 1 || len(first.Pools) != 1 ||
		first.Pools[0].ID != "np-pool-000" || first.Pools[0].CurrentNodes == nil || *first.Pools[0].CurrentNodes != 0 ||
		first.Pools[0].Autoscaling == nil || *first.Pools[0].Autoscaling {
		t.Fatalf("bad node pool projection: %+v", first)
	}
	get("other", "", http.StatusNotFound)
	get("project", "&offset=1", http.StatusBadRequest)
	get("project", "&account=duplicate", http.StatusBadRequest)
	after := "&offset=1&snapshot=" + url.QueryEscape(first.Snapshot)
	next := get("project", after, http.StatusOK)
	if next.Snapshot != first.Snapshot || len(next.Pools) != 1 || next.Pools[0].ID != "np-pool-001" {
		t.Fatalf("next page did not read the same snapshot: %+v", next)
	}

	if cluster.vendor == provider.TencentCVM {
		native := next.Pools[0]
		if native.Type != "Native" || native.CurrentNodes != nil || native.DesiredNodes == nil || *native.DesiredNodes != 2 ||
			native.MinNodes == nil || *native.MinNodes != 1 || native.Autoscaling == nil || !*native.Autoscaling {
			t.Fatalf("unexpected Native node pool projection: %+v", native)
		}
	}
	var persisted string
	if err := db.QueryRowContext(ctx, "SELECT pools::text FROM cmp_cluster_node_pools").Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(persisted, fixtureNodePoolSecret) {
		t.Fatal("node pool snapshot persisted user data")
	}

	for _, failure := range []struct {
		mode int32
		want provider.FailureKind
	}{{poolsThrottled, provider.Throttled}, {poolsDuplicate, provider.InvalidResponse}} {
		f.mode.Store(failure.mode)
		if err := refresh(); !provider.IsFailure(err, failure.want) {
			t.Fatalf("incomplete snapshot mode %d: %v", failure.mode, err)
		}
		if current := get("project", "", http.StatusOK); current.Snapshot != first.Snapshot {
			t.Fatal("failed refresh replaced the complete snapshot")
		}
	}
	f.mode.Store(poolsHealthy)

	store := inventory.NewStore(db)
	key := inventory.Key{Tenant: "tenant", Account: cluster.vendor, Region: cluster.region, Kind: provider.KubernetesCluster}
	waitForOld := func(action func()) {
		t.Helper()
		started, released := make(chan struct{}), make(chan struct{})
		result := make(chan error, 1)
		go func() {
			reader := nodePoolReader(func(ctx context.Context, _ provider.Scope, _ string) ([]provider.ClusterNodePool, error) {
				close(started)
				select {
				case <-released:
					return []provider.ClusterNodePool{{ID: "np-older", Name: "older"}}, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			})
			result <- store.RefreshClusterNodePools(ctx, key, cluster.id, 1, reader)
		}()
		defer close(released)
		select {
		case <-started:
		case err := <-result:
			t.Fatalf("refresh did not reach the provider: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		action()
		// 使用单值发送放行，defer 仍能在断言失败时唤醒读取。
		select {
		case released <- struct{}{}:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		if err := <-result; !errors.Is(err, inventory.ErrStale) {
			t.Fatalf("late snapshot was accepted: %v", err)
		}
	}
	var scheduleID int64
	if err := db.QueryRowContext(ctx, `INSERT INTO cmp_inventory_schedules (tenant_id,account_id,region,kind,target_id,interval_seconds)
		VALUES ('tenant',$1,$2,'kubernetes-node-pools',$3,300) RETURNING schedule_id`,
		cluster.vendor, cluster.region, cluster.id).Scan(&scheduleID); err != nil {
		t.Fatal(err)
	}
	execute := cloud.InventoryScheduleExecutor(db, map[string]string{ref: path}, transport)
	waitForOld(func() {
		f.changed.Store(true)
		runClusterSchedule(t, db, scheduleID, execute, "success", 0)
	})

	latest := get("project", "", http.StatusOK)
	if latest.Snapshot == first.Snapshot || len(latest.Pools) != 1 || latest.Pools[0].Name != "latest-pool" {
		t.Fatalf("newer node pool snapshot was lost: %+v", latest)
	}
	get("project", after, http.StatusConflict)
	f.mode.Store(poolsThrottled)
	runClusterSchedule(t, db, scheduleID, execute, "throttled", 1)
	if current := get("project", "", http.StatusOK); current.Snapshot != latest.Snapshot {
		t.Fatal("throttled schedule replaced a complete snapshot")
	}
	f.mode.Store(poolsHealthy)

	if _, err := db.ExecContext(ctx, `INSERT INTO cmp_asset_ownership(tenant_id,scope_id,external_id,project_id,revision)
		SELECT tenant_id,scope_id,$1,'other',2 FROM cmp_inventory_scopes WHERE kind='kubernetes-cluster'`, cluster.id); err != nil {
		t.Fatal(err)
	}
	get("project", "", http.StatusNotFound)
	get("other", "", http.StatusOK)
	f.mode.Store(poolsEmpty)
	if err := refresh(); err != nil {
		t.Fatal(err)
	}
	if empty := get("other", "", http.StatusOK); empty.Pools == nil || len(empty.Pools) != 0 || empty.Total != 0 {
		t.Fatalf("successful empty list was not published: %+v", empty)
	}
	waitForOld(func() {
		if _, err := db.ExecContext(ctx, `UPDATE cmp_cloud_accounts
			SET credential_revision=credential_revision+1 WHERE (tenant_id,account_id)=('tenant',$1)`, cluster.vendor); err != nil {
			t.Fatal(err)
		}
	})
	get("other", "", http.StatusNotFound)
}
