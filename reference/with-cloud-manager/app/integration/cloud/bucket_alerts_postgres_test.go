package cloudtest

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"with-cloud-manager/app/commands"
	"with-cloud-manager/app/integration/testsupport"
	alertservice "with-cloud-manager/app/services/alerts"
	"with-cloud-manager/internal/alerts"
)

const bucketEncryptionAlert = "bucket_encryption_unconfigured"
const bucketVersioningAlert = "bucket_versioning_inactive"

func TestAcceptanceBucketProtectionAlerts(t *testing.T) {
	db, app := prepareFreshnessReport(t)
	seedBucketAlertEvidence(t, db)
	ctx := t.Context()
	store := alerts.NewStore(db).WithProjectEvaluation(alertservice.EvaluateProjectAlerts)
	const project = "/v1/tenants/lab/projects/compute/alerts"
	list := func(path string) []alerts.Alert {
		t.Helper()
		var page alerts.Page
		response := freshnessRequest(t, app, path, freshnessToken)
		requireFreshnessStatus(t, response, http.StatusOK)
		testsupport.DecodeProjectData(t, response, &page)
		if strings.Contains(response.Body.String(), "fixture/private") || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("unsafe bucket alert response")
		}
		return page.Items
	}
	due := func(project string) {
		t.Helper()
		execFreshness(t, db, `UPDATE cmp_project_alert_checks SET next_at=statement_timestamp() +
			CASE WHEN (tenant_id,project_id)=('lab',$1) THEN interval '-1 second' ELSE interval '1 hour' END`, project)
	}
	evaluate := func(project string) {
		t.Helper()
		due(project)
		if worked, err := store.EvaluateNext(ctx); err != nil || !worked {
			t.Fatalf("bucket protection evaluation: worked=%t error=%v", worked, err)
		}
	}
	execFreshness(t, db, `ALTER TABLE cmp_operation_event_outbox ADD CONSTRAINT fixture_bucket_notification
		CHECK (event_type<>'project.alert.opened') NOT VALID`)
	due("compute")
	if _, err := store.EvaluateNext(ctx); err == nil || len(list(project)) != 0 {
		t.Fatal("notification failure did not roll back bucket alerts")
	}
	execFreshness(t, db, `ALTER TABLE cmp_operation_event_outbox DROP CONSTRAINT fixture_bucket_notification`)
	runConcurrentProjectEvaluators(t, store)
	encryption := list(project + "?rule=" + bucketEncryptionAlert)
	assertBucketAlertAccounts(t, encryption, "ali", "tc")
	assertBucketAlertAccounts(t, list(project+"?rule="+bucketVersioningAlert), "hw", "tc")
	var oldAli alerts.Alert
	for _, item := range encryption {
		if item.Account == "ali" {
			oldAli = item
		}
	}
	if oldAli.Status != "open" || oldAli.SubjectID != "ali" || oldAli.SourceStatus != "encryption_unconfigured" {
		t.Fatalf("unexpected account subject: %+v", oldAli)
	}
	const adminToken = "fixture-bucket-alert-admin-token"
	digest := sha256.Sum256([]byte(adminToken))
	execFreshness(t, db, `INSERT INTO cmp_principals(tenant_id,principal_id,kind,issuer,subject)
		VALUES ('lab','alert-admin','service','fixture','bucket-alerts');
		INSERT INTO cmp_memberships(tenant_id,project_id,principal_id,role) VALUES ('lab','compute','alert-admin','admin')`)
	execFreshness(t, db, `INSERT INTO cmp_service_tokens (token_hash,tenant_id,principal_id,expires_at)
		VALUES ($1,'lab','alert-admin',now()+interval '10 minutes')`, digest[:])
	ackPath := project + "/" + oldAli.ID + "/acknowledgement"
	scheduleResponse(t, app, "POST", ackPath, freshnessToken, `"1"`, "", 403)
	var acknowledged alerts.Alert
	testsupport.DecodeProjectData(t, scheduleResponse(t, app, "POST", ackPath, adminToken, `"1"`, "", 200), &acknowledged)
	if acknowledged.Status != "acknowledged" || acknowledged.Revision != 2 {
		t.Fatalf("unexpected bucket acknowledgement: %+v", acknowledged)
	}
	evaluate("shared")
	if shared := list(strings.Replace(project, "/compute/", "/shared/", 1)); len(shared) != 0 {
		t.Fatalf("sharing duplicated owner alerts: %+v", shared)
	}
	for _, wrong := range []string{strings.Replace(project, "/lab/", "/foreign/", 1), strings.Replace(project, "/compute/", "/denied/", 1)} {
		requireFreshnessStatus(t, freshnessRequest(t, app, wrong+"?rule="+bucketEncryptionAlert, freshnessToken), http.StatusForbidden)
	}
	checkBucketAlertCLI(t, app)

	// 已知健康的同账号桶不能覆盖过期、凭据换代或盘点待确认的证据缺口。
	execFreshness(t, db, `UPDATE cmp_bucket_encryption e SET state='aes256',
		observed_at=CASE WHEN e.external_id LIKE 'safe-cos@%' THEN now()-interval '2 days' ELSE now() END
		FROM cmp_inventory_scopes s WHERE e.scope_id=s.scope_id AND s.tenant_id='lab' AND s.account_id IN ('tc','ali');
		UPDATE cmp_cloud_accounts SET credential_revision=2 WHERE (tenant_id,account_id)=('lab','ali');
		UPDATE cmp_inventory_scopes SET credential_revision=2 WHERE (tenant_id,account_id,kind)=('lab','ali','object-bucket');
		UPDATE cmp_inventory_objects o SET missing_generation=2 FROM cmp_inventory_scopes s
			WHERE o.scope_id=s.scope_id AND (s.tenant_id,s.account_id)=('lab','hw');
		UPDATE cmp_bucket_versioning v SET state='enabled' FROM cmp_inventory_scopes s
			WHERE v.scope_id=s.scope_id AND (s.tenant_id,s.account_id)=('lab','hw');
		UPDATE cmp_inventory_schedules SET enabled=false WHERE (tenant_id,account_id,kind)=('lab','tc','object-bucket-versioning')`)
	evaluate("compute")
	assertBucketAlertAccounts(t, list(project+"?rule="+bucketEncryptionAlert), "ali", "tc")
	assertBucketAlertAccounts(t, list(project+"?rule="+bucketVersioningAlert), "hw")

	execFreshness(t, db, `UPDATE cmp_bucket_encryption e SET observed_at=now(),credential_revision=s.credential_revision
		FROM cmp_inventory_scopes s WHERE e.scope_id=s.scope_id AND s.tenant_id='lab' AND s.account_id IN ('tc','ali');
		UPDATE cmp_inventory_objects o SET missing_generation=NULL FROM cmp_inventory_scopes s
			WHERE o.scope_id=s.scope_id AND (s.tenant_id,s.account_id)=('lab','hw')`)
	evaluate("compute")
	assertBucketAlertAccounts(t, list(project+"?rule="+bucketEncryptionAlert+"&status=resolved"), "ali", "tc")
	assertBucketAlertAccounts(t, list(project+"?rule="+bucketVersioningAlert+"&status=resolved"), "hw", "tc")
	var resolvedAli alerts.Alert
	testsupport.DecodeProjectData(t, freshnessRequest(t, app, project+"/"+oldAli.ID, freshnessToken), &resolvedAli)
	if resolvedAli.Revision != 3 || resolvedAli.Status != "resolved" || resolvedAli.AcknowledgedBy != "alert-admin" {
		t.Fatalf("recovery lost the acknowledgement: %+v", resolvedAli)
	}
	execFreshness(t, db, `UPDATE cmp_bucket_encryption e SET state='unconfigured',observed_at=now() FROM cmp_inventory_scopes s
		WHERE e.scope_id=s.scope_id AND (s.tenant_id,s.account_id)=('lab','ali')`)
	evaluate("compute")
	active := list(project + "?rule=" + bucketEncryptionAlert)
	assertBucketAlertAccounts(t, active, "ali")
	if active[0].ID == oldAli.ID {
		t.Fatal("recurrence reopened the old alert record")
	}
	execFreshness(t, db, `INSERT INTO cmp_asset_ownership(tenant_id,scope_id,external_id,project_id,revision)
		SELECT s.tenant_id,s.scope_id,o.external_id,'shared',2 FROM cmp_inventory_scopes s
		JOIN cmp_inventory_objects o USING (scope_id) WHERE (s.tenant_id,s.account_id)=('lab','ali')`)
	evaluate("compute")
	evaluate("shared")
	assertBucketAlertAccounts(t, list(project+"?rule="+bucketEncryptionAlert))
	assertBucketAlertAccounts(t, list(strings.Replace(project, "/compute/", "/shared/", 1)+"?rule="+bucketEncryptionAlert), "ali")
	var opened, resolved, auditEvents int
	if err := db.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM cmp_operation_event_outbox WHERE event_type='project.alert.opened'),
		(SELECT count(*) FROM cmp_operation_event_outbox WHERE event_type='project.alert.resolved'),
		(SELECT count(*) FROM cmp_project_audit WHERE category='alert' AND outcome IN ($1,$2))`,
		bucketEncryptionAlert, bucketVersioningAlert).Scan(&opened, &resolved, &auditEvents); err != nil || opened != 6 || resolved != 5 || auditEvents != 12 {
		t.Fatalf("wrong alert audit or outbox transitions: opened=%d resolved=%d audit=%d: %v", opened, resolved, auditEvents, err)
	}
}

func seedBucketAlertEvidence(t *testing.T, db *sql.DB) {
	t.Helper()
	execFreshness(t, db, `INSERT INTO cmp_inventory_scopes (tenant_id,account_id,region,kind,credential_revision,lease_until,running)
		SELECT tenant_id,account_id,'global','object-bucket',1,now(),false FROM cmp_cloud_accounts WHERE account_id IN ('tc','ali','hw');
		WITH fixture(tenant,account,name,location) AS (VALUES
			('lab','tc','bkt-cos','ap-guangzhou'),('lab','tc','safe-cos','ap-guangzhou'),('lab','ali','bkt-oss','cn-hangzhou'),
			('lab','hw','bkt-obs','cn-north-4'),('foreign','tc','bkt-cos','ap-guangzhou'))
		INSERT INTO cmp_inventory_objects(scope_id,external_id,name,status,seen_generation,object_bucket)
			SELECT s.scope_id,f.name||'@2026-01-01T00:00:00Z',f.name,'observed',1,jsonb_build_object('location',f.location)
			FROM fixture f JOIN cmp_inventory_scopes s ON (s.tenant_id,s.account_id)=(f.tenant,f.account) WHERE s.kind='object-bucket';
		INSERT INTO cmp_inventory_schedules (tenant_id,account_id,region,kind,interval_seconds,enabled)
			SELECT tenant_id,account_id,region,setting,3600, NOT (account_id='ali' AND setting='object-bucket-versioning') FROM cmp_inventory_scopes
			CROSS JOIN (VALUES ('object-bucket-encryption'),('object-bucket-versioning')) AS config(setting) WHERE kind='object-bucket';
		INSERT INTO cmp_bucket_encryption (scope_id,external_id,credential_revision,location,state,refresh_started_at,observed_at)
			SELECT scope_id,external_id,1,object_bucket->>'location','unconfigured',
				now(),CASE name WHEN 'bkt-obs' THEN now()-interval '2 days' ELSE now() END
			FROM cmp_inventory_objects WHERE object_bucket IS NOT NULL;
		INSERT INTO cmp_bucket_versioning (scope_id,external_id,credential_revision,location,state,refresh_started_at,observed_at)
			SELECT scope_id,external_id,1,object_bucket->>'location',
				CASE name WHEN 'bkt-obs' THEN 'suspended' ELSE 'disabled' END,now(),now()
			FROM cmp_inventory_objects WHERE object_bucket IS NOT NULL;
		INSERT INTO cmp_operation_event_sinks (sink_id,tenant_id,project_id,endpoint_hash,include_alerts) VALUES
			('bucket-owner','lab','compute',decode(repeat('41',32),'hex'),true),
			('bucket-shared','lab','shared',decode(repeat('42',32),'hex'),true),
			('bucket-optout','lab','compute',decode(repeat('43',32),'hex'),false)`)
}

func assertBucketAlertAccounts(t *testing.T, items []alerts.Alert, expected ...string) {
	t.Helper()
	var actual []string
	for _, item := range items {
		if item.ResourceKind != "object-bucket" || item.SubjectID != item.Account {
			t.Fatalf("wrong object bucket scope: %+v", item)
		}
		actual = append(actual, item.Account)
	}
	slices.Sort(actual)
	if !slices.Equal(actual, expected) {
		t.Fatalf("alert accounts=%v, want %v", actual, expected)
	}
}

func checkBucketAlertCLI(t *testing.T, handler http.Handler) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	var output bytes.Buffer
	err := commands.RunAlertsClient(t.Context(), []string{"list", "lab", "compute", "--rule", bucketVersioningAlert},
		server.URL, freshnessToken, &output, server.Client().Transport)
	var page alerts.Page
	if decodeErr := json.Unmarshal(output.Bytes(), &page); err != nil || decodeErr != nil {
		t.Fatalf("bucket alert CLI failed: %v / %v", err, decodeErr)
	}
	assertBucketAlertAccounts(t, page.Items, "hw", "tc")
}
