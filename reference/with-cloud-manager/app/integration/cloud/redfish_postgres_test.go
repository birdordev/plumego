package cloudtest

import (
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	cloudmanager "with-cloud-manager/app"
	"with-cloud-manager/app/routers"
	"with-cloud-manager/app/services/cloud"
	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/redfish"
)

func TestAcceptanceRedfishInventory(t *testing.T) {
	db := dbtest.Open(t)
	seedInventoryProviderProject(t, db)
	ctx := t.Context()
	const ref = "fixture/redfish"
	const projects = "/v1/tenants/tenant/projects/"
	const uuid = "6dfa9bd0-98bf-49a1-ad93-a98567937e16"
	const firstID = "10101010-2121-4343-8282-545454545454"
	const secondID = "20202020-3131-4444-8383-656565656565"
	const systems = "/redfish/v1/Systems"
	var fault atomic.Int32
	var reads atomic.Int32
	var authenticatedReads atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		user, pass, ok := r.BasicAuth()
		if ok {
			authenticatedReads.Add(1)
		}
		root := r.URL.Path == "/redfish/v1/"
		if r.Method != http.MethodGet || (root && r.Header.Get("Authorization") != "") ||
			(!root && (!ok || user != "reader" || pass != "fixture-secret")) {
			t.Error("unexpected Redfish method or credential")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		write := func(body string) {
			w.Header().Set("Content-Type", "application/json")
			if _, err := w.Write([]byte(body)); err != nil {
				t.Error(err)
			}
		}
		switch r.URL.RequestURI() {
		case "/redfish/v1/":
			id := strings.ToUpper(uuid)
			if fault.Load() == 1 {
				id = firstID
			}
			write(`{"UUID":"` + id + `","Systems":{"@odata.id":"` + systems + `"}}`)
		case systems:
			next := systems + "?page=2"
			if fault.Load() == 4 {
				next = "https://192.0.2.10/redfish/v1/Systems?page=2"
			}
			write(`{"Members@odata.count":3,"Members":[{"@odata.id":"` + systems + `/one"}],"Members@odata.nextLink":"` + next + `"}`)
		case systems + "?page=2":
			if fault.Load() == 2 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			if fault.Load() == 3 {
				write(`{"Members@odata.count":3,"Members":[{"@odata.id":"` + systems + `/two"}]}`)
				return
			}
			write(`{"Members@odata.count":3,"Members":[{"@odata.id":"` + systems + `/two"},{"@odata.id":"` + systems + `/virtual"}]}`)
		case systems + "/one", systems + "/two", systems + "/virtual":
			id, kind, power := firstID, "Physical", "On"
			if r.URL.Path == systems+"/two" {
				id, power = secondID, "Off"
				if fault.Load() == 5 {
					id = firstID
				}
			}
			if r.URL.Path == systems+"/virtual" {
				kind = "Virtual"
			}
			write(`{"@odata.id":"` + r.URL.Path + `","UUID":"` + id + `","Name":"Node","SystemType":"` + kind + `","PowerState":"` + power + `"}`)
		default:
			t.Errorf("unexpected Redfish request: %s", r.URL.RequestURI())
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	profile := cloud.RedfishProfile{Ref: ref, Config: redfish.Config{Endpoint: server.URL, Site: "site-a", UUID: uuid,
		User: "reader", Password: "fixture-secret",
		CAPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}))}}
	path := filepath.Join(t.TempDir(), "redfish.json")
	saveProfile := func() {
		t.Helper()
		data, err := json.Marshal(profile)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	saveProfile()
	app, err := routers.NewAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, endpoint, data string, status int) string {
		t.Helper()
		r := httptest.NewRequestWithContext(ctx, method, projects+endpoint, strings.NewReader(data))
		r.Header.Set("Authorization", "Bearer "+vSphereFixtureBearer)
		if data != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if w.Code != status || strings.Contains(w.Body.String(), profile.Password) {
			t.Fatalf("unexpected Redfish account response: %d %s", w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	request("POST", "project/cloud-accounts", `{"id":"bmc","provider":"redfish","credential_ref":"`+ref+`"}`, 201)
	verify := []string{"verify-redfish", "tenant", "bmc"}
	scan := []string{"scan-redfish", "tenant", "bmc", "site-a", "bare-metal"}
	if !cloudmanager.ValidCommand(verify) || !cloudmanager.ValidCommand(scan) {
		t.Fatal("Redfish CLI commands unavailable")
	}
	if err := cloud.RunRedfish(ctx, db, scan, path, nil); err == nil || reads.Load() != 0 {
		t.Fatalf("pending account reached the BMC: %v", err)
	}
	for _, failure := range []int32{1, 2, 3, 4, 5} {
		fault.Store(failure)
		if err := cloud.RunRedfish(ctx, db, verify, path, nil); err == nil {
			t.Fatalf("invalid BMC response verified account: %d", failure)
		}
		if failure == 1 && authenticatedReads.Load() != 0 {
			t.Fatal("credentials crossed an unverified BMC identity")
		}
	}
	fault.Store(0)
	if err := cloud.RunRedfish(ctx, db, verify, path, nil); err != nil {
		t.Fatal(err)
	}
	binding, err := identity.NewStore(db).CloudAccountBinding(ctx, "tenant", "bmc")
	if err != nil || binding.Mode != "read_only" || binding.RemoteIdentity != "redfish:"+uuid+":site-a" {
		t.Fatalf("wrong Redfish binding: %+v %v", binding, err)
	}
	profile.Site = "wrong-site"
	saveProfile()
	before := authenticatedReads.Load()
	if err := cloud.RunRedfish(ctx, db, verify, path, nil); !provider.IsFailure(err, provider.Unauthorized) || authenticatedReads.Load() != before {
		t.Fatalf("site mismatch authenticated against the BMC: %v", err)
	}
	profile.Site = "site-a"
	saveProfile()
	if err := cloud.RunRedfish(ctx, db, []string{"scan-redfish", "tenant", "bmc", "-", "region"}, path, nil); err != nil {
		t.Fatal(err)
	}
	if err := cloud.RunRedfish(ctx, db, scan, path, nil); err != nil {
		t.Fatal(err)
	}
	const assets = "/assets?kind=bare-metal"
	body := request("GET", "project"+assets, "", 200)
	if !strings.Contains(body, firstID) || !strings.Contains(body, secondID) || strings.Count(body, `"name":"Node"`) != 2 {
		t.Fatalf("wrong bare-metal inventory: %s", body)
	}
	if body := request("GET", "other"+assets, "", 200); strings.Contains(body, firstID) || strings.Contains(body, secondID) {
		t.Fatal("bare-metal inventory crossed projects")
	}
	fault.Store(2)
	if err := cloud.RunRedfish(ctx, db, scan, path, nil); err == nil {
		t.Fatal("incomplete inventory succeeded")
	}
	if body := request("GET", "project"+assets, "", 200); !strings.Contains(body, secondID) || strings.Contains(body, "missing_candidate") {
		t.Fatalf("incomplete scan changed published inventory: %s", body)
	}
	fault.Store(0)
	key := inventory.Key{Tenant: "tenant", Account: "bmc", Region: "site-a", Kind: provider.BareMetal}
	runProviderScheduledInventory(t, db, key, ref, path)
	if _, err := db.ExecContext(ctx, `UPDATE cmp_cloud_accounts SET mode='write_enabled' WHERE account_id='bmc'`); err != nil {
		t.Fatal(err)
	}
	scope := identity.Scope{Tenant: "tenant", Project: "project", Principal: "admin"}
	if _, err := identity.NewStore(db).AuthorizeAccount(ctx, scope, "bmc", identity.Execute); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("Redfish write authorization did not fail closed: %v", err)
	}
}
