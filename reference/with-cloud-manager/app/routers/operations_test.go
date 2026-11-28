package routers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/birdordev/plumego/x/openapi"

	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/inventory"
	"with-cloud-manager/internal/operations"
)

func TestAcceptanceNetworkOperationAPI(t *testing.T) {
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
	_, err := db.ExecContext(ctx, `INSERT INTO cmp_tenants VALUES ('tenant');
	    INSERT INTO cmp_projects VALUES ('tenant','project');
	    INSERT INTO cmp_principals (tenant_id,principal_id,kind,issuer,subject) VALUES ('tenant','operator','service','fixture','operator');
	    INSERT INTO cmp_memberships (tenant_id,project_id,principal_id,role) VALUES ('tenant','project','operator','operator');
	    INSERT INTO cmp_cloud_accounts (tenant_id,account_id,project_id,provider,credential_ref,remote_identity,mode)
	    VALUES ('tenant','account','project','aws-ec2','fixture/ref','aws:fixture','write_enabled'),
	        ('tenant','stack','project','openstack','fixture/stack','site:project','write_enabled');`)
	if err != nil {
		t.Fatal(err)
	}
	const token = "fixture-network-api-bearer-token"
	hash := sha256.Sum256([]byte(token))
	if _, err := db.ExecContext(ctx, `INSERT INTO cmp_service_tokens(token_hash,tenant_id,principal_id,expires_at)
	    VALUES ($1,'tenant','operator',now()+interval '5 minutes')`, hash[:]); err != nil {
		t.Fatal(err)
	}
	app, err := NewAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path, match, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		key := sha256.Sum256([]byte(body))
		r.Header.Set("Idempotency-Key", hex.EncodeToString(key[:]))
		if match != "" {
			r.Header.Set("If-Match", match)
		}
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s status=%d want=%d: %s", method, path, w.Code, want, w.Body)
		}
		return w
	}
	const base = "/v1/tenants/tenant/projects/project/network-operations"
	const network = `{"account":"account","region":"us-east-1","kind":"network","name":"private","cidr":"10.8.0.0/16"}`
	const subnet = `{"account":"account","region":"us-east-1","kind":"subnet","name":"private-subnet","cidr":"10.8.2.0/24","parent_id":"vpc-12345678"}`
	const awsGroup = `{"account":"account","region":"us-east-1","kind":"security-group","name":"private-group","parent_id":"vpc-12345678"}`
	const neutronGroup = `{"account":"stack","region":"RegionOne","kind":"security-group","name":"private-group"}`
	request(http.MethodPost, base, "", subnet, http.StatusPreconditionFailed)
	request(http.MethodPost, base, "", awsGroup, http.StatusPreconditionFailed)
	request(http.MethodPost, base, `"2"`, neutronGroup, http.StatusBadRequest)
	request(http.MethodPost, base, "", strings.Replace(awsGroup, `,"parent_id":"vpc-12345678"`, "", 1), http.StatusBadRequest)
	request(http.MethodPost, base, `"2"`, network, http.StatusBadRequest)
	request(http.MethodPost, base, "", strings.Replace(network, `"kind":"network"`, `"kind":"network","kind":"subnet"`, 1), http.StatusBadRequest)
	accepted := request(http.MethodPost, base, "", network, http.StatusAccepted)
	location := accepted.Header().Get("Location")
	if !strings.HasPrefix(location, base+"/") {
		t.Fatalf("unexpected receipt location %q", location)
	}
	current := request(http.MethodGet, location, "", "", http.StatusOK)
	var response struct{ Data operations.Operation }
	if err := json.Unmarshal(current.Body.Bytes(), &response); err != nil || response.Data.ResourceKind != "network" ||
		response.Data.Status != "queued" || response.Data.Action != "create-network" {
		t.Fatalf("invalid network operation response: %s", current.Body)
	}
	request(http.MethodGet, location+"/events", "", "", http.StatusOK)
	group := request(http.MethodPost, base, "", neutronGroup, http.StatusAccepted)
	groupLocation := group.Header().Get("Location")
	if !strings.HasPrefix(groupLocation, base+"/") || groupLocation == location || !strings.Contains(group.Body.String(), `"resource_kind":"security-group"`) {
		t.Fatalf("invalid security group receipt %q: %s", groupLocation, group.Body)
	}
	spec := request(http.MethodGet, "/openapi.json", "", "", http.StatusOK)
	var doc openapi.Document
	if err := json.Unmarshal(spec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	post := doc.Paths["/v1/tenants/{tenant}/projects/{project}/network-operations"].Post
	if post == nil || post.OperationID != "createNetworkResource" || post.Responses["202"].Description == "" || post.Responses["412"].Description == "" {
		t.Fatalf("incomplete network contract: %+v", post)
	}
	if post.RequestBody == nil || post.RequestBody.Content["application/json"].Schema.Properties["rule"].Properties["from_port"].Type != "integer" {
		t.Fatalf("missing critical security-rule OpenAPI inputs: %+v", post.RequestBody)
	}
	deleteRecord := doc.Paths["/v1/tenants/{tenant}/projects/{project}/dns-zones/record-deletion"].Post
	if deleteRecord == nil || deleteRecord.OperationID != "deleteDNSRecord" ||
		deleteRecord.Responses["202"].Description == "" || deleteRecord.Responses["412"].Description == "" ||
		deleteRecord.RequestBody == nil || deleteRecord.RequestBody.Content["application/json"].Schema.Properties["observed_at"].Format != "date-time" {
		t.Fatalf("missing DNS deletion contract: %+v", deleteRecord)
	}
	snapshot := doc.Paths["/v1/tenants/{tenant}/projects/{project}/snapshots"].Post
	if snapshot == nil || snapshot.OperationID != "createVolumeSnapshot" || snapshot.Responses["412"].Description == "" ||
		snapshot.RequestBody == nil || snapshot.RequestBody.Content["application/json"].Schema.Properties["allow_attached"].Type != "boolean" {
		t.Fatalf("missing snapshot creation contract: %+v", snapshot)
	}
	deleteSnapshot := doc.Paths["/v1/tenants/{tenant}/projects/{project}/snapshots/delete"].Post
	if deleteSnapshot == nil || deleteSnapshot.OperationID != "deleteVolumeSnapshot" || deleteSnapshot.Responses["412"].Description == "" ||
		deleteSnapshot.RequestBody == nil || deleteSnapshot.RequestBody.Content["application/json"].Schema.Properties["asset_id"].Type != "string" {
		t.Fatalf("missing snapshot deletion contract: %+v", deleteSnapshot)
	}
	restore := doc.Paths["/v1/tenants/{tenant}/projects/{project}/snapshots/restore"].Post
	if restore == nil || restore.OperationID != "restoreVolumeFromSnapshot" || restore.Responses["412"].Description == "" ||
		restore.RequestBody == nil || restore.RequestBody.Content["application/json"].Schema.Properties["size_gib"].Type != "integer" {
		t.Fatalf("missing snapshot restore contract: %+v", restore)
	}
	deletion := doc.Paths["/v1/tenants/{tenant}/projects/{project}/network-operations/delete"].Post
	if deletion == nil || deletion.OperationID != "deleteNetworkResource" || deletion.Responses["412"].Description == "" ||
		deletion.RequestBody == nil || deletion.RequestBody.Content["application/json"].Schema.Properties["kind"].Type != "string" ||
		deletion.RequestBody.Content["application/json"].Schema.Properties["confirm_rule_removal"].Type != "boolean" {
		t.Fatalf("missing VPC and subnet deletion contract: %+v", deletion)
	}
	revoke := doc.Paths["/v1/tenants/{tenant}/projects/{project}/network-operations/revoke-rule"].Post
	if revoke == nil || revoke.OperationID != "revokeSecurityGroupRule" || revoke.Responses["412"].Description == "" ||
		revoke.RequestBody == nil || revoke.RequestBody.Content["application/json"].Schema.Properties["rule_id"].Type != "string" {
		t.Fatalf("missing revocation contract: %+v", revoke)
	}
	release := doc.Paths["/v1/tenants/{tenant}/projects/{project}/network-operations/release-elastic-ip"].Post
	if release == nil || release.OperationID != "releaseElasticIP" || release.Responses["412"].Description == "" ||
		release.RequestBody == nil || release.RequestBody.Content["application/json"].Schema.Properties["public_address"].Format != "ipv4" {
		t.Fatalf("missing elastic IP release contract: %+v", release)
	}
	for action, operationID := range map[string]string{"bind-elastic-ip": "bindElasticIP", "unbind-elastic-ip": "unbindElasticIP"} {
		binding := doc.Paths["/v1/tenants/{tenant}/projects/{project}/network-operations/"+action].Post
		if binding == nil || binding.OperationID != operationID || binding.Responses["412"].Description == "" ||
			binding.RequestBody == nil || binding.RequestBody.Content["application/json"].Schema.Properties["instance_revision"].Type != "integer" {
			t.Fatalf("missing elastic IP %s contract: %+v", action, binding)
		}
	}
}

func TestAcceptanceInstanceOperationAdmission(t *testing.T) {
	db := dbtest.Open(t)
	ctx := t.Context()
	for _, migrate := range []func() error{
		func() error { return identity.Migrate(ctx, db) }, func() error { return identity.MigrateServiceTokens(ctx, db) },
		func() error { return inventory.Migrate(ctx, db) }, func() error { return operations.Migrate(ctx, db) },
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
		INSERT INTO cmp_projects VALUES ('tenant','source'),('tenant','hidden'),('other','source');
		INSERT INTO cmp_principals (tenant_id,principal_id,kind,issuer,subject) VALUES
			('tenant','operator','service','fixture','operator'),('tenant','reader','service','fixture','reader'),
			('tenant','reviewer','service','fixture','reviewer');
		INSERT INTO cmp_memberships (tenant_id,project_id,principal_id,role) VALUES
			('tenant','source','operator','operator'),('tenant','source','reader','viewer'),('tenant','source','reviewer','approver');
		INSERT INTO cmp_cloud_accounts (tenant_id,account_id,project_id,provider,credential_ref,remote_identity,mode)
			VALUES ('tenant','account','source','aws-ec2','profile/ref','aws:fixture','read_only');
		INSERT INTO cmp_inventory_scopes (tenant_id,account_id,region,kind,credential_revision,lease_until)
			VALUES ('tenant','account','us-east-1','instance',1,now());
		INSERT INTO cmp_inventory_objects (scope_id,external_id,name,status,seen_generation)
			SELECT scope_id,id,id,'stopped',1 FROM cmp_inventory_scopes CROSS JOIN (VALUES ('vm'),('disk'),('spare')) AS v(id);
		INSERT INTO cmp_instance_operation_budgets VALUES ('tenant','source',2);`)
	const token = "fixture-instance-operation-bearer-token"
	const reader = "fixture-instance-readonly-bearer-token"
	const reviewer = "fixture-instance-reviewer-bearer-token"
	for principal, secret := range map[string]string{"operator": token, "reader": reader, "reviewer": reviewer} {
		digest := sha256.Sum256([]byte(secret))
		exec("INSERT INTO cmp_service_tokens(token_hash,tenant_id,principal_id,expires_at) VALUES ($1,'tenant',$2,now()+interval '5 minutes')", digest[:], principal)
	}
	app, err := NewAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	const base = "/v1/tenants/tenant/projects/source/instance-operations"
	const body = `{"account":"account","region":"us-east-1","asset_id":"vm","action":"start"}`
	request := func(method, path, bearer, key, match, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		if match != "" {
			req.Header.Set("If-Match", match)
		}
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		app.ServeHTTP(w, req)
		return w
	}
	expect := func(w *httptest.ResponseRecorder, status int) *httptest.ResponseRecorder {
		t.Helper()
		if w.Code != status {
			t.Fatalf("status %d want %d: %s", w.Code, status, w.Body)
		}
		return w
	}
	post := func(key, match, data string, status int) *httptest.ResponseRecorder {
		t.Helper()
		return expect(request(http.MethodPost, base, token, key, match, data), status)
	}
	expect(request(http.MethodPost, base, "", "first", `"1"`, body), 401)
	expect(request(http.MethodPost, base, reader, "first", `"1"`, body), 403)
	post("first", `"1"`, body, 403)
	exec("UPDATE cmp_cloud_accounts SET mode='write_enabled'")
	post("first", `"1"`, body, 412)
	post("", `"2"`, body, 400)

	// 同一幂等请求并发提交后应共享一份操作和队列记录。
	results := make(chan *httptest.ResponseRecorder, 2)
	start := make(chan struct{})
	for range 2 {
		go func() {
			<-start
			results <- request(http.MethodPost, base, token, "first", `"2"`, body)
		}()
	}
	close(start)
	first := expect(<-results, 202)
	second := expect(<-results, 202)
	location := first.Header().Get("Location")
	if location == "" || location != second.Header().Get("Location") {
		t.Fatal("duplicate submission did not replay the same receipt")
	}
	post("first", `"2"`, strings.Replace(body, "start", "stop", 1), 409)
	post("other-key", `"2"`, body, 409)
	var response struct{ Data operations.Operation }
	if res := expect(request(http.MethodGet, location, reader, "", "", ""), 200); json.Unmarshal(res.Body.Bytes(), &response) != nil ||
		response.Data.ID == "" || response.Data.Status != "queued" || response.Data.AssetID != "vm" {
		t.Fatalf("invalid operation status: %s", res.Body)
	}
	expect(request(http.MethodGet, strings.Replace(location, "/source/", "/hidden/", 1), token, "", "", ""), 403)
	expect(request(http.MethodGet, strings.Replace(location, "/tenant/", "/other/", 1), token, "", "", ""), 403)

	exec("ALTER TABLE cmp_operation_tasks ADD CONSTRAINT fail_new_queue CHECK (topic<>'cmp.instance.v1') NOT VALID")
	next := strings.Replace(body, `"vm"`, `"disk"`, 1)
	post("queued-proof", `"2"`, next, 503)
	assertCounts := func(want int) {
		t.Helper()
		var records, events, tasks int
		err := db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM cmp_instance_operations),
			(SELECT count(*) FROM cmp_instance_operation_events),(SELECT count(*) FROM cmp_operation_tasks)`).Scan(&records, &events, &tasks)
		if err != nil || records != want || events != want || tasks != want {
			t.Fatalf("operations=%d events=%d tasks=%d expected=%d error=%v", records, events, tasks, want, err)
		}
	}
	assertCounts(1)
	exec("ALTER TABLE cmp_operation_tasks DROP CONSTRAINT fail_new_queue")
	post("queued-proof", `"2"`, next, 202)
	post("quota-proof", `"2"`, strings.Replace(body, `"vm"`, `"spare"`, 1), 429)
	post("first", `"2"`, body, 202)
	exec("UPDATE cmp_memberships SET active=false WHERE principal_id='operator'")
	post("first", `"2"`, body, 403)
	assertCounts(2)
	exec("UPDATE cmp_memberships SET active=true,role='approver' WHERE principal_id='operator'")
	decision := `{"decision":"succeeded","evidence_ref":"provider-log/fixture","provider_request_id":"req-cloud-123"}`
	reviewPath := location + "/resolution"
	review := func(bearer, data string, status int) *httptest.ResponseRecorder {
		t.Helper()
		return expect(request(http.MethodPost, reviewPath, bearer, "", "", data), status)
	}
	review(reviewer, decision, 409)
	exec("UPDATE cmp_instance_operations SET status='needs_review',provider_request_id='req-cloud-123' WHERE operation_id=$1", response.Data.ID)
	exec("UPDATE cmp_operation_tasks SET status='done' WHERE id=$1", response.Data.ID)
	review(token, decision, 403)
	review(reader, decision, 403)
	review(reviewer, strings.Replace(decision, "req-cloud-123", "wrong", 1), 409)
	expect(request(http.MethodPost, strings.Replace(reviewPath, "/tenant/", "/other/", 1), reviewer, "", "", decision), 403)
	exec("ALTER TABLE cmp_instance_operation_events ADD CONSTRAINT fail_review CHECK (status<>'succeeded') NOT VALID")
	review(reviewer, decision, 503)
	if response := expect(request(http.MethodGet, location, reader, "", "", ""), 200); !strings.Contains(response.Body.String(), `"status":"needs_review"`) {
		t.Fatal("faulted review did not roll back operation")
	}
	exec("ALTER TABLE cmp_instance_operation_events DROP CONSTRAINT fail_review")
	if response := review(reviewer, decision, 200); !strings.Contains(response.Body.String(), `"outcome_reason":"manual_review"`) {
		t.Fatal("review outcome is missing")
	}
	review(reviewer, decision, 200)
	review(reviewer, strings.Replace(decision, "succeeded", "failed", 1), 409)
	var reviews int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM cmp_instance_operation_resolutions").Scan(&reviews); err != nil || reviews != 1 {
		t.Fatalf("review count=%d error=%v", reviews, err)
	}
	exec("UPDATE cmp_memberships SET role='operator' WHERE principal_id='operator'")
	post("after-review", `"2"`, body, 202)
	exec("UPDATE cmp_instance_operations SET status='succeeded' WHERE status='queued'")
	exec("UPDATE cmp_operation_tasks SET status='done' WHERE status='queued'")
	exec("INSERT INTO cmp_instance_approval_rules VALUES ('tenant','source','stop')")
	stopBody := strings.Replace(body, `"start"`, `"stop"`, 1)
	pending := post("needs-approval", `"2"`, stopBody, 202)
	pendingLocation := pending.Header().Get("Location")
	if !strings.Contains(pending.Body.String(), `"status":"awaiting_approval"`) || pendingLocation == "" {
		t.Fatal("review rule did not intercept the request")
	}
	if replay := post("needs-approval", `"2"`, stopBody, 202); replay.Header().Get("Location") != pendingLocation {
		t.Fatal("review admission changed the original operation")
	}
	countReady := func(want int) {
		t.Helper()
		var count int
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM cmp_operation_tasks WHERE status='queued'").Scan(&count); err != nil || count != want {
			t.Fatalf("queued tasks=%d want=%d error=%v", count, want, err)
		}
	}
	countReady(0)
	post("blocked-by-review", `"2"`, body, 409)
	approve := func(path, bearer, decision string, status int) *httptest.ResponseRecorder {
		t.Helper()
		return expect(request(http.MethodPost, path+"/approval", bearer, "", "", `{"decision":"`+decision+`"}`), status)
	}
	exec("UPDATE cmp_memberships SET role='approver' WHERE principal_id='operator'")
	approve(pendingLocation, token, "approve", 403)
	exec("UPDATE cmp_memberships SET role='operator' WHERE principal_id='operator'")
	approve(pendingLocation, reviewer, "approve", 412)
	approve(pendingLocation, reviewer, "reject", 200)
	countReady(0)
	eventsPath := pendingLocation + "/events"
	firstEvents := expect(request(http.MethodGet, eventsPath+"?limit=1", reader, "", "", ""), 200)
	var eventPage struct{ Data operations.EventPage }
	if err := json.Unmarshal(firstEvents.Body.Bytes(), &eventPage); err != nil || !eventPage.Data.HasMore ||
		len(eventPage.Data.Events) != 1 || eventPage.Data.Events[0].Status != "awaiting_approval" {
		t.Fatalf("operation event page: %s, %v", firstEvents.Body, err)
	}
	after := strconv.FormatInt(eventPage.Data.NextAfter, 10)
	lastEvents := expect(request(http.MethodGet, eventsPath+"?after="+after, reader, "", "", ""), 200)
	if err := json.Unmarshal(lastEvents.Body.Bytes(), &eventPage); err != nil || eventPage.Data.HasMore ||
		len(eventPage.Data.Events) != 1 || eventPage.Data.Events[0].Status != "failed" {
		t.Fatalf("operation event continuation: %s, %v", lastEvents.Body, err)
	}
	expect(request(http.MethodGet, eventsPath+"?after=0&after=1", reader, "", "", ""), 400)
	expect(request(http.MethodGet, strings.Replace(eventsPath, "/source/", "/hidden/", 1), reader, "", "", ""), 403)

	fresh := post("fresh-approval", `"2"`, stopBody, 202).Header().Get("Location")
	exec("ALTER TABLE cmp_operation_tasks ADD CONSTRAINT reject_approved_queue CHECK (topic<>'cmp.instance.v1') NOT VALID")
	approve(fresh, reviewer, "approve", 503)
	if result := expect(request(http.MethodGet, fresh, reader, "", "", ""), 200); !strings.Contains(result.Body.String(), `"status":"awaiting_approval"`) {
		t.Fatal("failed enqueue did not roll back approval")
	}
	exec("ALTER TABLE cmp_operation_tasks DROP CONSTRAINT reject_approved_queue")
	if result := approve(fresh, reviewer, "approve", 200); !strings.Contains(result.Body.String(), `"status":"queued"`) {
		t.Fatal("approval did not queue the operation")
	}
	approve(fresh, reviewer, "approve", 200)
	approve(fresh, reviewer, "reject", 409)
	countReady(1)

	const previewPath = base + "/deletion-preview?account=account&region=us-east-1&asset_id=spare"
	const deleteBody = `{"account":"account","region":"us-east-1","asset_id":"spare","action":"delete"}`
	const protect = `INSERT INTO cmp_protected_instances (scope_id,external_id)
		SELECT scope_id,external_id FROM cmp_inventory_objects WHERE external_id='spare'`
	exec(`UPDATE cmp_inventory_objects SET links='[{"kind":"volume","id":"disk"},{"kind":"volume","id":"second-disk"}]' WHERE external_id='spare'`)
	exec(protect)
	expect(request(http.MethodGet, previewPath, reader, "", "", ""), 403)
	expect(request(http.MethodGet, previewPath+"&asset_id=vm", token, "", "", ""), 400)
	preview := expect(request(http.MethodGet, previewPath, token, "", "", ""), 200)
	var deletion struct{ Data operations.DeletionPreview }
	if err := json.Unmarshal(preview.Body.Bytes(), &deletion); err != nil || !deletion.Data.Protected || !deletion.Data.ApprovalRequired ||
		deletion.Data.KnownVolumeLinks != 2 || deletion.Data.OwnershipRevision != 2 {
		t.Fatalf("invalid deletion preview: %s, %v", preview.Body, err)
	}
	post("mandatory-approval", `"2"`, deleteBody, 409)
	exec("DELETE FROM cmp_protected_instances")
	post("mandatory-approval", `"1"`, deleteBody, 412)
	deleting := post("mandatory-approval", `"2"`, deleteBody, 202)
	if !strings.Contains(deleting.Body.String(), `"status":"awaiting_approval"`) {
		t.Fatalf("unapproved deletion: %s", deleting.Body)
	}
	deletionLocation := deleting.Header().Get("Location")
	countReady(1)
	exec(protect)
	approve(deletionLocation, reviewer, "approve", 409)
	countReady(1)
	exec("DELETE FROM cmp_protected_instances")
	approve(deletionLocation, reviewer, "approve", 200)
	countReady(2)

	const instances = "/v1/tenants/tenant/projects/source/instances"
	const create = `{"account":"account","region":"us-east-1","name":"batch worker","image_id":"ami-01234567","size":"t3.micro","network_id":"subnet-01234567","security_group_id":"sg-01234567"}`
	exec(`UPDATE cmp_instance_operations SET status='failed' WHERE status='queued'; UPDATE cmp_operation_tasks SET status='done' WHERE status='queued';
		INSERT INTO cmp_inventory_scopes (tenant_id,account_id,region,kind,credential_revision,lease_until)
		SELECT 'tenant','account','us-east-1',kind,2,now() FROM (VALUES ('image'),('subnet'),('security-group')) AS v(kind);
		INSERT INTO cmp_inventory_objects (scope_id,external_id,name,status,seen_generation)
		SELECT scope_id,asset,asset,'available',1 FROM cmp_inventory_scopes AS s
		JOIN (VALUES ('image','ami-01234567'),('subnet','subnet-01234567'),('security-group','sg-01234567')) AS v(kind,asset) USING (kind);
		INSERT INTO cmp_instance_offerings VALUES ('tenant','account','us-east-1','t3.micro');
		UPDATE cmp_inventory_objects SET links='[{"kind":"network","relation":"in_network","id":"vpc-one"}]'
		WHERE external_id IN ('sg-01234567','subnet-01234567');
		UPDATE cmp_instance_operation_budgets SET max_managed_instances=1;`)
	expect(request(http.MethodPost, instances, token, "", "", create), 400)
	expect(request(http.MethodPost, instances, reader, "launch", "", create), 403)
	expect(request(http.MethodPost, instances, token, "launch", "", strings.Replace(create, "ami-01234567", "ami-01234568", 1)), 403)
	expect(request(http.MethodPost, instances, token, "launch", "", strings.Replace(create, "t3.micro", "t3.small", 1)), 400)
	exec(`UPDATE cmp_inventory_objects SET links='[{"kind":"network","relation":"in_network","id":"vpc-other"}]' WHERE external_id='sg-01234567'`)
	expect(request(http.MethodPost, instances, token, "launch", "", create), 403)
	exec(`UPDATE cmp_inventory_objects SET links='[{"kind":"network","relation":"in_network","id":"vpc-one"}]' WHERE external_id='sg-01234567'`)
	for range 2 {
		go func() { results <- request(http.MethodPost, instances, token, "launch", "", create) }()
	}
	launch := expect(<-results, 202)
	launchLocation := launch.Header().Get("Location")
	if !strings.HasPrefix(launchLocation, base+"/") || !strings.Contains(launch.Body.String(), `"action":"create"`) {
		t.Fatalf("invalid launch receipt %s %s", launchLocation, launch.Body)
	}
	if duplicate := expect(<-results, 202); duplicate.Header().Get("Location") != launchLocation {
		t.Fatal("concurrent creation submitted a second launch")
	}
	if repeat := expect(request(http.MethodPost, instances, token, "launch", "", strings.Replace(create, "batch worker", " batch worker ", 1)), 202); repeat.Header().Get("Location") != launchLocation {
		t.Fatal("normalized request failed to replay original launch")
	}
	expect(request(http.MethodPost, instances, token, "launch", "", strings.Replace(create, "t3.micro", "t3.small", 1)), 409)
	expect(request(http.MethodPost, instances, token, "quota", "", create), 429)
	expect(request(http.MethodGet, launchLocation, reader, "", "", ""), 200)
	countReady(1)

	exec(`INSERT INTO cmp_inventory_scopes (tenant_id,account_id,region,kind,credential_revision,lease_until)
		VALUES ('tenant','account','us-east-1','volume',2,now());
		INSERT INTO cmp_inventory_objects (scope_id,external_id,name,status,seen_generation)
		SELECT scope_id,'disk-volume','disk','available',1 FROM cmp_inventory_scopes WHERE kind='volume';`)
	const detach = `{"account":"account","region":"us-east-1","asset_id":"spare","action":"detach-volume","volume_id":"disk-volume","volume_revision":2,"guest_unmounted":true}`
	post("missing-unmount", `"2"`, strings.Replace(detach, `"guest_unmounted":true`, `"guest_unmounted":false`, 1), 400)
	if stale := post("stale-volume", `"2"`, strings.Replace(detach, `"volume_revision":2`, `"volume_revision":1`, 1), 412); !strings.Contains(stale.Body.String(), "current volume ownership revision required") {
		t.Fatalf("incorrect stale volume error: %s", stale.Body)
	}
	responseDetach := post("detach", `"2"`, detach, 202)
	if !strings.Contains(responseDetach.Body.String(), `"status":"awaiting_approval"`) || !strings.Contains(responseDetach.Body.String(), `"volume_id":"disk-volume"`) {
		t.Fatalf("invalid detach receipt: %s", responseDetach.Body)
	}
	countReady(1)
}
