package kvmtest

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/birdordev/plumego/x/openapi"

	consolehttp "with-cloud-manager/app/handlers/console"
	"with-cloud-manager/app/integration/testsupport"
	"with-cloud-manager/app/routers"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/kvm"
)

func TestAcceptanceKVMProjectInventory(t *testing.T) {
	db, first, second, _, vm := prepareKVMVMWithPool(t, &fixtureIPv4Pool)
	const siteB = fixtureKVMProject + "/sites/site-b"
	host := fixtureKVMHost()
	host.Agent = "replacement-agent"
	host.UUID = "feaa8a19-4765-4bb4-a383-3a1011ba0002"
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", siteB+"/hosts", "", "", host), 201, nil)
	report := fixtureKVMReport()
	report.UUID = host.UUID
	expectKVM(t, kvmRequest(t, second, fixtureKVMReplacement, "PUT", siteB+"/hosts/"+host.ID+"/capacity", "", "", report), 200, nil)
	var placement kvm.Placement
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "POST", siteB+"/placements", "", "site-b-vm", fixtureKVMPlacement()), 201, &placement)
	var other kvm.VM
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "POST", siteB+"/vms", "", "", kvm.VMInput{
		Placement: placement.ID, Volume: "site-b-disk",
	}), 202, &other)

	hosts, vms := fixtureKVMProject+"/hosts", fixtureKVMProject+"/vms"
	var hostPage kvm.InventoryPage[kvm.HostSummary]
	response := kvmRequest(t, second, fixtureKVMAgent, "GET", hosts+"?limit=1", "", "", nil)
	expectKVM(t, response, 200, &hostPage)
	if response.Header().Get("Cache-Control") != "no-store" || len(hostPage.Items) != 1 || hostPage.Items[0].Site != "site-a" ||
		hostPage.Items[0].ID != host.ID || hostPage.Next == "" || hostPage.Items[0].ReportedAt == nil {
		t.Fatalf("unexpected first host page: %+v", hostPage)
	}
	var nextHostPage kvm.InventoryPage[kvm.HostSummary]
	expectKVM(t, kvmRequest(t, first, fixtureKVMAgent, "GET", hosts+"?limit=1&after="+url.QueryEscape(hostPage.Next), "", "", nil), 200, &nextHostPage)
	if len(nextHostPage.Items) != 1 || nextHostPage.Items[0].Site != "site-b" || nextHostPage.Items[0].ID != host.ID || nextHostPage.Next != "" {
		t.Fatalf("unexpected last host page: %+v", nextHostPage)
	}

	var vmPage kvm.InventoryPage[kvm.VMSummary]
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "GET", vms+"?limit=1", "", "", nil), 200, &vmPage)
	if len(vmPage.Items) != 1 || vmPage.Items[0].ID != vm.ID || vmPage.Items[0].State != "awaiting_approval" ||
		vmPage.Items[0].Address != vm.Address || vmPage.Items[0].Host != vm.Host || vmPage.Next == "" {
		t.Fatalf("unexpected first VM page: %+v", vmPage)
	}
	for _, query := range []string{"?limit=1&after=" + url.QueryEscape(vmPage.Next), "?site=site-b"} {
		var nextVMPage kvm.InventoryPage[kvm.VMSummary]
		expectKVM(t, kvmRequest(t, second, fixtureKVMReviewer, "GET", vms+query, "", "", nil), 200, &nextVMPage)
		if len(nextVMPage.Items) != 1 || nextVMPage.Items[0].ID != other.ID || nextVMPage.Items[0].Site != "site-b" || nextVMPage.Next != "" {
			t.Fatalf("incorrect second VM page for %s: %+v", query, nextVMPage)
		}
	}
	t.Run("authorization and validation", func(t *testing.T) {
		for _, path := range []string{"/hosts", "/vms"} {
			expectKVM(t, kvmRequest(t, first, fixtureKVMGuest, "GET", fixtureKVMProject+path, "", "", nil), 403, nil)
			expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "GET", "/v1/tenants/lab/projects/other/kvm"+path, "", "", nil), 403, nil)
		}
		for _, query := range []string{"?site=", "?site=bad%2Fsite", "?site=site-a&site=site-b", "?unknown=1", "?limit=0", "?limit=101", "?after=site-a%2Fhost-one",
			"?site=site-b&after=" + url.QueryEscape(vmPage.Next)} {
			expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "GET", vms+query, "", "", nil), 400, nil)
		}
		var empty kvm.InventoryPage[kvm.VMSummary]
		expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "GET", "/v1/tenants/lab/projects/other/kvm/vms", "", "", nil), 200, &empty)
		if empty.Items == nil || len(empty.Items) != 0 || empty.Next != "" {
			t.Fatalf("VM list leaked across projects or returned null: %+v", empty)
		}
	})
	t.Run("browser review", func(t *testing.T) { verifyKVMBrowserReview(t, db, vm) })
}

func verifyKVMBrowserReview(t *testing.T, db *sql.DB, vm kvm.VM) {
	t.Helper()
	const origin = "https://kvm-console.example.test"
	const issuer = "https://issuer.example.test"
	ctx := t.Context()
	if _, err := db.ExecContext(ctx, `INSERT INTO cmp_principals(tenant_id,principal_id,kind,issuer,subject) VALUES
		('lab','browser-reviewer','human',$1,'browser-reviewer'),('lab','browser-viewer','human',$1,'browser-viewer')`, issuer); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO cmp_memberships(tenant_id,project_id,principal_id,role) VALUES
		('lab','compute','browser-reviewer','approver'),('lab','compute','browser-viewer','viewer')`); err != nil {
		t.Fatal(err)
	}
	pki := testsupport.NewConsolePKI(t, "fixture-gateway.example")
	app, err := routers.NewAPI(db, "", &consolehttp.ConsoleConfig{Origin: origin, CertFile: pki.CertFile, KeyFile: pki.KeyFile})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Prepare(); err != nil {
		t.Fatal(err)
	}
	sessions := identity.NewBrowserSessions(db)
	login := func(subject string) string {
		t.Helper()
		cookie, err := sessions.Login(ctx, "lab", issuer, subject, "")
		if err != nil {
			t.Fatal(err)
		}
		return cookie
	}
	send := func(method, path, cookie, csrf, version string) *httptest.ResponseRecorder {
		t.Helper()
		var body string
		if method == http.MethodPost {
			body = `{"decision":"approve"}`
		}
		req := httptest.NewRequestWithContext(ctx, method, path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		req.AddCookie(&http.Cookie{Name: consolehttp.ConsoleCookie, Value: cookie})
		req.Header.Set("Origin", origin)
		req.Header.Set(consolehttp.ConsoleCSRFHeader, csrf)
		req.Header.Set("If-Match", version)
		response := httptest.NewRecorder()
		app.ServeHTTP(response, req)
		return response
	}
	viewer, reviewer := login("browser-viewer"), login("browser-reviewer")
	path := fixtureVMSitePath + "/" + vm.ID + "/review"
	expectKVM(t, send("GET", fixtureKVMProject+"/vms", viewer, "", ""), 200, nil)
	expectKVM(t, send("GET", evacuationBatchesPath, viewer, "", ""), 200, nil)
	expectKVM(t, send("GET", evacuationFixturePlansPath, viewer, "", ""), 200, nil)
	expectKVM(t, send("GET", evacuationCandidatesPath, viewer, "", ""), 403, nil)
	expectKVM(t, send("POST", path, viewer, consolehttp.ConsoleCSRF(viewer), `"1"`), 403, nil)
	expectKVM(t, send("POST", path, reviewer, "", `"1"`), 403, nil)
	expectKVM(t, send("POST", path, reviewer, consolehttp.ConsoleCSRF(reviewer), `"2"`), 412, nil)
	var updated kvm.VM
	expectKVM(t, send("POST", path, reviewer, consolehttp.ConsoleCSRF(reviewer), `"1"`), 200, &updated)
	if updated.ID != vm.ID || updated.State != "queued" || updated.Revision != 2 {
		t.Fatalf("browser approval did not advance the selected VM: %+v", updated)
	}
	var spec openapi.Document
	response := send("GET", "/openapi.json", viewer, "", "")
	if err := json.Unmarshal(response.Body.Bytes(), &spec); err != nil {
		t.Fatal(err)
	}
	list := spec.Paths["/v1/tenants/{tenant}/projects/{project}/kvm/vms"].Get
	if list == nil || list.OperationID != "listProjectKVMVMs" || len(list.Security) != 2 {
		t.Fatalf("unexpected OpenAPI list contract: %+v", list)
	}
	if _, ok := list.Security[1]["consoleSession"]; !ok {
		t.Fatalf("browser authentication missing from OpenAPI list contract: %+v", list.Security)
	}
	page := list.Responses["200"].Content["application/json"].Schema.Properties["data"].Properties
	if page["items"].Items == nil || page["items"].Items.Properties["id"].Format != "uuid" || page["next"].Type != "string" {
		t.Fatalf("unexpected OpenAPI inventory page schema: %+v", page)
	}
	candidates := spec.Paths["/v1/tenants/{tenant}/projects/{project}/kvm/sites/{site}/hosts/{host}/fence/evacuation-candidates"].Get
	if candidates == nil || candidates.OperationID != "listKVMEvacuationCandidates" || len(candidates.Security) != 2 {
		t.Fatalf("unexpected OpenAPI evacuation candidate contract: %+v", candidates)
	}
	page = candidates.Responses["200"].Content["application/json"].Schema.Properties["data"].Properties
	if page["items"].Items == nil || page["items"].Items.Properties["vm_id"].Format != "uuid" ||
		page["mode"].Type != "string" || page["host_revision"].Type != "integer" || page["source_fence_generation"].Type != "integer" {
		t.Fatalf("unexpected OpenAPI evacuation candidate page schema: %+v", page)
	}
}
