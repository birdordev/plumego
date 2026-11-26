package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/birdordev/plumego/x/openapi"

	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/operations"
)

func TestAcceptanceProjectOperationHistory(t *testing.T) {
	db, ctx := dbtest.Open(t), t.Context()
	for _, migrate := range []func() error{
		func() error { return identity.Migrate(ctx, db) },
		func() error { return identity.MigrateServiceTokens(ctx, db) },
		func() error { return inventory.Migrate(ctx, db) },
		func() error { return operations.Migrate(ctx, db) },
	} {
		if err := migrate(); err != nil {
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
	    INSERT INTO cmp_projects VALUES ('tenant','project'),('tenant','other'),('tenant','empty'),('tenant','hidden'),('external','project');
	    INSERT INTO cmp_principals (tenant_id,principal_id,kind,issuer,subject) VALUES
	    ('tenant','reader','service','fixture','reader'),('external','reader','service','fixture','reader');
	    INSERT INTO cmp_memberships (tenant_id,project_id,principal_id,role) VALUES
	    ('tenant','project','reader','viewer'),('tenant','empty','reader','viewer'),('tenant','other','reader','viewer');
	    INSERT INTO cmp_cloud_accounts (tenant_id,account_id,project_id,provider,credential_ref,remote_identity,mode) VALUES
	    ('tenant','account','project','aws-ec2','fixture/one','aws:fixture','write_enabled'),
	    ('tenant','secondary','project','aws-ec2','fixture/two','aws:other','write_enabled'),
	    ('external','account','project','openstack','fixture/stack','site:project','read_only');`)
	const token = "fixture-operation-query-service-token"
	digest := sha256.Sum256([]byte(token))
	exec(`INSERT INTO cmp_service_tokens(token_hash,tenant_id,principal_id,expires_at)
	    VALUES ($1,'tenant','reader',now()+interval '5 minutes')`, digest[:])
	insert := func(id, tenant, project, account, kind, action, status, timestamp string) {
		t.Helper()
		exec(`INSERT INTO cmp_instance_operations (operation_id,tenant_id,project_id,principal_id,account_id,region,
		    resource_kind,action,status,idempotency_key,request_hash,credential_revision,membership_revision,ownership_revision,created_at)
		    VALUES ($1,$2,$3,'reader',$4,'us-east-1',$5,$6,$7,$1,decode(repeat('00',32),'hex'),1,1,0,$8::text::timestamptz)`,
			id, tenant, project, account, kind, action, status, timestamp)
	}
	insert("old-instance", "tenant", "project", "account", "instance", "create", "awaiting_approval", "2026-01-01T00:00:00Z")
	insert("same-a", "tenant", "project", "secondary", "subnet", "create-subnet", "observing", "2026-01-02T00:00:00.123456Z")
	insert("same-z", "tenant", "project", "account", "network", "create-network", "queued", "2026-01-02T00:00:00.123456Z")
	insert("recent-group", "tenant", "project", "account", "security-group", "create-security-group", "failed", "2026-01-03T00:00:00Z")
	insert("hidden-project", "tenant", "other", "account", "network", "create-network", "queued", "2026-01-04T00:00:00Z")
	insert("hidden-tenant", "external", "project", "account", "network", "create-network", "queued", "2026-01-04T00:00:00Z")
	exec("INSERT INTO cmp_instance_operation_events (operation_id,status) VALUES ('same-z','queued')")
	app, err := newAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	const path = "/v1/tenants/tenant/projects/project/operations"
	get := func(path, secret string, want int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if secret != "" {
			req.Header.Set("Authorization", "Bearer "+secret)
		}
		w := httptest.NewRecorder()
		app.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("GET %s status=%d want=%d: %s", path, w.Code, want, w.Body)
		}
		return w
	}
	checkPage := func(w *httptest.ResponseRecorder, wantIDs ...string) operations.OperationPage {
		t.Helper()
		var response struct{ Data operations.OperationPage }
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("invalid operation page: %s, %v", w.Body, err)
		}
		items := response.Data.Items
		ids := make([]string, 0, len(items))
		for _, item := range items {
			ids = append(ids, item.ID)
		}
		if items == nil || !slices.Equal(ids, wantIDs) {
			t.Fatalf("page IDs=%v want=%v, body=%s", ids, wantIDs, w.Body)
		}
		if strings.Contains(w.Body.String(), "request_hash") || strings.Contains(w.Body.String(), "principal_id") {
			t.Fatal("internal operation metadata escaped")
		}
		return response.Data
	}
	if got := get(path, "", http.StatusUnauthorized).Header().Get("WWW-Authenticate"); !strings.HasPrefix(got, "Bearer") {
		t.Fatalf("missing challenge %q", got)
	}
	get(strings.Replace(path, "/tenant/", "/external/", 1), token, http.StatusForbidden)
	get(strings.Replace(path, "/project/", "/hidden/", 1), token, http.StatusForbidden)
	checkPage(get(strings.Replace(path, "/project/", "/empty/", 1), token, http.StatusOK))
	checkPage(get(path+"?kind=volume", token, http.StatusOK))
	checkPage(get(path+"?kind=image", token, http.StatusOK))
	for _, query := range []string{"limit=101", "limit=0", "kind=region", "status=unexpected", "name=any", "account=", "cursor=!", "status=queued&status=failed", "account=%zz"} {
		get(path+"?"+query, token, http.StatusBadRequest)
	}

	first := checkPage(get(path+"?limit=2", token, http.StatusOK), "recent-group", "same-z")
	if first.Next == "" {
		t.Fatal("first page did not supply a cursor")
	}
	cursor := url.QueryEscape(first.Next)
	insert("newer", "tenant", "project", "account", "network", "create-network", "queued", "2026-01-05T00:00:00Z")
	if second := checkPage(get(path+"?limit=2&cursor="+cursor, token, http.StatusOK), "same-a", "old-instance"); second.Next != "" {
		t.Fatal("last page unexpectedly had a cursor")
	}
	get(path+"?status=queued&cursor="+cursor, token, http.StatusBadRequest)
	get(strings.Replace(path, "/project/", "/other/", 1)+"?cursor="+cursor, token, http.StatusBadRequest)
	checkPage(get(path+"?account=secondary&region=us-east-1&kind=subnet&action=create-subnet&status=observing", token, http.StatusOK), "same-a")
	get(path+"/hidden-project", token, http.StatusForbidden)
	get(path+"/same-z/events", token, http.StatusOK)
	spec := get("/openapi.json", token, http.StatusOK)
	var document openapi.Document
	if err := json.Unmarshal(spec.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	list := document.Paths["/v1/tenants/{tenant}/projects/{project}/operations"].Get
	if list == nil || list.OperationID != "listProjectOperations" || len(list.Security) != 1 || list.Responses["400"].Description == "" {
		t.Fatalf("missing operation list contract: %+v", list)
	}

	server := httptest.NewServer(app)
	defer server.Close()
	var output bytes.Buffer
	for _, command := range []struct {
		args []string
		want string
	}{
		{[]string{"list", "tenant", "project", "--kind", "subnet", "--status", "observing"}, `"id":"same-a"`},
		{[]string{"get", "tenant", "project", "same-z"}, `"action":"create-network"`},
		{[]string{"events", "tenant", "project", "same-z", "--after", "0", "--limit", "1"}, `"status":"queued"`},
	} {
		output.Reset()
		if err := runOperationsClient(ctx, command.args, server.URL, token, nil, &output, nil); err != nil ||
			!json.Valid(output.Bytes()) || !strings.Contains(output.String(), command.want) {
			t.Fatalf("CLI %v output=%s error=%v", command.args, output.String(), err)
		}
	}
	exec("UPDATE cmp_cloud_accounts SET mode='suspended' WHERE tenant_id='tenant' AND account_id='secondary'")
	checkPage(get(path+"?account=secondary", token, http.StatusOK), "same-a")
	exec("UPDATE cmp_memberships SET active=false WHERE tenant_id='tenant' AND project_id='project'")
	get(path+"?cursor="+cursor, token, http.StatusForbidden)
	exec("UPDATE cmp_service_tokens SET revoked_at=now() WHERE tenant_id='tenant'")
	get(path, token, http.StatusUnauthorized)
}
