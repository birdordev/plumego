package main

import (
	"crypto/sha256"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
)

func TestAcceptanceCloudAccountLifecycle(t *testing.T) {
	db := dbtest.Open(t)
	ctx := t.Context()
	for _, migrate := range []func() error{
		func() error { return identity.Migrate(ctx, db) },
		func() error { return identity.MigrateServiceTokens(ctx, db) },
		func() error { return identity.MigrateAccounts(ctx, db) },
	} {
		if err := migrate(); err != nil {
			t.Fatal(err)
		}
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO cmp_tenants VALUES ('tenant'),('other');
		INSERT INTO cmp_projects VALUES ('tenant','source'),('tenant','dest'),('tenant','private'),('other','dest');
		INSERT INTO cmp_principals (tenant_id,principal_id,kind,issuer,subject) VALUES
			('tenant','admin','service','fixture','admin'),('tenant','reader','service','fixture','reader');
		INSERT INTO cmp_memberships (tenant_id,project_id,principal_id,role) VALUES
			('tenant','source','admin','admin'),('tenant','dest','admin','admin'),('tenant','source','reader','viewer');`)
	token := strings.Repeat("fixture-admin-token-", 3)
	reader := strings.Repeat("fixture-reader-token-", 3)
	for principal, secret := range map[string]string{"admin": token, "reader": reader} {
		digest := sha256.Sum256([]byte(secret))
		exec("INSERT INTO cmp_service_tokens(token_hash,tenant_id,principal_id,expires_at) VALUES ($1,'tenant',$2,now()+interval '5 minutes')", digest[:], principal)
	}
	app, err := newAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	const source = "/v1/tenants/tenant/projects/source/cloud-accounts"
	const dest = "/v1/tenants/tenant/projects/dest/cloud-accounts/example"
	const account = source + "/example"
	call := func(method, path, bearer, etag, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		if etag != "" {
			req.Header.Set("If-Match", etag)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		app.ServeHTTP(w, req)
		if w.Code != status || strings.Contains(w.Body.String(), "fixture/ref/") {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body)
		}
		return w
	}
	input := `{"id":"example","provider":"openstack","credential_ref":"fixture/ref/original"}`
	call(http.MethodPost, source, "", "", input, 401)
	call(http.MethodPost, source, reader, "", input, 403)
	created := call(http.MethodPost, source, token, "", input, 201)
	revision := created.Header().Get("ETag")
	if created.Header().Get("Location") != account || revision == "" {
		t.Fatal("registration missing location or version")
	}
	call(http.MethodPost, source, token, "", input, 409)
	call(http.MethodGet, "/v1/tenants/other/projects/dest/cloud-accounts/example", token, "", "", 403)
	call(http.MethodPatch, account, token, "", `{"action":"suspend"}`, 412)
	call(http.MethodPatch, account, token, revision, `{"action":"transfer","target_project":"private"}`, 403)
	shared := call(http.MethodPatch, account, token, revision, `{"action":"share","target_project":"dest"}`, 200)
	call(http.MethodPatch, account, token, revision, `{"action":"suspend"}`, 412)
	rotated := call(http.MethodPatch, account, token, shared.Header().Get("ETag"), `{"action":"rotate","credential_ref":"fixture/ref/next"}`, 200)
	store := identity.NewStore(db)
	binding, err := store.CloudAccountBinding(ctx, "tenant", "example")
	if err != nil || binding.Mode != "pending" || binding.CredentialRef != "fixture/ref/next" {
		t.Fatalf("incorrect rotation: mode=%q error=%v", binding.Mode, err)
	}
	if err := store.VerifyCloudAccount(ctx, "tenant", "example", binding, "site:project"); err != nil {
		t.Fatal(err)
	}
	call(http.MethodPatch, account, token, rotated.Header().Get("ETag"), `{"action":"suspend"}`, 412)
	live := call(http.MethodGet, account, token, "", "", 200).Header().Get("ETag")

	exec(`ALTER TABLE cmp_cloud_account_events ADD CONSTRAINT test_fail_audit CHECK (action <> 'suspend')`)
	call(http.MethodPatch, account, token, live, `{"action":"suspend"}`, 503)
	if current := call(http.MethodGet, account, token, "", "", 200); current.Header().Get("ETag") != live || !strings.Contains(current.Body.String(), "read_only") {
		t.Fatal("audit failure did not roll back account state")
	}
	exec("ALTER TABLE cmp_cloud_account_events DROP CONSTRAINT test_fail_audit")
	suspended := call(http.MethodPatch, account, token, live, `{"action":"suspend"}`, 200)
	resumed := call(http.MethodPatch, account, token, suspended.Header().Get("ETag"), `{"action":"resume"}`, 200)
	call(http.MethodPatch, account, token, resumed.Header().Get("ETag"), `{"action":"transfer","target_project":"dest"}`, 200)
	call(http.MethodGet, account, token, "", "", 403)
	call(http.MethodGet, dest, token, "", "", 200)
	var events, grants int
	var remote sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM cmp_cloud_account_events),
		(SELECT count(*) FROM cmp_cloud_account_grants),remote_identity FROM cmp_cloud_accounts`).Scan(&events, &grants, &remote); err != nil ||
		events != 6 || grants != 0 || remote.String != "site:project" {
		t.Fatalf("events=%d grants=%d identity=%q error=%v", events, grants, remote.String, err)
	}
}
