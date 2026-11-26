package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/provider"
)

func TestAcceptanceTagPolicyAndCompliance(t *testing.T) {
	ctx := t.Context()
	db := dbtest.Open(t)
	if err := migrateCloudManager(ctx, db); err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO cmp_tenants VALUES ('org'),('foreign');
		INSERT INTO cmp_projects VALUES ('org','ops'),('org','private'),('foreign','ops');
		INSERT INTO cmp_principals(tenant_id,principal_id,kind,issuer,subject) VALUES
			('org','admin','service','fixture','admin'),('org','viewer','service','fixture','viewer'),
			('foreign','foreign','service','fixture','foreign');
		INSERT INTO cmp_memberships(tenant_id,project_id,principal_id,role) VALUES
			('org','ops','admin','admin'),('org','private','admin','admin'),
			('org','ops','viewer','viewer'),('foreign','ops','foreign','admin');
		INSERT INTO cmp_cloud_accounts(tenant_id,account_id,project_id,provider,remote_identity,credential_ref,mode) VALUES
			('org','tx','ops','tencent-cvm','10001','fixture-tx','read_only'),
			('org','ali','ops','alibaba-ecs','10002','fixture-ali','read_only'),
			('org','hw','private','huawei-ecs','domain:project','fixture-hw','read_only');`)
	token := func(name string) string { return "test-tag-compliance-credential-" + name }
	for _, name := range []string{"admin", "viewer", "foreign"} {
		hash := sha256.Sum256([]byte(token(name)))
		exec(`INSERT INTO cmp_service_tokens(token_hash,tenant_id,principal_id,expires_at)
			SELECT $1,tenant_id,principal_id,now()+interval '1 hour' FROM cmp_principals WHERE principal_id=$2`, hash[:], name)
	}
	const value = "opaque-cloud-tag-value"
	store := inventory.NewStore(db)
	scan := func(account string, objects ...provider.Object) {
		t.Helper()
		key := inventory.Key{Tenant: "org", Account: account, Region: "region", Kind: provider.Instance}
		claim, err := store.Claim(ctx, key, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if _, done, err := store.ScanPage(ctx, claim, imageDiscovery(func() provider.Page {
			return provider.Page{Objects: objects}
		})); err != nil || !done {
			t.Fatalf("inventory: done=%v err=%v", done, err)
		}
	}
	txGood := provider.Object{ID: "good", Name: "compliant", Tags: map[string]string{"team": value, "exposure": "internal"}}
	empty := provider.Object{ID: "empty", Name: "seen-empty", Tags: map[string]string{}}
	unknown := provider.Object{ID: "unreported", Name: "unreported"}
	blank := provider.Object{ID: "blank", Name: "tag-blank", Tags: map[string]string{"team": "\t  ", "exposure": "internal"}}
	scan("tx", txGood, empty, unknown, blank)
	scan("ali", provider.Object{ID: "ali-old", Tags: map[string]string{"team": value, "exposure": "internal"}})
	scan("hw", provider.Object{ID: "hidden", Name: "hidden-assets", Tags: map[string]string{"team": value, "exposure": "internal"}})
	exec(`UPDATE cmp_inventory_objects SET tag_observed_at=now()-interval '2 hours' WHERE external_id='ali-old'`)

	app, err := newAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Prepare(); err != nil {
		t.Fatal(err)
	}
	request := func(method, path, name, match, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if name != "" {
			req.Header.Set("Authorization", "Bearer "+token(name))
		}
		if match != "" {
			req.Header.Set("If-Match", match)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		app.ServeHTTP(w, req)
		return w
	}
	check := func(method, path, name, match, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		w := request(method, path, name, match, body)
		if w.Code != status || strings.Contains(w.Body.String(), value) || strings.Contains(w.Body.String(), "cmp_inventory") {
			t.Fatalf("%s %s: got %d want %d, %s", method, path, w.Code, status, w.Body)
		}
		return w
	}
	const project = "/v1/tenants/org/projects/ops"
	const policy = project + "/governance/tag-policy"
	const report = project + "/governance/tag-compliance?kind=instance"
	const first = `{"max_age_seconds":3600,"rules":[{"kind":"instance","required_keys":["team","exposure"]}]}`
	const second = `{"max_age_seconds":3600,"rules":[{"kind":"instance","required_keys":["team"]}]}`
	check("GET", policy, "", "", "", 401)
	check("GET", policy, "viewer", "", "", 404)
	check("POST", policy, "viewer", "", first, 403)
	check("POST", policy, "foreign", "", first, 403)
	check("POST", policy, "admin", "", strings.Replace(first, `"exposure"`, `"team"`, 1), 400)
	check("POST", policy, "admin", "", strings.Replace(first, `"max_age_seconds":3600`, `"max_age_seconds":3600,"max_age_seconds":60`, 1), 400)

	server := httptest.NewServer(app)
	defer server.Close()
	var cli bytes.Buffer
	if err := runGovernanceClient(ctx, []string{"policy-create", "org", "ops"}, server.URL, token("admin"), strings.NewReader(first), &cli, nil); err != nil ||
		!strings.Contains(cli.String(), `"revision":1`) {
		t.Fatalf("governance CLI creation: %s %v", cli.String(), err)
	}
	check("POST", policy, "admin", "", first, 409)
	check("PUT", policy, "admin", "", second, 412)
	check("POST", strings.Replace(policy, "/ops/", "/private/", 1), "admin", "", `{"max_age_seconds":3600,"rules":[]}`, 201)
	check("GET", strings.Replace(report, "/ops/", "/private/", 1), "admin", "", "", 404)
	if current := check("GET", policy, "viewer", "", "", 200); current.Header().Get("ETag") != `"1"` {
		t.Fatal("missing strong policy ETag")
	}
	read := func(path string) inventory.TagCompliance {
		t.Helper()
		w := check("GET", path, "viewer", "", "", 200)
		var envelope struct{ Data inventory.TagCompliance }
		if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		return envelope.Data
	}
	all := read(report)
	if len(all.Items) != 5 || all.PolicyRevision != 1 {
		t.Fatalf("wrong project resource set: %+v", all)
	}
	byID := make(map[string]inventory.TagComplianceItem)
	for _, item := range all.Items {
		byID[item.ID] = item
	}
	var keys string
	if err := db.QueryRowContext(ctx, `SELECT array_to_string(tag_keys,',') FROM cmp_inventory_objects WHERE external_id='good'`).Scan(&keys); err != nil ||
		keys != "exposure,team" {
		t.Fatalf("unexpected persisted tag projection: %q %v", keys, err)
	}
	if byID["good"].State != "compliant" || byID["empty"].State != "noncompliant" || len(byID["empty"].MissingKeys) != 2 ||
		byID["unreported"].Reason != "unobserved" || len(byID["unreported"].MissingKeys) != 0 ||
		byID["ali-old"].Reason != "observation_expired" || len(byID["blank"].MissingKeys) != 1 || byID["blank"].MissingKeys[0] != "team" {
		t.Fatalf("wrong classification: %+v", byID)
	}
	firstScope := read(report + "&limit=1")
	if len(firstScope.Items) != 1 || firstScope.Items[0].Account != "ali" || firstScope.Next == "" {
		t.Fatalf("unexpected first scope: %+v", firstScope)
	}
	nextScope := read(report + "&limit=1&cursor=" + url.QueryEscape(firstScope.Next))
	if len(nextScope.Items) != 1 || nextScope.Items[0].Account != "tx" || nextScope.Items[0].ID != "blank" {
		t.Fatalf("cursor skipped another scope: %+v", nextScope)
	}
	exec(`INSERT INTO cmp_cloud_account_grants(tenant_id,account_id,project_id) VALUES ('org','hw','ops')`)
	if shared := read(report + "&account=hw"); len(shared.Items) != 1 || shared.Items[0].ID != "hidden" {
		t.Fatalf("report ignored authorized sharing: %+v", shared)
	}
	exec(`DELETE FROM cmp_cloud_account_grants WHERE (tenant_id,account_id,project_id)=('org','hw','ops')`)
	if removed := read(report + "&account=hw"); len(removed.Items) != 0 {
		t.Fatalf("revoked grant is still visible: %+v", removed)
	}
	paged := read(report + "&state=noncompliant&limit=1")
	if len(paged.Items) != 1 || paged.Next == "" || len(read(report+"&state=noncompliant&cursor="+url.QueryEscape(paged.Next)).Items) != 1 {
		t.Fatalf("invalid report pagination: %+v", paged)
	}
	check("GET", report+"&state=unknown&cursor="+url.QueryEscape(paged.Next), "viewer", "", "", 400)
	check("GET", report+"&account=%00", "viewer", "", "", 400)
	cli.Reset()
	if err := runGovernanceClient(ctx, []string{"compliance", "org", "ops", "instance", "--state", "unknown", "--account", "ali"},
		server.URL, token("viewer"), nil, &cli, nil); err != nil || !strings.Contains(cli.String(), "observation_expired") {
		t.Fatalf("governance CLI report: %s %v", cli.String(), err)
	}

	versions := make(chan int, 2)
	for range 2 {
		go func() { versions <- request("PUT", policy, "admin", `"1"`, second).Code }()
	}
	a, b := <-versions, <-versions
	if !((a == 200 && b == 412) || (a == 412 && b == 200)) {
		t.Fatalf("parallel rule publication returned %d and %d", a, b)
	}
	check("GET", report+"&state=noncompliant&cursor="+url.QueryEscape(paged.Next), "viewer", "", "", 400)
	if old := check("GET", policy+"?revision=1", "viewer", "", "", 200); !strings.Contains(old.Body.String(), "exposure") {
		t.Fatal("the original rule revision was changed")
	}
	exec(`ALTER TABLE cmp_project_audit ADD CONSTRAINT fixture_tag_audit_rollback CHECK (category<>'tag_policy' OR revision<3)`)
	check("PUT", policy, "admin", `"2"`, first, 503)
	if current := check("GET", policy, "viewer", "", "", 200); current.Header().Get("ETag") != `"2"` {
		t.Fatal("a failed audit recorded a tag policy revision")
	}
	events := check("GET", project+"/audit?category=tag_policy", "admin", "", "", 200)
	if strings.Count(events.Body.String(), `"category":"tag_policy"`) != 2 {
		t.Fatalf("wrong publication audit events: %s", events.Body)
	}

	exec(`UPDATE cmp_cloud_accounts SET credential_revision=credential_revision+1 WHERE account_id='tx'`)
	key := inventory.Key{Tenant: "org", Account: "tx", Region: "region", Kind: provider.Instance}
	claim, err := store.Claim(ctx, key, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if items := read(report + "&account=tx&state=unknown").Items; len(items) != 4 || items[0].Reason != "credentials_changed" {
		t.Fatalf("old evidence was reused after a new claim: %+v", items)
	}
	txGood.Tags = nil
	if _, done, err := store.ScanPage(ctx, claim, imageDiscovery(func() provider.Page {
		return provider.Page{Objects: []provider.Object{txGood}}
	})); err != nil || !done {
		t.Fatalf("refresh: done=%v err=%v", done, err)
	}
	fresh := read(report + "&account=tx&state=unknown").Items
	if len(fresh) != 4 || fresh[0].Reason != "missing_candidate" || fresh[2].ID != "good" || fresh[2].Reason != "unobserved" {
		t.Fatalf("failed stale proof invalidation: %+v", fresh)
	}
	exec(`UPDATE cmp_memberships SET active=false WHERE tenant_id='org' AND project_id='ops' AND principal_id='viewer'`)
	check("GET", report, "viewer", "", "", 403)
	check("GET", "/openapi.json", "", "", "", 200)
}
