package main

import (
	"database/sql"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"with-cloud-manager/internal/operations"
)

func checkBareMetalReclaim(t *testing.T, db *sql.DB, fixture *redfishPowerFixture, reviewer, revision string,
	dial func([]string, string, string, any) error,
	call func(string, string, string, string, string, string, string, int) *httptest.ResponseRecorder, scan func()) {
	t.Helper()
	const project, actor = "project", vSphereFixtureBearer
	const site = "fixture-redfish-site-installer-token"
	const collection = "/bare-metal/reclaims"
	const transfer = `{"account":"bmc","region":"lab","kind":"bare-metal","asset_id":"` + powerSystem + `","target_project":"other"}`
	const input = `{"account":"bmc","region":"lab","system_id":"` + powerSystem + `","sanitizer_id":"installer"}`
	const claim = `{"system_id":"` + powerSystem + `"}`
	receipt := `{"system_id":"` + powerSystem + `","evidence_sha256":"` + strings.Repeat("c", 64) + `"}`
	call(project, "/assets/ownership", actor, "PATCH", revision, "", transfer, 409)
	fixture.on.Store(true)
	scan()
	call(project, collection, actor, "POST", revision, "powered-on", input, 409)
	fixture.on.Store(false)
	scan()
	current := collection + "/current?account=bmc&region=lab&system_id=" + powerSystem
	currentArgs := []string{"current-reclaim", "tenant", project, "--account", "bmc", "--region", "lab", "--system-id", powerSystem}
	var view operations.BareMetalReclaimView
	if err := dial(currentArgs, actor, "", &view); err != nil || view.Reclaim != nil {
		t.Fatalf("expected empty current reclaim: %+v %v", view, err)
	}
	call(project, current+"&account=bmc", actor, "GET", "", "", "", 400)
	call(project, collection, actor, "POST", "", "reclaim-1", input, 412)
	req := func(key string) operations.BareMetalReclaim {
		t.Helper()
		var result operations.BareMetalReclaim
		args := []string{"request-reclaim", "tenant", project, "--idempotency-key", key, "--if-match", strings.Trim(revision, `"`)}
		if err := dial(args, actor, input, &result); err != nil || result.State != "requested" {
			t.Fatalf("reclaim request: %+v %v", result, err)
		}
		return result
	}
	r := req("reclaim-1")
	view = operations.BareMetalReclaimView{}
	decodeProjectData(t, call(project, current, reviewer, "GET", "", "", "", 200), &view)
	if view.Reclaim == nil || view.Reclaim.ID != r.ID || view.Reclaim.State != "requested" {
		t.Fatalf("reviewer cannot discover current reclaim: %+v", view.Reclaim)
	}
	call("other", current, actor, "GET", "", "", "", 403)
	if replay := req("reclaim-1"); replay.ID != r.ID {
		t.Fatal("reclaim replay changed identity")
	}
	path := collection + "/" + r.ID
	call("other", path, actor, "GET", "", "", "", 403)
	call(project, path+"/approval", actor, "POST", "", "", `{"decision":"approve"}`, 403)
	call(project, path+"/claim", site, "POST", "", "", claim, 409)
	call(project, path+"/approval", reviewer, "POST", "", "", `{"decision":"approve"}`, 200)
	if _, err := db.ExecContext(t.Context(), `UPDATE cmp_memberships SET revision=revision+1
		WHERE (tenant_id,project_id,principal_id)=('tenant','project','installer')`); err != nil {
		t.Fatal(err)
	}
	call(project, path+"/claim", site, "POST", "", "", claim, 412)
	call(project, path+"/approval", reviewer, "POST", "", "", `{"decision":"reject"}`, 200)
	r = req("reclaim-2")
	path = collection + "/" + r.ID
	call(project, "/bare-metal/pxe", actor, "POST", revision, "reclaim-blocks-pxe",
		`{"account":"bmc","region":"lab","asset_id":"`+powerSystem+`","action":"stage-bare-metal-pxe"}`, 409)
	call(project, path+"/approval", reviewer, "POST", "", "", `{"decision":"approve"}`, 200)
	call(project, path+"/claim", actor, "POST", "", "", claim, 403)
	checkConcurrentReclaimClaims(t, dial, r.ID, site, claim)
	call(project, path+"/approval", reviewer, "POST", "", "", `{"decision":"reject"}`, 409)
	call(project, path+"/receipt", site, "POST", "", "", receipt, 409)
	scan()
	call(project, path+"/receipt", site, "POST", "", "", strings.Replace(receipt, powerSystem, powerBMC, 1), 409)
	var verified operations.BareMetalReclaim
	for i := 0; i < 2; i++ {
		if err := dial([]string{"report-reclaim", "tenant", project, r.ID}, site, receipt, &verified); err != nil || verified.State != "verified" {
			t.Fatalf("reclaim receipt: %+v %v", verified, err)
		}
	}
	call(project, path+"/receipt", site, "POST", "", "", strings.Replace(receipt, strings.Repeat("c", 64), strings.Repeat("d", 64), 1), 409)
	if _, err := db.ExecContext(t.Context(), `ALTER TABLE cmp_asset_ownership_events ADD CONSTRAINT fixture_reject_transfer CHECK (to_project<>'other')`); err != nil {
		t.Fatal(err)
	}
	call(project, "/assets/ownership", actor, "PATCH", revision, "", transfer, 503)
	state := func(want string) {
		t.Helper()
		var result operations.BareMetalReclaim
		if err := dial([]string{"get-reclaim", "tenant", project, r.ID}, actor, "", &result); err != nil || result.State != want {
			t.Fatalf("reclaim state: %+v %v, want %s", result, err, want)
		}
	}
	state("verified")
	if _, err := db.ExecContext(t.Context(), `ALTER TABLE cmp_asset_ownership_events DROP CONSTRAINT fixture_reject_transfer`); err != nil {
		t.Fatal(err)
	}
	call(project, "/assets/ownership", actor, "PATCH", revision, "", transfer, 200)
	state("consumed")
	var audits int
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM cmp_project_audit
		WHERE subject_id=$1 AND action LIKE 'bare_metal_reclaim_%'`, r.ID).Scan(&audits); err != nil || audits != 5 {
		t.Fatalf("expected five audited reclaim transitions, got %d: %v", audits, err)
	}
	var ownership int64
	if err := db.QueryRowContext(t.Context(), `SELECT revision FROM cmp_asset_ownership WHERE external_id=$1 AND project_id='other'`, powerSystem).
		Scan(&ownership); err != nil || strconv.FormatInt(ownership, 10) == strings.Trim(revision, `"`) {
		t.Fatalf("unexpected final ownership revision %d: %v", ownership, err)
	}
	back := strings.Replace(transfer, `"target_project":"other"`, `"target_project":"project"`, 1)
	otherRevision := strconv.Quote(strconv.FormatInt(ownership, 10))
	call("other", "/assets/ownership", actor, "PATCH", otherRevision, "", back, 409)
	checkBareMetalReclaimRecovery(t, db, reviewer, site, otherRevision, input, claim, receipt, back, dial, call, scan)
}

func checkConcurrentReclaimClaims(t *testing.T, dial func([]string, string, string, any) error, id, token, body string) {
	t.Helper()
	const total = 8
	results := make(chan error, total)
	for range total {
		go func() {
			var result operations.BareMetalReclaim
			results <- dial([]string{"claim-reclaim", "tenant", "project", id}, token, body, &result)
		}()
	}
	var accepted int
	for range total {
		if err := <-results; err == nil {
			accepted++
		} else if !strings.Contains(err.Error(), "HTTP 409") {
			t.Errorf("unexpected claim error: %v", err)
		}
	}
	if accepted != 1 {
		t.Fatalf("accepted %d concurrent claim requests, want one", accepted)
	}
}
