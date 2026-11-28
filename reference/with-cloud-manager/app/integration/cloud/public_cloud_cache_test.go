package cloudtest

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
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

func TestAcceptancePublicCloudCacheInventory(t *testing.T) {
	for _, f := range []*cacheFixture{
		{vpcCreationFixture: vpcCreationFixture{vendor: provider.TencentCVM, account: "100000000001", region: "ap-guangzhou"},
			command: "tencent", id: "crs-fixture", product: provider.TencentRedis},
		{vpcCreationFixture: vpcCreationFixture{vendor: provider.AlibabaECS, account: "1000000000000002", region: "cn-beijing"},
			command: "alibaba", id: "r-fixture", product: provider.AlibabaTair},
		{vpcCreationFixture: vpcCreationFixture{vendor: provider.HuaweiECS, account: "1234567890abcdef1234567890abcdef", region: "cn-north-4"},
			command: "huawei", id: "70000000-0000-4000-8000-000000000019", product: provider.HuaweiDCS},
	} {
		t.Run(f.command, func(t *testing.T) {
			t.Parallel()
			testPublicCloudCacheInventory(t, f)
		})
	}
}

func testPublicCloudCacheInventory(t *testing.T, f *cacheFixture) {
	t.Helper()
	ctx, db := t.Context(), dbtest.Open(t)
	if err := cloudmanager.MigrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	const actor, reviewer = "fixture-cache-inventory-operator", "fixture-cache-inventory-reviewer"
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
	config := cloud.PublicCloudProfile{Ref: ref, Config: publiccloud.Config{AccountID: f.account, Regions: []string{f.region}}}
	if f.vendor == provider.HuaweiECS {
		config.ProjectID, config.IAMToken = fixtureHuaweiProject, fixtureHuaweiIAM
	} else {
		config.AccessKeyID, config.AccessKeySecret, config.SecurityToken = fixtureLaunchKey, fixtureLaunchSecret, fixtureLaunchSession
	}
	path := filepath.Join(t.TempDir(), "profile.json")
	writeProfile := func(scopeWide bool) {
		t.Helper()
		config.CacheListScopeWide = scopeWide
		data, err := json.Marshal(config)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeProfile(false)

	args := []string{"scan-" + f.command, "tenant", f.vendor, f.region, "cache-instance"}
	scan := func(ctx context.Context) error { return cloud.RunPublicCloud(ctx, db, args, f.vendor, path, transport) }
	point := append(slices.Clone(args), f.id)
	if !cloudmanager.ValidCommand(point) {
		t.Fatal("cache confirmation is inaccessible from CLI")
	}
	confirm := func() error { return cloud.RunPublicCloud(ctx, db, point, f.vendor, path, transport) }
	f.lateMode.Store(1)
	if err := scan(ctx); !provider.IsFailure(err, provider.Throttled) {
		t.Fatalf("expected a late 429, got %v", err)
	}
	var checkpoint string
	var complete bool
	if err := db.QueryRowContext(ctx, `SELECT cursor,completed_at IS NOT NULL FROM cmp_inventory_scopes
		WHERE kind='cache-instance'`).Scan(&checkpoint, &complete); err != nil || checkpoint != "0:50:51" || complete {
		t.Fatalf("late failure lost checkpoint or completed scan: cursor=%q complete=%t err=%v", checkpoint, complete, err)
	}
	f.lateMode.Store(2)
	failure := provider.InvalidResponse
	if f.vendor == provider.HuaweiECS {
		failure = provider.Unauthorized
	}
	if err := scan(ctx); !provider.IsFailure(err, failure) {
		t.Fatalf("invalid cache page was accepted: %v", err)
	}
	f.lateMode.Store(0)
	if err := scan(ctx); err != nil || f.firstReads.Load() != 1 {
		t.Fatalf("cache scan restarted rather than resuming: first reads=%d err=%v", f.firstReads.Load(), err)
	}

	app, err := routers.NewAPI(db, "", nil)
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
		if strings.Contains(w.Body.String(), cacheFixtureConnection) {
			t.Fatalf("project response disclosed cache connection metadata")
		}
		return w
	}
	const selector = "?kind=cache-instance"
	const primary = selector + "&q=fixture-cache-000"
	var assets inventory.Assets
	testsupport.DecodeProjectData(t, get("project", selector+"&q=redis&limit=100"), &assets)
	if len(assets.Items) != 51 || !slices.ContainsFunc(assets.Items, func(asset inventory.Asset) bool {
		return asset.ID == f.id && asset.Cache != nil && asset.Cache.Engine == "redis" && asset.Cache.CapacityMiB == 128
	}) {
		t.Fatalf("got %d cache assets without the expected cache projection", len(assets.Items))
	}
	testsupport.DecodeProjectData(t, get("other", selector), &assets)
	if len(assets.Items) != 0 {
		t.Fatal("another project received cache assets")
	}
	var topology inventory.Topology
	testsupport.DecodeProjectData(t, get("project", "/topology"+primary), &topology)
	if len(topology.Nodes) != 1 || topology.Nodes[0].Asset.Cache == nil ||
		topology.Nodes[0].Asset.Cache.Product != f.product || len(topology.Nodes[0].Links) != 1 ||
		topology.Nodes[0].Links[0].ID != f.network() {
		t.Fatalf("missing scoped cache topology: %+v", topology)
	}
	body, err := json.Marshal(map[string]string{"account": f.vendor, "region": f.region, "kind": "network", "asset_id": f.network()})
	if err != nil {
		t.Fatal(err)
	}
	removeVPC := httptest.NewRequest(http.MethodPost, "/v1/tenants/tenant/projects/project/network-operations/delete", strings.NewReader(string(body)))
	removeVPC.Header.Set("Authorization", "Bearer "+actor)
	removeVPC.Header.Set("Content-Type", "application/json")
	removeVPC.Header.Set("Idempotency-Key", "blocked-by-cache")
	removeVPC.Header.Set("If-Match", `"1"`)
	blocked := httptest.NewRecorder()
	app.ServeHTTP(blocked, removeVPC)
	if blocked.Code != http.StatusConflict {
		t.Fatalf("VPC deletion ignored a cache dependency: %d %s", blocked.Code, blocked.Body)
	}
	records, err := csv.NewReader(get("project", "/export"+primary).Body).ReadAll()
	if err != nil || len(records) != 2 || !slices.Equal(records[0][9:], []string{"product", "engine", "engine_version", "capacity_mib"}) ||
		!slices.Equal(records[1][9:], []string{f.product, "redis", "6.0", "128"}) {
		t.Fatalf("incorrect cache CSV: %v %v", records, err)
	}

	f.changed.Store(true)
	runFixtureScheduledScan(t, db, inventory.Key{Tenant: "tenant", Account: f.vendor, Region: f.region, Kind: provider.CacheInstance}, f.vendor, ref, scan)
	var history inventory.Changes
	testsupport.DecodeProjectData(t, get("project", "/changes"+selector+"&q=7.0"), &history)
	if len(history.Items) != 1 || history.Items[0].Action != "changed" || history.Items[0].PreviousCache == nil ||
		history.Items[0].PreviousCache.Version != "6.0" || history.Items[0].PreviousCache.CapacityMiB != 128 ||
		history.Items[0].Cache == nil || history.Items[0].Cache.Version != "7.0" || history.Items[0].Cache.CapacityMiB != 256 {
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
		testsupport.DecodeProjectData(t, get("project", primary), &assets)
		if want == "" && len(assets.Items) == 0 {
			return
		}
		if len(assets.Items) != 1 || assets.Items[0].State != want {
			t.Fatalf("cache state should be %q, got %+v", want, assets.Items)
		}
	}
	markMissing()
	for i, want := range []provider.FailureKind{provider.OutcomeUnknown, provider.InvalidResponse, provider.OutcomeUnknown} {
		f.observation.Store(int32(i))
		if err := confirm(); !provider.IsFailure(err, want) {
			t.Fatalf("lookup mode %d = %v, want %s", i, err, want)
		}
		state("missing_candidate")
	}
	f.observation.Store(3)
	if err := confirm(); err != nil {
		t.Fatal(err)
	}
	state("observed")

	markMissing()
	writeProfile(true)
	f.observation.Store(4)
	if err := confirm(); !provider.IsFailure(err, provider.Unauthorized) {
		t.Fatalf("403 confirmed absence: %v", err)
	}
	state("missing_candidate")
	f.observation.Store(2)
	if err := confirm(); err != nil {
		t.Fatal(err)
	}
	state("")
	testsupport.DecodeProjectData(t, get("project", "/changes"+primary), &history)
	if !slices.ContainsFunc(history.Items, func(change inventory.Change) bool {
		return change.AssetID == f.id && change.Action == "removed"
	}) {
		t.Fatal("precise absence did not record cache removal")
	}
}
