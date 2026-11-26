package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/vsphere"
)

const vSphereFixtureRef = "fixture/vsphere-read"
const vSphereFixtureBearer = "fixture-vsphere-project-administrator-token"

func TestAcceptanceVSphereInventory(t *testing.T) {
	ctx, db := t.Context(), dbtest.Open(t)
	seedVSphereProject(t, db)
	f := &vSphereFixture{}
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { f.serve(t, w, r) }))
	t.Cleanup(upstream.Close)
	cfg := vsphere.Config{Endpoint: upstream.URL, Username: vSphereFixtureUsername, Password: vSphereFixturePassword,
		VCenterUUID: vCenterFixtureUUID, Datacenters: []string{"datacenter-7"},
		CAPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: upstream.Certificate().Raw}))}
	profile := vSphereProfile{Ref: vSphereFixtureRef, Config: cfg}
	path := filepath.Join(t.TempDir(), "vcenter.json")
	data, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	app, err := newAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	req := func(method, target, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequestWithContext(ctx, method, target, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+vSphereFixtureBearer)
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		if method == http.MethodPost && strings.HasSuffix(target, "/instance-operations") {
			r.Header.Set("If-Match", `"1"`)
			r.Header.Set("Idempotency-Key", "vsphere-write-test")
		}
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s %s returned %d: %s", method, target, w.Code, w.Body.String())
		}
		for _, sensitive := range []string{vSphereFixturePassword, vSphereFixtureCookie, vSphereFixtureToken} {
			if strings.Contains(w.Body.String(), sensitive) {
				t.Fatal("API disclosed vSphere credentials")
			}
		}
		return w
	}
	const projects = "/v1/tenants/tenant/projects/"
	const project = projects + "project"
	req(http.MethodPost, project+"/cloud-accounts", `{"id":"vcenter","provider":"vmware-vsphere","credential_ref":"fixture/vsphere-read"}`, http.StatusCreated)
	verify := []string{"verify-vsphere", "tenant", "vcenter"}
	if !validCommand(verify) || !validCommand([]string{"scan-vsphere", "tenant", "vcenter", "datacenter-7", "instance"}) {
		t.Fatal("vSphere commands unavailable")
	}
	f.wrongUUID.Store(true)
	if err := runVSphere(ctx, db, verify, path, nil); !provider.IsFailure(err, provider.Unauthorized) || f.logins.Load() != 0 {
		t.Fatalf("password crossed an unverified vCenter identity: %v", err)
	}
	f.wrongUUID.Store(false)
	f.hideDC.Store(true)
	if err := runVSphere(ctx, db, verify, path, nil); !provider.IsFailure(err, provider.Unauthorized) || f.logins.Load() != 1 || f.logouts.Load() != 1 {
		t.Fatalf("invisible datacenter verified or leaked a login: %v", err)
	}
	f.hideDC.Store(false)
	if err := runVSphere(ctx, db, verify, path, nil); err != nil {
		t.Fatal(err)
	}
	account, err := identity.NewStore(db).CloudAccountBinding(ctx, "tenant", "vcenter")
	if err != nil || account.RemoteIdentity != "vsphere:"+vCenterFixtureUUID || account.Mode != "read_only" {
		t.Fatalf("wrong vSphere binding: %+v, %v", account, err)
	}
	assetScope := inventory.Key{Tenant: "tenant", Account: "vcenter", Region: "datacenter-7", Kind: provider.Instance}
	scan := func(region string, kind provider.Kind) error {
		return runVSphere(ctx, db, []string{"scan-vsphere", "tenant", "vcenter", region, string(kind)}, path, nil)
	}
	if err := scan("datacenter-999", provider.Instance); !provider.IsFailure(err, provider.Unauthorized) {
		t.Fatalf("unauthorized datacenter was scanned: %v", err)
	}
	var outOfScope bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM cmp_inventory_scopes WHERE region='datacenter-999')`).Scan(&outOfScope); err != nil || outOfScope {
		t.Fatalf("out-of-scope datacenter changed inventory: %t, %v", outOfScope, err)
	}
	if err := scan("-", provider.Region); err != nil {
		t.Fatal(err)
	}
	f.failLate.Store(true)
	if err := scan(assetScope.Region, provider.Instance); !provider.IsFailure(err, provider.Throttled) {
		t.Fatalf("late 429 was not propagated: %v", err)
	}
	assertVSphereInstances(t, db, 0, 0)
	f.failLate.Store(false)
	f.repeated.Store(true)
	if err := scan(assetScope.Region, provider.Instance); !provider.IsFailure(err, provider.InvalidResponse) {
		t.Fatalf("repeated vSphere token was accepted: %v", err)
	}
	assertVSphereInstances(t, db, 0, 0)
	f.repeated.Store(false)

	pauseVSphereInventory(t, db, cfg, assetScope, account.Revision)
	if err := scan(assetScope.Region, provider.Instance); err != nil {
		t.Fatalf("identical image could not resume across sessions: %v", err)
	}
	assertVSphereInstances(t, db, 251, 0)
	pauseVSphereInventory(t, db, cfg, assetScope, account.Revision)
	f.changed.Store(true)
	if err := scan(assetScope.Region, provider.Instance); !provider.IsFailure(err, provider.ExpiredCursor) {
		t.Fatalf("changed image reused its previous checkpoint: %v", err)
	}
	assertVSphereInstances(t, db, 251, 0)
	runVSphereScheduledScan(t, db, assetScope, path)
	if err := scan(assetScope.Region, provider.Network); err != nil {
		t.Fatal(err)
	}
	if err := scan(assetScope.Region, provider.Image); err != nil {
		t.Fatal(err)
	}

	var topology inventory.Topology
	decodeProjectData(t, req(http.MethodGet, project+"/assets/topology?kind=instance&q=renamed", "", http.StatusOK), &topology)
	if len(topology.Nodes) != 1 || len(topology.Nodes[0].Links) != 1 || topology.Nodes[0].Links[0].ID != "dvportgroup-8" {
		t.Fatalf("missing renamed VM or its visible network: %+v", topology.Nodes)
	}
	var assets inventory.Assets
	decodeProjectData(t, req(http.MethodGet, project+"/assets?kind=image", "", http.StatusOK), &assets)
	if len(assets.Items) != 1 || assets.Items[0].Name != "fixture-vm-252" {
		t.Fatalf("vSphere templates not projected as images: %+v", assets.Items)
	}
	decodeProjectData(t, req(http.MethodGet, projects+"other/assets?kind=instance", "", http.StatusOK), &assets)
	if len(assets.Items) != 0 {
		t.Fatal("unshared vSphere inventory visible to sibling project")
	}

	checkVSphereCredentialFence(t, db, cfg, assetScope, account.Revision, path)
	f.restricted.Store(true)
	if err := scan(assetScope.Region, provider.Instance); err != nil {
		t.Fatal(err)
	}
	assertVSphereInstances(t, db, 251, 250)
	if validCommand([]string{"scan-vsphere", "tenant", "vcenter", assetScope.Region, "instance", "point-lookup"}) {
		t.Fatal("visibility-based absence became an automatic removal")
	}
	if _, err := db.ExecContext(ctx, `UPDATE cmp_cloud_accounts SET mode='write_enabled' WHERE account_id='vcenter'`); err != nil {
		t.Fatal(err)
	}
	req(http.MethodPost, project+"/instance-operations",
		`{"account":"vcenter","region":"datacenter-7","asset_id":"12300000-0000-4000-8000-000000000001","action":"start"}`, http.StatusForbidden)
	if f.logouts.Load() != f.logins.Load() || f.continues.Load() == 0 || f.destroyed.Load() == 0 {
		t.Fatalf("vSphere lifecycle imbalance: logins=%d logouts=%d views=%d continuations=%d", f.logins.Load(), f.logouts.Load(), f.destroyed.Load(), f.continues.Load())
	}
}

func checkVSphereCredentialFence(t *testing.T, db *sql.DB, cfg vsphere.Config, key inventory.Key, revision int64, path string) {
	t.Helper()
	ctx := t.Context()
	client, err := vsphere.Connect(ctx, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := client.Close(ctx); err != nil {
			t.Error(err)
		}
	}()
	store := inventory.NewStore(db)
	claim, err := store.ClaimWithCredentialRevision(ctx, key, time.Minute, revision)
	if err != nil {
		t.Fatal(err)
	}
	next, done, err := store.ScanPage(ctx, claim, client)
	if err != nil || done {
		t.Fatalf("vSphere collector did not create a checkpoint: %t, %v", done, err)
	}
	admin := identity.NewAccountAdmin(db)
	if _, err := admin.Change(ctx, vSphereFixtureBearer, identity.Scope{Tenant: key.Tenant, Project: "project"}, key.Account, revision,
		identity.AccountChange{Action: "rotate", CredentialRef: vSphereFixtureRef}); err != nil {
		t.Fatal(err)
	}
	if err := runVSphere(ctx, db, []string{"verify-vsphere", key.Tenant, key.Account}, path, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ScanPage(ctx, next, client); !errors.Is(err, inventory.ErrStale) {
		t.Fatalf("old vSphere session published after credential rotation: %v", err)
	}
}

func seedVSphereProject(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := t.Context()
	if err := migrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO cmp_tenants VALUES ('tenant');
		INSERT INTO cmp_projects VALUES ('tenant','project'),('tenant','other');
		INSERT INTO cmp_principals (tenant_id,principal_id,kind,issuer,subject) VALUES ('tenant','admin','service','fixture','admin');
		INSERT INTO cmp_memberships (tenant_id,project_id,principal_id,role) VALUES ('tenant','project','admin','admin'),('tenant','other','admin','admin')`); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte(vSphereFixtureBearer))
	if _, err := db.ExecContext(ctx, `INSERT INTO cmp_service_tokens(tenant_id,principal_id,token_hash,expires_at)
		VALUES ('tenant','admin',$1,now()+interval '10 minutes')`, hash[:]); err != nil {
		t.Fatal(err)
	}
}

func pauseVSphereInventory(t *testing.T, db *sql.DB, cfg vsphere.Config, key inventory.Key, revision int64) {
	t.Helper()
	ctx := t.Context()
	client, err := vsphere.Connect(ctx, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := client.Close(ctx); err != nil {
			t.Error(err)
		}
	}()
	store := inventory.NewStore(db)
	claim, err := store.ClaimWithCredentialRevision(ctx, key, time.Minute, revision)
	if err != nil {
		t.Fatal(err)
	}
	next, done, err := store.ScanPage(ctx, claim, client)
	if err != nil || done {
		t.Fatalf("expected persisted first page: completed=%t, %v", done, err)
	}
	var cursor string
	if err := db.QueryRowContext(ctx, `SELECT cursor FROM cmp_inventory_scopes WHERE (tenant_id,account_id,region,kind)=($1,$2,$3,$4)`,
		key.Tenant, key.Account, key.Region, key.Kind).Scan(&cursor); err != nil || !strings.HasSuffix(cursor, ":250") || strings.Contains(cursor, vSphereFixtureToken) {
		t.Fatalf("unexpected durable pagination: cursor=%q, err=%v", cursor, err)
	}
	if err := store.Yield(ctx, next); err != nil {
		t.Fatal(err)
	}
}

func assertVSphereInstances(t *testing.T, db *sql.DB, wantTotal, wantMissing int) {
	t.Helper()
	var total, missing, removed int
	err := db.QueryRowContext(t.Context(), `SELECT count(*),count(*) FILTER (WHERE missing_generation IS NOT NULL),count(*) FILTER (WHERE removed_at IS NOT NULL)
		FROM cmp_inventory_objects AS o JOIN cmp_inventory_scopes AS s USING(scope_id)
		WHERE s.kind='instance' AND s.account_id='vcenter'`).Scan(&total, &missing, &removed)
	if err != nil || total != wantTotal || missing != wantMissing || removed != 0 {
		t.Fatalf("unexpected inventory reduction: total=%d missing=%d removed=%d err=%v", total, missing, removed, err)
	}
}
