package routers

import (
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
)

func TestAcceptanceProjectAssetOwnership(t *testing.T) {
	db := dbtest.Open(t)
	ctx := t.Context()
	if err := identity.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := identity.MigrateServiceTokens(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := inventory.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO cmp_tenants VALUES ('tenant'),('other');
		INSERT INTO cmp_projects VALUES ('tenant','source'),('tenant','dest'),('other','outside');
		INSERT INTO cmp_principals (tenant_id,principal_id,kind,issuer,subject) VALUES ('tenant','admin','service','fixture','admin');
		INSERT INTO cmp_memberships (tenant_id,project_id,principal_id,role)
			VALUES ('tenant','source','admin','admin'),('tenant','dest','admin','admin');
		INSERT INTO cmp_cloud_accounts (tenant_id,account_id,project_id,provider,credential_ref,remote_identity,mode)
			VALUES ('tenant','account','source','openstack','profile/ref','site:project','read_only');
		INSERT INTO cmp_inventory_scopes (tenant_id,account_id,region,kind,credential_revision,lease_until)
			VALUES ('tenant','account','RegionOne','instance',1,now()),('tenant','account','RegionOne','volume',1,now());
		INSERT INTO cmp_inventory_objects (scope_id,external_id,name,status,seen_generation,links)
			SELECT scope_id,'vm','fixture-vm','running',1,'[{"kind":"volume","id":"disk","relation":"uses_volume"}]'
			FROM cmp_inventory_scopes WHERE kind='instance';
		INSERT INTO cmp_inventory_objects (scope_id,external_id,name,status,seen_generation)
			SELECT scope_id,'disk','fixture-disk','available',1 FROM cmp_inventory_scopes WHERE kind='volume';`)
	token := strings.Repeat("ownership-fixture-token-", 3)
	digest := sha256.Sum256([]byte(token))
	exec("INSERT INTO cmp_service_tokens(token_hash,tenant_id,principal_id,expires_at) VALUES ($1,'tenant','admin',now()+interval '5 minutes')", digest[:])
	app, err := NewAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	const source = "/v1/tenants/tenant/projects/source/assets"
	const dest = "/v1/tenants/tenant/projects/dest/assets"
	send := func(method, path, body, match string, status int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("If-Match", match)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, req)
		if w.Code != status {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body)
		}
		return w
	}
	contains := func(path, name string, expected bool) {
		t.Helper()
		if strings.Contains(send(http.MethodGet, path, "", "", 200).Body.String(), name) != expected {
			t.Fatalf("unexpected visibility for %s in %s", name, path)
		}
	}
	contains(dest, "fixture-vm", false)
	exec("UPDATE cmp_cloud_accounts SET credential_revision=2")
	const moveVM = `{"account":"account","region":"RegionOne","kind":"instance","asset_id":"vm","target_project":"dest"}`
	const moveDisk = `{"account":"account","region":"RegionOne","kind":"volume","asset_id":"disk","target_project":"dest"}`
	send(http.MethodPatch, source+"/ownership", strings.Replace(moveVM, `"dest"`, `"outside"`, 1), `"2"`, 403)
	send(http.MethodPatch, source+"/ownership", moveVM, `"1"`, 412)
	exec("ALTER TABLE cmp_asset_ownership_events ADD CONSTRAINT reject_transfer CHECK (to_project<>'dest')")
	send(http.MethodPatch, source+"/ownership", moveVM, `"2"`, 503)
	contains(source, "fixture-vm", true)
	exec("ALTER TABLE cmp_asset_ownership_events DROP CONSTRAINT reject_transfer")
	if got := send(http.MethodPatch, source+"/ownership", moveVM, `"2"`, 200).Header().Get("ETag"); got != `"3"` {
		t.Fatalf("wrong resulting ownership version: %s", got)
	}
	for _, suffix := range []string{"", "/changes", "/export", "/topology"} {
		contains(source+suffix, "fixture-vm", false)
		contains(dest+suffix, "fixture-vm", true)
	}
	contains(dest+"/topology", "fixture-disk", false)
	send(http.MethodPatch, source+"/ownership", moveDisk, `"2"`, 200)
	contains(dest+"/topology", "fixture-disk", true)
	send(http.MethodPatch, dest+"/ownership", strings.Replace(moveVM, `"dest"`, `"source"`, 1), `"2"`, 412)
	var events int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM cmp_asset_ownership_events").Scan(&events); err != nil || events != 2 {
		t.Fatalf("successful ownership events: %d %v", events, err)
	}
}
