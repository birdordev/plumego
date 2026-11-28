package cloudtest

import (
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"with-cloud-manager/internal/identity"
)

func TestAcceptanceCloudAccountWriteMode(t *testing.T) {
	db, app, _ := setupAccountInventory(t)
	for _, id := range []string{"tc", "ali", "hw"} {
		t.Run(id, func(t *testing.T) { checkCloudWriteMode(t, db, app, id) })
	}
	owner := identity.Scope{Tenant: "tenant", Project: "source", Principal: "admin"}
	for _, cloud := range []string{"vmware-vsphere", "redfish"} {
		admin := identity.NewAccountAdmin(db)
		account, err := admin.Register(t.Context(), inventoryAdminToken, owner,
			identity.AccountInput{ID: cloud, Provider: cloud, CredentialRef: "fixture/ref/" + cloud})
		if err != nil {
			t.Fatal(err)
		}
		verifyModeFixture(t, db, account.ID)
		accountInventoryCall(t, app, accountSourcePath+"/"+cloud, inventoryAdminToken, `{"action":"enable-write"}`, 409)
	}
	accountInventoryCall(t, app, accountSourcePath+"/redfish", inventoryAdminToken, `{"action":"enable-bare-metal-power"}`, 200)
}

func checkCloudWriteMode(t *testing.T, db *sql.DB, app http.Handler, id string) {
	t.Helper()
	path := accountSourcePath + "/" + id
	const enable = `{"action":"enable-write"}`
	const disable = `{"action":"disable-write"}`
	accountInventoryCall(t, app, path, inventoryAdminToken, enable, 409)
	verifyModeFixture(t, db, id)
	before := accountInventoryCall(t, app, path, inventoryAdminToken, "", 200)
	writeModeCall(t, app, path, inventoryViewerToken, before.Header().Get("ETag"), enable, 403)
	writeModeCall(t, app, path, inventoryAdminToken, before.Header().Get("ETag"), `{"action":"enable-write","target_project":"dest"}`, 400)
	active := accountInventoryCall(t, app, path, inventoryAdminToken, enable, 200)
	if etag := active.Header().Get("ETag"); etag == "" || etag == before.Header().Get("ETag") || !strings.Contains(active.Body.String(), "write_enabled") {
		t.Fatalf("invalid write enable response: %s", active.Body)
	}
	writeModeCall(t, app, path, inventoryAdminToken, before.Header().Get("ETag"), disable, 412)
	store := identity.NewStore(db)
	owner := identity.Scope{Tenant: "tenant", Project: "source", Principal: "admin"}
	if _, err := store.AuthorizeAccount(t.Context(), owner, id, identity.Execute); err != nil {
		t.Fatal(err)
	}
	if id == "tc" {
		shared := identity.Scope{Tenant: "tenant", Project: "dest", Principal: "admin"}
		if _, err := store.AuthorizeAccount(t.Context(), shared, id, identity.Execute); !errors.Is(err, identity.ErrForbidden) {
			t.Fatalf("read-only grant acquired write access: %v", err)
		}
	}
	accountInventoryCall(t, app, path, inventoryAdminToken, enable, 409)
	disabled := accountInventoryCall(t, app, path, inventoryAdminToken, disable, 200)
	if disabled.Header().Get("ETag") == active.Header().Get("ETag") || !strings.Contains(disabled.Body.String(), "read_only") {
		t.Fatalf("invalid disable write response: %s", disabled.Body)
	}
	if _, err := store.AuthorizeAccount(t.Context(), owner, id, identity.Execute); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("disabled account retained write access: %v", err)
	}
	if _, err := store.AuthorizeAccount(t.Context(), owner, id, identity.View); err != nil {
		t.Fatalf("disabled account lost read access: %v", err)
	}
	var audited int
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM cmp_project_audit
		WHERE (tenant_id,project_id,account_id)=('tenant','source',$1)
		AND action IN ('enable-write','disable-write') AND actor_id='admin'`, id).Scan(&audited); err != nil || audited != 2 {
		t.Fatalf("write changes were not audited: %d %v", audited, err)
	}
}

func verifyModeFixture(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	store := identity.NewStore(db)
	binding, err := store.CloudAccountBinding(t.Context(), "tenant", id)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.VerifyCloudAccount(t.Context(), "tenant", id, binding, "fixture-mode:"+id); err != nil {
		t.Fatal(err)
	}
}

func writeModeCall(t *testing.T, app http.Handler, target, token, version, body string, want int) {
	t.Helper()
	req, w := httptest.NewRequestWithContext(t.Context(), http.MethodPatch, target, strings.NewReader(body)), httptest.NewRecorder()
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("If-Match", version)
	app.ServeHTTP(w, req)
	if w.Code != want {
		t.Fatalf("write mode got %d want %d: %s", w.Code, want, w.Body)
	}
}
