package kvmtest

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	kvmapp "with-cloud-manager/app/kvm"
	"with-cloud-manager/internal/kvm"
)

func TestAcceptanceKVMHostFence(t *testing.T) {
	db, first, second := prepareKVM(t)
	const hostPath = fixtureVMHostPath
	const fencePath = hostPath + "/fence"
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", fixtureKVMSite+"/hosts", "", "", fixtureKVMHost()), 201, nil)
	sample := fixtureKVMReport()
	expectKVM(t, kvmRequest(t, second, fixtureKVMAgent, "PUT", hostPath+"/capacity", "", "", sample), 200, nil)
	budget := fixtureKVMPlacement()
	budget.VCPU = sample.Capacity.VCPU
	var held kvm.Placement
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "POST", fixtureVMPlacementPath, "", "held-before-fence", budget), 201, &held)

	change := func(action, evidence, replacement string) kvm.HostFenceChange {
		return kvm.HostFenceChange{Action: action, UUID: fixtureKVMUUID, EvidenceSHA256: evidence, ReplacementAgent: replacement}
	}
	sha := strings.Repeat("a", 64)
	rejoin := change("request-rejoin", strings.Repeat("b", 64), "replacement-agent")
	var host kvm.Host
	call := func(handler http.Handler, actor string, revision int64, input kvm.HostFenceChange, status int) {
		t.Helper()
		match := strconv.Quote(strconv.FormatInt(revision, 10))
		resp := kvmRequest(t, handler, actor, "POST", fencePath, match, "", input)
		if status != 200 {
			expectKVM(t, resp, status, nil)
			return
		}
		host = kvm.Host{}
		expectKVM(t, resp, status, &host)
		if host.Revision != revision+1 || !host.Draining || host.ReportedAt != nil || host.Capacity != (kvm.Capacity{}) {
			t.Fatalf("fence left reusable capacity or the wrong revision: %+v", host)
		}
	}
	wrong := change("request", "", "")
	wrong.UUID = "feaa8a19-4765-4bb4-a383-3a1011ba0002"
	call(first, fixtureKVMAdmin, 1, wrong, 409)
	call(second, fixtureKVMGuest, 1, change("request", "", ""), 403)
	call(first, fixtureKVMOperator, 1, change("request", "", ""), 403)
	call(first, fixtureKVMAdmin, 1, change("request", "", ""), 200)
	call(second, fixtureKVMAdmin, 1, change("request", "", ""), 412)
	if host.FenceState != "pending" || host.Generation != 2 {
		t.Fatalf("request did not revoke the old agent generation: %+v", host)
	}
	expectKVM(t, kvmRequest(t, second, fixtureKVMAgent, "PUT", hostPath+"/capacity", "", "", sample), 412, nil)
	sample.Generation = 2
	expectKVM(t, kvmRequest(t, first, fixtureKVMAgent, "PUT", hostPath+"/capacity", "", "", sample), 409, nil)
	agent := kvm.AgentIdentity{UUID: fixtureKVMUUID, Generation: 2}
	expectKVM(t, kvmRequest(t, second, fixtureKVMAgent, "POST", hostPath+"/vm-work", "", "", agent), 409, nil)
	expectKVM(t, kvmRequest(t, second, fixtureKVMAgent, "POST", hostPath+"/dhcp-plan/tenant-net", "", "", agent), 409, nil)
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "PATCH", hostPath, `"2"`, "", kvm.HostChange{Action: "resume"}), 409, nil)
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "POST", fixtureVMPlacementPath, "", "blocked-by-fence", budget), 409, nil)
	call(second, fixtureKVMAdmin, 2, change("confirm", sha, ""), 403)
	call(second, fixtureKVMReviewer, 2, change("confirm", sha, ""), 200)
	if host.FenceState != "fenced" || host.Generation != 2 {
		t.Fatalf("confirmation incorrectly changed the fence generation: %+v", host)
	}
	call(first, fixtureKVMAdmin, 3, rejoin, 200)
	call(second, fixtureKVMReviewer, 4, change("reject-rejoin", "", ""), 200)
	if host.FenceState != "fenced" {
		t.Fatalf("rejected rejoin ended the fence: %+v", host)
	}
	call(first, fixtureKVMAdmin, 5, rejoin, 200)
	const constraint = "fixture_reject_approval"
	if _, err := db.ExecContext(t.Context(), "ALTER TABLE cmp_kvm_host_fence_events ADD CONSTRAINT "+constraint+
		" CHECK (action<>'approve-rejoin')"); err != nil {
		t.Fatal(err)
	}
	call(second, fixtureKVMReviewer, 6, change("approve-rejoin", "", ""), 503)
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "GET", hostPath, "", "", nil), 200, &host)
	if host.Revision != 6 || host.FenceState != "rejoin_pending" || host.Agent != "host-one-agent" {
		t.Fatalf("failed audit released the fence: %+v", host)
	}
	if _, err := db.ExecContext(t.Context(), "ALTER TABLE cmp_kvm_host_fence_events DROP CONSTRAINT "+constraint); err != nil {
		t.Fatal(err)
	}
	call(second, fixtureKVMReviewer, 6, change("approve-rejoin", "", ""), 200)
	if host.FenceState != "" || host.Generation != 3 || host.Agent != "replacement-agent" {
		t.Fatalf("rejoin failed to bind replacement agent in a new generation: %+v", host)
	}
	verifyKVMFenceHistory(t, second, sha, rejoin.EvidenceSHA256)
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "PATCH", hostPath, `"7"`, "", kvm.HostChange{Action: "resume"}), 200, nil)
	sample.Generation = 3
	expectKVM(t, kvmRequest(t, second, fixtureKVMAgent, "PUT", hostPath+"/capacity", "", "", sample), 403, nil)
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "POST", fixtureVMPlacementPath, "", "needs-fresh-report", budget), 409, nil)
	expectKVM(t, kvmRequest(t, first, fixtureKVMReplacement, "PUT", hostPath+"/capacity", "", "", sample), 200, nil)
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "POST", fixtureVMPlacementPath, "", "held-capacity-survived", budget), 409, nil)
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "GET", fixtureVMPlacementPath+"/"+held.ID, "", "", nil), 200, &held)
	if held.State != "held" || held.Generation != 1 {
		t.Fatalf("fence changed the original hold: %+v", held)
	}
}

func verifyKVMFenceHistory(t *testing.T, handler http.Handler, fenceEvidence, rejoinEvidence string) {
	t.Helper()
	api := httptest.NewTLSServer(handler)
	defer api.Close()
	var output bytes.Buffer
	args := []string{"get-host-fence", "lab", "compute", "site-a", "host-one"}
	if err := kvmapp.RunKVMClient(t.Context(), args, api.URL, fixtureKVMOperator, nil, &output, api.Client().Transport); err != nil {
		t.Fatal(err)
	}
	var history kvm.HostFenceHistory
	if err := json.Unmarshal(output.Bytes(), &history); err != nil {
		t.Fatal(err)
	}
	if len(history.Events) != 6 || history.Events[0].Action != "approve-rejoin" ||
		history.Events[1].EvidenceSHA256 != rejoinEvidence || history.Events[4].EvidenceSHA256 != fenceEvidence {
		t.Fatalf("incomplete fence audit: %+v", history)
	}
}

func TestAcceptanceKVMHostFencePreservesInFlightVM(t *testing.T) {
	_, first, second, placement, vm := prepareKVMVM(t)
	vm = approveKVMVM(t, first, vm, 200)
	path := fixtureVMHostPath + "/vm-work/" + vm.ID
	agent := kvm.AgentIdentity{UUID: fixtureKVMUUID, Generation: 1}
	var armed kvm.VM
	expectKVM(t, kvmRequest(t, second, fixtureKVMAgent, "POST", path+"/arm", "", "",
		kvm.VMCommand{AgentIdentity: agent, Revision: vm.Revision}), 200, &armed)
	fence := func(actor, revision, action, evidence, replacement string, status int) {
		t.Helper()
		expectKVM(t, kvmRequest(t, first, actor, "POST", fixtureVMHostPath+"/fence", revision, "",
			kvm.HostFenceChange{Action: action, UUID: fixtureKVMUUID, EvidenceSHA256: evidence, ReplacementAgent: replacement}), status, nil)
	}
	fence(fixtureKVMAdmin, `"1"`, "request", "", "", 200)
	expectKVM(t, kvmRequest(t, second, fixtureKVMAgent, "PUT", path+"/observation", "", "",
		kvm.VMObservation{VMCommand: kvm.VMCommand{AgentIdentity: agent, Revision: armed.Revision}, Observed: "defined"}), 412, nil)
	agent.Generation = 2
	expectKVM(t, kvmRequest(t, second, fixtureKVMAgent, "PUT", path+"/observation", "", "",
		kvm.VMObservation{VMCommand: kvm.VMCommand{AgentIdentity: agent, Revision: armed.Revision}, Observed: "defined"}), 409, nil)
	fence(fixtureKVMReviewer, `"2"`, "confirm", strings.Repeat("e", 64), "", 200)
	fence(fixtureKVMAdmin, `"3"`, "request-rejoin", strings.Repeat("f", 64), "replacement-agent", 200)
	fence(fixtureKVMReviewer, `"4"`, "approve-rejoin", "", "", 409)
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "GET", fixtureVMSitePath+"/"+vm.ID, "", "", nil), 200, &vm)
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "GET", fixtureVMPlacementPath+"/"+placement.ID, "", "", nil), 200, &placement)
	if vm.State != "defining" || vm.Revision != armed.Revision || placement.State != "bound" {
		t.Fatalf("fence modified unresolved VM or released its placement: %+v %+v", vm, placement)
	}
}
