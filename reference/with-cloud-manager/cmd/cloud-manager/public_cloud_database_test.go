package main

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

	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/publiccloud"
)

func TestAcceptancePublicCloudDatabaseInventory(t *testing.T) {
	for _, f := range []*databaseFixture{
		{vpcCreationFixture: vpcCreationFixture{vendor: provider.TencentCVM, account: "100000000001", region: "ap-guangzhou"},
			command: "tencent", id: "cdb-fixture", product: provider.TencentCDB},
		{vpcCreationFixture: vpcCreationFixture{vendor: provider.AlibabaECS, account: "1000000000000002", region: "cn-hangzhou"},
			command: "alibaba", id: "rm-fixture", product: provider.AlibabaRDS},
		{vpcCreationFixture: vpcCreationFixture{vendor: provider.HuaweiECS, account: "1234567890abcdef1234567890abcdef", region: "cn-north-4"},
			command: "huawei", id: "97000000000040008000000000000001in01", product: provider.HuaweiRDS},
	} {
		t.Run(f.command, func(t *testing.T) {
			t.Parallel()
			testPublicCloudDatabaseInventory(t, f)
		})
	}
}

func testPublicCloudDatabaseInventory(t *testing.T, f *databaseFixture) {
	t.Helper()
	ctx, db := t.Context(), dbtest.Open(t)
	if err := migrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	const actor, reviewer = "fixture-database-inventory-operator", "fixture-database-inventory-reviewer"
	const ref = "fixture/snapshot-delete"
	seedSnapshotDeletion(t, db, &snapshotDeletionFixture{vendor: f.vendor, region: f.region,
		volume: "fixture-volume", id: "fixture-snapshot", class: "standard"}, f.remoteIdentity(), actor, reviewer)
	if _, err := db.ExecContext(ctx, `WITH scope AS (
		INSERT INTO cmp_inventory_scopes(tenant_id,account_id,region,kind,credential_revision,lease_until)
		VALUES ('tenant',$1,$2,'network',1,now()) RETURNING scope_id
	) INSERT INTO cmp_inventory_objects(scope_id,external_id,name,status,seen_generation)
		SELECT scope_id,$3,'fixture VPC','available',1 FROM scope`, f.vendor, f.region, f.databaseVPC()); err != nil {
		t.Fatal(err)
	}

	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.serveDatabase(t, w, r) }))
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
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	args := []string{"scan-" + f.command, "tenant", f.vendor, f.region, "database-instance"}
	scan := func(ctx context.Context) error { return runPublicCloud(ctx, db, args, f.vendor, path, transport) }
	point := append(slices.Clone(args), f.id)
	if !validCommand(point) {
		t.Fatal("database confirmation is inaccessible from CLI")
	}
	confirm := func() error { return runPublicCloud(ctx, db, point, f.vendor, path, transport) }
	f.lateMode.Store(1)
	if err := scan(ctx); !provider.IsFailure(err, provider.Throttled) {
		t.Fatalf("expected a late 429, got %v", err)
	}
	var checkpoint string
	var complete bool
	if err := db.QueryRowContext(ctx, `SELECT cursor,completed_at IS NOT NULL FROM cmp_inventory_scopes
		WHERE kind='database-instance'`).Scan(&checkpoint, &complete); err != nil || checkpoint != "0:50:51" || complete {
		t.Fatalf("late failure lost checkpoint or completed scan: cursor=%q complete=%t err=%v", checkpoint, complete, err)
	}
	f.lateMode.Store(2)
	if err := scan(ctx); !provider.IsFailure(err, provider.InvalidResponse) {
		t.Fatalf("incorrect region accepted: %v", err)
	}
	f.lateMode.Store(0)
	if err := scan(ctx); err != nil || f.firstReads.Load() != 1 {
		t.Fatalf("database scan restarted rather than resuming: first reads=%d err=%v", f.firstReads.Load(), err)
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
		for _, hidden := range []string{fixtureDatabaseHost, fixtureDatabaseUser, "192.0.2.99"} {
			if strings.Contains(w.Body.String(), hidden) {
				t.Fatalf("project response disclosed database connection metadata")
			}
		}
		return w
	}

	const selector = "?kind=database-instance"
	filter := selector + "&q=" + url.QueryEscape(f.id)
	var assets inventory.Assets
	decodeProjectData(t, get("project", selector+"&limit=100"), &assets)
	expected := 51
	if f.vendor == provider.TencentCVM {
		expected += 2
		for _, engine := range []string{"postgresql", "sqlserver"} {
			if !slices.ContainsFunc(assets.Items, func(asset inventory.Asset) bool {
				return asset.Database != nil && asset.Database.Engine == engine
			}) {
				t.Fatalf("Tencent scan did not reach product %s", engine)
			}
		}
	}
	if len(assets.Items) != expected {
		t.Fatalf("got %d database assets, want %d", len(assets.Items), expected)
	}
	decodeProjectData(t, get("other", selector), &assets)
	if len(assets.Items) != 0 {
		t.Fatalf("another project received database assets")
	}

	var topology inventory.Topology
	decodeProjectData(t, get("project", "/topology"+selector+"&q=fixture-db-000"), &topology)
	if len(topology.Nodes) != 1 || topology.Nodes[0].Asset.Database == nil ||
		topology.Nodes[0].Asset.Database.Product != f.product || len(topology.Nodes[0].Links) != 1 ||
		topology.Nodes[0].Links[0].ID != f.databaseVPC() {
		t.Fatalf("missing scoped database topology: %+v", topology)
	}
	body, err := json.Marshal(map[string]string{"account": f.vendor, "region": f.region, "kind": "network", "asset_id": f.databaseVPC()})
	if err != nil {
		t.Fatal(err)
	}
	removeVPC := httptest.NewRequest(http.MethodPost, "/v1/tenants/tenant/projects/project/network-operations/delete", strings.NewReader(string(body)))
	removeVPC.Header.Set("Authorization", "Bearer "+actor)
	removeVPC.Header.Set("Content-Type", "application/json")
	removeVPC.Header.Set("Idempotency-Key", "blocked-by-database")
	removeVPC.Header.Set("If-Match", `"1"`)
	blocked := httptest.NewRecorder()
	app.ServeHTTP(blocked, removeVPC)
	if blocked.Code != http.StatusConflict {
		t.Fatalf("VPC deletion ignored a database dependency: %d %s", blocked.Code, blocked.Body)
	}
	records, err := csv.NewReader(get("project", "/export"+selector+"&q=fixture-db-000").Body).ReadAll()
	if err != nil || len(records) != 2 || !slices.Contains(records[0], "engine_version") ||
		!slices.Contains(records[1], fixtureDatabaseVersion) || !slices.Contains(records[1], f.product) {
		t.Fatalf("incorrect database CSV: %v %v", records, err)
	}

	f.changed.Store(true)
	runFixtureScheduledScan(t, db, inventory.Key{Tenant: "tenant", Account: f.vendor, Region: f.region, Kind: provider.DatabaseInstance}, f.vendor, ref, scan)
	var history inventory.Changes
	decodeProjectData(t, get("project", "/changes"+selector+"&q="+fixtureDatabaseNext), &history)
	if len(history.Items) != 1 || history.Items[0].Action != "changed" || history.Items[0].PreviousDatabase == nil ||
		history.Items[0].PreviousDatabase.Version != fixtureDatabaseVersion || history.Items[0].Database == nil ||
		history.Items[0].Database.Version != fixtureDatabaseNext {
		t.Fatalf("missing engine upgrade history: %+v", history.Items)
	}

	f.missing.Store(true)
	if err := scan(ctx); err != nil {
		t.Fatal(err)
	}
	checkCandidate := func(want bool) {
		t.Helper()
		decodeProjectData(t, get("project", filter+"&limit=100"), &assets)
		candidate := slices.ContainsFunc(assets.Items, func(asset inventory.Asset) bool {
			return asset.ID == f.id && asset.State == "missing_candidate"
		})
		if candidate != want {
			t.Fatalf("database missing candidate=%t, want %t", candidate, want)
		}
	}
	checkCandidate(true)
	if err := confirm(); !provider.IsFailure(err, provider.OutcomeUnknown) {
		t.Fatalf("ambiguous 404 confirmed absence: %v", err)
	}
	f.observation.Store(1)
	if err := confirm(); !provider.IsFailure(err, provider.InvalidResponse) {
		t.Fatalf("wrong ID confirmed absence: %v", err)
	}
	checkCandidate(true)
	f.observation.Store(2)
	if err := confirm(); err != nil {
		t.Fatal(err)
	}
	checkCandidate(false)
	decodeProjectData(t, get("project", "/changes"+filter+"&limit=100"), &history)
	if !slices.ContainsFunc(history.Items, func(change inventory.Change) bool {
		return change.AssetID == f.id && change.Action == "removed"
	}) {
		t.Fatal("precise absence did not record asset removal")
	}
}
