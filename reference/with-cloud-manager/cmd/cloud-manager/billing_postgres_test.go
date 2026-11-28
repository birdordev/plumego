package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"with-cloud-manager/internal/billing"
	"with-cloud-manager/internal/dbtest"
)

func TestBillingAPIAndStorageCommands(t *testing.T) {
	db := dbtest.Open(t)
	ctx := t.Context()
	if err := migrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO cmp_tenants VALUES ('tenant'),('outside');
		INSERT INTO cmp_projects VALUES ('tenant','finance'),('tenant','shared'),('outside','finance');
		INSERT INTO cmp_principals(tenant_id,principal_id,kind,issuer,subject) VALUES
		    ('tenant','admin','service','test','admin'),('tenant','viewer','service','test','viewer');
		INSERT INTO cmp_memberships(tenant_id,project_id,principal_id,role) VALUES
		    ('tenant','finance','admin','admin'),('tenant','shared','admin','admin'),('tenant','finance','viewer','viewer');
		INSERT INTO cmp_cloud_accounts(tenant_id,account_id,project_id,provider,remote_identity,credential_ref,mode)
		VALUES ('tenant','cloud','finance','alibaba-ecs','10002','fixture','read_only');
		INSERT INTO cmp_cloud_account_grants(tenant_id,account_id,project_id) VALUES ('tenant','cloud','shared')`); err != nil {
		t.Fatal(err)
	}
	tokens := make(map[string]string)
	for _, name := range []string{"admin", "viewer"} {
		token := strings.Repeat(name, 12)
		hash := sha256.Sum256([]byte(token))
		if _, err := db.ExecContext(ctx, `INSERT INTO cmp_service_tokens(token_hash,tenant_id,principal_id,expires_at)
			VALUES ($1,'tenant',$2,statement_timestamp()+interval '1 hour')`, hash[:], name); err != nil {
			t.Fatal(err)
		}
		tokens[name] = token
	}
	fixture := t.TempDir()
	manifest := filepath.Join(fixture, "billing.json")
	source := filepath.Join(fixture, "export.csv")
	if err := os.WriteFile(manifest, []byte(`{"provider":"alibaba-ecs","offset_minutes":480,"expected_billed":{"CNY":"0.1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	original := []byte("BillingAccountId,BillingCurrency,BilledCost,EffectiveCost,ProviderName,BillingPeriodStart,BillingPeriodEnd,ResourceId\n" +
		"10002,CNY,0.1,0.08,Alibaba Cloud,2026-10-31T16:00:00Z,2026-11-30T16:00:00Z,test-sensitive-resource\n")
	if err := os.WriteFile(source, original, 0o600); err != nil {
		t.Fatal(err)
	}
	common := []string{"tenant", "finance", "cloud", "2026-11", billing.Focus}
	commands := func(command string, rest ...string) []string {
		return append(append([]string{command}, common...), rest...)
	}
	var out bytes.Buffer
	if err := runBillingStorageCommand(ctx, db, commands("billing-import", "0", manifest, source), &out); err != nil {
		t.Fatal(err)
	}
	var imported billing.Report
	if err := json.Unmarshal(out.Bytes(), &imported); err != nil || imported.Revision != 1 || imported.Rows != 1 {
		t.Fatalf("import receipt %s: %v", out.String(), err)
	}

	policy := filepath.Join(fixture, "allocation.json")
	if err := os.WriteFile(policy, []byte(`{"default":[{"center":"product","weight":7500},{"center":"operations","weight":2500}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runBillingStorageCommand(ctx, db, commands("billing-allocate", "1", "0", policy), &out); err != nil {
		t.Fatal(err)
	}
	var allocation billing.Allocation
	if err := json.Unmarshal(out.Bytes(), &allocation); err != nil || allocation.Revision != 1 || allocation.ReportDigest != imported.Digest {
		t.Fatalf("allocation receipt %s: %v", out.String(), err)
	}
	out.Reset()
	if err := runBillingStorageCommand(ctx, db, commands("billing-allocation-replay", "1", "1"), &out); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runBillingStorageCommand(ctx, db, commands("billing-replay", "1"), &out); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runBillingStorageCommand(ctx, db, commands("billing-source", "1", "1"), &out); err != nil || !bytes.Equal(original, out.Bytes()) {
		t.Fatalf("source export bytes=%d: %v", out.Len(), err)
	}

	app, err := newAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Prepare(); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(app)
	defer server.Close()
	out.Reset()
	if err := runBillingClient(ctx, append([]string{"get"}, common...), server.URL, tokens["admin"], nil, &out, nil); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{`"billed_cost":"0.100000000000"`, `"effective_cost":"0.080000000000"`, `"variance":"0.000000000000"`, imported.Digest} {
		if !strings.Contains(out.String(), text) {
			t.Fatalf("report lacks %s: %s", text, out.String())
		}
	}
	if strings.Contains(out.String(), "test-sensitive-resource") || strings.Contains(out.String(), "10002") {
		t.Fatal("raw billing scope or line returned in summary")
	}

	get := func(path, token string, status int) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		response := httptest.NewRecorder()
		app.ServeHTTP(response, request)
		if response.Code != status {
			t.Fatalf("GET %s: %d %s", path, response.Code, response.Body)
		}
		return response
	}
	query := "?account=cloud&period=2026-11&source=focus-1.0"
	path := "/v1/tenants/tenant/projects/finance/billing/reports"
	token := tokens["admin"]
	got := get(path+query+"&revision=1", token, http.StatusOK)
	if got.Header().Get("Cache-Control") != "no-store" || got.Header().Get("ETag") != `"1"` {
		t.Fatalf("report headers: %v", got.Header())
	}
	get(path+query, "", http.StatusUnauthorized)
	get(path+query, tokens["viewer"], http.StatusForbidden)
	get(path+query+"&revision=2", token, http.StatusNotFound)
	get(path+query+"&account=repeated", token, http.StatusBadRequest)
	get(path+query+"&revision=0", token, http.StatusBadRequest)
	get(strings.Replace(path, "finance", "shared", 1)+query, token, http.StatusForbidden)
	get(strings.Replace(path, "tenant/projects", "outside/projects", 1)+query, token, http.StatusForbidden)
	openAPI := get("/openapi.json", "", http.StatusOK)
	if !strings.Contains(openAPI.Body.String(), "getBillingReport") || !strings.Contains(openAPI.Body.String(), "getBillingAllocation") {
		t.Fatal("billing or allocation route missing from strict OpenAPI")
	}
	allocations := strings.Replace(path, "/reports", "/allocations", 1)
	view := get(allocations+query+"&revision=1&allocation_revision=1", token, http.StatusOK)
	if view.Header().Get("Cache-Control") != "no-store" || !strings.Contains(view.Body.String(), `"billed_cost":"0.075000000000"`) ||
		strings.Contains(view.Body.String(), "test-sensitive-resource") {
		t.Fatalf("allocation HTTP response: %s", view.Body)
	}
	get(allocations+query+"&allocation_revision=1", token, http.StatusBadRequest)
	get(allocations+query+"&revision=1&allocation_revision=02", token, http.StatusBadRequest)
	get(allocations+query, tokens["viewer"], http.StatusForbidden)
	get(strings.Replace(allocations, "finance", "shared", 1)+query, token, http.StatusForbidden)
	out.Reset()
	if err := runBillingClient(ctx, append([]string{"allocation-get"}, common...), server.URL, token, nil, &out, nil); err != nil ||
		!strings.Contains(out.String(), allocation.PolicyDigest) {
		t.Fatalf("allocation-get output: %s, %v", out.String(), err)
	}
	changed := bytes.Replace(original, []byte(",0.1,"), []byte(",0.2,"), 1)
	if err := os.WriteFile(source, changed, 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runBillingStorageCommand(ctx, db, commands("billing-import", "1", manifest, source), &out); err != nil {
		t.Fatal(err)
	}
	get(allocations+query, token, http.StatusNotFound)
	get(allocations+query+"&revision=1", token, http.StatusOK)
	exerciseBillingAllocationPublication(t, db, server, common, tokens)
	if _, err := db.ExecContext(ctx, "UPDATE cmp_cloud_accounts SET project_id='shared' WHERE account_id='cloud'"); err != nil {
		t.Fatal(err)
	}
	get(path+query, token, http.StatusForbidden)
	get(strings.Replace(path, "finance", "shared", 1)+query, token, http.StatusNotFound)
}
