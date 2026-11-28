package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/birdordev/plumego/x/openapi"

	"with-cloud-manager/internal/alerts"
	"with-cloud-manager/internal/billing"
)

const projectBudgetRoot = "/v1/tenants/tenant/projects/finance"
const projectBudgetFixtureQuery = "?period=2026-11&source=native&currency=CNY&basis=billed"
const projectBudgetFixtureBody = `{"limit":"100","warning_percent":80,"enabled":true,"accounts":["tc","hw","ali"]}`

func TestAcceptanceProjectBillingBudget(t *testing.T) {
	db, app := setupBillingSummary(t)
	ctx := t.Context()
	run := alerts.NewStore(db).WithProjectEvaluation(billing.EvaluateProjectBudgetAlerts)
	route := projectBudgetRoot + "/billing/project-budget" + projectBudgetFixtureQuery
	budgetRequest := func(method, token, version, body string, code int) billing.ProjectBudget {
		t.Helper()
		response := projectBudgetResponse(t, app, method, route, token, version, body, code)
		var budget billing.ProjectBudget
		if code < 300 {
			decodeProjectData(t, response, &budget)
			if response.Header().Get("ETag") != `"`+budget.Revision+`"` || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("unsafe budget response headers: %v", response.Header())
			}
		}
		return budget
	}
	budgetRequest("GET", "", "", "", 401)
	budgetRequest("GET", summaryAdmin, "", "", 404)
	budgetRequest("POST", summaryViewer, "", projectBudgetFixtureBody, 403)
	budgetRequest("POST", summaryAdmin, "", strings.Replace(projectBudgetFixtureBody, `"hw"`, `"tc"`, 1), 400)
	budgetRequest("POST", summaryAdmin, "", strings.Replace(projectBudgetFixtureBody, `"hw"`, `"not-priority"`, 1), 403)
	shared := strings.Replace(route, "/finance/", "/shared/", 1)
	projectBudgetResponse(t, app, "POST", shared, summaryAdmin, "", projectBudgetFixtureBody, 403)
	projectBudgetResponse(t, app, "POST", shared, summaryAdmin, "", strings.Replace(projectBudgetFixtureBody, `"tc","hw","ali"`, `"tc"`, 1), 403)
	scheduleResponse(t, app, "GET", strings.Replace(route, "/tenant/", "/outside/", 1), summaryAdmin, "", "", 403)
	out := budgetRequest("POST", summaryAdmin, "", projectBudgetFixtureBody, 201)
	budgetRequest("POST", summaryAdmin, "", projectBudgetFixtureBody, 409)
	if out.Revision != "1" || len(out.Accounts) != 3 || out.Accounts[0].Account != "ali" || out.Spent != nil || out.State != "unavailable" {
		t.Fatalf("budget did not capture the owned account set: %+v", out)
	}
	execBillingSummary(t, db, `INSERT INTO cmp_operation_event_sinks(sink_id,tenant_id,project_id,endpoint_hash,include_alerts)
		VALUES ('budget-fixture','tenant','finance',decode(repeat('31',32),'hex'),true)`)
	recheck := func() {
		t.Helper()
		markProjectBudgetDue(t, db)
		worked, err := run.EvaluateNext(ctx)
		if err != nil || !worked {
			t.Fatalf("budget evaluation: worked=%v, error=%v", worked, err)
		}
	}
	jobs := func() []alerts.Alert {
		t.Helper()
		response := scheduleResponse(t, app, "GET", projectBudgetRoot+"/alerts?status=active", summaryAdmin, "", "", 200)
		var page alerts.Page
		decodeProjectData(t, response, &page)
		return page.Items
	}

	publishProjectBudgetFixture(t, db, "ali", "CNY", "55", 0)
	publishProjectBudgetFixture(t, db, "tc", "CNY", "25.000000000000", 0)
	publishProjectBudgetFixture(t, db, "hw", "USD", "10", 0)
	incomplete := budgetRequest("GET", summaryAdmin, "", "", 200)
	if incomplete.ObservedAccounts != 2 || incomplete.Spent != nil || incomplete.Accounts[1].State != "amount_missing" ||
		incomplete.Accounts[1].ReportRevision != "1" || incomplete.State != "unavailable" {
		t.Fatalf("partial currency coverage reported complete: %+v", incomplete)
	}
	recheck()
	if len(jobs()) != 0 {
		t.Fatal("incomplete coverage opened a project budget alert")
	}
	publishProjectBudgetFixture(t, db, "hw", "CNY", "0.000000000001", 1)
	complete := budgetRequest("GET", summaryAdmin, "", "", 200)
	if complete.State != "warning" || complete.Spent == nil || *complete.Spent != "80.000000000001" || complete.ObservedAccounts != 3 {
		t.Fatalf("complete account amounts were wrong: %+v", complete)
	}
	execBillingSummary(t, db, `ALTER TABLE cmp_operation_event_outbox ADD CONSTRAINT project_budget_test_failure
		CHECK (event_type<>'project.alert.opened') NOT VALID`)
	markProjectBudgetDue(t, db)
	if _, err := run.EvaluateNext(ctx); err == nil || len(jobs()) != 0 {
		t.Fatal("outbox failure did not roll back the project budget alert")
	}
	execBillingSummary(t, db, `ALTER TABLE cmp_operation_event_outbox DROP CONSTRAINT project_budget_test_failure`)
	runConcurrentProjectEvaluators(t, run)
	active := jobs()
	if len(active) != 1 || active[0].Rule != "billing_budget_warning" ||
		active[0].ResourceKind != "billing-project-budget" || active[0].SubjectID != out.ID {
		t.Fatalf("unexpected budget warning: %+v", active)
	}
	alertPath := projectBudgetRoot + "/alerts/" + active[0].ID
	scheduleResponse(t, app, "GET", alertPath, summaryViewer, "", "", 403)
	response := scheduleResponse(t, app, "POST", alertPath+"/acknowledgement", summaryAdmin, `"1"`, "", 200)
	var ack alerts.Alert
	decodeProjectData(t, response, &ack)
	if ack.Status != "acknowledged" || ack.BudgetScope == nil || ack.BudgetScope.Currency != "CNY" {
		t.Fatalf("project budget alert could not be acknowledged or traced: %+v", ack)
	}

	execBillingSummary(t, db, `UPDATE cmp_cloud_accounts SET mode='suspended' WHERE (tenant_id,account_id)=('tenant','tc')`)
	recheck()
	unavailable := budgetRequest("GET", summaryAdmin, "", "", 200)
	if unavailable.State != "unavailable" || unavailable.Accounts[2].ReportRevision != "" || len(jobs()) != 1 {
		t.Fatalf("account suspension manufactured recovery or exposed source scope: %+v", unavailable)
	}
	execBillingSummary(t, db, `UPDATE cmp_cloud_accounts SET mode='read_only' WHERE (tenant_id,account_id)=('tenant','tc')`)
	publishProjectBudgetFixture(t, db, "tc", "CNY", "55", 1)
	recheck()
	active = jobs()
	if len(active) != 1 || active[0].Rule != "billing_budget_exceeded" {
		t.Fatalf("warning did not transition to a single exceeded alert: %+v", active)
	}
	publishProjectBudgetFixture(t, db, "ali", "CNY", "-10", 1)
	recheck()
	if read := budgetRequest("GET", summaryAdmin, "", "", 200); read.State != "normal" || len(jobs()) != 0 {
		t.Fatalf("complete refund did not resolve the alert: %+v", read)
	}
	var deliveries int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM cmp_operation_event_outbox WHERE sink_id='budget-fixture'`).Scan(&deliveries); err != nil || deliveries != 4 {
		t.Fatalf("expected one opening and recovery per level: %d %v", deliveries, err)
	}

	budgetRequest("PUT", summaryAdmin, "", projectBudgetFixtureBody, 412)
	budgetRequest("PUT", summaryAdmin, `"2"`, projectBudgetFixtureBody, 412)
	checkProjectBudgetConcurrentUpdate(t, db)
	budgetRequest("PUT", summaryAdmin, `"1"`, projectBudgetFixtureBody, 412)
	server := httptest.NewTLSServer(app)
	defer server.Close()
	var printed bytes.Buffer
	update := []string{"project-budget-update", "tenant", "finance", "2026-11", "native", "CNY", "billed", "2"}
	body := strings.Replace(projectBudgetFixtureBody, `"enabled":true`, `"enabled":false`, 1)
	if err := runBillingClient(ctx, update, server.URL, summaryAdmin, strings.NewReader(body), &printed, server.Client().Transport); err != nil {
		t.Fatalf("budget CLI update failed: %v", err)
	}
	if read := budgetRequest("GET", summaryAdmin, "", "", 200); read.Revision != "3" || read.State != "inactive" {
		t.Fatalf("CLI did not disable the selected revision: %+v", read)
	}
	var doc openapi.Document
	response = scheduleResponse(t, app, "GET", "/openapi.json", "", "", "", 200)
	if err := json.Unmarshal(response.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	path := doc.Paths["/v1/tenants/{tenant}/projects/{project}/billing/project-budget"]
	if path.Get == nil || path.Post == nil || path.Put == nil || len(path.Put.Security) == 0 {
		t.Fatal("OpenAPI does not include the versioned project budget contract")
	}
}

func projectBudgetResponse(t *testing.T, app http.Handler, method, path, token, version, body string, status int) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if version != "" {
		req.Header.Set("If-Match", version)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	app.ServeHTTP(response, req)
	if response.Code != status {
		t.Fatalf("%s %s returned %d, expected %d: %s", method, path, response.Code, status, response.Body)
	}
	return response
}

func markProjectBudgetDue(t *testing.T, db *sql.DB) {
	t.Helper()
	execBillingSummary(t, db, `UPDATE cmp_project_alert_checks SET next_at=clock_timestamp()+
		CASE WHEN (tenant_id,project_id)=('tenant','finance') THEN -interval '1 minute' ELSE interval '1 day' END`)
}

func runConcurrentProjectEvaluators(t *testing.T, run alerts.Store) {
	t.Helper()
	var group sync.WaitGroup
	outcomes := make(chan bool, 5)
	for range cap(outcomes) {
		group.Go(func() {
			worked, err := run.EvaluateNext(t.Context())
			if err != nil {
				t.Error(err)
			}
			outcomes <- worked
		})
	}
	group.Wait()
	close(outcomes)
	var took int
	for worked := range outcomes {
		if worked {
			took++
		}
	}
	if took != 1 {
		t.Fatalf("project evaluated %d times", took)
	}
}

func checkProjectBudgetConcurrentUpdate(t *testing.T, db *sql.DB) {
	t.Helper()
	key := billing.ProjectBudgetKey{Tenant: "tenant", Project: "finance", Period: "2026-11", Source: billing.Native, Currency: "CNY", Basis: billing.BudgetBilled}
	store := billing.NewStore(db)
	errs := make(chan error, 2)
	for _, limit := range []string{"105", "110"} {
		go func() {
			enabled := true
			_, err := store.SaveProjectBudget(t.Context(), summaryAdmin, key, 1, billing.ProjectBudgetInput{Accounts: []string{"tc", "hw", "ali"},
				BudgetInput: billing.BudgetInput{Limit: limit, Enabled: &enabled, WarningPercent: 80}})
			errs <- err
		}()
	}
	first, second := <-errs, <-errs
	oneUpdateWon := first == nil && errors.Is(second, billing.ErrBudgetStale) || second == nil && errors.Is(first, billing.ErrBudgetStale)
	if !oneUpdateWon {
		t.Fatalf("competing budget updates did not observe one version: %v %v", first, second)
	}
}

func publishProjectBudgetFixture(t *testing.T, db *sql.DB, account, currency, amount string, revision int64) {
	t.Helper()
	headers := "Account,Month,Currency,Billed"
	vendor, remote := "tencent-cvm", "10001"
	var project string
	switch account {
	case "ali":
		vendor, remote = "alibaba-ecs", "10002"
	case "hw":
		vendor, remote, project = "huawei-ecs", "10003", "Project"
		headers += ",Project"
		amount += ",project-3"
	}
	key := billing.Key{Tenant: "tenant", Project: "finance", Account: account, Period: "2026-11", Source: billing.Native}
	manifest := billing.Manifest{Provider: vendor, Columns: &billing.Columns{
		Account: "Account", Project: project, Period: "Month", Currency: "Currency", Billed: "Billed",
	}}
	content := []byte(headers + "\n" + strings.Join([]string{remote, key.Period, currency, amount}, ",") + "\n")
	if _, err := billing.NewStore(db).Import(t.Context(), key, manifest, revision, [][]byte{content}); err != nil {
		t.Fatal(err)
	}
}
