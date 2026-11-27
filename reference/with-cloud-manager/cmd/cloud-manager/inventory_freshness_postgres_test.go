package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/openapi"

	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

const freshnessPath = "/v1/tenants/lab/projects/compute/governance/inventory-freshness"
const freshnessToken = "fixture-inventory-freshness-viewer-0001"

func TestAcceptancePriorityCloudInventoryFreshness(t *testing.T) {
	db, handler := prepareFreshnessReport(t)
	store := inventory.NewStore(db)
	tencent := inventory.Key{Tenant: "lab", Account: "tc", Region: "ap-test", Kind: provider.Instance}
	alibaba := inventory.Key{Tenant: "lab", Account: "ali", Region: "cn-test", Kind: provider.Instance}
	finishFreshnessScan(t, store, startFreshnessScan(t, store, tencent))
	finishFreshnessScan(t, store, startFreshnessScan(t, store, alibaba))
	execFreshness(t, db, `UPDATE cmp_inventory_scopes SET completed_at=now()-interval '20 minutes'
		WHERE (tenant_id,account_id)=('lab','ali')`)

	first := getFreshnessReport(t, handler, freshnessPath+"?limit=1")
	want := inventory.FreshnessSummary{Total: 3, Fresh: 1, Stale: 1, Pending: 1}
	if first.Window != 900 || first.AsOf.IsZero() || first.Summary != want ||
		len(first.Items) != 1 || first.Items[0].Account != "tc" || first.Next == "" {
		t.Fatalf("invalid freshness snapshot: %+v", first)
	}
	second := getFreshnessReport(t, handler, freshnessPath+"?limit=1&after="+first.Next)
	if second.Summary != first.Summary || len(second.Items) != 1 || second.Items[0].Account != "ali" || second.Next == first.Next {
		t.Fatalf("invalid freshness continuation: %+v", second)
	}
	last := getFreshnessReport(t, handler, freshnessPath+"?limit=1&after="+second.Next)
	if len(last.Items) != 1 || last.Items[0].Account != "hw" || last.Items[0].CompletedAt != nil || last.Next != "" {
		t.Fatalf("legacy completion or pagination was miscounted: %+v", last)
	}
	report := getFreshnessReport(t, handler, freshnessPath+"?provider=alibaba-ecs&account=ali&state=pending&window_seconds=1800")
	if len(report.Items) != 0 || report.Summary != (inventory.FreshnessSummary{Total: 1, Fresh: 1}) {
		t.Fatalf("detail filter or window changed the wrong summary: %+v", report)
	}
	huawei := inventory.Key{Tenant: "lab", Account: "hw", Region: "cn-test", Kind: provider.Instance}
	finishFreshnessScan(t, store, startFreshnessScan(t, store, huawei))
	assertFreshnessItem(t, handler, freshnessPath+"?provider=huawei-ecs", "fresh", false)
	checkFreshnessRotation(t, db, handler, store, tencent)
	checkFreshnessAccess(t, db, handler)
	checkFreshnessCLIAndSpec(t, handler)
}

func prepareFreshnessReport(t *testing.T) (*sql.DB, http.Handler) {
	t.Helper()
	db := dbtest.Open(t)
	ctx := t.Context()
	if err := migrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	execFreshness(t, db, `INSERT INTO cmp_tenants VALUES ('lab'),('foreign');
		INSERT INTO cmp_projects VALUES ('lab','compute'),('lab','shared'),('lab','denied'),('foreign','compute');
		INSERT INTO cmp_principals (tenant_id,principal_id,kind,issuer,subject)
			VALUES ('lab','viewer','service','fixture','freshness'),('lab','browser','human','https://issuer.example.test','freshness');
		INSERT INTO cmp_memberships (tenant_id,project_id,principal_id,role)
			VALUES ('lab','compute','viewer','viewer'),('lab','shared','viewer','viewer'),('lab','compute','browser','viewer');
		INSERT INTO cmp_cloud_accounts (tenant_id,account_id,project_id,provider,remote_identity,credential_ref,mode)
			VALUES ('lab','tc','compute','tencent-cvm','fixture-tc','fixture/private-tc','read_only'),
			       ('lab','ali','compute','alibaba-ecs','fixture-ali','fixture/private-ali','write_enabled'),
			       ('lab','hw','compute','huawei-ecs','fixture-hw','fixture/private-hw','read_only'),
			       ('lab','paused','compute','tencent-cvm','fixture-paused','fixture/private-paused','suspended'),
			       ('foreign','tc','compute','tencent-cvm','foreign-tc','fixture/private-foreign','read_only');
		INSERT INTO cmp_cloud_account_grants (tenant_id,account_id,project_id) VALUES ('lab','tc','shared');
		INSERT INTO cmp_inventory_schedules (tenant_id,account_id,region,kind,interval_seconds,enabled,target_id) VALUES
			('lab','tc','ap-test','instance',60,true,''), ('lab','ali','cn-test','instance',60,true,''),
			('lab','hw','cn-test','instance',60,true,''), ('lab','tc','ap-other','instance',60,false,''),
			('lab','paused','ap-test','volume',60,true,''), ('foreign','tc','ap-test','instance',60,true,''),
			('lab','hw','cn-test','kubernetes-workloads',60,true,'cluster-1');
		INSERT INTO cmp_inventory_scopes (tenant_id,account_id,region,kind,credential_revision,lease_until,running,completed_at)
			VALUES ('lab','hw','cn-test','instance',1,now(),false,now()),
				('lab','tc','ap-unscheduled','volume',1,now(),false,now());`)
	hash := sha256.Sum256([]byte(freshnessToken))
	execFreshness(t, db, `INSERT INTO cmp_service_tokens (token_hash,tenant_id,principal_id,expires_at)
		VALUES ($1,'lab','viewer',now()+interval '10 minutes')`, hash[:])
	pki := makeConsolePKI(t, "fixture-gateway.example")
	app, err := newAPI(db, "", &consoleConfig{origin: "https://freshness.example.test", certFile: pki.certFile, keyFile: pki.keyFile})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Prepare(); err != nil {
		t.Fatal(err)
	}
	sessions := identity.NewBrowserSessions(db)
	cookie, err := sessions.Login(ctx, "lab", "https://issuer.example.test", "freshness", "")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequestWithContext(ctx, http.MethodGet, freshnessPath, nil)
	request.AddCookie(&http.Cookie{Name: consoleCookie, Value: cookie})
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	requireFreshnessStatus(t, response, http.StatusOK)
	return db, app
}

func checkFreshnessRotation(t *testing.T, db *sql.DB, handler http.Handler, store inventory.Store, key inventory.Key) {
	t.Helper()
	path := freshnessPath + "?account=tc"
	previous := startFreshnessScan(t, store, key)
	execFreshness(t, db, `UPDATE cmp_cloud_accounts SET credential_revision=2 WHERE (tenant_id,account_id)=('lab','tc')`)
	if _, _, err := store.ScanPage(t.Context(), previous, freshnessDiscovery{}); !errors.Is(err, inventory.ErrStale) {
		t.Fatalf("old scan published after credential rotation: %v", err)
	}
	assertFreshnessItem(t, handler, path, "pending", false)
	current := startFreshnessScan(t, store, key)
	partial, done, err := store.ScanPage(t.Context(), current, freshnessDiscovery{next: "next-page"})
	if err != nil || done {
		t.Fatalf("could not prepare partial scan: done=%t error=%v", done, err)
	}
	assertFreshnessItem(t, handler, path, "pending", true)
	execFreshness(t, db, `ALTER TABLE cmp_inventory_scopes ADD CONSTRAINT fixture_reject_completion
		CHECK (completed_credential_revision IS DISTINCT FROM 2)`)
	if _, _, err := store.ScanPage(t.Context(), partial, freshnessDiscovery{}); err == nil {
		t.Fatal("completion write unexpectedly succeeded")
	}
	assertFreshnessItem(t, handler, path, "pending", true)
	execFreshness(t, db, `ALTER TABLE cmp_inventory_scopes DROP CONSTRAINT fixture_reject_completion`)
	finishFreshnessScan(t, store, partial)
	assertFreshnessItem(t, handler, path, "fresh", false)
	startFreshnessScan(t, store, key)
	assertFreshnessItem(t, handler, path, "fresh", true)
}

func checkFreshnessAccess(t *testing.T, db *sql.DB, handler http.Handler) {
	t.Helper()
	sharedPath := strings.Replace(freshnessPath, "/compute/", "/shared/", 1)
	assertFreshnessItem(t, handler, sharedPath, "fresh", true)
	execFreshness(t, db, `DELETE FROM cmp_cloud_account_grants WHERE (tenant_id,account_id,project_id)=('lab','tc','shared')`)
	empty := getFreshnessReport(t, handler, sharedPath)
	if empty.Summary.Total != 0 || empty.Items == nil || len(empty.Items) != 0 {
		t.Fatalf("revoked shared account still appears: %+v", empty)
	}
	for _, denied := range []string{strings.Replace(freshnessPath, "/lab/", "/foreign/", 1), strings.Replace(freshnessPath, "/compute/", "/denied/", 1)} {
		requireFreshnessStatus(t, freshnessRequest(t, handler, denied, freshnessToken), http.StatusForbidden)
	}
	requireFreshnessStatus(t, freshnessRequest(t, handler, freshnessPath, ""), http.StatusUnauthorized)
	for _, query := range []string{"?limit=101", "?provider=bad+input", "?account=a%2Fb", "?after=0", "?after=01", "?state=unknown", "?state=fresh&state=stale", "?window_seconds=59", "?unrecognized=1"} {
		requireFreshnessStatus(t, freshnessRequest(t, handler, freshnessPath+query, freshnessToken), http.StatusBadRequest)
	}
}

func checkFreshnessCLIAndSpec(t *testing.T, handler http.Handler) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	var out bytes.Buffer
	if err := runGovernanceClient(t.Context(), []string{"inventory-freshness", "lab", "compute",
		"--provider", "tencent-cvm", "--window-seconds", "60", "--state", "fresh", "--limit", "1"},
		server.URL, freshnessToken, nil, &out, server.Client().Transport); err != nil {
		t.Fatal(err)
	}
	var receipt inventory.FreshnessReport
	if err := json.Unmarshal(out.Bytes(), &receipt); err != nil || receipt.Window != 60 ||
		receipt.Summary.Total != 1 || len(receipt.Items) != 1 || receipt.Items[0].Account != "tc" {
		t.Fatalf("unexpected CLI report: %+v, %v", receipt, err)
	}
	spec := freshnessRequest(t, handler, "/openapi.json", "")
	var document openapi.Document
	if err := json.Unmarshal(spec.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	op := document.Paths["/v1/tenants/{tenant}/projects/{project}/governance/inventory-freshness"].Get
	if op == nil || op.OperationID != "listInventoryFreshness" || len(op.Security) != 2 {
		t.Fatalf("unexpected freshness OpenAPI operation: %+v", op)
	}
}

type freshnessDiscovery struct{ next string }

func (f freshnessDiscovery) Discover(context.Context, provider.Scope, string) (provider.Page, error) {
	return provider.Page{Next: f.next}, nil
}

func startFreshnessScan(t *testing.T, store inventory.Store, key inventory.Key) inventory.Claim {
	t.Helper()
	claim, err := store.Claim(t.Context(), key, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return claim
}

func finishFreshnessScan(t *testing.T, store inventory.Store, claim inventory.Claim) {
	t.Helper()
	if _, done, err := store.ScanPage(t.Context(), claim, freshnessDiscovery{}); err != nil || !done {
		t.Fatalf("scan did not finish: done=%t error=%v", done, err)
	}
}

func assertFreshnessItem(t *testing.T, handler http.Handler, path, state string, running bool) {
	t.Helper()
	report := getFreshnessReport(t, handler, path)
	if len(report.Items) != 1 || report.Items[0].State != state || report.Items[0].Running != running ||
		(report.Items[0].CompletedAt == nil) != (state == "pending") {
		t.Fatalf("unexpected freshness state: %+v", report)
	}
}

func getFreshnessReport(t *testing.T, handler http.Handler, path string) inventory.FreshnessReport {
	t.Helper()
	response := freshnessRequest(t, handler, path, freshnessToken)
	requireFreshnessStatus(t, response, http.StatusOK)
	var result struct {
		Data inventory.FreshnessReport `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(response.Body.String(), "fixture/private") || strings.Contains(response.Body.String(), "fixture-tc") {
		t.Fatal("freshness response exposed provider identity or credential metadata")
	}
	return result.Data
}

func requireFreshnessStatus(t *testing.T, response *httptest.ResponseRecorder, want int) {
	t.Helper()
	if response.Code != want {
		t.Fatalf("unexpected freshness status %d (want %d): %s", response.Code, want, response.Body)
	}
}

func freshnessRequest(t *testing.T, handler http.Handler, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

func execFreshness(t *testing.T, db *sql.DB, statement string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), statement, args...); err != nil {
		t.Fatal(err)
	}
}
