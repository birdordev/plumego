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

	"github.com/birdordev/plumego/x/openapi"

	"with-cloud-manager/app/commands"
	"with-cloud-manager/app/integration/testsupport"
	"with-cloud-manager/internal/inventory"
)

const databaseProtectionFixturePath = "/v1/tenants/lab/projects/compute/governance/database-protection"

func TestAcceptanceDatabaseProtectionReport(t *testing.T) {
	db, app := prepareFreshnessReport(t)
	seedDatabaseProtection(t, db)
	read := func(path string) inventory.DatabaseProtectionReport {
		t.Helper()
		response := freshnessRequest(t, app, path, freshnessToken)
		requireFreshnessStatus(t, response, http.StatusOK)
		var report inventory.DatabaseProtectionReport
		testsupport.DecodeProjectData(t, response, &report)
		if response.Header().Get("Cache-Control") != "no-store" || report.Items == nil || report.AsOf.IsZero() {
			t.Fatal("report missed cache isolation or its snapshot")
		}
		return report
	}
	const path = databaseProtectionFixturePath
	want := inventory.DatabaseProtectionSummary{Total: 8, InventoryUnavailable: 2, RefreshFailed: 1,
		Backups:        inventory.DatabaseProtectionCounts{Ready: 1, Outdated: 1, Missing: 1, Unobserved: 5},
		RestoreWindows: inventory.DatabaseProtectionCounts{Ready: 2, Missing: 1, Stale: 1, Unobserved: 3, Unsupported: 1}}
	items := make(map[string]inventory.DatabaseProtectionItem)
	for next := ""; ; {
		params := url.Values{"limit": {"2"}}
		if next != "" {
			params.Set("after", next)
		}
		report := read(path + "?" + params.Encode())
		if report.Summary != want || len(report.Items) != 2 || report.Window != 86400 || report.MaxAge != 86400 {
			t.Fatalf("unexpected database protection page: %+v", report)
		}
		for _, item := range report.Items {
			key := item.Account + "/" + item.Region + "/" + item.ID
			if _, exists := items[key]; exists {
				t.Fatalf("asset repeated across pages: %s", key)
			}
			items[key] = item
		}
		next = report.Next
		if next == "" {
			break
		}
	}
	if len(items) != 8 || items["tc/ap-guangzhou/mssql-tracked"].RestoreWindows.Scope != "source_instance" ||
		items["ali/cn-hangzhou/rm-ready"].RestoreWindows.LastRefreshOutcome != "throttled" ||
		items["ali/cn-hangzhou/rm-ready"].Backups.LatestEvidenceAt == nil ||
		items["paused/ap-beijing/cdb-paused"].Backups.ObservedAt != nil ||
		items["tc/ap-beijing/cdb-missing"].Backups.LatestEvidenceAt != nil {
		t.Fatalf("incorrect project-scoped evidence: %+v", items)
	}
	failed := read(path + "?finding=refresh_failed")
	if failed.Summary != want || len(failed.Items) != 1 || failed.Items[0].ID != "rm-ready" {
		t.Fatalf("refresh failure filter affected totals or missed the throttled plan: %+v", failed)
	}
	old := read(path + "?account=hw&max_age_seconds=259200")
	if old.Summary.Total != 1 || old.Summary.Backups.Ready != 1 || old.Summary.RestoreWindows.Stale != 1 {
		t.Fatalf("evidence age was conflated with observation freshness: %+v", old)
	}
	recentWindow := read(path + "?account=hw&max_age_seconds=259200&window_seconds=259200")
	if recentWindow.Summary.RestoreWindows.Ready != 1 {
		t.Fatalf("observation window override was ignored: %+v", recentWindow)
	}
	shared := strings.Replace(path, "/compute/", "/shared/", 1)
	if report := read(shared); report.Summary.Total != 3 || report.Summary.RefreshFailed != 0 {
		t.Fatalf("shared project saw explicitly owned assets or account plans: %+v", report)
	}
	execFreshness(t, db, `UPDATE cmp_cloud_accounts SET credential_revision=2 WHERE (tenant_id,account_id)=('lab','ali');
		UPDATE cmp_inventory_scopes SET credential_revision=2 WHERE (tenant_id,account_id)=('lab','ali');
		DELETE FROM cmp_cloud_account_grants WHERE (tenant_id,account_id,project_id)=('lab','tc','shared')`)
	if report := read(shared); report.Summary.Total != 0 {
		t.Fatalf("revoked grant remained visible: %+v", report)
	}
	rotated := read(path + "?account=ali")
	if rotated.Summary.Backups.Unobserved != 2 || rotated.Summary.RestoreWindows.Unobserved != 1 ||
		rotated.Items[0].Backups.LatestEvidenceAt != nil || rotated.Items[0].RestoreWindows.ObservedAt != nil {
		t.Fatalf("old credential evidence carried forward: %+v", rotated)
	}
	checkDatabaseProtectionContract(t, app)
}

func seedDatabaseProtection(t *testing.T, db *sql.DB) {
	t.Helper()
	execFreshness(t, db, `
		DROP TRIGGER cmp_database_backup_evidence ON cmp_database_backups;
		DROP TRIGGER cmp_database_window_evidence ON cmp_database_restore_windows;
		DROP FUNCTION cmp_refresh_database_backup_evidence(),cmp_refresh_database_window_evidence();
		DROP FUNCTION cmp_latest_database_evidence(jsonb,text,boolean);
		ALTER TABLE cmp_database_backups DROP COLUMN latest_ready_started_at;
		ALTER TABLE cmp_database_restore_windows DROP COLUMN latest_window_end_at;
		DELETE FROM cmp_migrations WHERE version='0171_database_protection';
		WITH fixture(tenant,account,region,id,product,engine,missing) AS (VALUES
			('lab','ali','cn-hangzhou','rm-ready','alibaba-rds','mysql',NULL::bigint),
			('lab','ali','cn-hangzhou','rm-sqlserver','alibaba-rds','sqlserver',NULL),
			('lab','hw','cn-north-4','hw-old','huawei-rds','mysql',NULL),
			('lab','tc','ap-guangzhou','cdb-missing','tencent-cdb','mysql',NULL),
			('lab','tc','ap-beijing','cdb-missing','tencent-cdb','mysql',2),
			('lab','tc','ap-guangzhou','cdb-owned','tencent-cdb','mysql',NULL),
			('lab','tc','ap-guangzhou','mssql-tracked','tencent-sqlserver','sqlserver',NULL),
			('lab','paused','ap-beijing','cdb-paused','tencent-cdb','mysql',NULL),
			('foreign','tc','ap-guangzhou','cdb-missing','tencent-cdb','mysql',NULL)),
		scopes AS (
			INSERT INTO cmp_inventory_scopes (tenant_id,account_id,region,kind,credential_revision,lease_until,running)
			SELECT DISTINCT tenant,account,region,'database-instance',1,now(),false FROM fixture
			RETURNING scope_id,tenant_id,account_id,region
		)
		INSERT INTO cmp_inventory_objects(scope_id,external_id,name,status,seen_generation,missing_generation,database_instance)
		SELECT s.scope_id,f.id,f.id,'observed',1,f.missing,jsonb_build_object('product',f.product,'engine',f.engine)
		FROM fixture AS f JOIN scopes AS s ON (f.tenant,f.account,f.region)=(s.tenant_id,s.account_id,s.region);
		INSERT INTO cmp_asset_ownership(tenant_id,scope_id,external_id,project_id,revision)
			SELECT 'lab',scope_id,external_id,'compute',2 FROM cmp_inventory_objects WHERE external_id='cdb-owned';
		INSERT INTO cmp_database_backups(scope_id,external_id,product,engine,credential_revision,refresh_started_at,observed_at,window_start,window_end,backups)
		SELECT scope_id,external_id,database_instance->>'product',database_instance->>'engine',1,now(),now(),now()-interval '30 days',now(),
			jsonb_build_array(
				jsonb_build_object('status','pending','started_at',now()-interval '1 minute'),
				jsonb_build_object('status','ready','started_at',CASE WHEN external_id='hw-old' THEN now()-interval '2 days' ELSE now()-interval '1 hour' END))
		FROM cmp_inventory_objects WHERE external_id IN ('rm-ready','hw-old','cdb-missing','cdb-paused');
		INSERT INTO cmp_database_restore_windows(scope_id,external_id,product,engine,credential_revision,refresh_started_at,observed_at,recovery_scope,windows)
		SELECT scope_id,external_id,database_instance->>'product',database_instance->>'engine',1,now(),
			CASE WHEN external_id='hw-old' THEN now()-interval '2 days' ELSE now() END,
			CASE WHEN external_id='mssql-tracked' THEN 'source_instance' ELSE 'instance' END,
			CASE WHEN external_id IN ('cdb-missing','mssql-tracked') THEN '[]'::jsonb
				WHEN external_id='hw-old' THEN jsonb_build_array(jsonb_build_object('start_at',now()-interval '3 days','end_at',now()-interval '2 days'))
				ELSE jsonb_build_array(jsonb_build_object('start_at',now()-interval '2 hours','end_at',now()-interval '1 hour')) END
		FROM cmp_inventory_objects WHERE external_id IN ('rm-ready','hw-old','cdb-missing','mssql-tracked');
		INSERT INTO cmp_inventory_schedules(tenant_id,account_id,region,kind,target_id,interval_seconds,enabled,last_outcome,last_finished_at)
		VALUES ('lab','ali','cn-hangzhou','database-restore-windows','rm-ready',600,true,'throttled',now()),
		       ('lab','tc','ap-guangzhou','database-backups','mssql-tracked',600,false,'failure',now())
	`)
	if err := inventory.MigrateDatabaseProtection(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	execFreshness(t, db, `UPDATE cmp_database_backups SET backups='[{"status":"failed","started_at":"2025-01-01T00:00:00Z"}]'
		WHERE scope_id IN (SELECT scope_id FROM cmp_inventory_scopes WHERE (tenant_id,account_id,region)=('lab','tc','ap-guangzhou'))
		AND external_id='cdb-missing';
		UPDATE cmp_database_restore_windows SET windows=jsonb_build_array(
			jsonb_build_object('start_at',now()-interval '2 hours','end_at',now()-interval '1 hour'))
		WHERE external_id='mssql-tracked'`)
}

func checkDatabaseProtectionContract(t *testing.T, app http.Handler) {
	t.Helper()
	const path = databaseProtectionFixturePath
	requireFreshnessStatus(t, freshnessRequest(t, app, path, ""), http.StatusUnauthorized)
	requireFreshnessStatus(t, freshnessRequest(t, app, strings.Replace(path, "/lab/", "/foreign/", 1), freshnessToken), http.StatusForbidden)
	for _, invalid := range []string{"?window_seconds=59", "?max_age_seconds=2592001", "?limit=101", "?finding=unsupported",
		"?region=ap-beijing&after=tc/ap-guangzhou/cdb-missing", "?account=ali&account=tc", "?after=broken"} {
		requireFreshnessStatus(t, freshnessRequest(t, app, path+invalid, freshnessToken), http.StatusBadRequest)
	}
	spec := freshnessRequest(t, app, "/openapi.json", "")
	requireFreshnessStatus(t, spec, http.StatusOK)
	var doc openapi.Document
	if err := json.Unmarshal(spec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	op := doc.Paths["/v1/tenants/{tenant}/projects/{project}/governance/database-protection"].Get
	if op == nil || op.OperationID != "listDatabaseProtection" || len(op.Security) != 2 {
		t.Fatalf("missing database protection HTTP contract: %+v", op)
	}
	server := httptest.NewTLSServer(app)
	t.Cleanup(server.Close)
	var out bytes.Buffer
	err := commands.RunGovernanceClient(t.Context(), []string{"database-protection", "lab", "compute", "--provider", "huawei-ecs", "--region", "cn-north-4",
		"--max-age-seconds", "259200", "--finding", "recovery_attention", "--limit", "1"},
		server.URL, freshnessToken, nil, &out, server.Client().Transport)
	var report inventory.DatabaseProtectionReport
	if err != nil || json.Unmarshal(out.Bytes(), &report) != nil || len(report.Items) != 1 || report.Items[0].ID != "hw-old" ||
		report.Summary.RestoreWindows.Stale != 1 || report.Summary.Backups.Ready != 1 {
		t.Fatalf("invalid CLI database protection report: %v %s", err, out.String())
	}
}
