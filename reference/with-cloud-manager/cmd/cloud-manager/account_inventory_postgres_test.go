package main

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/birdordev/plumego/x/openapi"

	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
)

const accountSourcePath = "/v1/tenants/tenant/projects/source/cloud-accounts"
const accountDestPath = "/v1/tenants/tenant/projects/dest/cloud-accounts"
const inventoryAdminToken = "fixture-cloud-account-inventory-admin-0001"
const inventoryViewerToken = "fixture-cloud-account-inventory-viewer-0001"

func TestAcceptanceCloudAccountInventory(t *testing.T) {
	_, app, cookie := setupAccountInventory(t)
	const tc = accountSourcePath + "/tc"
	first := accountInventoryCall(t, app, accountSourcePath+"?limit=1", inventoryAdminToken, "", 200)
	page := accountPageData[identity.Account](t, first)
	if len(page.Items) != 1 || page.Items[0].ID != "ali" || page.Next != "ali" {
		t.Fatalf("incorrect account page: %+v", page)
	}
	last := accountPageData[identity.Account](t, accountInventoryCall(t, app, accountSourcePath+"?after=ali", inventoryAdminToken, "", 200))
	if len(last.Items) != 2 || last.Items[0].Provider != "huawei-ecs" || last.Items[1].Provider != "tencent-cvm" || last.Next != "" {
		t.Fatalf("incorrect account continuation: %+v", last)
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, accountSourcePath, nil)
	req.AddCookie(&http.Cookie{Name: consoleCookie, Value: cookie})
	browser := httptest.NewRecorder()
	app.ServeHTTP(browser, req)
	if browser.Code != 200 || strings.Contains(browser.Body.String(), "fixture/ref/") {
		t.Fatalf("browser account response: %d %s", browser.Code, browser.Body)
	}
	checkAccountInventoryAccess(t, app)
	shared := accountPageData[identity.AccountGrant](t, accountInventoryCall(t, app, tc+"/grants?limit=1", inventoryAdminToken, "", 200))
	if len(shared.Items) != 1 || shared.Items[0].Project != "dest" || shared.Items[0].AllowWrite || shared.Next != "dest" {
		t.Fatalf("incorrect shared grant page: %+v", shared)
	}
	shared = accountPageData[identity.AccountGrant](t, accountInventoryCall(t, app, tc+"/grants?after=dest", inventoryAdminToken, "", 200))
	if len(shared.Items) != 1 || shared.Items[0].Project != "third" || shared.Next != "" {
		t.Fatalf("incorrect shared grant continuation: %+v", shared)
	}
	accountInventoryCall(t, app, tc, inventoryAdminToken, `{"action":"unshare","target_project":"third"}`, 200)
	shared = accountPageData[identity.AccountGrant](t, accountInventoryCall(t, app, tc+"/grants", inventoryAdminToken, "", 200))
	if len(shared.Items) != 1 || shared.Items[0].Project != "dest" {
		t.Fatalf("revoked grant still visible: %+v", shared)
	}
	accountInventoryCall(t, app, tc, inventoryAdminToken, `{"action":"transfer","target_project":"dest"}`, 200)
	accountInventoryCall(t, app, tc+"/grants", inventoryAdminToken, "", 403)
	checkTransferredAccount(t, app)
	checkAccountBrowserMutation(t, app, cookie)
	checkAccountInventoryCLI(t, app)
}

func setupAccountInventory(t *testing.T) (*sql.DB, http.Handler, string) {
	t.Helper()
	db := dbtest.Open(t)
	ctx := t.Context()
	if err := migrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO cmp_tenants VALUES ('tenant'),('foreign');
		INSERT INTO cmp_projects VALUES ('tenant','source'),('tenant','dest'),('tenant','third'),('foreign','source');
		INSERT INTO cmp_principals (tenant_id,principal_id,kind,issuer,subject) VALUES
			('tenant','admin','service','fixture','admin'),('tenant','viewer','service','fixture','viewer'),
			('tenant','browser','human','https://issuer.example.test','admin');
		INSERT INTO cmp_memberships (tenant_id,project_id,principal_id,role) VALUES
			('tenant','source','admin','admin'),('tenant','dest','admin','admin'),('tenant','third','admin','admin'),
			('tenant','source','viewer','viewer'),('tenant','source','browser','admin');
		INSERT INTO cmp_cloud_accounts (tenant_id,account_id,project_id,provider,credential_ref) VALUES
			('tenant','tc','source','tencent-cvm','fixture/ref/tc'),('tenant','ali','source','alibaba-ecs','fixture/ref/ali'),
			('tenant','hw','source','huawei-ecs','fixture/ref/hw'),('foreign','tc','source','tencent-cvm','fixture/ref/foreign');
		INSERT INTO cmp_cloud_account_grants (tenant_id,account_id,project_id) VALUES
			('tenant','tc','dest'),('tenant','tc','third');`); err != nil {
		t.Fatal(err)
	}
	for principal, token := range map[string]string{"admin": inventoryAdminToken, "viewer": inventoryViewerToken} {
		hash := sha256.Sum256([]byte(token))
		if _, err := db.ExecContext(ctx, `INSERT INTO cmp_service_tokens (token_hash,tenant_id,principal_id,expires_at)
			VALUES ($1,'tenant',$2,now()+interval '10 minutes')`, hash[:], principal); err != nil {
			t.Fatal(err)
		}
	}
	pki := makeConsolePKI(t, "fixture-gateway.example")
	app, err := newAPI(db, "", &consoleConfig{origin: "https://accounts.example.test", certFile: pki.certFile, keyFile: pki.keyFile})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Prepare(); err != nil {
		t.Fatal(err)
	}
	cookie, err := identity.NewBrowserSessions(db).Login(ctx, "tenant", "https://issuer.example.test", "admin", "")
	if err != nil {
		t.Fatal(err)
	}
	return db, app, cookie
}

func checkAccountInventoryAccess(t *testing.T, app http.Handler) {
	t.Helper()
	const grants = accountSourcePath + "/tc/grants"
	for _, path := range []string{accountSourcePath, grants} {
		accountInventoryCall(t, app, path, "", "", 401)
		accountInventoryCall(t, app, path, inventoryViewerToken, "", 403)
		accountInventoryCall(t, app, strings.Replace(path, "/tenant/", "/foreign/", 1), inventoryAdminToken, "", 403)
		for _, query := range []string{"?limit=0", "?limit=101", "?after=", "?after=a%2Fb", "?limit=1&limit=2", "?provider=extra"} {
			accountInventoryCall(t, app, path+query, inventoryAdminToken, "", 400)
		}
	}
	page := accountPageData[identity.Account](t, accountInventoryCall(t, app, accountDestPath, inventoryAdminToken, "", 200))
	if page.Items == nil || len(page.Items) > 0 {
		t.Fatalf("shared accounts should not be listed as owned: %+v", page)
	}
	accountInventoryCall(t, app, accountDestPath+"/tc/grants", inventoryAdminToken, "", 403)
	spec := accountInventoryCall(t, app, "/openapi.json", "", "", 200)
	var doc openapi.Document
	if err := json.Unmarshal(spec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	const pattern = "/v1/tenants/{tenant}/projects/{project}/cloud-accounts"
	for path, id := range map[string]string{pattern: "listCloudAccounts", pattern + "/{account}/grants": "listCloudAccountGrants"} {
		op := doc.Paths[path].Get
		if op == nil || op.OperationID != id || len(op.Security) != 2 {
			t.Fatalf("missing account inventory contract: %s", path)
		}
	}
}

func checkAccountInventoryCLI(t *testing.T, app http.Handler) {
	t.Helper()
	server := httptest.NewTLSServer(app)
	t.Cleanup(server.Close)
	var output bytes.Buffer
	for _, test := range []struct {
		args        []string
		input, want string
	}{
		{[]string{"list", "tenant", "source", "--limit", "1"}, "", `"next":"ali"`},
		{[]string{"get", "tenant", "dest", "tc"}, "", `"project":"dest"`},
		{[]string{"grants", "tenant", "dest", "tc", "--limit", "1"}, "", `"items":[]`},
		{[]string{"register", "tenant", "source"}, `{"id":"cli-cloud","provider":"alibaba-ecs","credential_ref":"fixture/ref/cli"}`, `"mode":"pending"`},
		{[]string{"change", "tenant", "source", "cli-cloud", "1"}, `{"action":"suspend"}`, `"mode":"suspended"`},
	} {
		output.Reset()
		err := runCloudAccountsClient(t.Context(), test.args, server.URL, inventoryAdminToken, strings.NewReader(test.input), &output, server.Client().Transport)
		if err != nil || !json.Valid(output.Bytes()) || !strings.Contains(output.String(), test.want) || strings.Contains(output.String(), "fixture/ref/") {
			t.Fatalf("cloud account CLI %s: %v %s", test.args[0], err, output.String())
		}
	}
}

func checkAccountBrowserMutation(t *testing.T, app http.Handler, cookie string) {
	t.Helper()
	const data = `{"id":"browser-cloud","provider":"tencent-cvm","credential_ref":"fixture/ref/browser"}`
	for _, attempt := range []struct {
		csrf string
		want int
	}{{"", 403}, {consoleCSRF(cookie), 201}} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, accountSourcePath, strings.NewReader(data))
		req.Header.Set("Origin", "https://accounts.example.test")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(consoleCSRFHeader, attempt.csrf)
		req.AddCookie(&http.Cookie{Name: consoleCookie, Value: cookie})
		w := httptest.NewRecorder()
		app.ServeHTTP(w, req)
		if w.Code != attempt.want || strings.Contains(w.Body.String(), "fixture/ref/") {
			t.Fatalf("browser account registration: %d %s", w.Code, w.Body)
		}
	}
}

func checkTransferredAccount(t *testing.T, app http.Handler) {
	t.Helper()
	owned := accountPageData[identity.Account](t, accountInventoryCall(t, app, accountDestPath, inventoryAdminToken, "", 200))
	remaining := accountPageData[identity.Account](t, accountInventoryCall(t, app, accountSourcePath, inventoryAdminToken, "", 200))
	grants := accountPageData[identity.AccountGrant](t, accountInventoryCall(t, app, accountDestPath+"/tc/grants", inventoryAdminToken, "", 200))
	if len(owned.Items) != 1 || owned.Items[0].ID != "tc" || len(remaining.Items) != 2 || len(grants.Items) != 0 {
		t.Fatalf("transfer did not move account and clear grants: owned=%+v source=%+v grants=%+v", owned, remaining, grants)
	}
}

func accountPageData[T any](t *testing.T, w *httptest.ResponseRecorder) identity.AccountPage[T] {
	t.Helper()
	var envelope struct {
		Data identity.AccountPage[T] `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.Data
}

func accountInventoryCall(t *testing.T, app http.Handler, path, token, body string, status int) *httptest.ResponseRecorder {
	t.Helper()
	method, match := http.MethodGet, ""
	if body != "" {
		etag := accountInventoryCall(t, app, path, token, "", 200).Header().Get("ETag")
		method, match = http.MethodPatch, etag
	}
	request := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if match != "" {
		request.Header.Set("If-Match", match)
		request.Header.Set("Content-Type", "application/json")
	}
	writer := httptest.NewRecorder()
	app.ServeHTTP(writer, request)
	if writer.Code != status || strings.Contains(writer.Body.String(), "fixture/ref/") {
		t.Fatalf("%s %s: %d %s", method, path, writer.Code, writer.Body)
	}
	return writer
}
