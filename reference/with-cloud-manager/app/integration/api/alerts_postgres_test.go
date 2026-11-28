package apitest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"with-cloud-manager/app/commands"
	"with-cloud-manager/app/integration/testsupport"
	"with-cloud-manager/app/routers"
	"with-cloud-manager/internal/alerts"
	"with-cloud-manager/internal/audit"
	"with-cloud-manager/internal/controller"
	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/operations"
)

func TestAcceptanceProjectAlerts(t *testing.T) {
	db, ctx := dbtest.Open(t), t.Context()
	for _, migrate := range []func(context.Context, *sql.DB) error{
		identity.Migrate, inventory.Migrate, identity.MigrateServiceTokens, controller.Migrate, identity.MigrateAccounts,
		operations.Migrate, audit.Migrate,
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
	exec(`INSERT INTO cmp_tenants VALUES ('tenant'),('foreign');
		INSERT INTO cmp_projects VALUES ('tenant','project');`)
	if err := alerts.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := alerts.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO cmp_projects VALUES ('tenant','other'),('foreign','project');
		INSERT INTO cmp_principals (tenant_id,principal_id,kind,issuer,subject) VALUES
		  ('tenant','admin','service','test','admin'),('tenant','viewer','service','test','viewer'),
		  ('foreign','foreign-admin','service','test','foreign-admin');
		INSERT INTO cmp_memberships (tenant_id,project_id,principal_id,role) VALUES
		  ('tenant','project','admin','admin'),('tenant','other','admin','admin'),('tenant','project','viewer','viewer'),
		  ('foreign','project','foreign-admin','admin');
		INSERT INTO cmp_cloud_accounts (tenant_id,account_id,project_id,provider,credential_ref,remote_identity,mode) VALUES
		  ('tenant','aws','project','aws-ec2','fixture/private','aws:alerts','write_enabled'),
		  ('tenant','paused','project','openstack','fixture/other','openstack:alerts','suspended');
		INSERT INTO cmp_cloud_account_grants (tenant_id,account_id,project_id) VALUES ('tenant','aws','other');
		INSERT INTO cmp_inventory_schedules (tenant_id,account_id,region,kind,interval_seconds,scheduled_at,failures,last_outcome) VALUES
		  ('tenant','aws','us-east-1','instance',120,now()-interval '60 minutes',3,'throttled'),
		  ('tenant','aws','us-east-1','volume',120,now(),0,NULL),
		  ('tenant','paused','region','image',120,now()-interval '60 minutes',4,'failure');
		INSERT INTO cmp_instance_operations
		    (operation_id,tenant_id,project_id,principal_id,account_id,region,action,status,idempotency_key,request_hash,
		     credential_revision,membership_revision,ownership_revision,created_at,updated_at)
		SELECT id,'tenant','project','admin','aws','us-east-1','create',status,id,decode(repeat('00',32),'hex'),1,1,0,now()-age,NULL
		FROM (VALUES ('review-op','needs_review',interval '10 seconds'),('queued-op','queued',interval '11 minutes'),
		      ('active-op','observing',interval '5 minutes'),('slow-approval','awaiting_approval',interval '31 minutes')) v(id,status,age);
		INSERT INTO cmp_operation_event_sinks (sink_id,tenant_id,project_id,endpoint_hash,include_alerts) VALUES
		  ('primary','tenant','project',decode(repeat('00',32),'hex'),true),
		  ('legacy','tenant','project',decode(repeat('00',32),'hex'),false),
		  ('other','tenant','other',decode(repeat('00',32),'hex'),true);
		UPDATE cmp_project_alert_checks SET next_at=statement_timestamp()+interval '1 hour'
		  WHERE (tenant_id,project_id)<>('tenant','project');`)
	token := func(actor string) string { return "fixture-alert-test-credential-" + actor }
	for _, actor := range []string{"admin", "viewer", "foreign-admin"} {
		hash := sha256.Sum256([]byte(token(actor)))
		exec(`INSERT INTO cmp_service_tokens(token_hash,tenant_id,principal_id,expires_at)
		  SELECT $1,tenant_id,principal_id,now()+interval '5 minutes' FROM cmp_principals WHERE principal_id=$2`, hash[:], actor)
	}
	store := alerts.NewStore(db)
	count := func(query string, want int) {
		t.Helper()
		var n int
		if err := db.QueryRowContext(ctx, query).Scan(&n); err != nil || n != want {
			t.Fatalf("%s = %d; want %d: %v", query, n, want, err)
		}
	}
	exec(`ALTER TABLE cmp_operation_event_outbox ADD CONSTRAINT fixture_reject_alert
		CHECK (event_type<>'project.alert.opened') NOT VALID`)
	if _, err := store.EvaluateNext(ctx); err == nil {
		t.Fatal("outbox failure did not abort evaluation")
	}
	count("SELECT count(*) FROM cmp_project_alerts", 0)
	count("SELECT count(*) FROM cmp_project_audit", 0)
	count("SELECT count(*) FROM cmp_project_alert_checks WHERE next_at<=statement_timestamp()", 1)
	exec("ALTER TABLE cmp_operation_event_outbox DROP CONSTRAINT fixture_reject_alert")

	var wg sync.WaitGroup
	type result struct {
		worked bool
		err    error
	}
	evaluations := make(chan result, 8)
	for range cap(evaluations) {
		wg.Go(func() {
			worked, err := store.EvaluateNext(ctx)
			evaluations <- result{worked, err}
		})
	}
	wg.Wait()
	close(evaluations)
	evaluated := 0
	for res := range evaluations {
		if res.err != nil {
			t.Fatal(res.err)
		}
		if res.worked {
			evaluated++
		}
	}
	if evaluated != 1 {
		t.Fatalf("single due project evaluated %d times", evaluated)
	}
	count("SELECT count(*) FROM cmp_project_alerts", 5)
	count("SELECT count(*) FROM cmp_operation_event_outbox WHERE sink_id='primary' AND sequence IS NULL", 5)
	count("SELECT count(*) FROM cmp_operation_event_outbox WHERE sink_id<>'primary'", 0)

	app, err := routers.NewAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Prepare(); err != nil {
		t.Fatal(err)
	}
	send := func(method, path, actor, match, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if actor != "" {
			r.Header.Set("Authorization", "Bearer "+token(actor))
		}
		if match != "" {
			r.Header.Set("If-Match", match)
		}
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if w.Code != status || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "fixture/private") {
			t.Fatalf("%s %s wanted %d got %d: %s", method, path, status, w.Code, w.Body.String())
		}
		return w
	}
	const project = "/v1/tenants/tenant/projects/project"
	const collection = project + "/alerts"
	const otherProject = "/v1/tenants/tenant/projects/other/alerts"
	send("GET", collection, "", "", "", 401)
	send("GET", collection, "foreign-admin", "", "", 403)
	send("GET", collection+"?limit=1&limit=2", "viewer", "", "", 400)
	send("GET", collection+"?status=invalid", "viewer", "", "", 400)
	page := func(path, actor string) (p alerts.Page) {
		t.Helper()
		testsupport.DecodeProjectData(t, send("GET", path, actor, "", "", 200), &p)
		return p
	}
	first := page(collection+"?limit=1", "viewer")
	if len(first.Items) != 1 || first.Next != first.Items[0].ID {
		t.Fatalf("unsupported pagination: %+v", first)
	}
	if next := page(collection+"?limit=4&after="+first.Next, "viewer"); len(next.Items) != 4 || next.Next != "" {
		t.Fatalf("incomplete next page: %+v", next)
	}
	send("GET", otherProject+"?after="+first.Next, "admin", "", "", 400)
	if p := page(otherProject, "admin"); len(p.Items) != 0 {
		t.Fatalf("shared cloud account sent owner alerts to another project: %+v", p)
	}
	reviews := page(collection+"?rule=operation_review", "viewer")
	if len(reviews.Items) != 1 || reviews.Items[0].SubjectID != "review-op" || reviews.Items[0].Status != "open" {
		t.Fatalf("unexpected review alerts: %+v", reviews.Items)
	}
	id := reviews.Items[0].ID
	detail := collection + "/" + id
	ack := detail + "/acknowledgement"
	if w := send("GET", detail, "viewer", "", "", 200); w.Header().Get("ETag") != `"1"` {
		t.Fatalf("unexpected alert ETag: %v", w.Header())
	}
	send("GET", otherProject+"/"+id, "admin", "", "", 403)
	send("POST", ack, "viewer", `"1"`, "", 403)
	send("POST", ack, "admin", "", "", 412)
	send("POST", ack, "admin", `"1"`, "{}", 400)
	exec(`ALTER TABLE cmp_project_audit ADD CONSTRAINT fixture_reject_ack CHECK (action<>'acknowledged') NOT VALID`)
	send("POST", ack, "admin", `"1"`, "", 503)
	exec("ALTER TABLE cmp_project_audit DROP CONSTRAINT fixture_reject_ack")
	if w := send("GET", detail, "viewer", "", "", 200); w.Header().Get("ETag") != `"1"` {
		t.Fatalf("failed audit changed alert: %v", w.Header())
	}
	server := httptest.NewTLSServer(app)
	defer server.Close()
	var output bytes.Buffer
	if err := commands.RunAlertsClient(ctx, []string{"acknowledge", "tenant", "project", id, "--if-match", "1"}, server.URL,
		token("admin"), &output, server.Client().Transport); err != nil {
		t.Fatal(err)
	}
	var acknowledged alerts.Alert
	if err := json.Unmarshal(output.Bytes(), &acknowledged); err != nil || acknowledged.Status != "acknowledged" ||
		acknowledged.AcknowledgedBy != "admin" || acknowledged.Revision != 2 {
		t.Fatalf("CLI acknowledgement %+v: %v", acknowledged, err)
	}
	send("POST", ack, "admin", `"1"`, "", 412)
	if p := page(collection+"?status=acknowledged", "viewer"); len(p.Items) != 1 || p.Items[0].ID != id {
		t.Fatalf("acknowledgement suppressed or resolved the alert: %+v", p)
	}
	count("SELECT count(*) FROM cmp_operation_event_outbox", 5)
	now := time.Now().UTC()
	evaluate := func(where string) {
		t.Helper()
		exec("UPDATE cmp_project_alert_checks SET next_at=$1 WHERE "+where, now.Add(-time.Minute))
		worked, err := store.EvaluateNext(ctx)
		if err != nil || !worked {
			t.Fatalf("evaluation worked=%t err=%v", worked, err)
		}
	}
	const owner = "(tenant_id,project_id)=('tenant','project')"
	evaluate(owner)
	count("SELECT count(*) FROM cmp_project_alerts", 5)
	exec(`UPDATE cmp_instance_operations SET status='failed' WHERE operation_id='review-op';
		UPDATE cmp_instance_operations SET status='observing',updated_at=now() WHERE operation_id IN ('queued-op','slow-approval');
		UPDATE cmp_inventory_schedules SET failures=0,last_outcome='success' WHERE (account_id,kind)=('aws','instance');
		INSERT INTO cmp_inventory_scopes (tenant_id,account_id,region,kind,credential_revision,lease_until,completed_at)
		VALUES ('tenant','aws','us-east-1','instance',1,now(),now());`)
	evaluate(owner)
	if p := page(collection+"?status=resolved", "viewer"); len(p.Items) != 5 {
		t.Fatalf("unfinished recoveries: %+v", p)
	}
	var resolved alerts.Alert
	w := send("GET", detail, "viewer", "", "", 200)
	testsupport.DecodeProjectData(t, w, &resolved)
	if resolved.Status != "resolved" || resolved.Revision != 3 || resolved.AcknowledgedBy != "admin" ||
		resolved.ResolvedAt == nil || w.Header().Get("ETag") != `"3"` {
		t.Fatalf("resolution lost acknowledgement: %+v", resolved)
	}
	count("SELECT count(*) FROM cmp_operation_event_outbox WHERE event_type='project.alert.resolved'", 5)
	var ledger audit.Page
	testsupport.DecodeProjectData(t, send("GET", project+"/audit?category=alert&subject_id="+id, "admin", "", "", 200), &ledger)
	if len(ledger.Items) != 3 || ledger.Items[0].Action != "resolved" || ledger.Items[1].Actor != "admin" ||
		ledger.Items[1].Action != "acknowledged" || ledger.Items[2].Action != "opened" {
		t.Fatalf("lost audit transition: %+v", ledger)
	}
	exec("UPDATE cmp_instance_operations SET status='needs_review' WHERE operation_id='review-op'")
	evaluate(owner)
	if p := page(collection+"?rule=operation_review", "viewer"); len(p.Items) != 1 || p.Items[0].ID == id {
		t.Fatalf("recurrent issue reused an old alert: %+v", p)
	}
	exec("UPDATE cmp_inventory_schedules SET failures=3,last_outcome='failure' WHERE (account_id,kind)=('aws','instance')")
	evaluate(owner)
	if p := page(collection+"?rule=scan_failures", "viewer"); len(p.Items) != 1 {
		t.Fatalf("original owner did not receive inventory alert: %+v", p)
	}
	exec("UPDATE cmp_cloud_accounts SET project_id='other' WHERE account_id='aws'")
	evaluate(owner)
	evaluate("(tenant_id,project_id)=('tenant','other')")
	if p := page(collection+"?rule=scan_failures", "viewer"); len(p.Items) != 0 {
		t.Fatalf("original owner retained live inventory alerts after transfer: %+v", p)
	}
	if p := page(otherProject+"?rule=scan_failures", "admin"); len(p.Items) != 1 || p.Items[0].Account != "aws" {
		t.Fatalf("new owner did not receive inventory alert: %+v", p)
	}
	send("GET", collection+"?after="+id, "foreign-admin", "", "", 403)
}
