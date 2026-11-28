package billingtest

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"with-cloud-manager/app"
	"with-cloud-manager/app/commands"
	"with-cloud-manager/app/routers"
	"with-cloud-manager/internal/alerts"
	"with-cloud-manager/internal/billing"
	"with-cloud-manager/internal/dbtest"
)

func TestBillingBudgetLifecycle(t *testing.T) {
	ctx := t.Context()
	db := dbtest.Open(t)
	if err := app.MigrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`
		INSERT INTO cmp_tenants VALUES ('tenant'),('foreign');
		INSERT INTO cmp_projects VALUES ('tenant','finance'),('tenant','shared'),('foreign','finance');
		INSERT INTO cmp_principals(tenant_id,principal_id,kind,issuer,subject) VALUES
		    ('tenant','admin','service','test','admin'),('tenant','viewer','service','test','viewer'),
		    ('foreign','peer','service','test','peer');
		INSERT INTO cmp_memberships(tenant_id,project_id,principal_id,role) VALUES
		    ('tenant','finance','admin','admin'),('tenant','shared','admin','admin'),
		    ('tenant','finance','viewer','viewer'),('foreign','finance','peer','admin');
		INSERT INTO cmp_cloud_accounts(tenant_id,account_id,project_id,provider,remote_identity,credential_ref,mode)
		    VALUES ('tenant','cloud','finance','tencent-cvm','10001','fixture','read_only'),
		           ('tenant','unsupported','finance','aws-ec2','aws:fixture','fixture','read_only');
		INSERT INTO cmp_cloud_account_grants(tenant_id,account_id,project_id) VALUES ('tenant','cloud','shared');
		INSERT INTO cmp_operation_event_sinks(sink_id,tenant_id,project_id,endpoint_hash,include_alerts) VALUES
		    ('budget','tenant','finance',decode(repeat('00',32),'hex'),true),
		    ('shared','tenant','shared',decode(repeat('01',32),'hex'),true),
		    ('legacy','tenant','finance',decode(repeat('02',32),'hex'),false);
		UPDATE cmp_project_alert_checks SET next_at=clock_timestamp()+interval '1 hour';`)
	token := func(name string) string { return "test-administrator-billing-budget-" + name }
	for _, name := range []string{"admin", "viewer", "peer"} {
		hash := sha256.Sum256([]byte(token(name)))
		exec(`INSERT INTO cmp_service_tokens(token_hash,tenant_id,principal_id,expires_at)
			SELECT $1,tenant_id,principal_id,now()+interval '1 hour' FROM cmp_principals WHERE principal_id=$2`, hash[:], name)
	}
	app, err := routers.NewAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Prepare(); err != nil {
		t.Fatal(err)
	}
	request := func(method, path, name, match, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if name != "" {
			req.Header.Set("Authorization", "Bearer "+token(name))
		}
		if match != "" {
			req.Header.Set("If-Match", match)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		app.ServeHTTP(w, req)
		if w.Code != status {
			t.Fatalf("%s %s status %d, want %d: %s", method, path, w.Code, status, w.Body)
		}
		return w
	}
	scope := "/v1/tenants/tenant/projects/finance"
	query := "?account=cloud&period=2026-11&source=native&currency=CNY&basis=billed"
	route := scope + "/billing/budget" + query
	input := `{"limit":"100.000000000001","warning_percent":80,"enabled":true}`
	effectiveRoute := strings.Replace(route, "basis=billed", "basis=effective", 1)
	checkBudget := func(path, state string, revision int64) billing.Budget {
		t.Helper()
		got := request("GET", path, "admin", "", "", 200)
		var envelope struct {
			Data billing.Budget
		}
		if err := json.Unmarshal(got.Body.Bytes(), &envelope); err != nil || envelope.Data.State != state || envelope.Data.Revision != revision {
			t.Fatalf("budget=%+v, want %s revision %d: %v", envelope.Data, state, revision, err)
		}
		return envelope.Data
	}
	request("GET", route, "", "", "", 401)
	request("GET", route, "admin", "", "", 404)
	request("POST", route, "viewer", "", input, 403)
	request("POST", strings.Replace(route, "/finance/", "/shared/", 1), "admin", "", input, 403)
	request("POST", strings.Replace(route, "tenants/tenant/", "tenants/foreign/", 1), "admin", "", input, 403)
	request("POST", route, "peer", "", input, 403)
	request("POST", strings.Replace(route, "account=cloud", "account=unsupported", 1), "admin", "", input, 400)
	request("POST", route, "admin", "", `{"limit":"1","limit":"2","warning_percent":80,"enabled":true}`, 400)
	request("POST", route, "admin", "", `{"limit":"0","warning_percent":80,"enabled":true}`, 400)
	request("POST", route, "admin", "", `{"limit":"1","warning_percent":80}`, 400)
	server := httptest.NewServer(app)
	defer server.Close()
	args := []string{"budget-create", "tenant", "finance", "cloud", "2026-11", "native", "CNY", "billed"}
	var cli bytes.Buffer
	if err := commands.RunBillingClient(ctx, args, server.URL, token("admin"), strings.NewReader(input), &cli, nil); err != nil ||
		!strings.Contains(cli.String(), `"state":"unavailable"`) {
		t.Fatalf("CLI create: %s %v", cli.String(), err)
	}
	request("POST", route, "admin", "", input, 409)
	request("PUT", route, "admin", "", input, 412)
	request("POST", effectiveRoute, "admin", "", `{"limit":"20","warning_percent":90,"enabled":true}`, 201)

	store := billing.NewStore(db)
	key := billing.Key{Tenant: "tenant", Project: "finance", Account: "cloud", Period: "2026-11", Source: billing.Native}
	manifest := billing.Manifest{Provider: "tencent-cvm", Columns: &billing.Columns{
		Account: "Account", Period: "Month", Currency: "Currency", Billed: "Billed", Effective: "Effective",
	}}
	publish := func(expected int64, amount, effective string) (billing.Report, error) {
		return store.Import(ctx, key, manifest, expected, [][]byte{
			[]byte("Account,Month,Currency,Billed,Effective\n10001,2026-11,CNY," + amount + "," + effective + "\n"),
		})
	}
	publishOK := func(expected int64, amount, effective string) {
		t.Helper()
		if _, err := publish(expected, amount, effective); err != nil {
			t.Fatal(err)
		}
	}
	publishOK(0, "80.000000000000", "")
	checkBudget(route, "normal", 1)
	checkBudget(effectiveRoute, "unavailable", 1)
	publishOK(1, "80.000000000001", "20")
	budget := checkBudget(route, "warning", 1)
	checkBudget(effectiveRoute, "exceeded", 1)
	if budget.Spent == nil || *budget.Spent != "80.000000000001" || budget.ReportRevision == nil || *budget.ReportRevision != 2 {
		t.Fatalf("wrong exact spend or source version: %+v", budget)
	}
	totals := func(query string, expected int) {
		t.Helper()
		var count int
		if err := db.QueryRowContext(ctx, query).Scan(&count); err != nil || count != expected {
			t.Fatalf("count=%d, want %d: %v (%s)", count, expected, err, query)
		}
	}
	publishOK(0, "80.000000000001", "20")
	exec("UPDATE cmp_project_alert_checks SET next_at=clock_timestamp() WHERE project_id='finance' AND tenant_id='tenant'")
	if worked, err := alerts.NewStore(db).EvaluateNext(ctx); err != nil || !worked {
		t.Fatalf("evaluate legacy alerts: worked=%v error=%v", worked, err)
	}
	totals("SELECT count(*) FROM cmp_project_alerts WHERE resolved_at IS NULL", 2)
	totals("SELECT count(*) FROM cmp_operation_event_outbox WHERE sink_id='budget'", 2)
	exec("UPDATE cmp_project_alerts SET resolved_at=clock_timestamp() WHERE resolved_at IS NULL")
	totals("SELECT count(*) FROM cmp_project_alerts WHERE resolved_at IS NULL", 2)
	var warning string
	if err := db.QueryRowContext(ctx, "SELECT alert_id FROM cmp_project_alerts WHERE rule='billing_budget_warning'").Scan(&warning); err != nil {
		t.Fatal(err)
	}
	viewerAlerts := request("GET", scope+"/alerts?rule=billing_budget_warning", "viewer", "", "", 200)
	if strings.Contains(viewerAlerts.Body.String(), warning) {
		t.Fatal("viewer received finance alert")
	}
	details := request("GET", scope+"/alerts/"+warning, "admin", "", "", 200)
	if !strings.Contains(details.Body.String(), `"budget_scope":{"period":"2026-11","source":"native","currency":"CNY","basis":"billed"}`) ||
		strings.Contains(details.Body.String(), "100.000000000001") {
		t.Fatalf("unexpected budget alert details: %s", details.Body)
	}
	request("GET", scope+"/alerts/"+warning, "viewer", "", "", 403)
	request("GET", scope+"/alerts?after="+warning, "viewer", "", "", 400)
	request("POST", scope+"/alerts/"+warning+"/acknowledgement", "admin", `"1"`, "", 200)

	exec(`ALTER TABLE cmp_operation_event_outbox ADD CONSTRAINT test_budget_outbox CHECK (event_type<>'project.alert.opened') NOT VALID`)
	if _, err := publish(2, "100.000000000001", ""); err == nil {
		t.Fatal("outbox failure did not abort report publication")
	}
	totals("SELECT count(*) FROM cmp_billing_reports", 2)
	totals("SELECT count(*) FROM cmp_project_alerts WHERE resolved_at IS NULL", 2)
	exec("ALTER TABLE cmp_operation_event_outbox DROP CONSTRAINT test_budget_outbox")
	publishOK(2, "100.000000000001", "")
	checkBudget(route, "exceeded", 1)
	checkBudget(effectiveRoute, "unavailable", 1)
	exec("UPDATE cmp_project_alerts SET resolved_at=clock_timestamp() WHERE resolved_at IS NULL")
	totals("SELECT count(*) FROM cmp_project_alerts WHERE resolved_at IS NULL AND rule='billing_budget_exceeded'", 2)
	totals("SELECT count(*) FROM cmp_project_alerts WHERE rule='billing_budget_warning' AND acknowledged_at IS NOT NULL AND resolved_at IS NOT NULL", 1)
	publishOK(3, "70", "5")
	totals("SELECT count(*) FROM cmp_project_alerts WHERE resolved_at IS NULL", 0)
	input = `{"limit":"50","warning_percent":70,"enabled":true}`
	request("PUT", route, "admin", `"1"`, input, 200)
	request("PUT", route, "admin", `"1"`, input, 412)
	checkBudget(route, "exceeded", 2)
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, limit := range []string{"40", "45"} {
		go func() {
			<-start
			enabled := true
			_, err := store.SaveBudget(ctx, token("admin"), billing.BudgetKey{Key: key, Currency: "CNY", Basis: "billed"},
				2, billing.BudgetInput{Limit: limit, WarningPercent: 80, Enabled: &enabled})
			results <- err
		}()
	}
	close(start)
	a, b := <-results, <-results
	oneUpdateWon := a == nil && errors.Is(b, billing.ErrBudgetStale) || b == nil && errors.Is(a, billing.ErrBudgetStale)
	if !oneUpdateWon {
		t.Fatalf("concurrent writes should choose one revision: %v %v", a, b)
	}
	checkBudget(route, "exceeded", 3)
	totals("SELECT count(*) FROM cmp_project_audit WHERE category='billing_budget'", 4)
	totals("SELECT count(*) FROM cmp_operation_event_outbox WHERE sink_id<>'budget'", 0)
	totals(`SELECT count(*) FROM cmp_operation_event_outbox WHERE convert_from(body,'UTF8')::jsonb ?| ARRAY['spent','limit','amount']`, 0)
	exec("UPDATE cmp_cloud_accounts SET project_id='shared' WHERE account_id='cloud'")
	request("GET", route, "admin", "", "", 403)
	request("GET", strings.Replace(route, "/finance/", "/shared/", 1), "admin", "", "", 404)
	totals("SELECT count(*) FROM cmp_project_alerts WHERE resolved_at IS NULL", 0)
}
