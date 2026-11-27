package main

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"with-cloud-manager/internal/alerts"
	"with-cloud-manager/internal/dbtest"
)

const (
	queueRecoveryAlert = "operation_queue_recovery"
	queueAnomalyAlert  = "operation_queue_anomaly"
	queueAlertPath     = "/v1/tenants/lab/projects/compute/alerts"
)

func TestAcceptanceOperationQueueAlerts(t *testing.T) {
	db := dbtest.Open(t)
	ctx := t.Context()
	if err := migrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	const token = "fixture-operation-queue-alert-viewer"
	const adminToken = "fixture-operation-queue-alert-admin"
	seedOperationBacklog(t, db, token)
	seedQueueAlertEvidence(t, db, adminToken)
	store := alerts.NewStore(db).WithProjectEvaluation(evaluateProjectAlerts)
	app, err := newAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	list := func(rule, status string) []alerts.Alert {
		t.Helper()
		var page alerts.Page
		response := freshnessRequest(t, app, queueAlertPath+"?rule="+rule+"&status="+status, token)
		decodeProjectData(t, response, &page)
		return page.Items
	}
	evaluate := func() {
		t.Helper()
		pitrExec(t, db, `UPDATE cmp_project_alert_checks SET next_at=now()-interval '1 minute'
			WHERE (tenant_id,project_id)=('lab','compute')`)
		if worked, err := store.EvaluateNext(ctx); !worked || err != nil {
			t.Fatalf("alert evaluation worked=%t error=%v", worked, err)
		}
	}
	assertCounts := func(active, opened, resolved int) {
		t.Helper()
		var current, sentOpen, sentResolved int
		err := db.QueryRowContext(ctx, `SELECT
			(SELECT count(*) FROM cmp_project_alerts WHERE rule LIKE 'operation_queue_%' AND resolved_at IS NULL),
			count(*) FILTER(WHERE o.event_type='project.alert.opened'),
			count(*) FILTER(WHERE o.event_type='project.alert.resolved')
			FROM cmp_operation_event_outbox o JOIN cmp_project_alerts a USING (alert_id)
			WHERE a.rule LIKE 'operation_queue_%'`).Scan(&current, &sentOpen, &sentResolved)
		if err != nil || current != active || sentOpen != opened || sentResolved != resolved {
			t.Fatalf("count active=%d opened=%d resolved=%d error=%v", current, sentOpen, sentResolved, err)
		}
	}
	// 发件箱失败时，检查计划、审计和告警应一起回滚。
	pitrExec(t, db, `ALTER TABLE cmp_operation_event_outbox ADD CONSTRAINT reject_queue_alert_for_test
		CHECK (convert_from(body,'UTF8')::jsonb->>'rule'<>'operation_queue_anomaly') NOT VALID`)
	if worked, err := store.EvaluateNext(ctx); worked || err == nil {
		t.Fatalf("notification failure evaluated a project: worked=%t error=%v", worked, err)
	}
	assertCounts(0, 0, 0)
	var rolledBack bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT FROM cmp_project_alert_checks
		WHERE (tenant_id,project_id)=('lab','compute') AND next_at<=statement_timestamp())
		AND NOT EXISTS (SELECT FROM cmp_project_audit WHERE category='alert')`).Scan(&rolledBack); err != nil || !rolledBack {
		t.Fatalf("failed notification advanced checks or left audit: %t, %v", rolledBack, err)
	}
	pitrExec(t, db, `ALTER TABLE cmp_operation_event_outbox DROP CONSTRAINT reject_queue_alert_for_test`)
	var workers sync.WaitGroup
	const replicas = 6
	results := make(chan bool, replicas)
	for range replicas {
		workers.Go(func() {
			worked, err := store.EvaluateNext(ctx)
			if err != nil {
				t.Error(err)
			}
			results <- worked
		})
	}
	workers.Wait()
	close(results)
	claimed := 0
	for worked := range results {
		if worked {
			claimed++
		}
	}
	if claimed != 1 {
		t.Fatalf("due project checks committed %d times", claimed)
	}
	assertCounts(4, 4, 0)
	recovery, anomalies := list(queueRecoveryAlert, "active"), list(queueAnomalyAlert, "active")
	assertQueueAlertAccounts(t, recovery, "recoverable", "hw", "tc")
	assertQueueAlertAccounts(t, anomalies, "anomaly", "ali", "hw")
	var first alerts.Alert
	for _, alert := range anomalies {
		if alert.Account == "ali" {
			first = alert
		}
	}
	scheduleResponse(t, app, "POST", queueAlertPath+"/"+first.ID+"/acknowledgement", token, `"1"`, "", 403)
	var acknowledged alerts.Alert
	decodeProjectData(t, scheduleResponse(t, app, "POST", queueAlertPath+"/"+first.ID+"/acknowledgement", adminToken, `"1"`, "", 200), &acknowledged)
	if acknowledged.Status != "acknowledged" || acknowledged.Revision != 2 {
		t.Fatalf("queue alert acknowledgement was not retained: %+v", acknowledged)
	}
	for _, wrong := range []string{strings.Replace(queueAlertPath, "/lab/", "/foreign/", 1), strings.Replace(queueAlertPath, "/compute/", "/hidden/", 1)} {
		requireFreshnessStatus(t, freshnessRequest(t, app, wrong+"?rule="+queueAnomalyAlert, token), http.StatusForbidden)
	}
	server := httptest.NewTLSServer(app)
	defer server.Close()
	var output bytes.Buffer
	var page alerts.Page
	err = runAlertsClient(ctx, []string{"list", "lab", "compute", "--rule", queueRecoveryAlert}, server.URL, token, &output, server.Client().Transport)
	if err != nil || json.Unmarshal(output.Bytes(), &page) != nil || len(page.Items) != 2 {
		t.Fatalf("CLI queue alert filter failed: %s, %v", &output, err)
	}
	var safeBodies bool
	err = db.QueryRowContext(ctx, `SELECT COALESCE(bool_and(o.sink_id='queue-alerts' AND payload->>'tenant'='lab'
		AND payload->>'project'='compute' AND payload->>'account'=a.account_id AND payload->>'rule'=a.rule
		AND NOT (payload ? 'lease_owner') AND convert_from(body,'UTF8') NOT LIKE '%private-payload%'),false)
		FROM cmp_operation_event_outbox o JOIN cmp_project_alerts a USING (alert_id)
		CROSS JOIN LATERAL (SELECT convert_from(o.body,'UTF8')::jsonb AS payload) b WHERE a.rule LIKE 'operation_queue_%'`).Scan(&safeBodies)
	if err != nil || !safeBodies {
		t.Fatalf("unsafe subscription evidence: %t, %v", safeBodies, err)
	}

	evaluate()
	assertCounts(4, 4, 0)
	pitrExec(t, db, `UPDATE cmp_operation_tasks SET expires_at=NULL WHERE id='expires';
		UPDATE cmp_operation_tasks SET lease_until=now()+interval '1 minute' WHERE id IN ('tc-overdue','tc-overdue-east','recovery');
		UPDATE cmp_instance_operations SET status='failed' WHERE operation_id IN ('dead','foreign-task');`)
	evaluate()
	assertCounts(1, 4, 3)
	active := list(queueAnomalyAlert, "active")
	if len(active) != 1 || active[0].ID != first.ID || active[0].Status != "acknowledged" {
		t.Fatalf("partial repair closed or reset a queue alert: %+v", active)
	}
	pitrExec(t, db, `UPDATE cmp_instance_operations SET status='failed' WHERE operation_id='missing'`)
	evaluate()
	assertCounts(0, 4, 4)
	if len(list(queueAnomalyAlert, "resolved")) != 2 || len(list(queueRecoveryAlert, "resolved")) != 2 {
		t.Fatal("project did not retain recovery history")
	}
	pitrExec(t, db, `UPDATE cmp_instance_operations SET status='queued' WHERE operation_id='missing'`)
	evaluate()
	assertCounts(1, 5, 4)
	active = list(queueAnomalyAlert, "active")
	if len(active) != 1 || active[0].ID == first.ID || active[0].Status != "open" {
		t.Fatalf("recurrence reused the closed evidence: %+v", active)
	}
}

func assertQueueAlertAccounts(t *testing.T, page []alerts.Alert, source string, expected ...string) {
	t.Helper()
	actual := make([]string, 0, len(page))
	for _, entry := range page {
		if entry.SourceStatus != source || entry.SubjectID != entry.Account || entry.ResourceKind != "operation-queue" || entry.Region != "" {
			t.Fatalf("incorrect queue alert evidence: %+v", entry)
		}
		actual = append(actual, entry.Account)
	}
	slices.Sort(actual)
	if !slices.Equal(actual, expected) {
		t.Fatalf("queue alerts for %s: %v, want %v", source, actual, expected)
	}
}

func seedQueueAlertEvidence(t *testing.T, db *sql.DB, adminToken string) {
	t.Helper()
	digest := sha256.Sum256([]byte(adminToken))
	pitrExec(t, db, `INSERT INTO cmp_principals (tenant_id,principal_id,kind,issuer,subject)
		VALUES ('lab','admin','service','fixture','queue-admin');
		INSERT INTO cmp_memberships (tenant_id,project_id,principal_id,role) VALUES ('lab','compute','admin','admin');
		INSERT INTO cmp_operation_event_sinks (sink_id,tenant_id,project_id,endpoint_hash,include_alerts) VALUES
		('queue-alerts','lab','compute',decode(repeat('01',32),'hex'),true),
		('operation-only','lab','compute',decode(repeat('02',32),'hex'),false),
		('other-project','lab','hidden',decode(repeat('03',32),'hex'),true);`)
	pitrExec(t, db, `INSERT INTO cmp_service_tokens(token_hash,tenant_id,principal_id,expires_at)
		VALUES ($1,'lab','admin',now()+interval '5 minutes')`, digest[:])
	pitrExec(t, db, `INSERT INTO cmp_instance_operations (operation_id,tenant_id,project_id,principal_id,account_id,
		region,resource_kind,action,status,idempotency_key,request_hash,credential_revision,membership_revision,ownership_revision)
		SELECT id,'lab',project,'viewer',account,region,'network','create-network','applying',id,decode(repeat('00',32),'hex'),1,1,0
		FROM (VALUES ('tc-overdue','compute','tc','ap-test'),('tc-overdue-east','compute','tc','ap-other'),
		('ali-deferred','compute','ali','cn-test'),('hidden-no-task','hidden','tc','ap-test')) AS f(id,project,account,region);
		INSERT INTO cmp_operation_tasks (id,topic,tenant_id,priority,status,attempts,max_attempts,available_at,created_at,updated_at,lease_owner,lease_until)
		SELECT id,'cmp.instance.v1','lab',0,'leased',1,8,now()+delay,now()-interval '5 minutes',now(),'secret-owner',now()-interval '3 minutes'
		FROM (VALUES ('tc-overdue','-5 minutes'::interval),('tc-overdue-east','-5 minutes'),('ali-deferred','1 hour')) f(id,delay);
		UPDATE cmp_operation_tasks SET lease_until=now()-interval '3 minutes' WHERE id='recovery';
		UPDATE cmp_project_alert_checks SET next_at=CASE WHEN (tenant_id,project_id)=('lab','compute') THEN now()-interval '1 minute'
		ELSE now()+interval '1 hour' END;`)
}
