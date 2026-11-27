package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/operations"
	"with-cloud-manager/internal/provider"
	"with-cloud-manager/internal/provider/redfish"
)

func TestAcceptanceRedfishPower(t *testing.T) {
	ctx, db := t.Context(), dbtest.Open(t)
	seedInventoryProviderProject(t, db)
	const reviewer = "fixture-redfish-independent-reviewer-token"
	const ref = "fixture/bmc-power"
	if _, err := db.ExecContext(ctx, `INSERT INTO cmp_operation_event_sinks(sink_id,tenant_id,project_id,endpoint_hash)
		VALUES ('redfish-events','tenant','project',decode(repeat('00',32),'hex'))`); err != nil {
		t.Fatal(err)
	}
	reviewerHash := sha256.Sum256([]byte(reviewer))
	if _, err := db.ExecContext(ctx, `INSERT INTO cmp_principals(tenant_id,principal_id,kind,issuer,subject)
		VALUES ('tenant','reviewer','service','fixture','reviewer');
		INSERT INTO cmp_memberships(tenant_id,project_id,principal_id,role) VALUES ('tenant','project','reviewer','approver')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO cmp_service_tokens(tenant_id,principal_id,token_hash,expires_at)
		VALUES ('tenant','reviewer',$1,now()+interval '10 minutes')`, reviewerHash[:]); err != nil {
		t.Fatal(err)
	}
	fixture := &redfishPowerFixture{revokeApproval: func() error {
		_, err := db.ExecContext(ctx, `UPDATE cmp_memberships SET revision=revision+1
			WHERE (tenant_id,project_id,principal_id)=('tenant','project','reviewer')`)
		return err
	}}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fixture.serve(t, w, r) }))
	defer server.Close()
	profile := redfishProfile{Ref: ref, Config: redfish.Config{Endpoint: server.URL, Site: "lab", UUID: powerBMC,
		User: "user", Password: "fake-password", CAPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}))}}
	path := filepath.Join(t.TempDir(), "profile.json")
	saveProfile := func() {
		t.Helper()
		data, err := json.Marshal(profile)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	saveProfile()
	app, err := newAPI(db, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	const base = "/v1/tenants/tenant/projects/"
	const project = "project"
	const actor = vSphereFixtureBearer
	call := func(p, uri, token, verb, match, key, input string, code int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequestWithContext(ctx, verb, base+p+uri, strings.NewReader(input))
		r.Header.Set("Authorization", "Bearer "+token)
		if input != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		if match != "" {
			r.Header.Set("If-Match", match)
		}
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if w.Code != code || strings.Contains(w.Body.String(), "fake-password") {
			t.Fatalf("%s %s got %d, want %d: %s", verb, uri, w.Code, code, w.Body.String())
		}
		return w
	}
	call(project, "/cloud-accounts", actor, "POST", "", "", `{"id":"bmc","provider":"redfish","credential_ref":"`+ref+`"}`, 201)
	if err := runRedfish(ctx, db, []string{"verify-redfish", "tenant", "bmc"}, path, nil); err != nil {
		t.Fatal(err)
	}
	scan := func() {
		t.Helper()
		if err := runRedfish(ctx, db, []string{"scan-redfish", "tenant", "bmc", "lab", "bare-metal"}, path, nil); err != nil {
			t.Fatal(err)
		}
	}
	scan()
	revision := `"1"`
	submitAt := func(route, key, action string, status int) operations.Operation {
		t.Helper()
		w := call(project, route, actor, "POST", revision, key,
			`{"account":"bmc","region":"lab","asset_id":"`+powerSystem+`","action":"`+action+`"}`, status)
		var op operations.Operation
		if status == http.StatusAccepted {
			decodeProjectData(t, w, &op)
			if w.Header().Get("Location") != base+project+"/operations/"+op.ID {
				t.Fatal("incorrect operation receipt location")
			}
		}
		return op
	}
	submit := func(key, action string, status int) operations.Operation {
		t.Helper()
		return submitAt("/bare-metal/power", key, action, status)
	}
	submit("disabled", "power-on-bare-metal", 403)
	account := call(project, "/cloud-accounts/bmc", actor, "GET", "", "", "", 200)
	enabled := call(project, "/cloud-accounts/bmc", actor, "PATCH", account.Header().Get("ETag"), "",
		`{"action":"enable-bare-metal-power"}`, 200)
	revision = enabled.Header().Get("ETag")
	submit("stale", "power-on-bare-metal", 412)
	scan()
	op := submit("power-on", "power-on-bare-metal", 202)
	if op.ID == "" || op.Status != "awaiting_approval" || op.ResourceKind != provider.BareMetal {
		t.Fatalf("unexpected receipt: %+v", op)
	}
	api := httptest.NewServer(app)
	defer api.Close()
	var replay bytes.Buffer
	args := []string{"bare-metal-power", "tenant", project, "--idempotency-key", "power-on", "--if-match", strings.Trim(revision, `"`)}
	body := strings.NewReader(`{"account":"bmc","region":"lab","asset_id":"` + powerSystem + `","action":"power-on-bare-metal"}`)
	if err := runOperationsClient(ctx, args, api.URL, actor, body, &replay, nil); err != nil {
		t.Fatal(err)
	}
	var prior operations.Operation
	if err := json.Unmarshal(replay.Bytes(), &prior); err != nil || prior.ID != op.ID {
		t.Fatalf("CLI replay did not return the original operation: %v", err)
	}
	submit("power-on", "shutdown-bare-metal", 409)
	submit("concurrent", "shutdown-bare-metal", 409)
	call("other", "/operations/"+op.ID, actor, "GET", "", "", "", 403)
	call(project, "/operations/"+op.ID+"/approval", actor, "POST", "", "", `{"decision":"approve"}`, 403)
	store := operations.NewStore(db)
	scope := identity.Scope{Tenant: "tenant", Project: project}
	approve := func(op operations.Operation) {
		t.Helper()
		if _, err := store.Approve(ctx, reviewer, scope, op.ID, operations.Approval{Decision: "approve"}); err != nil {
			t.Fatal(err)
		}
	}
	worker, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- store.RunWorker(worker, func(ctx context.Context, binding identity.CloudAccountBinding) (provider.InstanceOperator, error) {
			return connectOperator(ctx, binding, path)
		})
	}()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("worker did not stop")
		}
	}()
	wait := func(op operations.Operation, status string) operations.Operation {
		t.Helper()
		return waitFixturePower(t, db, store, scope, actor, op.ID, status, true)
	}
	approve(op)
	if got := wait(op, "failed"); got.OutcomeReason != "provider_power_disabled" || fixture.writes.Load() != 0 {
		t.Fatalf("disabled worker wrote power state: %+v", got)
	}
	profile.AllowPower = true
	saveProfile()
	op = submit("wrong-target", "power-on-bare-metal", 202)
	fixture.unsafeTarget.Store(true)
	approve(op)
	if got := wait(op, "failed"); got.OutcomeReason != "power_target_unconfirmed" || fixture.writes.Load() != 0 {
		t.Fatalf("wrong action target reached write: %+v", got)
	}
	fixture.unsafeTarget.Store(false)
	op = submit("revoked-approval", "power-on-bare-metal", 202)
	fixture.revoke.Store(true)
	approve(op)
	if got := wait(op, "failed"); got.OutcomeReason != "dispatch_unavailable" || fixture.writes.Load() != 0 {
		t.Fatalf("revoked approval reached write: %+v", got)
	}
	op = submit("lost-receipt", "power-on-bare-metal", 202)
	fixture.drop.Store(true)
	approve(op)
	wait(op, "succeeded")
	if !fixture.on.Load() || fixture.writes.Load() != 1 {
		t.Fatal("lost receipt was not recovered through a single write")
	}
	var events int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM cmp_operation_event_outbox AS b
		JOIN cmp_instance_operation_events AS e ON b.sequence=e.event_id
		WHERE e.operation_id=$1 AND b.event_type='bare-metal.operation.succeeded'`, op.ID).Scan(&events); err != nil || events != 1 {
		t.Fatalf("wrong succeeded outbox count %d: %v", events, err)
	}
	op = submit("graceful-shutdown", "shutdown-bare-metal", 202)
	approve(op)
	wait(op, "succeeded")
	if fixture.on.Load() || fixture.writes.Load() != 2 {
		t.Fatal("expected one graceful shutdown request")
	}

	op = submit("resume-observation", "power-on-bare-metal", 202)
	fixture.pending.Store(true)
	approve(op)
	wait(op, "observing")
	if _, err := db.ExecContext(ctx, `UPDATE cmp_operation_tasks SET max_attempts=2 WHERE id=$1`, op.ID); err != nil {
		t.Fatal(err)
	}
	wait(op, "needs_review")
	if fixture.writes.Load() != 3 || fixture.on.Load() {
		t.Fatal("an unconfirmed operation was resent or reported as on")
	}
	retryPath := "/operations/" + op.ID + "/retry-observation"
	// 等待终态的队列回执提交，再由独立审批人复开只读观察。
	deadline, stop := context.WithTimeout(ctx, 3*time.Second)
	defer stop()
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		var queueState string
		err := db.QueryRowContext(deadline, "SELECT status FROM cmp_operation_tasks WHERE id=$1", op.ID).Scan(&queueState)
		if err != nil {
			t.Fatal(err)
		}
		if queueState == "done" {
			break
		}
		select {
		case <-deadline.Done():
			t.Fatal("missing queue receipt")
		case <-tick.C:
		}
	}
	call(project, "/operations/"+op.ID+"/resolution", reviewer, "POST", "", "",
		`{"decision":"succeeded","evidence_ref":"manual"}`, 409)
	fixture.on.Store(true)
	call(project, retryPath, reviewer, "POST", "", "", `{"evidence_ref":"redfish-case-1"}`, 200)
	wait(op, "succeeded")
	if fixture.writes.Load() != 3 {
		t.Fatal("review retry dispatched another BMC operation")
	}
	checkRedfishPXE(t, db, fixture, func() {
		profile.AllowPXE = true
		saveProfile()
	}, func(key, action string, status int) operations.Operation {
		return submitAt("/bare-metal/pxe", key, action, status)
	}, approve, wait)
	dial := func(args []string, token, input string, result any) error {
		var output bytes.Buffer
		if err := runOperationsClient(ctx, args, api.URL, token, strings.NewReader(input), &output, nil); err != nil {
			return err
		}
		return json.Unmarshal(output.Bytes(), result)
	}
	checkBareMetalDeployment(t, db, fixture, reviewer, dial, func(project, uri, token, verb, input string, status int) *httptest.ResponseRecorder {
		return call(project, uri, token, verb, "", "", input, status)
	}, submitAt, approve, wait)
	checkBareMetalReclaim(t, db, fixture, reviewer, revision, dial, call, scan)
	current := call(project, "/cloud-accounts/bmc", actor, "GET", "", "", "", 200)
	call(project, "/cloud-accounts/bmc", actor, "PATCH", current.Header().Get("ETag"), "",
		`{"action":"disable-bare-metal-power"}`, 200)
	submit("turned-off", "shutdown-bare-metal", 403)
}
