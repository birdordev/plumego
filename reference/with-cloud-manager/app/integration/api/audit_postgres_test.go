package apitest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"with-cloud-manager/app/commands"
	"with-cloud-manager/app/routers"
	"with-cloud-manager/internal/audit"
	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/operations"
)

func TestAcceptanceProjectAudit(t *testing.T) {
	db, ctx := dbtest.Open(t), t.Context()
	for _, migrate := range []func(context.Context, *sql.DB) error{
		identity.Migrate, identity.MigrateServiceTokens, identity.MigrateAccounts, identity.MigrateBrowserSessions,
		inventory.Migrate, operations.Migrate,
	} {
		if err := migrate(ctx, db); err != nil {
			t.Fatal(err)
		}
	}
	exec := func(statement string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, statement, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO cmp_tenants VALUES ('tenant'),('external');
	    INSERT INTO cmp_projects VALUES ('tenant','project'),('tenant','destination'),('tenant','unrelated'),('external','project');
	    INSERT INTO cmp_principals (tenant_id,principal_id,kind,issuer,subject) VALUES
	    ('tenant','admin','service','fixture','admin'),('tenant','viewer','service','fixture','viewer'),
	    ('tenant','=1+1','service','fixture','applicant'),('tenant','reviewer','service','fixture','reviewer'),
	    ('external','external-admin','service','fixture','external');
	    INSERT INTO cmp_memberships (tenant_id,project_id,principal_id,role) VALUES
	    ('tenant','project','admin','admin'),('tenant','destination','admin','admin'),('tenant','unrelated','admin','admin'),
	    ('tenant','project','viewer','viewer'),('tenant','project','reviewer','approver'),('external','project','external-admin','admin');
	    INSERT INTO cmp_cloud_accounts (tenant_id,account_id,project_id,provider,credential_ref,remote_identity,mode,credential_revision) VALUES
	    ('tenant','cloud','project','aws-ec2','private/credential/current','aws:main','write_enabled',2),
	    ('external','cloud','project','openstack','private/credential/external','site:external','read_only',1);
	    INSERT INTO cmp_cloud_account_events(tenant_id,account_id,actor_id,action,project_id,target_project,revision,created_at) VALUES
	    ('tenant','cloud','=1+1','register','project',NULL,1,'2026-01-01T00:00:00Z'),
	    ('tenant','cloud','admin','share','project','destination',2,'2026-01-02T00:00:00Z'),
	    ('external','cloud','external-admin','register','project',NULL,1,'2026-01-02T00:00:00Z');
	    INSERT INTO cmp_inventory_scopes (tenant_id,account_id,region,kind,credential_revision,lease_until)
	    VALUES ('tenant','cloud','us-east-1','instance',2,now());
	    INSERT INTO cmp_inventory_objects (scope_id,external_id,name,status,seen_generation)
	    SELECT scope_id,'asset-one','display name','available',1 FROM cmp_inventory_scopes;
	    INSERT INTO cmp_asset_ownership_events(scope_id,external_id,tenant_id,actor_id,from_project,to_project,revision,created_at)
	    SELECT scope_id,'asset-one','tenant','admin','project','destination',2,'2026-01-02T00:00:00Z' FROM cmp_inventory_scopes;
	    INSERT INTO cmp_instance_operations (operation_id,tenant_id,project_id,principal_id,account_id,region,action,status,
	    idempotency_key,request_hash,credential_revision,membership_revision,ownership_revision)
	    VALUES ('old-op','tenant','project','=1+1','cloud','us-east-1','create','needs_review','audit-fixture',
	    decode(repeat('00',32),'hex'),2,1,0);
	    INSERT INTO cmp_instance_operation_events(operation_id,status,recorded_at) VALUES
	    ('old-op','awaiting_approval','2026-01-03T00:00:00Z'),('old-op','queued','2026-01-04T00:00:00Z');
	    INSERT INTO cmp_instance_operation_approvals(operation_id,tenant_id,reviewer_id,decision,decided_at)
	    VALUES ('old-op','tenant','reviewer','approve','2026-01-04T00:00:00Z');
	    INSERT INTO cmp_instance_operation_resolutions(operation_id,tenant_id,reviewer_id,decision,evidence_ref,request_id,reviewed_at)
	    VALUES ('old-op','tenant','reviewer','failed','private/evidence/ref','provider-request-private','2026-01-05T00:00:00Z');
	    INSERT INTO cmp_instance_observation_retries(operation_id,tenant_id,reviewer_id,evidence_ref,created_at)
	    VALUES ('old-op','tenant','reviewer','private/retry/ref','2026-01-05T00:00:00Z');`)
	const admin = "fixture-project-audit-admin-service-token"
	const viewer = "fixture-project-audit-viewer-service-token"
	const external = "fixture-project-audit-external-service-token"
	const reviewer = "fixture-project-audit-reviewer-service-token"
	for _, pair := range []struct{ tenant, principal, secret string }{
		{"tenant", "admin", admin}, {"tenant", "viewer", viewer}, {"external", "external-admin", external}, {"tenant", "reviewer", reviewer},
	} {
		digest := sha256.Sum256([]byte(pair.secret))
		exec("INSERT INTO cmp_service_tokens(token_hash,tenant_id,principal_id,expires_at) VALUES ($1,$2,$3,now()+interval '5 minutes')",
			digest[:], pair.tenant, pair.principal)
	}
	if err := audit.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := audit.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	app, err := routers.NewAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	const scope = "/v1/tenants/tenant/projects/project"
	const path = scope + "/audit"
	call := func(method, uri, secret, input string, want int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, uri, strings.NewReader(input))
		if secret != "" {
			req.Header.Set("Authorization", "Bearer "+secret)
		}
		if input != "" {
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("If-Match", `"2"`)
			if uri == scope+"/instance-operations" {
				req.Header.Set("Idempotency-Key", "new-audit-operation")
			}
		}
		w := httptest.NewRecorder()
		app.ServeHTTP(w, req)
		if w.Code != want || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s %s status=%d want=%d: %s", method, uri, w.Code, want, w.Body)
		}
		return w
	}
	page := func(uri, token string) audit.Page {
		t.Helper()
		w := call(http.MethodGet, uri, token, "", http.StatusOK)
		for _, secret := range []string{"private/credential/", "private/evidence/", "private/retry/", "provider-request-private", "request_hash"} {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatal("audit output contains suppressed material")
			}
		}
		var data struct{ Data audit.Page }
		if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil || data.Data.Items == nil {
			t.Fatalf("invalid audit page: %v", err)
		}
		return data.Data
	}

	call(http.MethodGet, path, "", "", http.StatusUnauthorized)
	call(http.MethodGet, path, viewer, "", http.StatusForbidden)
	call(http.MethodGet, path+"/export", viewer, "", http.StatusForbidden)
	call(http.MethodGet, path, external, "", http.StatusForbidden)
	extPath := strings.Replace(path, "/tenant/", "/external/", 1)
	if result := page(extPath, external); len(result.Items) != 1 || result.Items[0].Actor != "external-admin" {
		t.Fatalf("tenant audit isolation failed: %+v", result)
	}
	destination := strings.Replace(path, "/project/", "/destination/", 1)
	if result := page(destination, admin); len(result.Items) != 2 || result.Items[0].RelatedProject != "project" || result.Items[1].RelatedProject != "project" {
		t.Fatalf("counterparty history lost: %+v", result)
	}
	if result := page(strings.Replace(path, "/project/", "/unrelated/", 1), admin); len(result.Items) != 0 {
		t.Fatalf("unrelated project audit: %+v", result)
	}
	byTime := page(path+"?from=2026-01-04T00:00:00Z&until=2026-01-05T00:00:00Z", admin)
	if len(byTime.Items) != 2 {
		t.Fatalf("date range did not include its start and exclude the end: %+v", byTime)
	}
	seen := map[string]audit.Event{}
	next := ""
	for range 8 {
		result := page(path+"?limit=1"+next, admin)
		if len(result.Items) != 1 || result.Items[0].ID == "" || seen[result.Items[0].ID].ID != "" {
			t.Fatalf("invalid or repeated cursor result: %+v", result)
		}
		seen[result.Items[0].ID] = result.Items[0]
		if next == "" {
			for _, query := range []string{path + "?category=operation", destination} {
				separator := "?"
				if strings.Contains(query, "?") {
					separator = "&"
				}
				call(http.MethodGet, query+separator+"cursor="+url.QueryEscape(result.Next), admin, "", http.StatusBadRequest)
			}
		}
		next = "&cursor=" + url.QueryEscape(result.Next)
	}
	if next != "&cursor=" {
		t.Fatal("audit history diverges from its source rows")
	}
	rows := page(path+"?subject_id=old-op", admin).Items
	actors := map[string]string{}
	for _, row := range rows {
		actors[row.Outcome] = row.Actor
	}
	if len(rows) != 5 || actors["awaiting_approval"] != "=1+1" || actors["queued"] != "" ||
		actors["approve"] != "reviewer" || actors["failed"] != "reviewer" || actors["restarted"] != "reviewer" {
		t.Fatalf("incorrect actor or system attribution: %+v", rows)
	}
	for _, query := range []string{"category=invalid", "limit=201", "from=invalid", "from=2026-01-05T00:00:00Z&until=2026-01-04T00:00:00Z", "category=operation&category=approval"} {
		call(http.MethodGet, path+"?"+query, admin, "", http.StatusBadRequest)
	}
	newOp := call(http.MethodPost, scope+"/instance-operations", admin,
		`{"account":"cloud","region":"us-east-1","asset_id":"asset-one","action":"delete"}`, http.StatusAccepted)
	var newReceipt struct{ Data operations.Operation }
	if err := json.Unmarshal(newOp.Body.Bytes(), &newReceipt); err != nil || newReceipt.Data.ID == "" {
		t.Fatalf("new operation was not receipted: %v", err)
	}
	call(http.MethodPost, scope+"/operations/"+newReceipt.Data.ID+"/approval", reviewer, `{"decision":"approve"}`, http.StatusOK)
	live := page(path+"?subject_id="+url.QueryEscape(newReceipt.Data.ID), admin)
	if len(live.Items) != 3 {
		t.Fatalf("expected submission, reviewer decision and queued state: %+v", live)
	}
	actors = map[string]string{}
	for _, row := range live.Items {
		actors[row.Outcome] = row.Actor
	}
	if actors["awaiting_approval"] != "admin" || actors["approve"] != "reviewer" || actors["queued"] != "" {
		t.Fatalf("live operation attribution: %+v", live)
	}

	rotate := `{"action":"rotate","credential_ref":"private/credential/replacement"}`
	exec("ALTER TABLE cmp_project_audit ADD CONSTRAINT fail_project_audit CHECK (action<>'rotate') NOT VALID")
	call(http.MethodPatch, scope+"/cloud-accounts/cloud", admin, rotate, http.StatusServiceUnavailable)
	var revision int
	if err := db.QueryRowContext(ctx, "SELECT credential_revision FROM cmp_cloud_accounts WHERE tenant_id='tenant' AND account_id='cloud'").Scan(&revision); err != nil || revision != 2 {
		t.Fatalf("failed audit did not roll back source transaction: %d %v", revision, err)
	}
	exec("ALTER TABLE cmp_project_audit DROP CONSTRAINT fail_project_audit")
	call(http.MethodPatch, scope+"/cloud-accounts/cloud", admin, rotate, http.StatusOK)
	if latest := page(path+"?category=account&limit=1", admin); len(latest.Items) != 1 || latest.Items[0].Action != "rotate" || latest.Items[0].Revision != 3 {
		t.Fatalf("live event missing: %+v", latest)
	}

	export := call(http.MethodGet, path+"/export?category=account", admin, "", http.StatusOK)
	if !strings.HasPrefix(export.Header().Get("Content-Type"), "text/csv") || export.Header().Get("Content-Disposition") == "" {
		t.Fatal("audit export is not a download")
	}
	cells, err := csv.NewReader(export.Body).ReadAll()
	if err != nil || len(cells) != 4 || cells[3][8] != "'=1+1" {
		t.Fatalf("CSV formula prefix not escaped: %+v %v", cells, err)
	}
	call(http.MethodGet, path+"/export?limit=1", admin, "", http.StatusBadRequest)
	server := httptest.NewServer(app)
	defer server.Close()
	var cli bytes.Buffer
	if err := commands.RunAuditClient(ctx, []string{"list", "tenant", "project", "--category", "approval", "--limit", "5"}, server.URL, admin, &cli, nil); err != nil ||
		!json.Valid(cli.Bytes()) || !strings.Contains(cli.String(), `"outcome":"approve"`) {
		t.Fatalf("invalid audit CLI list: %v", err)
	}
	cli.Reset()
	if err := commands.RunAuditClient(ctx, []string{"export", "tenant", "project", "--subject-id", "old-op"}, server.URL, admin, &cli, nil); err != nil {
		t.Fatal(err)
	}
	if records, err := csv.NewReader(&cli).ReadAll(); err != nil || len(records) != 6 {
		t.Fatalf("invalid audit CLI export: %v", err)
	}
	exec(`INSERT INTO cmp_project_audit(tenant_id,project_id,category,action,subject_id,resource_kind,account_id,occurred_at)
	    SELECT 'tenant','project','operation','start','generated-'||n,'instance','cloud',now()
	    FROM generate_series(1,2001) AS n;`)
	if refused := call(http.MethodGet, path+"/export", admin, "", http.StatusUnprocessableEntity); strings.HasPrefix(refused.Header().Get("Content-Type"), "text/csv") {
		t.Fatal("oversized export emitted a partial CSV")
	}
	exec("UPDATE cmp_memberships SET role='viewer' WHERE (tenant_id,project_id,principal_id)=('tenant','project','admin')")
	call(http.MethodGet, path, admin, "", http.StatusForbidden)
}
