package main

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/openapi"

	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/operations"
	"with-cloud-manager/internal/provider"
)

func TestAcceptanceProjectOperationBacklog(t *testing.T) {
	db := dbtest.Open(t)
	if err := migrateCloudManager(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	const token = "fixture-project-operation-backlog-viewer"
	anchor := seedOperationBacklog(t, db, token)
	app, err := newAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	const path = "/v1/tenants/lab/projects/compute/operation-backlog"
	get := func(target, token string, status int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("GET %s status=%d want=%d: %s", target, w.Code, status, w.Body)
		}
		return w
	}
	readReport := func(target string) operations.BacklogReport {
		t.Helper()
		w := get(target, token, http.StatusOK)
		var envelope struct{ Data operations.BacklogReport }
		if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil || w.Header().Get("Cache-Control") != "no-store" ||
			envelope.Data.AsOf.Before(anchor) || envelope.Data.Providers == nil {
			t.Fatalf("invalid scoped backlog: %s, %v", w.Body, err)
		}
		for _, value := range []string{"reference-secret", "secret-owner", "private-payload", "hidden-operation", "request_hash"} {
			if strings.Contains(w.Body.String(), value) {
				t.Fatal("operation backlog exposed internal or foreign data")
			}
		}
		return envelope.Data
	}
	if got := get(path, "", http.StatusUnauthorized).Header().Get("WWW-Authenticate"); !strings.HasPrefix(got, "Bearer") {
		t.Fatalf("missing authentication challenge: %q", got)
	}
	get(strings.Replace(path, "/lab/", "/foreign/", 1), token, http.StatusForbidden)
	get(strings.Replace(path, "/compute/", "/hidden/", 1), token, http.StatusForbidden)
	for _, query := range []string{"account=", "limit=1", "region=bad+region", "account=%zz", "region=cn-test&region=ap-test"} {
		get(path+"?"+query, token, http.StatusBadRequest)
	}
	empty := readReport(strings.Replace(path, "/compute/", "/empty/", 1))
	if empty.Summary != (operations.BacklogCounts{}) || len(empty.Providers) != 0 {
		t.Fatalf("empty project leaked operations: %+v", empty)
	}
	report := readReport(path)
	summary := report.Summary
	if summary.AwaitingApproval != 1 || summary.Ready != 2 || summary.Deferred != 2 || summary.Leased != 2 ||
		summary.Recoverable != 1 || summary.NeedsReview != 1 || summary.QueueAnomalies != 4 || len(report.Providers) != 3 {
		t.Fatalf("wrong operation backlog totals: %+v", report)
	}
	for got, want := range map[*time.Time]time.Time{
		summary.EarliestReadyAt: anchor.Add(-3 * time.Hour), summary.EarliestRecoveryAt: anchor.Add(-time.Minute),
		summary.NextDeferredAt: anchor.Add(2 * time.Hour),
	} {
		if got == nil || !got.Equal(want) {
			t.Fatalf("wrong eligibility time: %v, want %s", got, want)
		}
	}
	if report.Providers[0].Provider != provider.AlibabaECS || report.Providers[0].QueueAnomalies != 2 ||
		report.Providers[1].Provider != provider.HuaweiECS || report.Providers[1].Recoverable != 1 ||
		report.Providers[2].Provider != provider.TencentCVM || report.Providers[2].Ready != 2 {
		t.Fatalf("provider groups do not match scoped tasks: %+v", report.Providers)
	}
	filtered := readReport(path + "?account=tc&region=ap-test")
	if filtered.Summary.Ready != 1 || filtered.Summary.Leased != 1 || filtered.Summary.AwaitingApproval != 0 ||
		filtered.Summary.EarliestReadyAt == nil || !filtered.Summary.EarliestReadyAt.Equal(anchor.Add(-3*time.Hour)) || len(filtered.Providers) != 1 {
		t.Fatalf("account and region did not constrain both layers: %+v", filtered)
	}
	server := httptest.NewTLSServer(app)
	defer server.Close()
	var output bytes.Buffer
	err = runOperationsClient(t.Context(), []string{"backlog", "lab", "compute", "--account", "hw", "--region", "cn-test"},
		server.URL, token, nil, &output, server.Client().Transport)
	var cli operations.BacklogReport
	if err != nil || json.Unmarshal(output.Bytes(), &cli) != nil || len(cli.Providers) != 1 || cli.Providers[0].Provider != provider.HuaweiECS || cli.Summary.Recoverable != 1 {
		t.Fatalf("unexpected CLI snapshot: %s, %v", &output, err)
	}
	var contract openapi.Document
	if err := json.Unmarshal(get("/openapi.json", token, http.StatusOK).Body.Bytes(), &contract); err != nil {
		t.Fatal(err)
	}
	op := contract.Paths["/v1/tenants/{tenant}/projects/{project}/operation-backlog"].Get
	if op == nil || op.OperationID != "getProjectOperationBacklog" || len(op.Security) != 1 || op.Responses["503"].Description == "" ||
		op.Responses["200"].Content["application/json"].Schema.Properties["data"].Properties["summary"].Properties["queue_anomalies"].Type != "integer" {
		t.Fatalf("missing project backlog OpenAPI contract: %+v", op)
	}
	pitrExec(t, db, `UPDATE cmp_memberships SET active=false WHERE (tenant_id,project_id)=('lab','compute')`)
	get(path, token, http.StatusForbidden)
}

func seedOperationBacklog(t *testing.T, db *sql.DB, token string) time.Time {
	t.Helper()
	pitrExec(t, db, `INSERT INTO cmp_tenants VALUES ('lab'),('foreign');
		INSERT INTO cmp_projects VALUES ('lab','compute'),('lab','empty'),('lab','hidden'),('foreign','compute');
		INSERT INTO cmp_principals (tenant_id,principal_id,kind,issuer,subject)
		VALUES ('lab','viewer','service','fixture','viewer'),('foreign','viewer','service','fixture','viewer');
		INSERT INTO cmp_memberships (tenant_id,project_id,principal_id,role)
		VALUES ('lab','compute','viewer','viewer'),('lab','empty','viewer','viewer');
		INSERT INTO cmp_cloud_accounts (tenant_id,account_id,project_id,provider,credential_ref,remote_identity,mode)
		VALUES ('lab','tc','compute','tencent-cvm','reference-secret','fixture-tc','write_enabled'),
		('lab','ali','compute','alibaba-ecs','reference-secret','fixture-ali','write_enabled'),
		('lab','hw','compute','huawei-ecs','reference-secret','fixture-hw','write_enabled'),
		('foreign','tc','compute','openstack','reference-secret','fixture-foreign','read_only');`)
	digest := sha256.Sum256([]byte(token))
	pitrExec(t, db, `INSERT INTO cmp_service_tokens(token_hash,tenant_id,principal_id,expires_at)
		VALUES ($1,'lab','viewer',now()+interval '5 minutes')`, digest[:])
	pitrExec(t, db, `INSERT INTO cmp_instance_operations (operation_id,tenant_id,project_id,principal_id,account_id,
		region,resource_kind,action,status,idempotency_key,request_hash,credential_revision,membership_revision,ownership_revision)
		SELECT id,'lab',project,'viewer',account,region,'network','create-network',state,id,decode(repeat('00',32),'hex'),1,1,0
		FROM (VALUES ('approval','compute','tc','ap-other','awaiting_approval'),('ready','compute','tc','ap-test','queued'),
		('ready-two','compute','tc','ap-other','queued'),('lease','compute','tc','ap-test','applying'),
		('review','compute','tc','ap-test','needs_review'),('closed','compute','tc','ap-test','failed'),
		('delay','compute','ali','cn-test','observing'),('expires','compute','ali','cn-test','queued'),
		('missing','compute','ali','cn-test','queued'),('recovery','compute','hw','cn-test','applying'),
		('lease-ttl','compute','hw','cn-test','observing'),('leased-delayed','compute','hw','cn-test','observing'),
		('dead','compute','hw','cn-test','applying'),('foreign-task','compute','hw','cn-test','queued'),
		('hidden-operation','hidden','tc','ap-test','queued')) AS f(id,project,account,region,state);`)
	var anchor time.Time
	if err := db.QueryRowContext(t.Context(), `SELECT date_trunc('second',clock_timestamp())`).Scan(&anchor); err != nil {
		t.Fatal(err)
	}
	pitrExec(t, db, `INSERT INTO cmp_operation_tasks (id,topic,tenant_id,payload,priority,status,attempts,max_attempts,
		available_at,created_at,updated_at,lease_owner,lease_until,expires_at)
		SELECT id,'cmp.instance.v1',tenant,convert_to('private-payload','UTF8'),0,state,1,8,
		$1::timestamptz+available,$1::timestamptz-interval '4 hours',$1::timestamptz,
		CASE WHEN lease IS NOT NULL THEN 'secret-owner' END,$1::timestamptz+lease,$1::timestamptz+expiry
		FROM (VALUES ('ready','lab','queued','-3 hours'::interval,NULL::interval,NULL::interval),('ready-two','lab','queued','-2 hours',NULL,NULL),
		('lease','lab','leased','-3 hours','1 hour',NULL),('closed','lab','queued','-1 hour',NULL,NULL),
		('delay','lab','queued','2 hours',NULL,NULL),('expires','lab','queued','-1 hour',NULL,'-1 minute'),
		('recovery','lab','leased','-3 hours','-1 minute',NULL),('lease-ttl','lab','leased','-3 hours','1 hour','-1 minute'),
		('leased-delayed','lab','leased','3 hours','-1 minute',NULL),('dead','lab','dead','-1 hour',NULL,NULL),
		('foreign-task','foreign','queued','-1 hour',NULL,NULL),('hidden-operation','lab','queued','-1 hour',NULL,NULL))
		AS f(id,tenant,state,available,lease,expiry)`, anchor)
	return anchor
}
