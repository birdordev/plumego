package cloudtest

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"with-cloud-manager/app/commands"
	"with-cloud-manager/app/integration/testsupport"
	alertservice "with-cloud-manager/app/services/alerts"
	"with-cloud-manager/internal/alerts"
)

func TestAcceptanceDatabaseProtectionAlerts(t *testing.T) {
	db, app := prepareFreshnessReport(t)
	seedDatabaseProtection(t, db)
	execFreshness(t, db, `
		INSERT INTO cmp_inventory_schedules (tenant_id,account_id,region,kind,target_id,interval_seconds,enabled)
		VALUES ('lab','tc','ap-guangzhou','database-backups','cdb-missing',600,true),
		       ('lab','tc','ap-beijing','database-backups','cdb-missing',600,true),
		       ('lab','hw','cn-north-4','database-backups','hw-old',600,true),
		       ('lab','hw','cn-north-4','database-restore-windows','hw-old',600,true),
		       ('lab','ali','cn-hangzhou','database-restore-windows','rm-sqlserver',600,true),
		       ('foreign','tc','ap-guangzhou','database-backups','cdb-missing',600,true);
		UPDATE cmp_inventory_objects SET missing_generation=NULL WHERE external_id='cdb-missing';
		UPDATE cmp_database_backups SET backups=jsonb_build_array(jsonb_build_object('status','ready','started_at',now()-interval '2 days'))
			WHERE scope_id IN (SELECT scope_id FROM cmp_inventory_scopes WHERE (tenant_id,account_id,region)=('lab','tc','ap-beijing'))
			AND external_id='cdb-missing';
		UPDATE cmp_database_restore_windows SET windows='[]' WHERE external_id='rm-ready';
		INSERT INTO cmp_operation_event_sinks (sink_id,tenant_id,project_id,endpoint_hash,include_alerts) VALUES
			('database-owner','lab','compute',decode(repeat('51',32),'hex'),true),
			('database-shared','lab','shared',decode(repeat('52',32),'hex'),true),
			('database-optout','lab','compute',decode(repeat('53',32),'hex'),false)`)

	const backup = "database_backup_coverage"
	const restore = "database_restore_coverage"
	const base = "/v1/tenants/lab/projects/"
	const tc = "tc/ap-guangzhou/cdb-missing"
	const beijing = "tc/ap-beijing/cdb-missing"
	const hw = "hw/cn-north-4/hw-old"
	const ali = "ali/cn-hangzhou/rm-ready"
	store := alerts.NewStore(db).WithProjectEvaluation(alertservice.EvaluateProjectAlerts)
	due := func(project string) {
		t.Helper()
		execFreshness(t, db, `UPDATE cmp_project_alert_checks SET next_at=statement_timestamp() +
			CASE WHEN (tenant_id,project_id)=('lab',$1) THEN interval '-1 second' ELSE interval '1 hour' END`, project)
	}
	evaluate := func(project string) {
		t.Helper()
		due(project)
		if worked, err := store.EvaluateNext(t.Context()); err != nil || !worked {
			t.Fatalf("database protection evaluation: worked=%t error=%v", worked, err)
		}
	}
	requireAlerts := func(project, rule, status string, want map[string]string) map[string]alerts.Alert {
		t.Helper()
		path := base + project + "/alerts?rule=" + rule + "&status=" + status
		response := freshnessRequest(t, app, path, freshnessToken)
		requireFreshnessStatus(t, response, http.StatusOK)
		if strings.Contains(response.Body.String(), "fixture/private") {
			t.Fatal("database alert leaked private credential data")
		}
		var page alerts.Page
		testsupport.DecodeProjectData(t, response, &page)
		actual := make(map[string]alerts.Alert, len(page.Items))
		for _, item := range page.Items {
			if item.ResourceKind != "database-instance" || item.SourceStatus != want[item.SubjectID] ||
				!strings.HasPrefix(item.SubjectID, item.Account+"/"+item.Region+"/") {
				t.Fatalf("unexpected database alert: %+v", item)
			}
			actual[item.SubjectID] = item
		}
		if len(actual) != len(want) || len(actual) != len(page.Items) {
			t.Fatalf("unexpected subjects for %s: %+v", rule, page.Items)
		}
		return actual
	}

	execFreshness(t, db, `ALTER TABLE cmp_operation_event_outbox ADD CONSTRAINT fixture_database_notification
		CHECK (convert_from(body,'UTF8')::jsonb->>'rule' NOT IN ('database_backup_coverage','database_restore_coverage')) NOT VALID`)
	due("compute")
	if _, err := store.EvaluateNext(t.Context()); err == nil {
		t.Fatal("expected database alert notification to fail")
	}
	requireAlerts("compute", backup, "all", nil)
	requireAlerts("compute", restore, "all", nil)
	execFreshness(t, db, `ALTER TABLE cmp_operation_event_outbox DROP CONSTRAINT fixture_database_notification`)
	runConcurrentProjectEvaluators(t, store)
	opened := requireAlerts("compute", backup, "active", map[string]string{tc: "missing", beijing: "outdated", hw: "outdated"})
	requireAlerts("compute", restore, "active", map[string]string{ali: "missing"})
	evaluate("shared")
	requireAlerts("shared", backup, "all", nil)
	requireFreshnessStatus(t, freshnessRequest(t, app, "/v1/tenants/foreign/projects/compute/alerts?rule="+backup, freshnessToken), http.StatusForbidden)

	server := httptest.NewTLSServer(app)
	t.Cleanup(server.Close)
	var output bytes.Buffer
	err := commands.RunAlertsClient(t.Context(), []string{"list", "lab", "compute", "--rule", restore},
		server.URL, freshnessToken, &output, server.Client().Transport)
	if err != nil || !strings.Contains(output.String(), ali) {
		t.Fatalf("restore alert missing from CLI: %v", err)
	}

	execFreshness(t, db, `UPDATE cmp_database_backups SET observed_at=now()-interval '2 days' WHERE external_id='cdb-missing';
		UPDATE cmp_cloud_accounts SET credential_revision=2 WHERE (tenant_id,account_id)=('lab','ali');
		UPDATE cmp_inventory_scopes SET credential_revision=2 WHERE (tenant_id,account_id,kind)=('lab','ali','database-instance');
		UPDATE cmp_inventory_schedules SET enabled=false WHERE (tenant_id,account_id,kind,target_id)=('lab','hw','database-backups','hw-old')`)
	evaluate("compute")
	requireAlerts("compute", backup, "active", map[string]string{tc: "missing", beijing: "outdated"})
	requireAlerts("compute", restore, "active", map[string]string{ali: "missing"})

	execFreshness(t, db, `UPDATE cmp_database_backups SET observed_at=now(),backups=jsonb_build_array(
		jsonb_build_object('status','ready','started_at',now()-interval '1 hour')) WHERE external_id='cdb-missing';
		UPDATE cmp_database_restore_windows SET observed_at=now(),credential_revision=2,windows=jsonb_build_array(
			jsonb_build_object('start_at',now()-interval '2 hours','end_at',now()-interval '1 hour')) WHERE external_id='rm-ready'`)
	evaluate("compute")
	requireAlerts("compute", backup, "active", nil)
	requireAlerts("compute", restore, "active", nil)
	resolved := requireAlerts("compute", backup, "resolved", map[string]string{tc: "missing", beijing: "outdated", hw: "outdated"})
	if resolved[tc].ID != opened[tc].ID || resolved[tc].Revision != 2 {
		t.Fatalf("recovery changed the identity of the alert: %+v", resolved[tc])
	}

	execFreshness(t, db, `UPDATE cmp_database_backups SET backups='[]'
		WHERE scope_id IN (SELECT scope_id FROM cmp_inventory_scopes WHERE (tenant_id,account_id,region)=('lab','tc','ap-guangzhou'))
		AND external_id='cdb-missing'`)
	evaluate("compute")
	active := requireAlerts("compute", backup, "active", map[string]string{tc: "missing"})
	if active[tc].ID == opened[tc].ID {
		t.Fatal("recurrence reused the resolved alert")
	}
	execFreshness(t, db, `INSERT INTO cmp_asset_ownership (tenant_id,scope_id,external_id,project_id,revision)
		SELECT tenant_id,scope_id,'cdb-missing','shared',2 FROM cmp_inventory_scopes
		WHERE (tenant_id,account_id,region,kind)=('lab','tc','ap-guangzhou','database-instance')`)
	evaluate("compute")
	evaluate("shared")
	requireAlerts("compute", backup, "active", nil)
	requireAlerts("shared", backup, "active", map[string]string{tc: "missing"})

	var openedEvents, resolvedEvents, sharedEvents, optOutEvents int
	err = db.QueryRowContext(t.Context(), `SELECT
		count(*) FILTER (WHERE sink_id='database-owner' AND event_type='project.alert.opened'),
		count(*) FILTER (WHERE sink_id='database-owner' AND event_type='project.alert.resolved'),
		count(*) FILTER (WHERE sink_id='database-shared' AND event_type='project.alert.opened'),
		count(*) FILTER (WHERE sink_id='database-optout')
		FROM cmp_operation_event_outbox
		WHERE convert_from(body,'UTF8')::jsonb->>'rule' IN ($1,$2)`, backup, restore).
		Scan(&openedEvents, &resolvedEvents, &sharedEvents, &optOutEvents)
	if err != nil || openedEvents != 5 || resolvedEvents != 5 || sharedEvents != 1 || optOutEvents != 0 {
		t.Fatalf("database notifications opened=%d resolved=%d shared=%d opted-out=%d: %v",
			openedEvents, resolvedEvents, sharedEvents, optOutEvents, err)
	}
}
