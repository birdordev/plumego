package apitest

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"with-cloud-manager/app/routers"
	"with-cloud-manager/app/services/cloud"
	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/openstack"
)

func TestAcceptanceOpenStackScan(t *testing.T) {
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
	if _, err := db.ExecContext(ctx, `INSERT INTO cmp_tenants VALUES ('tenant');
		INSERT INTO cmp_projects VALUES ('tenant','project');
		INSERT INTO cmp_principals (tenant_id,principal_id,kind,issuer,subject) VALUES ('tenant','reader','service','fixture','reader');
		INSERT INTO cmp_memberships (tenant_id,project_id,principal_id,role) VALUES ('tenant','project','reader','viewer');
		INSERT INTO cmp_cloud_accounts (tenant_id,account_id,project_id,provider,credential_ref)
		VALUES ('tenant','account','project','openstack','credential/profile');`); err != nil {
		t.Fatal(err)
	}
	const token = "fixture-neutron-rule-reader-access-token"
	digest := sha256.Sum256([]byte(token))
	if _, err := db.ExecContext(ctx, `INSERT INTO cmp_service_tokens(token_hash,tenant_id,principal_id,expires_at)
		VALUES ($1,'tenant','reader',now()+interval '5 minutes')`, digest[:]); err != nil {
		t.Fatal(err)
	}
	var origin string
	var auths, firstPage, secondPage atomic.Int32
	router := http.NewServeMux()
	router.HandleFunc("POST /v3/auth/tokens", func(w http.ResponseWriter, _ *http.Request) {
		auths.Add(1)
		w.Header().Set("X-Subject-Token", "fixture-token")
		w.WriteHeader(http.StatusCreated)
		if _, err := fmt.Fprintf(w, `{"token":{"project":{"id":"project"},"catalog":[
			{"type":"compute","endpoints":[{"region_id":"RegionOne","interface":"public","url":%q}]},
			{"type":"network","endpoints":[{"region_id":"RegionOne","interface":"public","url":%q}]}
		]}}`, origin+"/compute/v2.1", origin+"/network"); err != nil {
			t.Error(err)
		}
	})
	const groupID, ruleID = "00000000-0000-0000-0000-000000000015", "00000000-0000-0000-0000-000000000016"
	router.HandleFunc("GET /network/v2.0/security-groups", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Auth-Token") != "fixture-token" {
			t.Error("missing network token")
		}
		if _, err := fmt.Fprintf(w, `{"security_groups":[{"id":%q,"name":"example group"}]}`, groupID); err != nil {
			t.Error(err)
		}
	})
	router.HandleFunc("GET /network/v2.0/security-group-rules", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Auth-Token") != "fixture-token" || r.URL.Query().Get("project_id") != "project" {
			t.Error("missing scoped rule inventory authorization")
		}
		if _, err := fmt.Fprintf(w, `{"security_group_rules":[{"id":%q,"security_group_id":%q,"project_id":"project",
			"direction":"ingress","ethertype":"IPv4","protocol":"tcp","port_range_min":443,"port_range_max":443,
			"remote_ip_prefix":"192.0.2.0/24"}]}`, ruleID, groupID); err != nil {
			t.Error(err)
		}
	})
	router.HandleFunc("GET /compute/v2.1/servers/detail", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Auth-Token") != "fixture-token" {
			t.Error("missing fixture token")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("marker") == "" {
			firstPage.Add(1)
			if _, err := fmt.Fprint(w, `{"servers":[{"id":"one"}],"servers_links":[{"rel":"next","href":"?marker=one&limit=200"}]}`); err != nil {
				t.Error(err)
			}
			return
		}
		if secondPage.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		if _, err := fmt.Fprint(w, `{"servers":[{"id":"two"}]}`); err != nil {
			t.Error(err)
		}
	})
	server := httptest.NewTLSServer(router)
	defer server.Close()
	origin = server.URL
	profile := cloud.OpenStackProfile{Ref: "bad/profile", Config: openstack.Config{
		Site: "site", Project: "project", IdentityURL: origin + "/v3",
		ApplicationID: "fixture-id", Secret: "fixture-secret", EndpointOrigins: []string{origin},
	}}
	path := filepath.Join(t.TempDir(), "openstack.json")
	writeProfile := func() {
		t.Helper()
		data, err := json.Marshal(profile)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	verify := []string{"verify-openstack", "tenant", "account"}
	scan := []string{"scan-openstack", "tenant", "account", "RegionOne", "instance"}
	writeProfile()
	if err := cloud.RunOpenStack(ctx, db, verify, path, server.Client()); err == nil || auths.Load() != 0 {
		t.Fatalf("unbound credential sent request: count %d, err %v", auths.Load(), err)
	}
	profile.Ref = "credential/profile"
	writeProfile()
	if err := cloud.RunOpenStack(ctx, db, scan, path, server.Client()); err == nil || auths.Load() != 0 {
		t.Fatalf("pending identity allowed scan: count %d, err %v", auths.Load(), err)
	}
	if err := cloud.RunOpenStack(ctx, db, verify, path, server.Client()); err != nil {
		t.Fatal(err)
	}
	var remote, mode string
	if err := db.QueryRowContext(ctx, "SELECT remote_identity,mode FROM cmp_cloud_accounts").Scan(&remote, &mode); err != nil || remote != "site:project" || mode != "read_only" {
		t.Fatalf("account state %s/%s: %v", remote, mode, err)
	}
	if err := cloud.RunOpenStack(ctx, db, scan, path, server.Client()); !provider.IsFailure(err, provider.Throttled) {
		t.Fatalf("first scan: %v", err)
	}
	if err := cloud.RunOpenStack(ctx, db, scan, path, server.Client()); err != nil {
		t.Fatal(err)
	}
	var assets int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM cmp_inventory_objects").Scan(&assets); err != nil ||
		assets != 2 || firstPage.Load() != 1 || secondPage.Load() != 2 {
		t.Fatalf("asset count %d, first page %d, second page %d: %v", assets, firstPage.Load(), secondPage.Load(), err)
	}
	for _, kind := range []string{"security-group", "security-group-rule"} {
		if err := cloud.RunOpenStack(ctx, db, []string{"scan-openstack", "tenant", "account", "RegionOne", kind}, path, server.Client()); err != nil {
			t.Fatalf("scan %s: %v", kind, err)
		}
	}
	var parent, kind, status string
	if err := db.QueryRowContext(ctx, `SELECT o.links->0->>'id',s.kind,o.status FROM cmp_inventory_objects AS o
		JOIN cmp_inventory_scopes AS s USING (scope_id) WHERE o.external_id=$1`, ruleID).Scan(&parent, &kind, &status); err != nil || parent != groupID || kind != "security-group-rule" || status != "available" {
		t.Fatalf("scanned rule parent=%q kind=%q status=%q: %v", parent, kind, status, err)
	}
	app, err := routers.NewAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/tenants/tenant/projects/project/assets/topology?kind=security-group-rule", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	app.ServeHTTP(w, r)
	var body struct{ Data inventory.Topology }
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != http.StatusOK || len(body.Data.Nodes) != 1 {
		t.Fatalf("HTTP topology code=%d body=%s error=%v", w.Code, w.Body, err)
	}
	node := body.Data.Nodes[0]
	if node.Asset.ID != ruleID || node.Asset.OwnershipSource != "security_group" || len(node.Links) != 1 || node.Links[0].ID != groupID {
		t.Fatalf("HTTP returned incomplete rule ownership or parent topology: %+v", node)
	}
}
