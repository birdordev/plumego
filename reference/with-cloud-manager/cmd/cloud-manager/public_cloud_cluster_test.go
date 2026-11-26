package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/publiccloud"
)

func TestAcceptancePublicCloudClusterInventory(t *testing.T) {
	for _, f := range publicCloudClusterFixtures() {
		t.Run(f.command, func(t *testing.T) {
			t.Parallel()
			testPublicCloudClusterInventory(t, f)
		})
	}
}

func publicCloudClusterFixtures() []*clusterFixture {
	return []*clusterFixture{
		{vpcCreationFixture: vpcCreationFixture{vendor: provider.TencentCVM, account: "100000000001", region: "ap-guangzhou"},
			command: "tencent", id: "cls-fixture", product: provider.TencentTKE, clusterType: "MANAGED_CLUSTER", limit: 50},
		{vpcCreationFixture: vpcCreationFixture{vendor: provider.AlibabaECS, account: "1000000000000002", region: "cn-beijing"},
			command: "alibaba", id: "c-fixture", product: provider.AlibabaACK, clusterType: "ManagedKubernetes", limit: 20},
		{vpcCreationFixture: vpcCreationFixture{vendor: provider.HuaweiECS, account: "1234567890abcdef1234567890abcdef", region: "cn-north-4"},
			command: "huawei", id: "70000000-0000-4000-8000-000000000019", product: provider.HuaweiCCE, clusterType: "VirtualMachine", limit: 50},
	}
}

func testPublicCloudClusterInventory(t *testing.T, f *clusterFixture) {
	t.Helper()
	ctx, db := t.Context(), dbtest.Open(t)
	if err := migrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	const actor, reviewer = "fixture-cluster-inventory-operator", "fixture-cluster-inventory-reviewer"
	const ref = "fixture/snapshot-delete"
	seedSnapshotDeletion(t, db, &snapshotDeletionFixture{vendor: f.vendor, region: f.region,
		volume: "fixture-volume", id: "fixture-snapshot", class: "standard"}, f.remoteIdentity(), actor, reviewer)
	if _, err := db.ExecContext(ctx, `WITH scope AS (
		INSERT INTO cmp_inventory_scopes(tenant_id,account_id,region,kind,credential_revision,lease_until)
		VALUES ('tenant',$1,$2,'network',1,now()) RETURNING scope_id
	) INSERT INTO cmp_inventory_objects(scope_id,external_id,name,status,seen_generation)
		SELECT scope_id,$3,'fixture VPC','available',1 FROM scope`, f.vendor, f.region, f.network()); err != nil {
		t.Fatal(err)
	}

	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.serve(t, w, r) }))
	t.Cleanup(remote.Close)
	endpoint, err := url.Parse(remote.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := cloudFixtureTransport{endpoint, remote.Client().Transport}
	config := publicCloudProfile{Ref: ref, Config: publiccloud.Config{AccountID: f.account, Regions: []string{f.region}}}
	if f.vendor == provider.HuaweiECS {
		config.ProjectID, config.IAMToken = fixtureHuaweiProject, fixtureHuaweiIAM
	} else {
		config.AccessKeyID, config.AccessKeySecret, config.SecurityToken = fixtureLaunchKey, fixtureLaunchSecret, fixtureLaunchSession
	}
	path := filepath.Join(t.TempDir(), "profile.json")
	writeProfile := func(scopeWide bool) {
		t.Helper()
		config.KubernetesListScopeWide = scopeWide
		data, err := json.Marshal(config)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeProfile(false)

	args := []string{"scan-" + f.command, "tenant", f.vendor, f.region, "kubernetes-cluster"}
	scan := func(ctx context.Context) error { return runPublicCloud(ctx, db, args, f.vendor, path, transport) }
	point := append(slices.Clone(args), f.id)
	if !validCommand(point) {
		t.Fatal("cluster confirmation is inaccessible from CLI")
	}
	confirm := func() error { return runPublicCloud(ctx, db, point, f.vendor, path, transport) }
	f.mode.Store(clusterRateLimit)
	if err := scan(ctx); !provider.IsFailure(err, provider.Throttled) {
		t.Fatalf("expected 429, got %v", err)
	}
	var checkpoint string
	var complete bool
	var persisted int
	if err := db.QueryRowContext(ctx, `SELECT cursor,completed_at IS NOT NULL,
		(SELECT count(*) FROM cmp_inventory_objects AS o WHERE o.scope_id=s.scope_id)
		FROM cmp_inventory_scopes AS s WHERE kind='kubernetes-cluster'`).Scan(&checkpoint, &complete, &persisted); err != nil {
		t.Fatal(err)
	}
	wantCheckpoint, wantPersisted := fmt.Sprintf("0:%d:%d", f.limit, f.limit+1), f.limit
	if f.vendor == provider.HuaweiECS {
		wantCheckpoint, wantPersisted = "", 0
	}
	if checkpoint != wantCheckpoint || complete || persisted != wantPersisted {
		t.Fatalf("wrong checkpoint after 429: cursor=%q complete=%t persisted=%d", checkpoint, complete, persisted)
	}
	f.mode.Store(clusterBadScope)
	failure := provider.InvalidResponse
	if f.vendor == provider.HuaweiECS {
		failure = provider.Unauthorized
	}
	if err := scan(ctx); !provider.IsFailure(err, failure) {
		t.Fatalf("invalid cluster page was accepted: %v", err)
	}
	f.mode.Store(clusterNormal)
	wantFirstReads := int32(1)
	if f.vendor == provider.HuaweiECS {
		wantFirstReads = 3
	}
	if err := scan(ctx); err != nil || f.firstReads.Load() != wantFirstReads {
		t.Fatalf("cluster scan did not honor checkpoint: first reads=%d err=%v", f.firstReads.Load(), err)
	}

	app, err := newAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	get := func(project, path string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/v1/tenants/tenant/projects/"+project+"/assets"+path, nil)
		r.Header.Set("Authorization", "Bearer "+actor)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("query %s status=%d: %s", path, w.Code, w.Body)
		}
		if strings.Contains(w.Body.String(), clusterFixtureSecret) {
			t.Fatalf("project response disclosed cluster connection metadata")
		}
		return w
	}
	const selector = "?kind=kubernetes-cluster"
	const primary = selector + "&q=fixture-cluster-000"
	var assets inventory.Assets
	decodeProjectData(t, get("project", selector+"&q="+url.QueryEscape(f.clusterType)+"&limit=100"), &assets)
	if len(assets.Items) != f.limit+1 || !slices.ContainsFunc(assets.Items, func(asset inventory.Asset) bool {
		return asset.ID == f.id && asset.Cluster != nil && asset.Cluster.Type == f.clusterType && asset.Cluster.NodeCount != nil && *asset.Cluster.NodeCount == 0
	}) {
		t.Fatalf("got %d clusters without the expected projection", len(assets.Items))
	}
	decodeProjectData(t, get("other", selector), &assets)
	if len(assets.Items) != 0 {
		t.Fatal("another project received cluster assets")
	}
	var topology inventory.Topology
	decodeProjectData(t, get("project", "/topology"+primary), &topology)
	if len(topology.Nodes) != 1 || topology.Nodes[0].Asset.Cluster == nil ||
		topology.Nodes[0].Asset.Cluster.Product != f.product || len(topology.Nodes[0].Links) != 1 ||
		topology.Nodes[0].Links[0].ID != f.network() {
		t.Fatalf("missing scoped cluster topology: %+v", topology)
	}
	body, err := json.Marshal(map[string]string{"account": f.vendor, "region": f.region, "kind": "network", "asset_id": f.network()})
	if err != nil {
		t.Fatal(err)
	}
	removeVPC := httptest.NewRequest(http.MethodPost, "/v1/tenants/tenant/projects/project/network-operations/delete", strings.NewReader(string(body)))
	removeVPC.Header.Set("Authorization", "Bearer "+actor)
	removeVPC.Header.Set("Content-Type", "application/json")
	removeVPC.Header.Set("Idempotency-Key", "blocked-by-cluster")
	removeVPC.Header.Set("If-Match", `"1"`)
	blocked := httptest.NewRecorder()
	app.ServeHTTP(blocked, removeVPC)
	if blocked.Code != http.StatusConflict {
		t.Fatalf("VPC deletion ignored a cluster dependency: %d %s", blocked.Code, blocked.Body)
	}
	records, err := csv.NewReader(get("project", "/export"+primary).Body).ReadAll()
	if err != nil || len(records) != 2 || !slices.Equal(records[0][9:], []string{"product", "cluster_type", "kubernetes_version", "node_count"}) ||
		!slices.Equal(records[1][9:], []string{f.product, f.clusterType, "1.30.4", "0"}) {
		t.Fatalf("incorrect cluster CSV: %v %v", records, err)
	}

	f.changed.Store(true)
	runFixtureScheduledScan(t, db, inventory.Key{Tenant: "tenant", Account: f.vendor, Region: f.region, Kind: provider.KubernetesCluster}, f.vendor, ref, scan)
	var history inventory.Changes
	decodeProjectData(t, get("project", "/changes"+selector+"&q=1.31.2"), &history)
	if len(history.Items) != 1 || history.Items[0].Action != "changed" || history.Items[0].PreviousCluster == nil ||
		history.Items[0].PreviousCluster.Version != "1.30.4" || history.Items[0].PreviousCluster.NodeCount == nil || *history.Items[0].PreviousCluster.NodeCount != 0 ||
		history.Items[0].Cluster == nil || history.Items[0].Cluster.Version != "1.31.2" || history.Items[0].Cluster.NodeCount == nil || *history.Items[0].Cluster.NodeCount != 3 {
		t.Fatalf("missing upgrade history: %+v", history.Items)
	}

	f.missing.Store(true)
	markMissing := func() {
		t.Helper()
		if err := scan(ctx); err != nil {
			t.Fatal(err)
		}
	}
	state := func(want string) {
		t.Helper()
		decodeProjectData(t, get("project", primary), &assets)
		if want == "" && len(assets.Items) == 0 {
			return
		}
		if len(assets.Items) != 1 || assets.Items[0].State != want {
			t.Fatalf("cluster state should be %q, got %+v", want, assets.Items)
		}
	}
	markMissing()
	checkBlocked := func(mode int32, want provider.FailureKind) {
		t.Helper()
		f.mode.Store(mode)
		if err := confirm(); !provider.IsFailure(err, want) {
			t.Fatalf("lookup mode %d = %v, want %s", mode, err, want)
		}
		state("missing_candidate")
	}
	checkBlocked(clusterNotFound, provider.OutcomeUnknown)
	checkBlocked(clusterIncomplete, provider.InvalidResponse)
	if f.vendor != provider.HuaweiECS {
		checkBlocked(clusterWrongLookup, provider.InvalidResponse)
	}
	checkBlocked(clusterNormal, provider.OutcomeUnknown)

	f.missing.Store(false)
	if err := confirm(); err != nil {
		t.Fatal(err)
	}
	state("observed")

	f.missing.Store(true)
	markMissing()
	writeProfile(true)
	checkBlocked(clusterForbidden, provider.Unauthorized)
	f.mode.Store(clusterNormal)
	if err := confirm(); err != nil {
		t.Fatal(err)
	}
	state("")

	decodeProjectData(t, get("project", "/changes"+primary), &history)
	if !slices.ContainsFunc(history.Items, func(change inventory.Change) bool {
		return change.AssetID == f.id && change.Action == "removed"
	}) {
		t.Fatal("precise absence did not record cluster removal")
	}
}
