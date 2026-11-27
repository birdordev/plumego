package main

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/birdordev/plumego/x/openapi"

	"with-cloud-manager/internal/billing"
	"with-cloud-manager/internal/dbtest"
)

const summaryAdmin = "fixture-project-billing-admin-token"
const summaryViewer = "fixture-project-billing-viewer-token"

func TestAcceptanceProjectBillingSummary(t *testing.T) {
	db, app := setupBillingSummary(t)
	latest := seedBillingSummary(t, db)
	path := "/v1/tenants/tenant/projects/finance/billing/summary"
	query := "?from=2026-11&through=2026-12&source=native&currency=CNY"
	get := func(path, token string, status int) *httptest.ResponseRecorder {
		t.Helper()
		return scheduleResponse(t, app, "GET", path, token, "", "", status)
	}
	read := func(path string) billing.ProjectSummary {
		t.Helper()
		response := get(path, summaryAdmin, 200)
		if response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("project billing summary is cacheable")
		}
		for _, secret := range []string{"billing-private-label", "fixture-billing-profile", "outside-tc-id"} {
			if strings.Contains(response.Body.String(), secret) {
				t.Fatalf("response exposed raw account or billing fields: %s", response.Body)
			}
		}
		var report billing.ProjectSummary
		decodeProjectData(t, response, &report)
		return report
	}
	result := read(path + query + "&limit=1")
	if len(result.Months) != 2 {
		t.Fatalf("unexpected monthly coverage: %+v", result)
	}
	nov, dec := result.Months[0], result.Months[1]
	if nov.EligibleAccounts != 4 || nov.Published != 3 || nov.WithCurrency != 2 ||
		nov.Total == nil || nov.Total.Billed != "0.200000000001" || nov.Total.Effective != nil || nov.Total.ExpectedBilled != nil ||
		dec.EligibleAccounts != 4 || dec.Published != 1 || dec.WithCurrency != 0 || dec.Total != nil || result.AsOf.IsZero() {
		t.Fatalf("invalid source, currency, version or coverage projection: %+v / %+v", nov, dec)
	}
	var rows []billing.ProjectReportAccount
	for page := result; ; page = read(path + query + "&limit=1&after=" + url.QueryEscape(page.Next)) {
		if !reflect.DeepEqual(page.Months, result.Months) || len(page.Accounts) != 1 {
			t.Fatalf("paging changed the monthly totals or page width: %+v", page)
		}
		rows = append(rows, page.Accounts[0])
		if page.Next == "" {
			break
		}
		if len(rows) >= 8 {
			t.Fatal("account cursor repeated")
		}
	}
	if len(rows) != 8 || rows[1].Account != "blank" || rows[1].Report != nil || rows[2].Account != "hw" ||
		rows[2].Report == nil || rows[2].Billed != nil || rows[3].Account != "tc" || rows[3].Report == nil ||
		rows[3].Report.Revision != "9007199254740993" || rows[3].Report.Digest != latest ||
		rows[5].Account != "blank" || rows[5].Report == nil || rows[5].Report.Rows != 0 {
		t.Fatalf("wrong source references or account status: %+v", rows)
	}
	missing := read(path + query + "&missing_only=true")
	if len(missing.Accounts) != 4 || !reflect.DeepEqual(missing.Months, result.Months) {
		t.Fatalf("missing-only filter changed totals: %+v", missing)
	}
	for _, row := range missing.Accounts {
		if row.Report != nil {
			t.Fatal("published account appeared as missing")
		}
	}
	tc := read(path + query + "&provider=tencent-cvm")
	firstMonth := tc.Months[0]
	if firstMonth.EligibleAccounts != 2 || firstMonth.Total == nil || firstMonth.Total.Billed != "0.300000000002" ||
		firstMonth.Total.Effective == nil || *firstMonth.Total.Effective != "0.010000000002" ||
		firstMonth.Total.Variance == nil || *firstMonth.Total.Variance != "0.000000000002" {
		t.Fatalf("provider filter or decimal arithmetic failed: %+v", firstMonth)
	}
	usd := read(path + strings.Replace(query, "CNY", "USD", 1))
	if usd.Months[1].Total == nil || usd.Months[1].Total.Billed != "0" ||
		usd.Months[0].Total == nil || usd.Months[0].Total.Billed != "2.000000000000" {
		t.Fatalf("original currency or explicit zero lost: %+v", usd.Months)
	}
	focus := read(path + strings.Replace(query, "native", "focus-1.0", 1))
	if focus.Months[0].Published != 1 || focus.Months[0].Total == nil || focus.Months[0].Total.Billed != "5.000000000000" {
		t.Fatalf("source switch mixed the native and FOCUS statements: %+v", focus)
	}
	empty := read(strings.Replace(path, "/finance/", "/shared/", 1) + query)
	if empty.Months[0].EligibleAccounts != 0 || empty.Accounts == nil || len(empty.Accounts) != 0 {
		t.Fatalf("the account grant disclosed billing: %+v", empty)
	}
	get(path+query, "", 401)
	get(path+query, summaryViewer, 403)
	get(strings.Replace(path, "/tenant/", "/outside/", 1)+query, summaryAdmin, 403)
	for _, raw := range []string{query + "&limit=0", query + "&currency=USD", query + "&missing_only=1", query + "&after=2027-01/tc",
		query + "&provider=aws-ec2", strings.Replace(query, "2026-12", "2027-11", 1), strings.Replace(query, "CNY", "cny", 1),
		strings.Replace(query, "2026-12", "2026-10", 1), query + "&wrong=1"} {
		get(path+raw, summaryAdmin, 400)
	}
	version := get(strings.Replace(path, "/summary", "/reports", 1)+"?account=tc&period=2026-11&source=native&revision=9007199254740993", summaryAdmin, 200)
	if version.Header().Get("ETag") != `"9007199254740993"` || !strings.Contains(version.Body.String(), `"revision_text":"9007199254740993"`) ||
		!strings.Contains(version.Body.String(), latest) {
		t.Fatalf("selected source revision changed or lost precision: %s", version.Body)
	}
	openAPI := get("/openapi.json", "", 200)
	var document openapi.Document
	if err := json.Unmarshal(openAPI.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	op := document.Paths["/v1/tenants/{tenant}/projects/{project}/billing/summary"].Get
	if op == nil || op.OperationID != "getProjectBillingSummary" || len(op.Security) == 0 ||
		op.Responses["200"].Content["application/json"].Schema.Properties["data"].Properties["months"].Type != "array" {
		t.Fatalf("strict OpenAPI does not describe project billing summary: %+v", op)
	}
	server := httptest.NewTLSServer(app)
	defer server.Close()
	var out bytes.Buffer
	err := runBillingClient(t.Context(), []string{"summary", "tenant", "finance", "2026-11", "2026-12", "native", "CNY",
		"--provider", "tencent-cvm", "--missing-only", "--limit", "1", "--after", "2026-11/blank"},
		server.URL, summaryAdmin, nil, &out, server.Client().Transport)
	if err != nil || !strings.Contains(out.String(), `"provider":"tencent-cvm"`) || !strings.Contains(out.String(), `"account":"tc"`) {
		t.Fatalf("CLI did not follow the summary query: %v %s", err, &out)
	}
}

func setupBillingSummary(t *testing.T) (*sql.DB, http.Handler) {
	t.Helper()
	db := dbtest.Open(t)
	if err := migrateCloudManager(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	execBillingSummary(t, db, `
		INSERT INTO cmp_tenants VALUES ('tenant'),('outside');
		INSERT INTO cmp_projects VALUES ('tenant','finance'),('tenant','shared'),('outside','finance');
		INSERT INTO cmp_principals(tenant_id,principal_id,kind,issuer,subject) VALUES
			('tenant','admin','service','test','admin'),('tenant','viewer','service','test','viewer');
		INSERT INTO cmp_memberships(tenant_id,project_id,principal_id,role) VALUES
			('tenant','finance','admin','admin'),('tenant','shared','admin','admin'),('tenant','finance','viewer','viewer');
		INSERT INTO cmp_cloud_accounts(tenant_id,account_id,project_id,provider,remote_identity,credential_ref,mode) VALUES
			('tenant','tc','finance','tencent-cvm','10001','fixture-billing-profile','read_only'),
			('tenant','ali','finance','alibaba-ecs','10002','fixture-billing-profile','read_only'),
			('tenant','hw','finance','huawei-ecs','10003:project-3','fixture-billing-profile','write_enabled'),
			('tenant','blank','finance','tencent-cvm','10004','fixture-billing-profile','read_only'),
			('tenant','suspended','finance','tencent-cvm','10005','fixture-billing-profile','read_only'),
			('tenant','not-priority','finance','aws-ec2','10006','fixture-billing-profile','read_only'),
			('outside','tc','finance','tencent-cvm','outside-tc-id','fixture-billing-profile','read_only');
		INSERT INTO cmp_cloud_account_grants(tenant_id,account_id,project_id) VALUES ('tenant','tc','shared');`)
	for _, item := range []struct{ name, token string }{{"admin", summaryAdmin}, {"viewer", summaryViewer}} {
		hash := sha256.Sum256([]byte(item.token))
		execBillingSummary(t, db, `INSERT INTO cmp_service_tokens(token_hash,tenant_id,principal_id,expires_at)
			VALUES ($1,'tenant',$2,statement_timestamp()+interval '1 hour')`, hash[:], item.name)
	}
	app, err := newAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	return db, app
}

func seedBillingSummary(t *testing.T, db *sql.DB) string {
	t.Helper()
	store := billing.NewStore(db)
	native := func(tenant, account, month, vendor, rows string, expected int64, controls map[string]string) billing.Report {
		t.Helper()
		key := billing.Key{Tenant: tenant, Project: "finance", Account: account, Period: month, Source: billing.Native}
		columns := &billing.Columns{Account: "Account", Period: "Month", Currency: "Currency",
			Billed: "Billed", Effective: "Effective", Resource: "Resource"}
		headers := "Account,Month,Currency,Billed,Effective,Resource"
		if vendor == "huawei-ecs" {
			columns.Project = "Project"
			headers += ",Project"
		}
		report, err := store.Import(t.Context(), key, billing.Manifest{Provider: vendor, Columns: columns, ExpectedBilled: controls},
			expected, [][]byte{[]byte(headers + "\n" + rows)})
		if err != nil {
			t.Fatalf("import %s %s: %v", account, month, err)
		}
		return report
	}
	native("tenant", "tc", "2026-11", "tencent-cvm", "10001,2026-11,CNY,10,8,billing-private-label\n", 0, nil)
	latest := native("tenant", "tc", "2026-11", "tencent-cvm", "10001,2026-11,CNY,0.300000000002,0.010000000002,billing-private-label\n",
		1, map[string]string{"CNY": "0.3"})
	native("tenant", "ali", "2026-11", "alibaba-ecs", "10002,2026-11,CNY,-0.100000000001,,\n", 0, nil)
	native("tenant", "hw", "2026-11", "huawei-ecs", "10003,2026-11,USD,2,1,,project-3\n", 0, nil)
	native("tenant", "blank", "2026-12", "tencent-cvm", "", 0, map[string]string{"USD": "0"})
	native("tenant", "suspended", "2026-11", "tencent-cvm", "10005,2026-11,CNY,100,99,\n", 0, nil)
	native("outside", "tc", "2026-11", "tencent-cvm", "outside-tc-id,2026-11,CNY,1000,100,\n", 0, nil)
	execBillingSummary(t, db, `UPDATE cmp_cloud_accounts SET mode='suspended' WHERE account_id='suspended';
		UPDATE cmp_billing_reports SET revision=9007199254740993 WHERE (tenant_id,account_id,revision)=('tenant','tc',2)`)
	focus := billing.Key{Tenant: "tenant", Project: "finance", Account: "ali", Period: "2026-11", Source: billing.Focus}
	input := "BillingAccountId,BillingCurrency,BilledCost,EffectiveCost,ProviderName,BillingPeriodStart,BillingPeriodEnd\n" +
		"10002,CNY,5,4,Alibaba Cloud,2026-11-01T00:00:00Z,2026-12-01T00:00:00Z\n"
	if _, err := store.Import(t.Context(), focus, billing.Manifest{Provider: "alibaba-ecs"}, 0, [][]byte{[]byte(input)}); err != nil {
		t.Fatal(err)
	}
	return latest.Digest
}

func execBillingSummary(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatalf("seed billing summary: %v", err)
	}
}
