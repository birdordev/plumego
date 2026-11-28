package cloudtest

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"with-cloud-manager/app/commands"
	"with-cloud-manager/app/integration/testsupport"
	"with-cloud-manager/internal/audit"
	"with-cloud-manager/internal/operations"
)

func TestAcceptanceProjectOperationBudget(t *testing.T) {
	db, app := prepareFreshnessReport(t)
	const project = "/v1/tenants/lab/projects/compute"
	const path = project + "/operation-budget"
	const admin = "fixture-project-operation-budget-admin"
	seedOperationBudgetAdmin(t, db, admin)
	read := func(path string) operations.ProjectBudget {
		t.Helper()
		var budget operations.ProjectBudget
		testsupport.DecodeProjectData(t, scheduleResponse(t, app, "GET", path, freshnessToken, "", "", 200), &budget)
		return budget
	}
	put := func(token, match, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		return scheduleResponse(t, app, "PUT", path, token, match, body, status)
	}
	if budget := read(path); budget != (operations.ProjectBudget{Revision: 1, MaxActive: 100, MaxManagedInstances: 100}) {
		t.Fatalf("wrong default budget: %+v", budget)
	}
	scheduleResponse(t, app, "GET", "/v1/tenants/foreign/projects/compute/operation-budget", freshnessToken, "", "", 403)
	scheduleResponse(t, app, "GET", strings.Replace(path, "/compute/", "/denied/", 1), freshnessToken, "", "", 403)
	const limits = `{"max_active":4,"max_managed_instances":3}`
	put(freshnessToken, `"1"`, limits, 403)
	put(admin, "", limits, 412)
	for _, invalid := range []string{
		`{"max_active":2}`, `{"max_active":0,"max_managed_instances":0}`,
		`{"max_active":2,"max_managed_instances":null}`, `{"max_active":2,"max_managed_instances":100001}`,
		`{"max_active":2,"max_active":3,"max_managed_instances":1}`,
	} {
		put(admin, `"1"`, invalid, 400)
	}
	first := put(admin, `"1"`, limits, 200)
	if first.Header().Get("ETag") != `"2"` || first.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("budget response lacks cache controls or ETag: %v", first.Header())
	}
	if budget := read(path); budget.Revision != 2 || budget.MaxActive != 4 || budget.MaxManagedInstances != 3 {
		t.Fatalf("updated budget mismatch: %+v", budget)
	}

	operation := func(target, key, match, payload string, status int) operations.Operation {
		t.Helper()
		request := httptest.NewRequestWithContext(t.Context(), "POST", project+target, strings.NewReader(payload))
		request.Header.Set("Authorization", "Bearer "+admin)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", key)
		if match != "" {
			request.Header.Set("If-Match", match)
		}
		response := httptest.NewRecorder()
		app.ServeHTTP(response, request)
		requireFreshnessStatus(t, response, status)
		var value operations.Operation
		if status == http.StatusAccepted {
			testsupport.DecodeProjectData(t, response, &value)
		}
		return value
	}

	const createInput = `{"account":"ali","region":"cn-test","name":"budget fixture","image_id":"image-one","size":"fixture-size","network_id":"subnet-one","security_group_id":"group-one"}`
	created := operation("/instances", "first-create", "", createInput, 202)
	operation("/instance-operations", "existing-power", `"1"`, `{"account":"ali","region":"cn-test","asset_id":"i-one","action":"stop"}`, 202)

	const lower = `{"max_active":1,"max_managed_instances":0}`
	results := make(chan int, 2)
	for range cap(results) {
		go func() {
			req := httptest.NewRequestWithContext(t.Context(), "PUT", path, strings.NewReader(lower))
			req.Header.Set("Authorization", "Bearer "+admin)
			req.Header.Set("If-Match", `"2"`)
			req.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			app.ServeHTTP(response, req)
			results <- response.Code
		}()
	}
	count := map[int]int{}
	count[<-results]++
	count[<-results]++
	if count[200] != 1 || count[412] != 1 {
		t.Fatalf("concurrent budget updates: %v", count)
	}
	if budget := read(path); budget != (operations.ProjectBudget{
		Revision: 3, MaxActive: 1, ActiveOperations: 2, MaxManagedInstances: 0, ReservedInstances: 1,
	}) {
		t.Fatalf("lowering changed existing operations or reservations: %+v", budget)
	}
	operation("/instance-operations", "limited-power", `"1"`, `{"account":"ali","region":"cn-test","asset_id":"i-two","action":"stop"}`, 429)
	if result := operation("/instances", "first-create", "", createInput, 202); result.ID != created.ID {
		t.Fatal("budget changes broke idempotent replay")
	}
	const managedOnly = `{"max_active":10,"max_managed_instances":0}`
	put(admin, `"3"`, managedOnly, 200)
	operation("/instances", "limited-create", "", createInput, 429)

	execFreshness(t, db, `ALTER TABLE cmp_project_audit ADD CONSTRAINT reject_operation_budget_audit
		CHECK (category<>'operation_budget') NOT VALID`)
	put(admin, `"4"`, limits, 503)
	execFreshness(t, db, `ALTER TABLE cmp_project_audit DROP CONSTRAINT reject_operation_budget_audit`)
	if budget := read(path); budget.Revision != 4 || budget.MaxActive != 10 || budget.MaxManagedInstances != 0 {
		t.Fatalf("failed audit changed the stored quota: %+v", budget)
	}
	if shared := read(strings.Replace(path, "/compute/", "/shared/", 1)); shared.MaxActive != 100 || shared.ActiveOperations != 0 {
		t.Fatalf("project quota crossed a shared cloud account: %+v", shared)
	}

	server := httptest.NewTLSServer(app)
	t.Cleanup(server.Close)
	var output bytes.Buffer
	err := commands.RunOperationsClient(t.Context(), []string{"set-budget", "lab", "compute", "--if-match", "4"},
		server.URL, admin, strings.NewReader(managedOnly), &output, server.Client().Transport)
	if err != nil || !strings.Contains(output.String(), `"revision":"4"`) {
		t.Fatalf("CLI no-op update changed the version: %v %s", err, output.String())
	}
	var events audit.Page
	testsupport.DecodeProjectData(t, scheduleResponse(t, app, "GET", project+"/audit?category=operation_budget",
		admin, "", "", 200), &events)
	if len(events.Items) != 3 || events.Items[0].Revision != 4 || events.Items[0].Action != "replace-operation-budget" {
		t.Fatalf("wrong quota audit history: %+v", events.Items)
	}
}

func seedOperationBudgetAdmin(t *testing.T, db *sql.DB, token string) {
	t.Helper()
	execFreshness(t, db, `INSERT INTO cmp_principals (tenant_id,principal_id,kind,issuer,subject)
		VALUES ('lab','budget-admin','service','fixture','budget-admin');
		INSERT INTO cmp_memberships (tenant_id,project_id,principal_id,role) VALUES ('lab','compute','budget-admin','admin');
		INSERT INTO cmp_inventory_scopes (tenant_id,account_id,region,kind,credential_revision,lease_until)
		SELECT 'lab','ali','cn-test',kind,1,now() FROM (VALUES ('image'),('subnet'),('security-group'),('instance')) AS v(kind);
		INSERT INTO cmp_inventory_objects (scope_id,external_id,name,status,seen_generation,links)
		SELECT scope_id,asset,asset,state,1,CASE WHEN s.kind IN ('subnet','security-group')
			THEN '[{"kind":"network","relation":"in_network","id":"vpc-one"}]'::jsonb ELSE '[]'::jsonb END
		FROM cmp_inventory_scopes AS s JOIN (VALUES ('image','image-one','available'),('subnet','subnet-one','available'),
			('security-group','group-one','available'),('instance','i-one','running'),('instance','i-two','running')) AS v(kind,asset,state) USING (kind)
		WHERE (s.tenant_id,s.account_id,s.region)=('lab','ali','cn-test');
		INSERT INTO cmp_instance_offerings VALUES ('lab','ali','cn-test','fixture-size')`)
	digest := sha256.Sum256([]byte(token))
	execFreshness(t, db, `INSERT INTO cmp_service_tokens (token_hash,tenant_id,principal_id,expires_at)
		VALUES ($1,'lab','budget-admin',now()+interval '10 minutes')`, digest[:])
}
