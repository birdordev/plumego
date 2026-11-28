package kvmtest

import (
	"bytes"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	kvmapp "with-cloud-manager/app/kvm"
	"with-cloud-manager/internal/kvm"
)

const fixtureVMHostPath = fixtureKVMSite + "/hosts/host-one"
const fixtureVMSitePath = fixtureKVMSite + "/vms"
const fixtureVMPlacementPath = fixtureKVMSite + "/placements"
const fixtureVMVolumes = `{"boot_volumes":["boot-ready"]}`

func prepareKVMVM(t *testing.T) (*sql.DB, http.Handler, http.Handler, kvm.Placement, kvm.VM) {
	t.Helper()
	return prepareKVMVMWithPool(t, nil)
}

func prepareKVMVMWithPool(t *testing.T, pool *kvm.IPv4PoolSpec) (*sql.DB, http.Handler, http.Handler, kvm.Placement, kvm.VM) {
	t.Helper()
	db, first, second := prepareKVM(t)
	if pool != nil {
		expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", fixtureKVMSite+"/networks", "", "", pool), 201, nil)
	}
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", fixtureKVMSite+"/hosts", "", "", fixtureKVMHost()), 201, nil)
	expectKVM(t, kvmRequest(t, second, fixtureKVMAgent, "PUT", fixtureVMHostPath+"/capacity", "", "", fixtureKVMReport()), 200, nil)
	var p kvm.Placement
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "POST", fixtureVMPlacementPath, "", "vm-create", fixtureKVMPlacement()), 201, &p)
	var vm kvm.VM
	input := kvm.VMInput{Placement: p.ID, Volume: "boot-ready"}
	response := kvmRequest(t, second, fixtureKVMOperator, "POST", fixtureVMSitePath, "", "", input)
	expectKVM(t, response, 202, &vm)
	if vm.State != "awaiting_approval" || vm.Host != "host-one" || vm.Generation != 1 ||
		response.Header().Get("Location") != fixtureVMSitePath+"/"+vm.ID || response.Header().Get("ETag") != `"1"` {
		t.Fatalf("invalid VM receipt: %+v %v", vm, response.Header())
	}
	return db, first, second, p, vm
}

func approveKVMVM(t *testing.T, handler http.Handler, vm kvm.VM, want int) kvm.VM {
	t.Helper()
	var result kvm.VM
	var data any
	if want == http.StatusOK {
		data = &result
	}
	req := kvmRequest(t, handler, fixtureKVMReviewer, "POST", fixtureVMSitePath+"/"+vm.ID+"/review",
		strconv.Quote(strconv.FormatInt(vm.Revision, 10)), "", kvm.VMReview{Decision: "approve"})
	expectKVM(t, req, want, data)
	return result
}

func TestAcceptanceKVMVMExecution(t *testing.T) {
	db, first, second, p, vm := prepareKVMVM(t)
	path := fixtureVMSitePath + "/" + vm.ID
	expectKVM(t, kvmRequest(t, first, fixtureKVMGuest, "GET", path, "", "", nil), 403, nil)
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "POST", path+"/review", `"1"`, "",
		kvm.VMReview{Decision: "approve"}), 403, nil)
	var replay kvm.VM
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "POST", fixtureVMSitePath, "", "",
		kvm.VMInput{Placement: p.ID, Volume: vm.Volume}), 202, &replay)
	if replay != vm {
		t.Fatalf("VM submission was not idempotent: %+v %+v", replay, vm)
	}
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "POST", fixtureVMSitePath, "", "",
		kvm.VMInput{Placement: p.ID, Volume: "different-disk"}), 409, nil)
	var another kvm.Placement
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "POST", fixtureVMPlacementPath, "", "same-volume", fixtureKVMPlacement()), 201, &another)
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "POST", fixtureVMSitePath, "", "",
		kvm.VMInput{Placement: another.ID, Volume: vm.Volume}), 409, nil)
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "DELETE", fixtureVMPlacementPath+"/"+another.ID, "", "", nil), 200, nil)
	vm = approveKVMVM(t, first, vm, 200)

	server := httptest.NewTLSServer(second)
	t.Cleanup(server.Close)
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	api := kvmapp.KVMVMAPI{Target: base.JoinPath(fixtureVMHostPath), Token: fixtureKVMAgent, Transport: server.Client().Transport}
	daemon := &fakeKVMDaemon{}
	get := func() kvm.VM {
		t.Helper()
		var current kvm.VM
		expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "GET", path, "", "", nil), 200, &current)
		return current
	}
	step := func(state string, wantUnknown bool) {
		t.Helper()
		var out bytes.Buffer
		err := kvmapp.RunKVMVMJob(t.Context(), api, "host-one", "1", strings.NewReader(fixtureVMVolumes), &out, daemon.runner(t))
		if (err != nil) != wantUnknown || get().State != state {
			t.Fatalf("unexpected agent step: want state=%s err=%t, got state=%s err=%v", state, wantUnknown, get().State, err)
		}
	}
	var ignored bytes.Buffer
	if err := kvmapp.RunKVMVMJob(t.Context(), api, "host-one", "1", strings.NewReader(`{"boot_volumes":["other-disk"]}`), &ignored, daemon.runner(t)); err == nil || get().State != "queued" {
		t.Fatalf("non-allowlisted boot volume obtained a write grant: %v", err)
	}
	step("defining", true)
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "PATCH", fixtureVMHostPath, `"1"`, "",
		kvm.HostChange{Action: "rebind", Agent: "replacement-agent"}), 409, nil)
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "DELETE", fixtureVMPlacementPath+"/"+p.ID, "", "", nil), 409, nil)
	if _, err := db.ExecContext(t.Context(), `UPDATE cmp_kvm_placements
		SET created_at=clock_timestamp()-interval '2 minutes',expires_at=clock_timestamp()-interval '1 minute' WHERE placement_id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	var held kvm.Placement
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "GET", fixtureVMPlacementPath+"/"+p.ID, "", "", nil), 200, &held)
	if held.State != "bound" {
		t.Fatalf("consumed placement expired during observation: %+v", held)
	}
	full := fixtureKVMPlacement()
	full.VCPU = 4
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "POST", fixtureVMPlacementPath, "", "expired-but-bound", full), 409, nil)
	originalSpec := daemon.spec
	daemon.spec = bytes.ReplaceAll(daemon.spec, []byte(`volume="boot-ready"`), []byte(`volume="different"`))
	step("defining", false)
	if get().Observation != "mismatch" {
		t.Fatal("a foreign domain volume was incorrectly accepted")
	}
	daemon.spec = originalSpec
	step("defined", false)
	step("starting", true)
	step("running", false)
	step("running", false)
	if !slices.Equal(daemon.writes, []string{"define", "start"}) || daemon.id != vm.ID {
		t.Fatalf("unexpected external writes or VM ID: %v %q", daemon.writes, daemon.id)
	}
	var eventCount int
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM cmp_kvm_events WHERE placement_id=$1 AND action LIKE 'vm_%'`,
		p.ID).Scan(&eventCount); err != nil || eventCount != 6 {
		t.Fatalf("incomplete VM audit: %d %v", eventCount, err)
	}
	checkKVMResidentCapacity(t, first, second, api, daemon, get())
}

func TestAcceptanceKVMVMDispatchFence(t *testing.T) {
	db, first, second, _, vm := prepareKVMVM(t)
	vm = approveKVMVM(t, first, vm, 200)
	agent := kvm.AgentIdentity{UUID: fixtureKVMUUID, Generation: 1}
	cmd := kvm.VMCommand{AgentIdentity: agent, Revision: vm.Revision}
	work := fixtureVMHostPath + "/vm-work"
	arm := work + "/" + vm.ID + "/arm"
	expectKVM(t, kvmRequest(t, first, fixtureKVMReplacement, "POST", work, "", "", agent), 403, nil)
	stale := agent
	stale.Generation++
	expectKVM(t, kvmRequest(t, second, fixtureKVMAgent, "POST", work, "", "", stale), 412, nil)
	if _, err := db.ExecContext(t.Context(), "UPDATE cmp_memberships SET active=false WHERE principal_id='reviewer'"); err != nil {
		t.Fatal(err)
	}
	expectKVM(t, kvmRequest(t, first, fixtureKVMAgent, "POST", arm, "", "", cmd), 403, nil)
	if _, err := db.ExecContext(t.Context(), "UPDATE cmp_memberships SET active=true WHERE principal_id='reviewer'"); err != nil {
		t.Fatal(err)
	}
	expectKVM(t, kvmRequest(t, first, fixtureKVMAgent, "POST", arm, "", "", cmd), 403, nil)

	// 撤销与恢复后的新审批仍需先撤回未发送的旧任务。
	var rejected kvm.VM
	expectKVM(t, kvmRequest(t, first, fixtureKVMReviewer, "POST", fixtureVMSitePath+"/"+vm.ID+"/review", `"2"`, "",
		kvm.VMReview{Decision: "reject"}), 200, &rejected)
	if rejected.State != "rejected" {
		t.Fatalf("approval withdrawal did not cancel unsent VM: %+v", rejected)
	}
	var p kvm.Placement
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "POST", fixtureVMPlacementPath, "", "renew-review", fixtureKVMPlacement()), 201, &p)
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "POST", fixtureVMSitePath, "", "",
		kvm.VMInput{Placement: p.ID, Volume: "boot-ready"}), 202, &vm)
	vm = approveKVMVM(t, first, vm, 200)
	arm = work + "/" + vm.ID + "/arm"
	cmd.Revision = vm.Revision

	results := make([]*httptest.ResponseRecorder, 8)
	var wg sync.WaitGroup
	for i := range results {
		handler := first
		if i%2 > 0 {
			handler = second
		}
		wg.Go(func() { results[i] = kvmRequest(t, handler, fixtureKVMAgent, "POST", arm, "", "", cmd) })
	}
	wg.Wait()
	granted := 0
	for _, r := range results {
		if r.Code == 412 {
			continue
		}
		expectKVM(t, r, 200, nil)
		granted++
	}
	if granted != 1 {
		t.Fatalf("expected a single write grant, got %d", granted)
	}
	var observation kvm.VM
	expectKVM(t, kvmRequest(t, second, fixtureKVMAgent, "PUT", work+"/"+vm.ID+"/observation", "", "",
		kvm.VMObservation{VMCommand: kvm.VMCommand{AgentIdentity: agent, Revision: vm.Revision + 1}, Observed: "mismatch"}), 200, &observation)
	if observation.State != "defining" || observation.Observation != "mismatch" {
		t.Fatalf("foreign domain was accepted or forgotten: %+v", observation)
	}
}

func TestKVMVMCLIPaths(t *testing.T) {
	base, err := url.Parse("https://example.test")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		args         []string
		method, path string
		status       int
	}{
		{[]string{"register-image", "lab", "compute", "site-a"}, "POST", fixtureKVMSite + "/images", 201},
		{[]string{"get-image", "lab", "compute", "site-a", "raw-test"}, "GET", fixtureKVMSite + "/images/raw-test", 200},
		{[]string{"register-network", "lab", "compute", "site-a"}, "POST", fixtureKVMSite + "/networks", 201},
		{[]string{"get-network", "lab", "compute", "site-a", "app"}, "GET", fixtureKVMSite + "/networks/app", 200},
		{[]string{"submit-vm", "lab", "compute", "site-a"}, "POST", fixtureVMSitePath, 202},
		{[]string{"get-vm", "lab", "compute", "site-a", fixtureKVMUUID}, "GET", fixtureVMSitePath + "/" + fixtureKVMUUID, 200},
		{[]string{"shutdown-vm", "lab", "compute", "site-a", fixtureKVMUUID, "3"}, "POST", fixtureVMSitePath + "/" + fixtureKVMUUID + "/shutdown", 202},
		{[]string{"retire-vm", "lab", "compute", "site-a", fixtureKVMUUID, "3"}, "POST", fixtureVMSitePath + "/" + fixtureKVMUUID + "/retirement", 202},
		{[]string{"review-vm", "lab", "compute", "site-a", fixtureKVMUUID, "3"}, "POST", fixtureVMSitePath + "/" + fixtureKVMUUID + "/review", 200},
	} {
		plan, err := kvmapp.PlanKVMRequest(base, test.args)
		if err != nil || plan.Target.String() != base.String()+test.path || plan.Method != test.method || plan.Status != test.status {
			t.Fatalf("incorrect VM CLI plan: %+v %v", plan, err)
		}
	}
}
