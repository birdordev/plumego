package apitest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"with-cloud-manager/app/commands"
	"with-cloud-manager/app/integration/testsupport"
	"with-cloud-manager/app/routers"
	"with-cloud-manager/internal/audit"
	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/operations"
)

func TestAcceptanceServiceCatalog(t *testing.T) {
	ctx := t.Context()
	db := dbtest.Open(t)
	for _, migrate := range []func(context.Context, *sql.DB) error{
		identity.Migrate, identity.MigrateServiceTokens, identity.MigrateAccounts, inventory.Migrate, operations.Migrate, audit.Migrate,
	} {
		if err := migrate(ctx, db); err != nil {
			t.Fatal(err)
		}
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO cmp_tenants VALUES ('tenant'),('separate');
		INSERT INTO cmp_projects VALUES ('tenant','project'),('tenant','other'),('separate','project');
		INSERT INTO cmp_principals (tenant_id,principal_id,kind,issuer,subject) VALUES
		('tenant','catalog-admin','service','fixture','admin'),('tenant','operator','service','fixture','operator'),
		('tenant','viewer','service','fixture','viewer'),('separate','outsider','service','fixture','outsider');
		INSERT INTO cmp_memberships (tenant_id,project_id,principal_id,role) VALUES
		('tenant','project','catalog-admin','admin'),('tenant','project','operator','operator'),('tenant','project','viewer','viewer'),
		('tenant','other','catalog-admin','admin'),('separate','project','outsider','admin');
		INSERT INTO cmp_cloud_accounts (tenant_id,account_id,project_id,provider,credential_ref,remote_identity,mode)
		VALUES ('tenant','account','project','aws-ec2','fixture/ref','aws:fixture','write_enabled');
		INSERT INTO cmp_inventory_scopes (tenant_id,account_id,region,kind,credential_revision,lease_until)
		SELECT 'tenant','account','us-east-1',kind,1,now() FROM (VALUES ('image'),('subnet'),('security-group')) AS v(kind);
		INSERT INTO cmp_inventory_objects (scope_id,external_id,name,status,seen_generation)
		SELECT scope_id,asset,asset,'available',1 FROM cmp_inventory_scopes
		JOIN (VALUES ('image','ami-01234567'),('subnet','subnet-01234567'),('security-group','sg-01234567')) AS v(kind,asset) USING (kind);
		UPDATE cmp_inventory_objects SET links='[{"kind":"network","relation":"in_network","id":"vpc-one"}]'
		WHERE external_id IN ('sg-01234567','subnet-01234567');
		INSERT INTO cmp_instance_offerings VALUES ('tenant','account','us-east-1','t3.micro');
		INSERT INTO cmp_instance_operation_budgets (tenant_id,project_id,max_managed_instances) VALUES ('tenant','project',1);
		INSERT INTO cmp_instance_approval_rules VALUES ('tenant','project','create');`)
	token := func(principal string) string { return "fixture-token-for-catalog-" + principal }
	for _, principal := range []string{"catalog-admin", "operator", "viewer", "outsider"} {
		hash := sha256.Sum256([]byte(token(principal)))
		exec(`INSERT INTO cmp_service_tokens(token_hash,tenant_id,principal_id,expires_at)
			SELECT $1,tenant_id,principal_id,now()+interval '5 minutes' FROM cmp_principals WHERE principal_id=$2`, hash[:], principal)
	}
	app, err := routers.NewAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Prepare(); err != nil {
		t.Fatal(err)
	}
	send := func(method, path, principal, body, match, key string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if principal != "" {
			r.Header.Set("Authorization", "Bearer "+token(principal))
		}
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		if match != "" {
			r.Header.Set("If-Match", match)
		}
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		return w
	}
	check := func(w *httptest.ResponseRecorder, status int) *httptest.ResponseRecorder {
		t.Helper()
		if w.Code != status || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "fixture/ref") {
			t.Fatalf("want %d, got %d %s", status, w.Code, w.Body.String())
		}
		return w
	}
	const base = "/v1/tenants/tenant/projects/project"
	const list = base + "/service-catalog"
	const item = list + "/small-vm"
	const launch = item + "/launch"
	const spec = `"title":"Small VM","account":"account","region":"us-east-1","image_id":"ami-01234567","size":"t3.micro","network_id":"subnet-01234567","security_group_id":"sg-01234567"`
	const create = `{"id":"small-vm",` + spec + "}"
	check(send("GET", list, "", "", "", ""), 401)
	check(send("GET", list, "outsider", "", "", ""), 403)
	check(send("GET", list+"?limit=2&limit=5", "viewer", "", "", ""), 400)
	check(send("GET", list+"?include_disabled=true", "viewer", "", "", ""), 403)
	check(send("POST", list, "operator", create, "", ""), 403)
	check(send("POST", list, "catalog-admin", strings.Replace(create, "t3.micro", "unsupported", 1), "", ""), 400)
	check(send("POST", list, "catalog-admin", strings.Replace(create, `"title":`, `"title":"Duplicate","title":`, 1), "", ""), 400)
	created := check(send("POST", list, "catalog-admin", create, "", ""), 201)
	if created.Header().Get("ETag") != `"1"` || created.Header().Get("Location") != item {
		t.Fatalf("unexpected create headers: %v", created.Header())
	}
	check(send("POST", list, "catalog-admin", create, "", ""), 409)
	check(send("POST", list, "catalog-admin", strings.Replace(create, "small-vm", "tier-two", 1), "", ""), 201)
	var firstPage operations.CatalogPage
	testsupport.DecodeProjectData(t, check(send("GET", list+"?limit=1", "viewer", "", "", ""), 200), &firstPage)
	if len(firstPage.Items) != 1 || firstPage.Next != "small-vm" {
		t.Fatalf("unexpected first page: %+v", firstPage)
	}
	var nextPage operations.CatalogPage
	testsupport.DecodeProjectData(t, check(send("GET", list+"?limit=1&after="+firstPage.Next, "viewer", "", "", ""), 200), &nextPage)
	if len(nextPage.Items) != 1 || nextPage.Items[0].ID != "tier-two" || nextPage.Next != "" {
		t.Fatalf("unexpected next page: %+v", nextPage)
	}
	check(send("GET", strings.Replace(item, "/project/", "/other/", 1), "catalog-admin", "", "", ""), 403)
	check(send("GET", item+"/versions/1", "viewer", "", "", ""), 403)
	check(send("POST", launch, "viewer", `{"version":1,"name":"vm"}`, "", "key"), 403)
	check(send("POST", launch, "operator", `{"version":1,"name":"vm"}`, "", ""), 400)
	const request = `{"version":1,"name":"catalog VM"}`
	var receipt operations.Operation
	accepted := check(send("POST", launch, "operator", request, "", "first"), 202)
	testsupport.DecodeProjectData(t, accepted, &receipt)
	if receipt.CatalogTemplate != "small-vm" || receipt.CatalogVersion != 1 || receipt.Status != "awaiting_approval" ||
		accepted.Header().Get("Location") != base+"/instance-operations/"+receipt.ID {
		t.Fatalf("unexpected launch receipt: %+v", receipt)
	}
	check(send("POST", launch, "operator", request, "", "another"), 429)
	check(send("POST", launch, "operator", strings.Replace(request, "catalog VM", "changed VM", 1), "", "first"), 409)
	var saved operations.Operation
	testsupport.DecodeProjectData(t, check(send("GET", base+"/operations/"+receipt.ID, "viewer", "", "", ""), 200), &saved)
	if !reflect.DeepEqual(saved, receipt) {
		t.Fatalf("operation details lost launch provenance: %+v", saved)
	}

	revisionResult := make(chan *httptest.ResponseRecorder, 2)
	for range 2 {
		go func() { revisionResult <- send("PUT", item, "catalog-admin", "{"+spec+"}", `"1"`, "") }()
	}
	codes := map[int]int{}
	for range 2 {
		w := <-revisionResult
		codes[w.Code]++
	}
	if codes[200] != 1 || codes[412] != 1 {
		t.Fatalf("concurrent publications: %v", codes)
	}
	check(send("POST", launch, "operator", request, "", "old-version"), 412)
	check(send("DELETE", item, "catalog-admin", "", `"1"`, ""), 412)
	retired := check(send("DELETE", item, "catalog-admin", "", `"2"`, ""), 200)
	if retired.Header().Get("ETag") != `"3"` {
		t.Fatalf("retirement did not change ETag: %v", retired.Header())
	}
	check(send("GET", item, "viewer", "", "", ""), 403)
	check(send("GET", item, "catalog-admin", "", "", ""), 200)
	check(send("POST", launch, "operator", `{"version":2,"name":"catalog VM"}`, "", "retired"), 412)
	var replay operations.Operation
	testsupport.DecodeProjectData(t, check(send("POST", launch, "operator", request, "", "first"), 202), &replay)
	if replay.ID != receipt.ID {
		t.Fatalf("retirement changed the idempotent receipt: %+v", replay)
	}
	clientServer := httptest.NewTLSServer(app)
	defer clientServer.Close()
	var output bytes.Buffer
	if err := commands.RunCatalogClient(ctx, []string{"launch", "tenant", "project", "small-vm", "--idempotency-key", "first"},
		clientServer.URL, token("operator"), strings.NewReader(request), &output, clientServer.Client().Transport); err != nil {
		t.Fatal(err)
	}
	var cli operations.Operation
	if err := json.Unmarshal(output.Bytes(), &cli); err != nil || !reflect.DeepEqual(cli, replay) {
		t.Fatalf("CLI lost operation provenance: %+v, %v", cli, err)
	}
	var original operations.CatalogVersion
	testsupport.DecodeProjectData(t, check(send("GET", item+"/versions/1", "catalog-admin", "", "", ""), 200), &original)
	if original.Version != 1 || original.Title != "Small VM" {
		t.Fatalf("historical version lost: %+v", original)
	}

	exec("ALTER TABLE cmp_project_audit ADD CONSTRAINT block_catalog_publish CHECK (category <> 'catalog' OR action <> 'publish') NOT VALID")
	check(send("PUT", item, "catalog-admin", "{"+spec+"}", `"3"`, ""), 503)
	var unchanged operations.CatalogItem
	testsupport.DecodeProjectData(t, check(send("GET", item, "catalog-admin", "", "", ""), 200), &unchanged)
	if unchanged.Enabled || unchanged.Version != 2 || unchanged.Revision != 3 {
		t.Fatalf("failed audit changed the template: %+v", unchanged)
	}
	check(send("GET", item+"/versions/3", "catalog-admin", "", "", ""), 403)
	exec("ALTER TABLE cmp_project_audit DROP CONSTRAINT block_catalog_publish")
	output.Reset()
	if err := commands.RunCatalogClient(ctx, []string{"publish", "tenant", "project", "small-vm", "--if-match", "3"},
		clientServer.URL, token("catalog-admin"), strings.NewReader("{"+spec+"}"), &output, clientServer.Client().Transport); err != nil {
		t.Fatal(err)
	}
	var restored operations.CatalogItem
	if err := json.Unmarshal(output.Bytes(), &restored); err != nil || !restored.Enabled || restored.Version != 3 || restored.Revision != 4 {
		t.Fatalf("CLI did not publish current template: %+v, %v", restored, err)
	}
	exec("DELETE FROM cmp_instance_offerings")
	check(send("POST", launch, "operator", `{"version":3,"name":"catalog VM"}`, "", "dependency-deleted"), 400)
	var events audit.Page
	testsupport.DecodeProjectData(t, check(send("GET", base+"/audit?category=catalog&subject_id=small-vm", "catalog-admin", "", "", ""), 200), &events)
	if len(events.Items) != 4 || events.Items[0].Action != "publish" || events.Items[0].Revision != 4 ||
		events.Items[1].Action != "retire" || events.Items[1].Account != "account" || events.Items[1].Actor != "catalog-admin" {
		t.Fatalf("unexpected catalog audit: %+v", events.Items)
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM cmp_instance_launches").Scan(&count); err != nil || count != 1 {
		t.Fatalf("want one persisted launch, got %d: %v", count, err)
	}
}
