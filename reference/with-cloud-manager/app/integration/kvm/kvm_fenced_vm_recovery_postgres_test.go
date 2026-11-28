package kvmtest

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	kvmapp "with-cloud-manager/app/kvm"
	"with-cloud-manager/internal/kvm"
)

func TestAcceptanceKVMFencedVMRecovery(t *testing.T) {
	for _, tc := range []struct {
		name, state, observed, want string
	}{
		{"lost definition", "defining", "absent", "rejected"},
		{"confirmed definition", "defining", "defined", "stopped"},
		{"confirmed start", "starting", "running", "running"},
		{"shutdown failed", "stopping", "running", "running"},
		{"confirmed shutdown", "stopping", "defined", "stopped"},
		{"confirmed removal", "undefining", "absent", "retired"},
		{"removal failed", "undefining", "defined", "stopped"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, first, second, placement, v := prepareKVMVMWithPool(t, &fixtureIPv4Pool)
			v = approveKVMVM(t, first, v, 200)
			prepareFencedVMState(t, db, second, v, tc.state)
			revision := strconv.Quote(strconv.FormatInt(v.Revision+1, 10))
			path := fixtureVMSitePath + "/" + v.ID + "/fence-recovery"
			input := kvm.FencedVMRecoveryInput{ID: "recovery-1", HostUUID: fixtureKVMUUID, FenceGeneration: 2,
				Observed: tc.observed, EvidenceSHA256: strings.Repeat("a", 64)}
			if tc.observed == "absent" {
				input.SecondEvidenceSHA256 = strings.Repeat("b", 64)
			}
			fence := func(actor, version, action, evidence, replacement string, status int) {
				t.Helper()
				expectKVM(t, kvmRequest(t, first, actor, "POST", fixtureVMHostPath+"/fence", version, "",
					kvm.HostFenceChange{Action: action, UUID: fixtureKVMUUID, EvidenceSHA256: evidence, ReplacementAgent: replacement}), status, nil)
			}
			fence(fixtureKVMAdmin, `"1"`, "request", "", "", 200)
			expectKVM(t, kvmRequest(t, second, fixtureKVMAdmin, "POST", path, revision, "", input), 409, nil)
			fence(fixtureKVMReviewer, `"2"`, "confirm", strings.Repeat("c", 64), "", 200)
			if tc.name == "lost definition" {
				checkFencedVMRecoveryRejections(t, first, path, revision, input)
			}
			var proposed, replay kvm.FencedVMRecovery
			expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", path, revision, "", input), 202, &proposed)
			expectKVM(t, kvmRequest(t, second, fixtureKVMAdmin, "POST", path, revision, "", input), 202, &replay)
			if proposed.Status != "pending" || replay.ID != proposed.ID || !replay.RequestedAt.Equal(proposed.RequestedAt) ||
				proposed.SourceState != tc.state {
				t.Fatalf("bad recovery proposal or replay: %+v %+v", proposed, replay)
			}
			decision := kvm.FencedVMRecoveryReview{ID: proposed.ID, Decision: "approve"}
			call := func(actor string, status int, result any) {
				t.Helper()
				expectKVM(t, kvmRequest(t, second, actor, "POST", path+"/review", revision, "", decision), status, result)
			}
			call(fixtureKVMAdmin, 403, nil)
			if tc.name == "lost definition" {
				checkFencedVMRecoveryRollback(t, db, first, v.ID, tc.state, placement.ID, call)
			}
			var approved kvm.FencedVMRecovery
			call(fixtureKVMReviewer, 200, &approved)
			call(fixtureKVMReviewer, 200, &replay)
			if approved.ResultState != tc.want || approved.Status != "approved" || replay.ReviewedAt == nil ||
				approved.ReviewedAt == nil || !replay.ReviewedAt.Equal(*approved.ReviewedAt) {
				t.Fatalf("wrong approval result: %+v %+v", approved, replay)
			}
			var vm kvm.VM
			expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "GET", fixtureVMSitePath+"/"+v.ID, "", "", nil), 200, &vm)
			if vm.State != tc.want || vm.Revision != v.Revision+2 || vm.Observation != tc.observed {
				t.Fatalf("unreconciled VM: %+v", vm)
			}
			expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "GET", fixtureVMPlacementPath+"/"+placement.ID, "", "", nil), 200, &placement)
			wantReleased := tc.want == "rejected" || tc.want == "retired"
			if placement.BootVolumeRetained != wantReleased || (placement.State == "released") != wantReleased {
				t.Fatalf("incorrect recovery reservation: %+v", placement)
			}
			if tc.name == "lost definition" {
				checkFencedVMRecoveryCLI(t, second, vm.ID, tc.want)
			}
			fence(fixtureKVMAdmin, `"3"`, "request-rejoin", strings.Repeat("d", 64), "replacement-agent", 200)
			fence(fixtureKVMReviewer, `"4"`, "approve-rejoin", "", "", 200)
			checkFencedVMRecoveryReallocation(t, first, second, placement, vm, wantReleased)
		})
	}
}

func checkFencedVMRecoveryReallocation(t *testing.T, first, second http.Handler, old kvm.Placement, vm kvm.VM, released bool) {
	t.Helper()
	if old.Address == "" {
		t.Fatal("the test VM must have a registered address")
	}
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "PATCH", fixtureVMHostPath, `"5"`, "",
		kvm.HostChange{Action: "resume"}), 200, nil)
	report := fixtureKVMReport()
	report.Generation = 3
	expectKVM(t, kvmRequest(t, second, fixtureKVMReplacement, "PUT", fixtureVMHostPath+"/capacity", "", "", report), 200, nil)
	input := fixtureKVMPlacement()
	input.IPv4 = old.Address
	if !released {
		expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "POST", fixtureVMPlacementPath, "", "reuse-address", input), 409, nil)
		return
	}
	var next kvm.Placement
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "POST", fixtureVMPlacementPath, "", "reuse-address", input), 201, &next)
	if next.Address != old.Address {
		t.Fatalf("address was not reallocated: %+v", next)
	}
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "POST", fixtureVMSitePath, "", "",
		kvm.VMInput{Placement: next.ID, Volume: vm.Volume}), 409, nil)
}

func prepareFencedVMState(t *testing.T, db *sql.DB, handler http.Handler, v kvm.VM, state string) {
	t.Helper()
	if state == "defining" {
		agent := kvm.AgentIdentity{UUID: fixtureKVMUUID, Generation: 1}
		expectKVM(t, kvmRequest(t, handler, fixtureKVMAgent, "POST", fixtureVMHostPath+"/vm-work/"+v.ID+"/arm", "", "",
			kvm.VMCommand{AgentIdentity: agent, Revision: v.Revision}), 200, nil)
		return
	}
	// 夹具从已批准的任务写入中间态，覆盖各终态的真实数据库约束。
	if _, err := db.ExecContext(t.Context(), `UPDATE cmp_kvm_vms
		SET state=$2,revision=revision+1,
		shutdown_requester_id=CASE WHEN $2 IN ('stopping','undefining') THEN 'operator' ELSE '' END,
		shutdown_requester_revision=CASE WHEN $2 IN ('stopping','undefining') THEN 1 ELSE 0 END,
		shutdown_reviewer_id=CASE WHEN $2 IN ('stopping','undefining') THEN 'reviewer' ELSE '' END,
		shutdown_reviewer_revision=CASE WHEN $2 IN ('stopping','undefining') THEN 1 ELSE 0 END,
		retire_requester_id=CASE WHEN $2='undefining' THEN 'administrator' ELSE '' END,
		retire_requester_revision=CASE WHEN $2='undefining' THEN 1 ELSE 0 END,
		retire_reviewer_id=CASE WHEN $2='undefining' THEN 'reviewer' ELSE '' END,
		retire_reviewer_revision=CASE WHEN $2='undefining' THEN 1 ELSE 0 END
		WHERE vm_id=$1::uuid`, v.ID, state); err != nil {
		t.Fatal(err)
	}
}

func checkFencedVMRecoveryRejections(t *testing.T, handler http.Handler, path, revision string, input kvm.FencedVMRecoveryInput) {
	t.Helper()
	expectKVM(t, kvmRequest(t, handler, fixtureKVMGuest, "GET", path, "", "", nil), 403, nil)
	expectKVM(t, kvmRequest(t, handler, fixtureKVMOperator, "POST", path, revision, "", input), 403, nil)
	wrong := input
	wrong.SecondEvidenceSHA256 = wrong.EvidenceSHA256
	expectKVM(t, kvmRequest(t, handler, fixtureKVMAdmin, "POST", path, revision, "", wrong), 400, nil)
	wrong = input
	wrong.FenceGeneration--
	expectKVM(t, kvmRequest(t, handler, fixtureKVMAdmin, "POST", path, revision, "", wrong), 412, nil)
	wrong = input
	wrong.HostUUID = "feaa8a19-4765-4bb4-a383-3a1011ba0002"
	expectKVM(t, kvmRequest(t, handler, fixtureKVMAdmin, "POST", path, revision, "", wrong), 409, nil)
}

func checkFencedVMRecoveryRollback(t *testing.T, db *sql.DB, handler http.Handler, vm, state, placement string,
	review func(string, int, any)) {
	t.Helper()
	const constraint = "fixture_reject_recovery"
	if _, err := db.ExecContext(t.Context(), "ALTER TABLE cmp_kvm_fenced_vm_recoveries ADD CONSTRAINT "+constraint+
		" CHECK (status<>'approved')"); err != nil {
		t.Fatal(err)
	}
	review(fixtureKVMReviewer, 503, nil)
	var before kvm.VM
	var hold kvm.Placement
	expectKVM(t, kvmRequest(t, handler, fixtureKVMOperator, "GET", fixtureVMSitePath+"/"+vm, "", "", nil), 200, &before)
	expectKVM(t, kvmRequest(t, handler, fixtureKVMOperator, "GET", fixtureVMPlacementPath+"/"+placement, "", "", nil), 200, &hold)
	if before.State != state || hold.State != "bound" || hold.BootVolumeRetained {
		t.Fatalf("failed audit committed the recovery: %+v %+v", before, hold)
	}
	if _, err := db.ExecContext(t.Context(), "ALTER TABLE cmp_kvm_fenced_vm_recoveries DROP CONSTRAINT "+constraint); err != nil {
		t.Fatal(err)
	}
}

func checkFencedVMRecoveryCLI(t *testing.T, handler http.Handler, id, state string) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	var output bytes.Buffer
	args := []string{"get-vm-recovery", "lab", "compute", "site-a", id}
	if err := kvmapp.RunKVMClient(t.Context(), args, server.URL, fixtureKVMOperator, nil, &output, server.Client().Transport); err != nil {
		t.Fatal(err)
	}
	var result kvm.FencedVMRecoveryView
	if err := json.Unmarshal(output.Bytes(), &result); err != nil || result.Recovery == nil || result.Recovery.ResultState != state {
		t.Fatalf("CLI failed to read the recovery decision: %+v %v", result, err)
	}
}
