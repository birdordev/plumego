package main

import (
	"bytes"
	"database/sql"
	"encoding/xml"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"with-cloud-manager/internal/kvm"
)

func checkKVMRetirement(t *testing.T, db *sql.DB, first, second http.Handler, api kvmVMAPI, daemon *fakeKVMDaemon, vm kvm.VM, placement kvm.Placement) {
	t.Helper()
	path := fixtureVMSitePath + "/" + vm.ID
	work := fixtureVMHostPath + "/vm-work"
	reservePath := fixtureVMPlacementPath + "/" + placement.ID
	get := func() kvm.VM {
		t.Helper()
		var current kvm.VM
		expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "GET", path, "", "", nil), 200, &current)
		return current
	}
	assertHold := func(state string, retained bool) {
		t.Helper()
		var current kvm.Placement
		expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "GET", reservePath, "", "", nil), 200, &current)
		if current.State != state || current.BootVolumeRetained != retained {
			t.Fatalf("placement state=%+v, want %s retained=%t", current, state, retained)
		}
	}
	rev := func(v kvm.VM) string { return strconv.Quote(strconv.FormatInt(v.Revision, 10)) }
	request := func(token, match string, status int, data any) {
		t.Helper()
		response := kvmRequest(t, first, token, "POST", path+"/retirement", match, "", nil)
		expectKVM(t, response, status, data)
		if status == 202 && response.Header().Get("Location") != path {
			t.Fatalf("bad retirement receipt: %v", response.Header())
		}
	}
	run := func(phase, evidence string, fails bool) kvm.VM {
		t.Helper()
		var output bytes.Buffer
		err := runKVMVMJob(t.Context(), api, vm.Host, "2", strings.NewReader(fixtureVMVolumes), &output, daemon.runner(t))
		current := get()
		if (err != nil) != fails || current.State != phase || (evidence != "" && current.Observation != evidence) {
			t.Fatalf("state=%+v, want %q/%q, err=%v", current, phase, evidence, err)
		}
		return current
	}

	request(fixtureKVMOperator, rev(vm), 403, nil)
	request(fixtureKVMGuest, rev(vm), 403, nil)
	request(fixtureKVMAdmin, "", 412, nil)
	oldRevision := rev(vm)
	request(fixtureKVMAdmin, oldRevision, 202, &vm)
	request(fixtureKVMAdmin, oldRevision, 412, nil)
	agent := kvm.AgentIdentity{UUID: fixtureKVMUUID, Generation: 2}
	var pending kvm.VMWork
	expectKVM(t, kvmRequest(t, second, fixtureKVMReplacement, "POST", work, "", "", agent), 200, &pending)
	if vm.State != "retire_requested" || pending.Job != nil {
		t.Fatalf("retirement could execute without approval: %+v %+v", vm, pending)
	}
	expectKVM(t, kvmRequest(t, second, fixtureKVMReviewer, "POST", path+"/review", rev(vm), "",
		kvm.VMReview{Decision: "reject"}), 200, &vm)
	if vm.State != "stopped" {
		t.Fatalf("retirement refusal did not restore stopped state: %+v", vm)
	}
	request(fixtureKVMAdmin, rev(vm), 202, &vm)
	vm = approveKVMVM(t, second, vm, 200)

	for _, protect := range []*bool{&daemon.snapshot, &daemon.checkpoint, &daemon.managedSave, &daemon.autoStart, &daemon.transient} {
		*protect = true
		run("retire_queued", "mismatch", true)
		*protect = false
	}
	originalXML := daemon.spec
	for _, modify := range []func(*kvmDomain){
		func(domain *kvmDomain) { domain.OS.NVRAM = &struct{}{} },
		func(domain *kvmDomain) { domain.TPMDevices = []struct{}{{}} },
		func(domain *kvmDomain) { domain.Metadata.Owner.Placement += "-foreign" },
	} {
		domain := desiredKVMDomain(vm)
		modify(&domain)
		var err error
		daemon.spec, err = xml.Marshal(domain)
		if err != nil {
			t.Fatal(err)
		}
		run("retire_queued", "mismatch", true)
	}
	daemon.spec = originalXML
	daemon.id = ""
	run("retire_queued", "absent", true)
	assertHold("bound", false)
	daemon.id = vm.ID

	dispatched := run("undefining", "", true)
	assertHold("bound", false)
	if !slices.Equal(daemon.writes, []string{"define", "start", "shutdown", "undefine"}) {
		t.Fatalf("invalid domain effects: %v", daemon.writes)
	}
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "PATCH", fixtureVMHostPath, `"2"`, "",
		kvm.HostChange{Action: "rebind", Agent: "host-one-agent"}), 409, nil)
	firstAbsence := run("undefining", "absent", false)
	expectKVM(t, kvmRequest(t, first, fixtureKVMReplacement, "PUT", work+"/"+vm.ID+"/observation", "", "",
		kvm.VMObservation{VMCommand: kvm.VMCommand{AgentIdentity: agent, Revision: dispatched.Revision}, Observed: "absent"}), 412, nil)
	assertHold("bound", false)

	daemon.id = vm.ID
	present := run("undefining", "defined", false)
	if present.Revision <= firstAbsence.Revision {
		t.Fatal("a contradictory observation did not invalidate prior absence")
	}
	assertHold("bound", false)
	daemon.id = ""
	run("undefining", "absent", false)
	if _, err := db.ExecContext(t.Context(), `ALTER TABLE cmp_kvm_events ADD CONSTRAINT reject_retired
		CHECK (action<>'vm_retired') NOT VALID`); err != nil {
		t.Fatal(err)
	}
	run("undefining", "absent", true)
	assertHold("bound", false)
	if _, err := db.ExecContext(t.Context(), `ALTER TABLE cmp_kvm_events DROP CONSTRAINT reject_retired`); err != nil {
		t.Fatal(err)
	}
	run("retired", "absent", false)
	assertHold("released", true)
	var activity int
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM cmp_kvm_events
		WHERE placement_id=$1 AND action IN ('vm_undefine','vm_retired')`, placement.ID).Scan(&activity); err != nil || activity != 2 {
		t.Fatalf("retirement audit not exactly once: %d %v", activity, err)
	}

	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "PATCH", fixtureVMHostPath, `"2"`, "", kvm.HostChange{Action: "resume"}), 200, nil)
	var report bytes.Buffer
	if err := observeKVMHost(t.Context(), api.target, vm.Host, "2", api.token,
		strings.NewReader(`{"vcpu":4,"memory_mib":4096,"disk_mib":16384}`), &report, api.transport, daemon.runner(t).kvmProbe); err != nil {
		t.Fatal(err)
	}
	requestCapacity := fixtureKVMPlacement()
	requestCapacity.VCPU = 4
	requestCapacity.IPv4 = vm.Address
	var available kvm.Placement
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "POST", fixtureVMPlacementPath, "", "retired-capacity", requestCapacity), 201, &available)
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "POST", fixtureVMSitePath, "", "",
		kvm.VMInput{Placement: available.ID, Volume: vm.Volume}), 409, nil)
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "POST", fixtureVMSitePath, "", "",
		kvm.VMInput{Placement: available.ID, Volume: "replacement-disk"}), 202, nil)
	if available.Address != vm.Address || available.MAC != vm.MAC {
		t.Fatalf("retired VM IPv4 slot was not returned: %+v", available)
	}
}
