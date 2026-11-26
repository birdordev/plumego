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

func TestAcceptancePublicCloudBucketInventory(t *testing.T) {
	for _, f := range []*bucketFixture{
		{vpcCreationFixture: vpcCreationFixture{vendor: provider.TencentCVM, account: "100000000001", region: "ap-guangzhou"},
			command: "tencent", product: provider.TencentCOS, name: "fixture-first-1250000000", location: "ap-beijing"},
		{vpcCreationFixture: vpcCreationFixture{vendor: provider.AlibabaECS, account: "1000000000000002", region: "cn-hangzhou"},
			command: "alibaba", product: provider.AlibabaOSS, name: "fixture-first-oss", location: "cn-shanghai"},
		{vpcCreationFixture: vpcCreationFixture{vendor: provider.HuaweiECS, account: "1234567890abcdef1234567890abcdef", region: "cn-north-4"},
			command: "huawei", product: provider.HuaweiOBS, name: "fixture-first-obs", location: "cn-north-1"},
	} {
		t.Run(f.command, func(t *testing.T) {
			t.Parallel()
			testPublicCloudBucketInventory(t, f)
		})
	}
}

func testPublicCloudBucketInventory(t *testing.T, f *bucketFixture) {
	t.Helper()
	ctx, db := t.Context(), dbtest.Open(t)
	if err := migrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	const actor, reviewer = "fixture-bucket-inventory-operator", "fixture-bucket-inventory-reviewer"
	const ref = "fixture/snapshot-delete"
	seedSnapshotDeletion(t, db, &snapshotDeletionFixture{vendor: f.vendor, region: f.region,
		volume: "fixture-volume", id: "fixture-snapshot", class: "standard"}, f.remoteIdentity(), actor, reviewer)
	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.serveBuckets(t, w, r) }))
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
	writeProfile := func() {
		t.Helper()
		data, err := json.Marshal(config)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeProfile()

	args := []string{"scan-" + f.command, "tenant", f.vendor, "global", "object-bucket"}
	scan := func(ctx context.Context) error { return runPublicCloud(ctx, db, args, f.vendor, path, transport) }
	retiredID := f.name + "@" + fixtureBucketOld
	newID := f.name + "@" + fixtureBucketNew
	point := append(slices.Clone(args), retiredID)
	if !validCommand(point) {
		t.Fatal("bucket confirmation is inaccessible from CLI")
	}
	confirm := func() error { return runPublicCloud(ctx, db, point, f.vendor, path, transport) }
	wrongRegion := slices.Clone(args)
	wrongRegion[3] = f.region
	if err := runPublicCloud(ctx, db, wrongRegion, f.vendor, path, transport); err == nil || f.firstReads.Load() != 0 {
		t.Fatalf("regional scope reached global list: %v", err)
	}

	f.mode.Store(2)
	if err := scan(ctx); !provider.IsFailure(err, provider.Unauthorized) {
		t.Fatalf("bucket list from a different owner was accepted: %v", err)
	}
	var rows int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM cmp_inventory_objects AS o
		JOIN cmp_inventory_scopes AS s USING (scope_id) WHERE s.kind='object-bucket'`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("unverified owner stored buckets: rows=%d err=%v", rows, err)
	}
	f.mode.Store(1)
	if err := scan(ctx); !provider.IsFailure(err, provider.Throttled) {
		t.Fatalf("expected throttling, got %v", err)
	}
	var checkpoint string
	var complete bool
	if err := db.QueryRowContext(ctx, `SELECT cursor, completed_at IS NOT NULL
		FROM cmp_inventory_scopes WHERE kind='object-bucket'`).Scan(&checkpoint, &complete); err != nil || complete {
		t.Fatalf("failed list completed: cursor=%q complete=%t err=%v", checkpoint, complete, err)
	}
	paginated := f.vendor != provider.HuaweiECS
	if paginated && checkpoint != "fixture-next" {
		t.Fatalf("late throttling dropped the marker: %q", checkpoint)
	}
	reads := f.firstReads.Load()
	f.mode.Store(0)
	if err := scan(ctx); err != nil || (paginated && f.firstReads.Load() != reads) {
		t.Fatalf("failed to resume list from marker: %v", err)
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
		for _, hidden := range []string{fixtureBucketHost, fixtureOBSToken, fixtureOBSSecret} {
			if strings.Contains(w.Body.String(), hidden) {
				t.Fatal("project inventory disclosed provider identity material or connection metadata")
			}
		}
		return w
	}
	const selector = "?kind=object-bucket"
	filter := selector + "&q=" + url.QueryEscape(f.location)
	var assets inventory.Assets
	decodeProjectData(t, get("project", filter), &assets)
	want := 1
	if paginated {
		want = 2
	}
	if len(assets.Items) != want || !slices.ContainsFunc(assets.Items, func(asset inventory.Asset) bool {
		return asset.ID == retiredID && asset.Name == f.name && asset.Region == "global" &&
			asset.Bucket != nil && asset.Bucket.Product == f.product && asset.Bucket.Location == f.location
	}) {
		t.Fatalf("missing global bucket projection: %+v", assets.Items)
	}
	decodeProjectData(t, get("other", selector), &assets)
	if len(assets.Items) != 0 {
		t.Fatal("unrelated project can read buckets")
	}
	csvPath := "/export" + selector + "&q=" + url.QueryEscape(retiredID)
	records, err := csv.NewReader(get("project", csvPath).Body).ReadAll()
	if err != nil || len(records) != 2 || !slices.Contains(records[0], "created_at") ||
		!slices.Contains(records[1], f.location) || !slices.Contains(records[1], fixtureBucketOld) {
		t.Fatalf("bad bucket export %v %v", records, err)
	}

	if _, err := db.ExecContext(ctx, `INSERT INTO cmp_asset_ownership(tenant_id,scope_id,external_id,project_id,revision)
		SELECT tenant_id,scope_id,$1,'other',2 FROM cmp_inventory_scopes WHERE kind='object-bucket'`, retiredID); err != nil {
		t.Fatal(err)
	}
	f.recreated.Store(true)
	runFixtureScheduledScan(t, db, inventory.Key{Tenant: "tenant", Account: f.vendor, Region: "global", Kind: provider.ObjectBucket}, f.vendor, ref, scan)
	lookup := func(project, id, state string) {
		t.Helper()
		decodeProjectData(t, get(project, selector+"&q="+url.QueryEscape(id)), &assets)
		if state == "" && len(assets.Items) == 0 {
			return
		}
		if state == "" || len(assets.Items) != 1 || assets.Items[0].ID != id || assets.Items[0].State != state {
			t.Fatalf("bucket %s visible in %s with unexpected state: %+v", id, project, assets.Items)
		}
	}
	lookup("project", newID, "observed")
	lookup("other", newID, "")
	lookup("other", retiredID, "missing_candidate")

	if err := confirm(); !provider.IsFailure(err, provider.OutcomeUnknown) {
		t.Fatalf("a potentially filtered listing confirmed absence: %v", err)
	}
	lookup("other", retiredID, "missing_candidate")
	config.Config.BucketListAccountWide = true
	writeProfile()

	f.mode.Store(3)
	if err := confirm(); !provider.IsFailure(err, provider.OutcomeUnknown) {
		t.Fatalf("incomplete verification or 404 confirmed absence: %v", err)
	}
	f.mode.Store(2)
	if err := confirm(); !provider.IsFailure(err, provider.Unauthorized) {
		t.Fatalf("wrong owner confirmed absence: %v", err)
	}
	lookup("other", retiredID, "missing_candidate")
	f.mode.Store(0)
	if err := confirm(); err != nil {
		t.Fatal(err)
	}
	lookup("other", retiredID, "")
	lookup("project", newID, "observed")
	var history inventory.Changes
	decodeProjectData(t, get("other", "/changes"+selector+"&q="+url.QueryEscape(retiredID)), &history)
	if !slices.ContainsFunc(history.Items, func(change inventory.Change) bool {
		return change.AssetID == retiredID && change.Action == "removed" && change.Bucket != nil &&
			change.Bucket.CreatedAt.Format("2006-01-02T15:04:05Z07:00") == fixtureBucketOld
	}) {
		t.Fatal("old bucket removal lacked a scoped history record")
	}
}
