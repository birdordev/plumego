package cloudtest

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"with-cloud-manager/app/commands"
	"with-cloud-manager/app/integration/testsupport"
	alertservice "with-cloud-manager/app/services/alerts"
	"with-cloud-manager/internal/alerts"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

func TestAcceptanceTagComplianceAlerts(t *testing.T) {
	db, app := setupBillingSummary(t)
	ctx := t.Context()
	const policy = projectBudgetRoot + "/governance/tag-policy"
	const open = projectBudgetRoot + "/alerts?rule=tag_noncompliant"
	const baseBody = `{"max_age_seconds":3600,"rules":[{"kind":"instance","required_keys":["fixture-managed"]}]}`
	on := strings.Replace(baseBody, `"rules"`, `"alert_on_noncompliance":true,"rules"`, 1)
	off := strings.Replace(on, `:true`, `:false`, 1)

	write := func(method, path, token, revision, body string, status int) {
		t.Helper()
		request := httptest.NewRequestWithContext(ctx, method, path, strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Content-Type", "application/json")
		if revision != "" {
			request.Header.Set("If-Match", revision)
		}
		response := httptest.NewRecorder()
		app.ServeHTTP(response, request)
		if response.Code != status {
			t.Fatalf("policy %s returned %d: %s", method, response.Code, response.Body)
		}
	}

	read := func(path string, revision int64, notify bool) {
		t.Helper()
		var result inventory.TagPolicy
		response := scheduleResponse(t, app, "GET", path, summaryAdmin, "", "", 200)
		testsupport.DecodeProjectData(t, response, &result)
		if result.Revision != revision || result.AlertOnNoncompliance == nil || *result.AlertOnNoncompliance != notify ||
			response.Header().Get("ETag") != strconv.Quote(strconv.FormatInt(revision, 10)) {
			t.Fatalf("incorrect historical policy or alert preference: %+v", result)
		}
	}
	list := func(path string) []alerts.Alert {
		t.Helper()
		var page alerts.Page
		response := scheduleResponse(t, app, "GET", path, summaryAdmin, "", "", 200)
		testsupport.DecodeProjectData(t, response, &page)
		if strings.Contains(response.Body.String(), "fixture-managed") || strings.Contains(response.Body.String(), "sensitive-label-value") {
			t.Fatal("project alert exposed a tag key or value")
		}
		return page.Items
	}
	store := inventory.NewStore(db)
	scan := func(account string, tags map[string]string) {
		t.Helper()
		key := inventory.Key{Tenant: "tenant", Account: account, Region: "region", Kind: provider.Instance}
		claim, err := store.Claim(ctx, key, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if _, complete, err := store.ScanPage(ctx, claim, imageDiscovery(func() provider.Page {
			return provider.Page{Objects: []provider.Object{{ID: "same-instance", Tags: tags}}}
		})); err != nil || !complete {
			t.Fatalf("asset scan did not publish evidence: %t %v", complete, err)
		}
	}
	good := map[string]string{"fixture-managed": "sensitive-label-value"}
	scan("tc", map[string]string{})
	scan("ali", good)
	scan("hw", nil)
	evaluator := alerts.NewStore(db).WithProjectEvaluation(alertservice.EvaluateProjectAlerts)
	evaluate := func() {
		t.Helper()
		markProjectBudgetDue(t, db)
		worked, err := evaluator.EvaluateNext(ctx)
		if err != nil || !worked {
			t.Fatalf("tag alert evaluation: worked=%v error=%v", worked, err)
		}
	}
	write(http.MethodPost, policy, summaryAdmin, "", baseBody, 201)
	read(policy, 1, false)
	evaluate()
	if len(list(open)) != 0 {
		t.Fatal("a legacy policy enabled alerts")
	}
	write(http.MethodPut, policy, summaryViewer, `"1"`, on, 403)
	write(http.MethodPut, policy, summaryAdmin, `"1"`, on, 200)
	read(policy, 2, true)
	read(policy+"?revision=1", 1, false)
	execBillingSummary(t, db, `INSERT INTO cmp_operation_event_sinks(sink_id,tenant_id,project_id,endpoint_hash,include_alerts)
		VALUES ('tag-fixture','tenant','finance',decode(repeat('31',32),'hex'),true);
		ALTER TABLE cmp_operation_event_outbox ADD CONSTRAINT tag_test_outbox_failure CHECK (event_type<>'project.alert.opened') NOT VALID`)
	markProjectBudgetDue(t, db)
	if _, err := evaluator.EvaluateNext(ctx); err == nil || len(list(open)) != 0 {
		t.Fatal("notification failure did not roll back the tag alert")
	}
	execBillingSummary(t, db, "ALTER TABLE cmp_operation_event_outbox DROP CONSTRAINT tag_test_outbox_failure")
	runConcurrentProjectEvaluators(t, evaluator)
	items := list(open)
	if len(items) != 1 || items[0].SubjectID != "2/instance" || items[0].Account != "" || items[0].SourceStatus != "noncompliant" {
		t.Fatalf("wrong published policy or alert aggregation: %+v", items)
	}
	first := items[0].ID
	sharedPolicy := strings.Replace(policy, "/finance/", "/shared/", 1)
	write(http.MethodPost, sharedPolicy, summaryAdmin, "", on, 201)
	execBillingSummary(t, db, `UPDATE cmp_project_alert_checks SET next_at=CASE WHEN (tenant_id,project_id)=('tenant','shared')
		THEN now()-interval '1 second' ELSE now()+interval '1 hour' END`)
	if worked, err := evaluator.EvaluateNext(ctx); err != nil || !worked || len(list(strings.Replace(open, "/finance/", "/shared/", 1))) != 0 {
		t.Fatalf("shared-only inventory duplicated the owner alert: %t %v", worked, err)
	}
	server := httptest.NewTLSServer(app)
	t.Cleanup(server.Close)
	var cli bytes.Buffer
	err := commands.RunAlertsClient(ctx, []string{"list", "tenant", "finance", "--rule", "tag_noncompliant"},
		server.URL, summaryAdmin, &cli, server.Client().Transport)
	var page alerts.Page
	if decodeErr := json.Unmarshal(cli.Bytes(), &page); err != nil || decodeErr != nil || len(page.Items) != 1 || page.Items[0].ID != first {
		t.Fatalf("alert CLI did not select the policy alert: %v %v", err, decodeErr)
	}
	scan("tc", good)
	evaluate()
	if pending := list(open); len(pending) != 1 || pending[0].ID != first {
		t.Fatalf("unknown evidence incorrectly resolved a tag alert: %+v", pending)
	}
	scan("hw", good)
	evaluate()
	if done := list(open + "&status=resolved"); len(done) != 1 || done[0].ID != first {
		t.Fatalf("complete compliance did not resolve the tag alert: %+v", done)
	}
	scan("ali", map[string]string{})
	write(http.MethodPut, policy, summaryAdmin, `"2"`, baseBody, 200)
	read(policy, 3, true)
	evaluate()
	if active := list(open); len(active) != 1 || active[0].SubjectID != "3/instance" || active[0].ID == first {
		t.Fatalf("a new policy revision did not get a separate alert: %+v", active)
	}
	write(http.MethodPut, policy, summaryAdmin, `"3"`, baseBody, 200)
	read(policy, 4, true)
	evaluate()
	if active := list(open); len(active) != 1 || active[0].SubjectID != "4/instance" || len(list(open+"&status=resolved")) != 2 {
		t.Fatalf("new revision left an alert open against the superseded policy: %+v", active)
	}
	write(http.MethodPut, policy, summaryAdmin, `"4"`, off, 200)
	read(policy, 5, false)
	evaluate()
	if len(list(open)) != 0 || len(list(open+"&status=resolved")) != 3 {
		t.Fatal("explicit opt-out did not close the old policy alert")
	}
}
