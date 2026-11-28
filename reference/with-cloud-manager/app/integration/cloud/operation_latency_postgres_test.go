package cloudtest

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/openapi"

	cloudmanager "with-cloud-manager/app"
	"with-cloud-manager/app/commands"
	"with-cloud-manager/app/routers"
	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/operations"
	"with-cloud-manager/internal/provider"
)

func TestAcceptanceProjectOperationLatency(t *testing.T) {
	db := dbtest.Open(t)
	if err := cloudmanager.MigrateCloudManager(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	const token = "fixture-project-operation-latency-viewer"
	anchor := seedOperationBacklog(t, db, token)
	from, until := anchor.Add(-2*time.Hour), anchor.Add(-time.Hour)
	seedLatencyCohort(t, db, from, until)
	app, err := routers.NewAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	const path = "/v1/tenants/lab/projects/compute/operation-latency"
	get := func(target, credential string, status int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
		if credential != "" {
			req.Header.Set("Authorization", "Bearer "+credential)
		}
		w := httptest.NewRecorder()
		app.ServeHTTP(w, req)
		if w.Code != status || strings.Contains(w.Body.String(), "fixture/sensitive") || strings.Contains(w.Body.String(), "cmp_instance") {
			t.Fatalf("GET %s status=%d want=%d: %s", target, w.Code, status, w.Body)
		}
		return w
	}
	params := url.Values{"from": {from.Format(time.RFC3339)}, "until": {until.Format(time.RFC3339)}}
	window := "?" + params.Encode()
	read := func(target string) operations.LatencyReport {
		t.Helper()
		w := get(target, token, http.StatusOK)
		var envelope struct{ Data operations.LatencyReport }
		if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil || w.Header().Get("Cache-Control") != "no-store" ||
			!envelope.Data.From.Equal(from) || !envelope.Data.Until.Equal(until) || envelope.Data.AsOf.Before(anchor) || envelope.Data.Providers == nil {
			t.Fatalf("invalid evidence response: %s, %v", w.Body, err)
		}
		return envelope.Data
	}
	get(path+window, "", http.StatusUnauthorized)
	get(strings.Replace(path, "/lab/", "/foreign/", 1)+window, token, http.StatusForbidden)
	get(strings.Replace(path, "/compute/", "/hidden/", 1)+window, token, http.StatusForbidden)
	for _, invalid := range []string{"", "?until=no-date", window + "&from=" + params.Get("from"), window + "&region=", window + "&limit=1",
		"?from=2026-01-01T00:00:00Z&until=2026-02-03T00:00:00Z", "?from=2026-01-01T00:00:00.000000001Z&until=2026-01-01T00:00:01Z"} {
		get(path+invalid, token, http.StatusBadRequest)
	}
	report := read(path + window)
	summary := report.Summary
	if summary.Accepted != 9 || summary.Enqueued != 7 || summary.Succeeded != 5 || summary.Failed != 1 ||
		summary.NeedsReview != 1 || summary.Pending != 2 || len(report.Providers) != 3 {
		t.Fatalf("incorrect complete cohort: %+v", report)
	}
	checkLatencyDistribution(t, summary.QueueWait, 3, 2000, 3000, 3000)
	checkLatencyDistribution(t, summary.Convergence, 2, 5000, 10000, 10000)
	if report.Providers[0].Provider != provider.AlibabaECS || report.Providers[0].Convergence != (operations.LatencyDistribution{}) ||
		report.Providers[1].Provider != provider.HuaweiECS || report.Providers[1].Convergence.Samples != 0 ||
		report.Providers[2].Provider != provider.TencentCVM || report.Providers[2].Convergence.Samples != 2 {
		t.Fatalf("manual recovery or incomplete histories were included: %+v", report.Providers)
	}
	filtered := read(path + window + "&account=tc&region=latency-test&action=start")
	if len(filtered.Providers) != 1 || filtered.Summary.Accepted != 2 || filtered.Summary.Succeeded != 2 {
		t.Fatalf("filters did not constrain the cohort: %+v", filtered)
	}
	emptyPath := strings.Replace(path, "/compute/", "/empty/", 1)
	if empty := read(emptyPath + window); empty.Summary != (operations.LatencyTotals{}) || len(empty.Providers) != 0 {
		t.Fatalf("empty cohort returned fabricated timings: %+v", empty)
	}
	server := httptest.NewTLSServer(app)
	defer server.Close()
	var out bytes.Buffer
	err = commands.RunOperationsClient(t.Context(), []string{"latency", "lab", "compute", "--from", params.Get("from"), "--until", params.Get("until"), "--action", "start"},
		server.URL, token, nil, &out, server.Client().Transport)
	var cli operations.LatencyReport
	if err != nil || json.Unmarshal(out.Bytes(), &cli) != nil || cli.Summary.Accepted != 5 || cli.Summary.Convergence.Samples != 2 {
		t.Fatalf("unexpected latency CLI response: %s, %v", &out, err)
	}
	if cli.Scope != (operations.LatencyScope{Tenant: "lab", Project: "compute", Action: "start"}) {
		t.Fatalf("latency snapshot did not bind the authorized filters: %+v", cli.Scope)
	}
	checkProjectLatencyGate(t, server, token, from, until)
	var document openapi.Document
	if err := json.Unmarshal(get("/openapi.json", token, http.StatusOK).Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	op := document.Paths["/v1/tenants/{tenant}/projects/{project}/operation-latency"].Get
	if op == nil || op.OperationID != "getProjectOperationLatency" || len(op.Security) != 1 || op.Responses["422"].Description == "" {
		t.Fatalf("missing latency OpenAPI contract: %+v", op)
	}
	pitrExec(t, db, `INSERT INTO cmp_instance_operations (operation_id,tenant_id,project_id,principal_id,account_id,
		region,resource_kind,action,status,idempotency_key,request_hash,credential_revision,membership_revision,ownership_revision,created_at)
		SELECT 'latency-overflow-'||n,'lab','empty','viewer','tc','ap-test','network','create-network','failed',
		'latency-overflow-'||n,decode(repeat('00',32),'hex'),1,1,0,$1 FROM generate_series(1,$2) n`, from, operations.LatencyLimit+1)
	get(emptyPath+window, token, http.StatusUnprocessableEntity)
	pitrExec(t, db, `UPDATE cmp_memberships SET active=false WHERE (tenant_id,project_id)=('lab','compute')`)
	get(path+window, token, http.StatusForbidden)
}

func checkProjectLatencyGate(t *testing.T, server *httptest.Server, token string, from, until time.Time) {
	t.Helper()
	manifest := commands.OperationLatencyGateManifest{Version: 1, Tenant: "lab", Project: "compute", From: from, Until: until}
	for _, target := range []struct{ account, provider string }{{"tc", provider.TencentCVM}, {"ali", provider.AlibabaECS}, {"hw", provider.HuaweiECS}} {
		manifest.Cases = append(manifest.Cases, commands.OperationLatencyGateCase{
			Name: target.account + "-start", Provider: target.provider, Account: target.account, Region: "latency-test", Action: "start",
			MinimumAccepted: 1, MinimumSuccessBPS: 10000,
			Queue:       commands.OperationLatencyGateSLO{MinimumCoverageBPS: 5000, MaximumP95MS: latencyGateNumber(3000)},
			Convergence: commands.OperationLatencyGateSLO{MinimumCoverageBPS: 10000, MaximumP95MS: latencyGateNumber(10000)},
		})
	}
	var output bytes.Buffer
	err := commands.RunOperationsClient(t.Context(), []string{"verify-latency", "lab", "compute"}, server.URL, token,
		bytes.NewReader(marshalLatencyGateManifest(t, manifest)), &output, server.Client().Transport)
	var report commands.OperationLatencyGateReport
	if err == nil || json.Unmarshal(output.Bytes(), &report) != nil || report.ThresholdMet || len(report.Cases) != 3 || !report.Cases[0].Passed ||
		report.Cases[1].Passed || report.Cases[2].Passed || report.Cases[1].Observed == nil || report.Cases[2].Observed == nil ||
		report.Cases[1].Observed.Convergence.Samples != 0 || report.Cases[2].Observed.Convergence.Samples != 0 ||
		report.Cases[1].Observed.Succeeded != 2 || report.Cases[2].Observed.Succeeded != 1 {
		t.Fatalf("incomplete provider evidence passed the release check: %s, %v", &output, err)
	}
}

func checkLatencyDistribution(t *testing.T, got operations.LatencyDistribution, samples, p50, p95, max int64) {
	t.Helper()
	if got.Samples != samples || got.P50MS == nil || *got.P50MS != p50 || got.P95MS == nil || *got.P95MS != p95 || got.MaxMS == nil || *got.MaxMS != max {
		t.Fatalf("wrong latency distribution: %+v", got)
	}
}

func seedLatencyCohort(t *testing.T, db *sql.DB, from, until time.Time) {
	t.Helper()
	pitrExec(t, db, `UPDATE cmp_instance_operations SET created_at=$1
		WHERE operation_id IN ('approval','ready','review','closed','hidden-operation')`, from)
	pitrExec(t, db, `UPDATE cmp_instance_operations SET created_at=$1 WHERE operation_id='missing'`, until)
	pitrExec(t, db, `INSERT INTO cmp_inventory_scopes (tenant_id,account_id,region,kind,credential_revision,lease_until)
		SELECT tenant_id,account_id,'latency-test','instance',1,now() FROM cmp_cloud_accounts WHERE tenant_id='lab';
		INSERT INTO cmp_inventory_objects (scope_id,external_id,name,status,seen_generation)
		SELECT scope_id,asset,asset,'running',1 FROM cmp_inventory_scopes
		JOIN (VALUES ('tc','tc-one'),('tc','tc-two'),('ali','ali-manual'),('ali','ali-unobserved'),('hw','hw-review')) f(account,asset)
		ON account_id=account;`)
	pitrExec(t, db, `INSERT INTO cmp_instance_operations (operation_id,tenant_id,project_id,principal_id,account_id,region,
		resource_kind,scope_id,external_id,action,status,outcome_reason,idempotency_key,request_hash,credential_revision,membership_revision,ownership_revision,created_at)
		SELECT i.external_id,s.tenant_id,'compute','viewer',s.account_id,s.region,'instance',i.scope_id,i.external_id,'start','succeeded',
		CASE WHEN i.external_id='ali-manual' THEN 'manual_review' ELSE 'provider_confirmed' END,
		i.external_id,decode(repeat('00',32),'hex'),1,1,1,$1 FROM cmp_inventory_objects i JOIN cmp_inventory_scopes s USING (scope_id)`, from)
	pitrExec(t, db, `INSERT INTO cmp_operation_tasks (id,topic,tenant_id,payload,priority,status,attempts,max_attempts,
		available_at,created_at,updated_at,first_lease_at)
		SELECT operation_id,'cmp.instance.v1','lab',convert_to('fixture/sensitive','UTF8'),0,'done',2,8,
		$1::timestamptz,$1::timestamptz,$1::timestamptz,$1::timestamptz+make_interval(secs=>wait)
		FROM cmp_instance_operations JOIN (VALUES ('tc-one',1),('tc-two',2),('ali-manual',3),('ali-unobserved',NULL),('hw-review',NULL)) f(id,wait)
		ON operation_id=id`, from)
	pitrExec(t, db, `INSERT INTO cmp_instance_operation_events (operation_id,status,recorded_at)
		SELECT id,state,$1::timestamptz+make_interval(secs=>seconds)
		FROM (VALUES ('tc-one','applying',2),('tc-one','succeeded',7),('tc-two','applying',3),('tc-two','succeeded',13),
		('ali-manual','applying',3),('ali-manual','succeeded',9),('ali-unobserved','succeeded',11),
		('hw-review','applying',4),('hw-review','needs_review',6),('hw-review','succeeded',20)) f(id,state,seconds)`, from)
}
