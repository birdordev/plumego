package consoletest

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	consolehttp "with-cloud-manager/app/handlers/console"
	"with-cloud-manager/app/integration/testsupport"
	"with-cloud-manager/app/routers"
	"with-cloud-manager/internal/audit"
	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/operations"
)

func TestAcceptanceConsoleAcrossReplicas(t *testing.T) {
	db, ctx := dbtest.Open(t), t.Context()
	for _, migrate := range []func(context.Context, *sql.DB) error{
		identity.Migrate, identity.MigrateServiceTokens, identity.MigrateAccounts, identity.MigrateBrowserSessions, inventory.Migrate, operations.Migrate, audit.Migrate,
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
	const origin, issuer, proxyDNS = "https://console.example.test", "https://identity.example.test/realms/example", "identity-proxy.example.test"
	const subject = "stable-employee-subject"
	exec(`INSERT INTO cmp_tenants VALUES ('tenant'),('external');
	    INSERT INTO cmp_projects VALUES ('tenant','admin-sandbox'),('tenant','project'),('tenant','viewer'),('tenant','hidden'),('external','project');`)
	exec(`INSERT INTO cmp_principals (tenant_id,principal_id,kind,issuer,subject) VALUES
	    ('tenant','operator','human',$1,$2),('tenant','other-issuer','human','https://different.example.test','wrong-issuer'),
	    ('tenant','service','service',$1,'a-service'),('external','operator','human',$1,'other-tenant')`, issuer, subject)
	exec(`INSERT INTO cmp_memberships (tenant_id,project_id,principal_id,role) VALUES
	    ('tenant','admin-sandbox','operator','admin'),('tenant','project','operator','operator'),('tenant','viewer','operator','viewer'),
	    ('tenant','project','service','viewer'),
	    ('tenant','project','other-issuer','operator'),('external','project','operator','operator');
	    INSERT INTO cmp_cloud_accounts (tenant_id,account_id,project_id,provider,credential_ref,remote_identity,mode) VALUES
	    ('tenant','account','project','aws-ec2','fixture/ref','aws:fixture','write_enabled');
	    INSERT INTO cmp_inventory_scopes (tenant_id,account_id,region,kind,credential_revision,lease_until)
	    VALUES ('tenant','account','us-east-1','instance',1,now());
	    INSERT INTO cmp_inventory_objects (scope_id,external_id,name,status,seen_generation)
	    SELECT scope_id,'i-01234567','fixture worker','running',1 FROM cmp_inventory_scopes;`)
	const bearer = "fixture-console-legacy-service-token"
	hash := sha256.Sum256([]byte(bearer))
	exec(`INSERT INTO cmp_service_tokens (token_hash,tenant_id,principal_id,expires_at)
	    VALUES ($1,'tenant','service',now()+interval '5 minutes')`, hash[:])

	pki := testsupport.NewConsolePKI(t, proxyDNS)
	env := map[string]string{"CLOUD_MANAGER_CONSOLE_ORIGIN": origin, "CLOUD_MANAGER_CONSOLE_ISSUER": issuer,
		"CLOUD_MANAGER_CONSOLE_PROXY_DNS": proxyDNS, "CLOUD_MANAGER_CONSOLE_PROXY_CA": pki.CaFile,
		"CLOUD_MANAGER_CONSOLE_CERT": pki.CertFile, "CLOUD_MANAGER_CONSOLE_KEY": pki.KeyFile}
	config, err := consolehttp.LoadConsoleConfig(func(key string) string { return env[key] })
	if err != nil || config == nil {
		t.Fatalf("valid console configuration: %v", err)
	}
	first := serveConsoleReplica(t, db, *config)
	second := serveConsoleReplica(t, db, *config)
	trusted := pki.Client(pki.Gateway)
	badGateway := pki.Client(pki.WrongDNS)
	direct := pki.Client(tls.Certificate{})
	const projectPath = "/v1/tenants/tenant/projects/project"
	req := func(client *http.Client, site, method, path, body string, cookie *http.Cookie, headers http.Header, want int) ([]byte, http.Header) {
		t.Helper()
		request, err := http.NewRequestWithContext(ctx, method, site+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		request.Header = headers.Clone()
		if request.Header == nil {
			request.Header = make(http.Header)
		}
		if body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		if cookie != nil {
			request.AddCookie(cookie)
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = response.Body.Close() }()
		data, err := io.ReadAll(response.Body)
		if err != nil || response.StatusCode != want {
			t.Fatalf("%s %s status=%d wanted=%d error=%v", method, path, response.StatusCode, want, err)
		}
		return data, response.Header
	}

	app, err := routers.NewAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	disabled := httptest.NewRecorder()
	app.ServeHTTP(disabled, httptest.NewRequest(http.MethodGet, "/console", nil))
	if disabled.Code != http.StatusNotFound {
		t.Fatalf("console enabled without gateway configuration: %d", disabled.Code)
	}
	html, headers := req(direct, first, http.MethodGet, "/console", "", nil, nil, http.StatusOK)
	if !strings.Contains(string(html), "/console/app.js") || headers.Get("Cache-Control") != "no-store" ||
		!strings.Contains(headers.Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatal("console page lacks secure asset wiring")
	}
	linked, linkedHeaders := req(direct, second, http.MethodGet, "/console?project=admin-sandbox&view=cloud-accounts", "", nil, nil, http.StatusOK)
	if string(linked) != string(html) || linkedHeaders.Get("Content-Security-Policy") != headers.Get("Content-Security-Policy") {
		t.Fatal("a workspace link must serve the same console entry point and policy")
	}
	_, headers = req(direct, first, http.MethodGet, "/console/app.js", "", nil, nil, http.StatusOK)
	if !strings.HasPrefix(headers.Get("Content-Type"), "text/javascript") || headers.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("console application script has unsafe media type")
	}
	chunks, err := fs.ReadDir(consolehttp.ConsoleChunks, "web/_embedded/assets")
	if err != nil || len(chunks) == 0 {
		t.Fatalf("console Vite chunks missing: %v", err)
	}
	for _, chunk := range chunks {
		body, headers := req(direct, first, http.MethodGet, "/console/assets/"+chunk.Name(), "", nil, nil, http.StatusOK)
		if len(body) == 0 || !strings.Contains(headers.Get("Cache-Control"), "immutable") ||
			!strings.HasPrefix(headers.Get("Content-Type"), "text/javascript") {
			t.Fatalf("Vite chunk %s was not served with the expected policy", chunk.Name())
		}
	}
	req(direct, first, http.MethodGet, "/console/assets/missing.js", "", nil, nil, http.StatusNotFound)
	loginHeaders := http.Header{"Origin": {origin}, http.CanonicalHeaderKey(consolehttp.ConsoleSubjectHeader): {subject}}
	const loginBody = `{"tenant":"tenant"}`
	req(direct, first, http.MethodPost, "/console/login", loginBody, nil, loginHeaders, http.StatusForbidden)
	req(badGateway, first, http.MethodPost, "/console/login", loginBody, nil, loginHeaders, http.StatusForbidden)
	for _, invalid := range []http.Header{
		{"Origin": {"https://other.example.test"}, http.CanonicalHeaderKey(consolehttp.ConsoleSubjectHeader): {subject}},
		{"Origin": {origin}, http.CanonicalHeaderKey(consolehttp.ConsoleSubjectHeader): {subject, "other"}},
		{"Origin": {origin}, http.CanonicalHeaderKey(consolehttp.ConsoleSubjectHeader): {"wrong-issuer"}},
		{"Origin": {origin}, http.CanonicalHeaderKey(consolehttp.ConsoleSubjectHeader): {"a-service"}},
	} {
		req(trusted, first, http.MethodPost, "/console/login", loginBody, nil, invalid, http.StatusForbidden)
	}
	req(trusted, first, http.MethodPost, "/console/login", `{"tenant":"external"}`, nil, loginHeaders, http.StatusForbidden)
	login := func(previous *http.Cookie) *http.Cookie {
		t.Helper()
		body, headers := req(trusted, first, http.MethodPost, "/console/login", loginBody, previous, loginHeaders, http.StatusOK)
		cookies := (&http.Response{Header: headers}).Cookies()
		if len(cookies) != 1 {
			t.Fatal("login did not issue a session")
		}
		cookie := cookies[0]
		if cookie.Name != consolehttp.ConsoleCookie || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode ||
			cookie.Path != "/" || cookie.Domain != "" || cookie.MaxAge != 3600 || strings.Contains(string(body), cookie.Value) {
			t.Fatal("browser session cookie is unsafe")
		}
		return cookie
	}
	cookie := login(nil)
	profile, _ := req(direct, second, http.MethodGet, "/console/session", "", cookie, nil, http.StatusOK)
	var session struct {
		Data struct {
			identity.BrowserProfile
			CSRF string
		}
	}
	if err := json.Unmarshal(profile, &session); err != nil || session.Data.Tenant != "tenant" || session.Data.Principal != "operator" ||
		len(session.Data.Projects) != 3 || session.Data.Projects[0].Role != "admin" || session.Data.Projects[1].Role != "operator" || session.Data.CSRF == "" ||
		strings.Contains(string(profile), cookie.Value) {
		t.Fatal("session not usable on another replica")
	}
	req(direct, second, http.MethodGet, projectPath+"/assets", "", cookie, nil, http.StatusOK)
	req(direct, second, http.MethodGet, "/v1/tenants/tenant/projects/admin-sandbox/audit", "", cookie, nil, http.StatusOK)
	req(direct, second, http.MethodGet, projectPath+"/audit/export", "", cookie, nil, http.StatusForbidden)
	req(direct, second, http.MethodGet, "/v1/tenants/external/projects/project/assets", "", cookie, nil, http.StatusForbidden)
	const mutation = `{"items":[{"account":"account","region":"us-east-1","asset_id":"i-01234567","action":"stop","ownership_revision":1}]}`
	writeHeaders := http.Header{"Origin": {origin}, http.CanonicalHeaderKey(consolehttp.ConsoleCSRFHeader): {session.Data.CSRF}, "Idempotency-Key": {"console-action"}}
	req(direct, second, http.MethodPost, projectPath+"/operation-batches", mutation, cookie, nil, http.StatusForbidden)
	wrongOrigin := writeHeaders.Clone()
	wrongOrigin.Set("Origin", "https://other.example.test")
	req(direct, second, http.MethodPost, projectPath+"/operation-batches", mutation, cookie, wrongOrigin, http.StatusForbidden)
	req(direct, second, http.MethodPost, "/v1/tenants/tenant/projects/viewer/operation-batches", mutation, cookie, writeHeaders, http.StatusForbidden)
	req(direct, second, http.MethodPost, projectPath+"/operation-batches", mutation, cookie, writeHeaders, http.StatusAccepted)
	var applicant string
	if err := db.QueryRowContext(ctx, "SELECT principal_id FROM cmp_operation_batches WHERE idempotency_key='console-action'").Scan(&applicant); err != nil ||
		applicant != "operator" {
		t.Fatal("browser operation lost its human requester")
	}
	legacy := http.Header{"Authorization": {"Bearer " + bearer}}
	req(direct, second, http.MethodGet, projectPath+"/assets", "", nil, legacy, http.StatusOK)
	invalidBearer := http.Header{"Authorization": {"Bearer definitely-not-a-service-token"}}
	req(direct, second, http.MethodGet, projectPath+"/assets", "", cookie, invalidBearer, http.StatusUnauthorized)

	replacement := login(cookie)
	req(direct, second, http.MethodGet, "/console/session", "", cookie, nil, http.StatusUnauthorized)
	req(direct, second, http.MethodPost, "/console/logout", "", replacement, writeHeaders, http.StatusForbidden)
	exec("UPDATE cmp_memberships SET active=false WHERE tenant_id='tenant' AND project_id='project' AND principal_id='operator'")
	req(direct, second, http.MethodGet, projectPath+"/assets", "", replacement, nil, http.StatusForbidden)
	_, clear := req(direct, second, http.MethodPost, "/console/logout", "", replacement,
		http.Header{"Origin": {origin}, http.CanonicalHeaderKey(consolehttp.ConsoleCSRFHeader): {consolehttp.ConsoleCSRF(replacement.Value)}}, http.StatusOK)
	if deleted := (&http.Response{Header: clear}).Cookies(); len(deleted) != 1 || deleted[0].MaxAge >= 0 {
		t.Fatal("logout did not clear the client cookie")
	}
	req(direct, first, http.MethodGet, "/console/session", "", replacement, nil, http.StatusUnauthorized)
	expired := login(nil)
	exec("UPDATE cmp_browser_sessions SET expires_at=now()-interval '1 second'")
	req(direct, first, http.MethodGet, "/console/session", "", expired, nil, http.StatusUnauthorized)
	if removed, err := identity.NewBrowserSessions(db).PurgeExpired(ctx); err != nil || removed != 1 {
		t.Fatalf("expired cleanup count=%d error=%v", removed, err)
	}
}

func serveConsoleReplica(t *testing.T, db *sql.DB, cfg consolehttp.ConsoleConfig) string {
	t.Helper()
	app, err := routers.NewAPI(db, "", &cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.ConfigureTLS(app); err != nil {
		t.Fatal(err)
	}
	server, err := app.Server()
	if err != nil {
		t.Fatal(err)
	}
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- server.ServeTLS(listener, "", "") }()
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Error(err)
		}
		if err := <-done; !errors.Is(err, http.ErrServerClosed) {
			t.Error(err)
		}
	})
	return "https://" + listener.Addr().String()
}
