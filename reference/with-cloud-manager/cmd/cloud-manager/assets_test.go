package main

import (
	"crypto/sha256"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/birdordev/plumego/x/openapi"

	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
)

func TestAcceptanceProjectAssetAPI(t *testing.T) {
	db := dbtest.Open(t)
	ctx := t.Context()
	for _, migrate := range []func() error{
		func() error { return identity.Migrate(ctx, db) },
		func() error { return inventory.Migrate(ctx, db) },
		func() error { return identity.MigrateServiceTokens(ctx, db) },
	} {
		if err := migrate(); err != nil {
			t.Fatal(err)
		}
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO cmp_tenants VALUES ('tenant'),('other');
		INSERT INTO cmp_projects VALUES ('tenant','project'),('tenant','shared'),('tenant','hidden'),('other','project');
		INSERT INTO cmp_principals (tenant_id,principal_id,kind,issuer,subject) VALUES ('tenant','agent','service','local','agent');
		INSERT INTO cmp_memberships (tenant_id,project_id,principal_id,role) VALUES
			('tenant','project','agent','viewer'),('tenant','shared','agent','viewer');
		INSERT INTO cmp_cloud_accounts (tenant_id,account_id,project_id,provider,remote_identity,credential_ref,mode)
			VALUES ('tenant','owned','project','aws-ec2','aws:1','profile/1','read_only'),
			('tenant','private','hidden','aws-ec2','aws:2','profile/2','read_only'),
			('other','owned','project','aws-ec2','aws:3','profile/3','read_only');
		INSERT INTO cmp_cloud_account_grants (tenant_id,account_id,project_id) VALUES ('tenant','owned','shared');
		INSERT INTO cmp_inventory_scopes (tenant_id,account_id,region,kind,credential_revision,lease_until,completed_at)
			SELECT tenant_id,account_id,'us-east-1','instance',credential_revision,now(),now() FROM cmp_cloud_accounts;
		INSERT INTO cmp_inventory_objects (scope_id,external_id,name,status,seen_generation)
			SELECT scope_id, id, tenant_id || '/' || account_id|| '/' || id,'running',1 FROM cmp_inventory_scopes
			CROSS JOIN (VALUES ('one'),('two')) AS ids(id);`)
	token := strings.Repeat("fixture-token-material-", 3)
	hash := sha256.Sum256([]byte(token))
	exec("INSERT INTO cmp_service_tokens(token_hash,tenant_id,principal_id,expires_at) VALUES ($1,'tenant','agent',now()+interval '10 minutes')", hash[:])

	app, err := newAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	get := func(path, bearer string, want int) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		app.ServeHTTP(w, req)
		if w.Code != want || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("GET %s returned %d %s", path, w.Code, w.Body)
		}
		return w
	}
	decode := func(w *httptest.ResponseRecorder) inventory.Assets {
		t.Helper()
		var payload struct{ Data inventory.Assets }
		if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		return payload.Data
	}
	spec := httptest.NewRecorder()
	app.ServeHTTP(spec, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))
	var doc openapi.Document
	if spec.Code != http.StatusOK || json.Unmarshal(spec.Body.Bytes(), &doc) != nil {
		t.Fatal("invalid OpenAPI document")
	}
	op := doc.Paths["/v1/tenants/{tenant}/projects/{project}/assets"].Get
	if op == nil || op.OperationID != "listProjectAssets" || len(op.Security) != 1 ||
		op.Responses["403"].Description == "" || op.Responses["503"].Description == "" {
		t.Fatalf("asset contract missing: %+v", op)
	}
	workloads := doc.Paths["/v1/tenants/{tenant}/projects/{project}/assets/kubernetes-clusters/{asset}/workloads"].Get
	if workloads == nil || workloads.OperationID != "readProjectClusterWorkloads" || len(workloads.Security) != 1 ||
		workloads.Responses["409"].Description == "" || workloads.Responses["404"].Description == "" {
		t.Fatalf("workload contract missing: %+v", workloads)
	}
	item := workloads.Responses["200"].Content["application/json"].Schema.Properties["data"].Properties["workloads"].Items
	if item == nil || item.Properties["namespace"].Type != "string" || item.Properties["ready"].Type != "integer" {
		t.Fatalf("workload response schema missing: %+v", item)
	}
	base := "/v1/tenants/tenant/projects/project/assets"
	if got := get(base, "", 401).Header().Get("WWW-Authenticate"); !strings.Contains(got, "Bearer") {
		t.Fatalf("missing challenge: %s", got)
	}
	get("/v1/tenants/other/projects/project/assets", token, 403)
	get("/v1/tenants/tenant/projects/hidden/assets", token, 403)
	get(base+"?limit=1&limit=2", token, 400)
	first := decode(get(base+"?limit=1&kind=instance", token, 200))
	if len(first.Items) != 1 || first.Items[0].Name != "tenant/owned/one" ||
		first.Items[0].LastFullSync == nil || first.Next == "" {
		t.Fatalf("unexpected first page: %+v", first)
	}
	second := decode(get(base+"?limit=1&kind=instance&cursor="+url.QueryEscape(first.Next), token, 200))
	if len(second.Items) != 1 || second.Items[0].Name != "tenant/owned/two" || second.Next != "" {
		t.Fatalf("unexpected second page: %+v", second)
	}
	get(base+"?kind=volume&cursor="+url.QueryEscape(first.Next), token, 400)
	sharedPath := "/v1/tenants/tenant/projects/shared/assets"
	if shared := decode(get(sharedPath+"?q=TWO", token, 200)); len(shared.Items) != 1 || shared.Items[0].ID != "two" {
		t.Fatalf("shared or searchable asset missing: %+v", shared)
	}
	history := base + "/changes"
	exec(`UPDATE cmp_inventory_objects AS o SET status='stopped' FROM cmp_inventory_scopes AS s
		WHERE o.scope_id=s.scope_id AND s.tenant_id='tenant' AND s.account_id='owned' AND o.external_id='one'`)
	readChanges := func(path string) inventory.Changes {
		t.Helper()
		var response struct{ Data inventory.Changes }
		if err := json.Unmarshal(get(path, token, 200).Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response.Data
	}
	changes := readChanges(history + "?limit=1")
	if len(changes.Items) != 1 || changes.Items[0].Action != "changed" ||
		changes.Items[0].PreviousStatus == nil || *changes.Items[0].PreviousStatus != "running" ||
		changes.Items[0].Status != "stopped" || changes.Next == "" {
		t.Fatalf("unexpected history: %+v", changes)
	}
	rest := readChanges(history + "?cursor=" + url.QueryEscape(changes.Next))
	if len(rest.Items) != 2 || rest.Items[0].Action != "discovered" || rest.Items[1].Action != "discovered" {
		t.Fatalf("unexpected history continuation: %+v", rest)
	}
	get("/v1/tenants/other/projects/project/assets/changes", token, 403)
	export := sharedPath + "/export"
	exec(`UPDATE cmp_inventory_objects AS o SET name=' =1+1' FROM cmp_inventory_scopes AS s
		WHERE o.scope_id=s.scope_id AND s.tenant_id='tenant' AND s.account_id='owned' AND o.external_id='one'`)
	csvResponse := get(export+"?kind=instance", token, 200)
	csvRows, err := csv.NewReader(csvResponse.Body).ReadAll()
	if err != nil || len(csvRows) != 3 || len(csvRows[1]) != 9 ||
		csvRows[1][3] != "one" || csvRows[1][4] != "' =1+1" ||
		csvRows[2][3] != "two" || csvResponse.Header().Get("Content-Disposition") != `attachment; filename="assets.csv"` {
		t.Fatalf("unexpected authorized CSV: %+v %v", csvRows, err)
	}
	get("/v1/tenants/other/projects/project/assets/export", token, 403)
	get(export+"?limit=10", token, 400)
	exec(`INSERT INTO cmp_inventory_objects (scope_id,external_id,name,status,seen_generation)
		SELECT scope_id,'extra-'||n,'added','running',1 FROM cmp_inventory_scopes
		CROSS JOIN generate_series(1,1999) AS n WHERE tenant_id='tenant' AND account_id='owned'`)
	if failure := get(export, token, 422); failure.Header().Get("Content-Disposition") != "" {
		t.Fatal("oversized export started a CSV response")
	}
	if narrowed := get(export+"?q=1%2B1", token, 200); !strings.Contains(narrowed.Body.String(), "' =1+1") {
		t.Fatal("filtered export did not return the expected asset")
	}
	exec(`INSERT INTO cmp_inventory_scopes (tenant_id,account_id,region,kind,credential_revision,lease_until)
		SELECT tenant_id,account_id,'us-east-1','volume',credential_revision,now() FROM cmp_cloud_accounts;
		INSERT INTO cmp_inventory_objects (scope_id,external_id,name,status,seen_generation)
			SELECT scope_id,'target',tenant_id||'/'||account_id,'available',1
			FROM cmp_inventory_scopes WHERE kind='volume';
		INSERT INTO cmp_inventory_objects (scope_id,external_id,name,status,seen_generation)
			SELECT scope_id,'hidden-target','private target','available',1 FROM cmp_inventory_scopes
			WHERE kind='volume' AND account_id='private';
		UPDATE cmp_inventory_objects AS o SET links='[
		    {"kind":"volume","id":"target","relation":"uses_volume"},
		    {"kind":"volume","id":"target","relation":"uses_volume"},
		    {"kind":"volume","id":"hidden-target","relation":"uses_volume"}]'
		FROM cmp_inventory_scopes AS s WHERE o.scope_id=s.scope_id AND s.tenant_id='tenant'
			AND s.account_id='owned' AND s.kind='instance' AND o.external_id='one';`)
	topology := sharedPath + "/topology"
	readTopology := func(path string) inventory.Topology {
		t.Helper()
		var payload struct{ Data inventory.Topology }
		if err := json.Unmarshal(get(path, token, 200).Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		return payload.Data
	}
	graph := readTopology(topology + "?kind=instance&q=1%2B1")
	if len(graph.Nodes) != 1 || len(graph.Nodes[0].Links) != 1 ||
		graph.Nodes[0].Links[0].ID != "target" || graph.Nodes[0].Links[0].Name != "tenant/owned" {
		t.Fatalf("incorrect topology: %+v", graph)
	}
	exec(`UPDATE cmp_inventory_objects AS o SET removed_at=now() FROM cmp_inventory_scopes AS s
		WHERE o.scope_id=s.scope_id AND s.tenant_id='tenant' AND s.account_id='owned'
		  AND s.kind='volume' AND o.external_id='target'`)
	graph = readTopology(topology + "?kind=instance&q=1%2B1")
	if len(graph.Nodes) != 1 || len(graph.Nodes[0].Links) != 0 {
		t.Fatalf("confirmed removal left a visible edge: %+v", graph)
	}
	firstGraph := readTopology(topology + "?kind=instance&limit=1")
	followingGraph := readTopology(topology + "?kind=instance&limit=1&cursor=" + url.QueryEscape(firstGraph.Next))
	if len(firstGraph.Nodes) != 1 || firstGraph.Next == "" || len(followingGraph.Nodes) != 1 ||
		firstGraph.Nodes[0].Asset.ID == followingGraph.Nodes[0].Asset.ID {
		t.Fatalf("incorrect topology pagination: %+v / %+v", firstGraph, followingGraph)
	}
	get("/v1/tenants/other/projects/project/assets/topology", token, 403)
	get(topology+"?limit=51", token, 400)
	exec("DELETE FROM cmp_cloud_account_grants")
	if shared := decode(get(sharedPath, token, 200)); len(shared.Items) != 0 {
		t.Fatalf("revoked account grant exposed assets: %+v", shared)
	}
	if changes := readChanges(sharedPath + "/changes"); len(changes.Items) != 0 {
		t.Fatalf("revoked share exposed history: %+v", changes)
	}
	if graph := readTopology(topology); len(graph.Nodes) != 0 {
		t.Fatalf("revoked grant exposed topology: %+v", graph)
	}
	empty, err := csv.NewReader(get(export, token, 200).Body).ReadAll()
	if err != nil || len(empty) != 1 {
		t.Fatalf("revoked share exported data: %+v %v", empty, err)
	}
	exec("UPDATE cmp_service_tokens SET revoked_at=now()")
	get(export, token, 401)
	get(base, token, 401)
}
