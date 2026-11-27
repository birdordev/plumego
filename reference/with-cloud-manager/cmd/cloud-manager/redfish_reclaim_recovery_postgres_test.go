package main

import (
	"database/sql"
	"net/http/httptest"
	"strings"
	"testing"

	"with-cloud-manager/internal/operations"
)

func checkBareMetalReclaimRecovery(t *testing.T, db *sql.DB, reviewer, site, revision, input, claim, receipt, transfer string,
	dial func([]string, string, string, any) error,
	call func(string, string, string, string, string, string, string, int) *httptest.ResponseRecorder, scan func()) {
	t.Helper()
	apply := func(query string) {
		t.Helper()
		if _, err := db.ExecContext(t.Context(), query); err != nil {
			t.Fatal(err)
		}
	}
	apply(`INSERT INTO cmp_cloud_account_grants(tenant_id,account_id,project_id) VALUES ('tenant','bmc','other');
		INSERT INTO cmp_memberships(tenant_id,project_id,principal_id,role)
		VALUES ('tenant','other','installer','viewer'),('tenant','other','reviewer','approver')`)
	const actor = vSphereFixtureBearer
	const collection = "/bare-metal/reclaims"
	current := collection + "/current?account=bmc&region=lab&system_id=" + powerSystem
	var view operations.BareMetalReclaimView
	decodeProjectData(t, call("other", current, actor, "GET", "", "", "", 200), &view)
	if view.Reclaim != nil {
		t.Fatalf("new owner received consumed reclaim: %+v", view.Reclaim)
	}
	var reclaim operations.BareMetalReclaim
	decodeProjectData(t, call("other", collection, actor, "POST", revision, "recovery-fixture", input, 202), &reclaim)
	path := collection + "/" + reclaim.ID
	post := func(suffix, token, body string, status int) {
		t.Helper()
		call("other", path+suffix, token, "POST", "", "", body, status)
	}
	proposal := func(id string) string {
		return `{"recovery_id":"` + id + `","system_id":"` + powerSystem + `","isolation_sha256":"` +
			strings.Repeat("e", 64) + `","evidence_sha256":"` + strings.Repeat("f", 64) + `"}`
	}
	decision := func(id, decision string) string {
		return `{"recovery_id":"` + id + `","decision":"` + decision + `"}`
	}
	post("/approval", reviewer, `{"decision":"approve"}`, 200)
	post("/claim", site, claim, 200)
	post("/recovery", actor, proposal("recover-1"), 409)
	apply(`UPDATE cmp_memberships SET revision=revision+1 WHERE (tenant_id,project_id,principal_id)=('tenant','other','installer')`)
	scan()
	post("/receipt", site, receipt, 412)
	post("/recovery", site, proposal("recover-1"), 403)
	call("project", path+"/recovery", actor, "POST", "", "", proposal("recover-1"), 403)
	post("/recovery", actor, proposal("recover-1"), 200)
	post("/recovery", actor, strings.Replace(proposal("recover-1"), strings.Repeat("f", 64), strings.Repeat("a", 64), 1), 409)
	post("/recovery/approval", actor, decision("recover-1", "approve"), 403)
	post("/recovery/approval", reviewer, decision("recover-1", "approve"), 409)
	apply(`UPDATE cmp_cloud_accounts SET credential_revision=credential_revision+1 WHERE (tenant_id,account_id)=('tenant','bmc')`)
	post("/recovery/approval", reviewer, decision("recover-1", "approve"), 412)
	post("/recovery/approval", reviewer, decision("recover-1", "reject"), 200)
	post("/recovery/approval", reviewer, decision("recover-1", "approve"), 409)

	client := func(command, token, payload string) operations.BareMetalReclaim {
		t.Helper()
		var result operations.BareMetalReclaim
		if err := dial([]string{command, "tenant", "other", reclaim.ID}, token, payload, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	for range 2 {
		r := client("recover-reclaim", actor, proposal("recover-2"))
		if r.State != "claimed" || r.Recovery == nil || r.Recovery.ID != "recover-2" || r.Recovery.State != "requested" {
			t.Fatalf("unexpected recovery proposal: %+v", r)
		}
	}
	decodeProjectData(t, call("other", current, reviewer, "GET", "", "", "", 200), &view)
	if view.Reclaim == nil || view.Reclaim.Recovery == nil || view.Reclaim.Recovery.ID != "recover-2" {
		t.Fatalf("reviewer cannot discover pending recovery: %+v", view.Reclaim)
	}
	scan()
	post("/receipt", site, receipt, 412)
	call("other", "/assets/ownership", actor, "PATCH", revision, "", transfer, 409)
	apply(`ALTER TABLE cmp_project_audit ADD CONSTRAINT fixture_reject_recovery CHECK (action<>'bare_metal_reclaim_recovery_verified')`)
	post("/recovery/approval", reviewer, decision("recover-2", "approve"), 503)
	r := client("get-reclaim", actor, "")
	if r.State != "claimed" || r.Recovery == nil || r.Recovery.State != "requested" {
		t.Fatalf("failed audit advanced recovery: %+v", r)
	}
	apply(`ALTER TABLE cmp_project_audit DROP CONSTRAINT fixture_reject_recovery`)
	for range 2 {
		r = client("review-reclaim-recovery", reviewer, decision("recover-2", "approve"))
		if r.State != "verified" || r.Recovery == nil || r.Recovery.State != "verified" || r.EvidenceSHA256 != strings.Repeat("f", 64) {
			t.Fatalf("unexpected recovery decision: %+v", r)
		}
	}
	post("/claim", site, claim, 409)
	call("other", "/assets/ownership", actor, "PATCH", revision, "", transfer, 200)
	r = client("get-reclaim", actor, "")
	if r.State != "consumed" || r.Recovery == nil || r.Recovery.State != "verified" {
		t.Fatalf("lost consumed recovery evidence: %+v", r)
	}
}
