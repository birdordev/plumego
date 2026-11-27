package main

import (
	"crypto/sha256"
	"database/sql"
	"net/http/httptest"
	"strings"
	"testing"

	"with-cloud-manager/internal/operations"
	"with-cloud-manager/internal/provider"
)

func checkBareMetalDeployment(t *testing.T, db *sql.DB, fixture *redfishPowerFixture, reviewer string, dial func([]string, string, string, any) error,
	call func(string, string, string, string, string, int) *httptest.ResponseRecorder,
	submit func(string, string, string, int) operations.Operation, approve func(operations.Operation),
	wait func(operations.Operation, string) operations.Operation) {
	t.Helper()
	const installer = "fixture-redfish-site-installer-token"
	digest := sha256.Sum256([]byte(installer))
	if _, err := db.ExecContext(t.Context(), `INSERT INTO cmp_principals(tenant_id,principal_id,kind,issuer,subject)
		VALUES ('tenant','installer','service','fixture','installer');
		INSERT INTO cmp_memberships(tenant_id,project_id,principal_id,role) VALUES ('tenant','project','installer','viewer')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO cmp_service_tokens(tenant_id,principal_id,token_hash,expires_at)
		VALUES ('tenant','installer',$1,now()+interval '10 minutes')`, digest[:]); err != nil {
		t.Fatal(err)
	}
	fixture.pxeConflict.Store(false)
	fixture.pending.Store(false)
	fixture.on.Store(false)
	fixture.bootMode.Store(0)
	// 上一个测试故意保留待复核操作，这里模拟已独立确认失败。
	if _, err := db.ExecContext(t.Context(), `UPDATE cmp_instance_operations SET status='failed' WHERE status='needs_review' AND action='stage-bare-metal-pxe'`); err != nil {
		t.Fatal(err)
	}
	stageBoot := func(key string) (operations.Operation, operations.Operation) {
		t.Helper()
		stage := submit("/bare-metal/pxe", key+"-pxe", provider.StageBareMetalPXE, 202)
		approve(stage)
		wait(stage, "succeeded")
		return stage, submit("/bare-metal/power", key+"-boot", "power-on-bare-metal", 202)
	}
	actor := vSphereFixtureBearer
	image, evidence := strings.Repeat("a", 64), strings.Repeat("b", 64)
	const path = "/bare-metal/deployments"
	register := func(stage, boot operations.Operation, code int) operations.BareMetalDeployment {
		t.Helper()
		w := call("project", path, actor, "POST", `{"stage_operation":"`+stage.ID+`","boot_operation":"`+boot.ID+
			`","installer_id":"installer","image_sha256":"`+image+`"}`, code)
		var d operations.BareMetalDeployment
		if code == 201 {
			decodeProjectData(t, w, &d)
		}
		return d
	}
	stage, boot := stageBoot("installed")
	bootPath := "/operations/" + boot.ID
	readBoot := func(token string) *operations.BareMetalDeploymentView {
		t.Helper()
		var op operations.Operation
		decodeProjectData(t, call("project", bootPath, token, "GET", "", 200), &op)
		return op.BareMetalDeployment
	}
	if view := readBoot(actor); view == nil || view.StageOperation != stage.ID || view.Deployment != nil {
		t.Fatalf("admin cannot discover confirmed PXE: %+v", view)
	}
	if view := readBoot(reviewer); view != nil {
		t.Fatalf("reviewer received registration candidate: %+v", view)
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE cmp_instance_operations SET updated_at=updated_at-interval '3 hours' WHERE operation_id=$1`, stage.ID); err != nil {
		t.Fatal(err)
	}
	if view := readBoot(actor); view != nil {
		t.Fatalf("expired PXE shown as eligible: %+v", view)
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE cmp_instance_operations SET updated_at=updated_at+interval '3 hours' WHERE operation_id=$1`, stage.ID); err != nil {
		t.Fatal(err)
	}
	d := register(stage, boot, 201)
	registration := `{"stage_operation":"` + stage.ID + `","boot_operation":"` + boot.ID +
		`","installer_id":"installer","image_sha256":"` + image + `"}`
	var replay operations.BareMetalDeployment
	err := dial([]string{"register-deployment", "tenant", "project"}, actor, registration, &replay)
	if err != nil || replay.ID != d.ID || d.State != "pending" || d.SystemID != powerSystem {
		t.Fatalf("wrong deployment registration: %+v %+v %v", d, replay, err)
	}
	base := path + "/" + d.ID
	payload := `{"system_id":"` + powerSystem + `","image_sha256":"` + image + `","result":"installed","evidence_sha256":"` + evidence + `"}`
	call("other", base, actor, "GET", "", 403)
	call("other", bootPath, actor, "GET", "", 403)
	var got operations.BareMetalDeployment
	if err := dial([]string{"get-deployment", "tenant", "project", d.ID}, actor, "", &got); err != nil || got.ID != d.ID {
		t.Fatalf("CLI deployment lookup: %+v %v", got, err)
	}
	call("project", base+"/receipt", actor, "POST", payload, 403)
	call("project", base+"/receipt", installer, "POST", payload, 409)
	if view := readBoot(reviewer); view == nil || view.Deployment == nil || view.Deployment.ID != d.ID ||
		view.Deployment.ImageSHA256 != image || view.StageOperation != "" {
		t.Fatalf("reviewer cannot verify immutable deployment: %+v", view)
	}
	decisionPath := bootPath + "/approval"
	call("project", decisionPath, reviewer, "POST", `{"decision":"approve"}`, 409)
	call("project", decisionPath, reviewer, "POST", `{"decision":"approve","deployment_id":"other"}`, 409)
	if _, err := db.ExecContext(t.Context(), `UPDATE cmp_bare_metal_deployments
		SET created_at=created_at-interval '2 hours',expires_at=created_at-interval '1 hour' WHERE deployment_id=$1`, d.ID); err != nil {
		t.Fatal(err)
	}
	approval := `{"decision":"approve","deployment_id":"` + d.ID + `"}`
	call("project", decisionPath, reviewer, "POST", approval, 409)
	if _, err := db.ExecContext(t.Context(), `UPDATE cmp_bare_metal_deployments
		SET created_at=created_at+interval '2 hours',expires_at=expires_at+interval '3 hours' WHERE deployment_id=$1`, d.ID); err != nil {
		t.Fatal(err)
	}
	var approved operations.Operation
	approveArgs := []string{"approve", "tenant", "project", boot.ID, "--decision", "approve", "--deployment-id", d.ID}
	if err := dial(approveArgs, reviewer, "", &approved); err != nil || approved.ID != boot.ID {
		t.Fatalf("bound CLI approval failed: %+v %v", approved, err)
	}
	wait(boot, "succeeded")
	submit("/bare-metal/power", "deployment-lock", "shutdown-bare-metal", 409)
	call("project", base+"/receipt", installer, "POST", strings.Replace(payload, powerSystem, powerBMC, 1), 409)
	call("project", base+"/receipt", installer, "POST", strings.Replace(payload, image, evidence, 1), 409)
	for i := 0; i < 2; i++ {
		var recorded operations.BareMetalDeployment
		err := dial([]string{"report-deployment", "tenant", "project", d.ID}, installer, payload, &recorded)
		if err != nil {
			t.Fatal(err)
		}
		if recorded.State != "installed" || recorded.RecordedAt == nil || recorded.EvidenceSHA256 != evidence {
			t.Fatalf("wrong installation receipt: %+v", recorded)
		}
	}
	if view := readBoot(reviewer); view == nil || view.Deployment == nil || view.Deployment.State != "installed" ||
		view.Deployment.EvidenceSHA256 != evidence {
		t.Fatalf("operation detail did not return installation receipt: %+v", view)
	}
	if err := dial(approveArgs, reviewer, "", &approved); err != nil || approved.Status != "succeeded" {
		t.Fatalf("bound approval did not replay: %+v %v", approved, err)
	}
	call("project", base+"/receipt", installer, "POST", strings.Replace(payload, "installed", "failed", 1), 409)
	fixture.on.Store(false)
	stage, boot = stageBoot("stale-pxe")
	d = register(stage, boot, 201)
	fixture.bootMode.Store(0)
	before := fixture.writes.Load()
	call("project", "/operations/"+boot.ID+"/approval", reviewer, "POST", `{"decision":"approve","deployment_id":"`+d.ID+`"}`, 200)
	if failed := wait(boot, "failed"); failed.OutcomeReason != "bare_metal_deployment_preconditions_unconfirmed" || fixture.writes.Load() != before {
		t.Fatalf("deployment without PXE reached BMC: %+v", failed)
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE cmp_memberships SET revision=revision+1
		WHERE (tenant_id,project_id,principal_id)=('tenant','project','installer')`); err != nil {
		t.Fatal(err)
	}
	call("project", path+"/"+d.ID+"/receipt", installer, "POST", payload, 412)
}
