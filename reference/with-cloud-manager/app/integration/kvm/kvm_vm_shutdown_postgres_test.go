package kvmtest

import (
	"bytes"
	"encoding/xml"
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

func TestAcceptanceKVMVMShutdown(t *testing.T) {
	db, first, second, placement, vm := prepareKVMVMWithPool(t, &fixtureIPv4Pool)
	vm = approveKVMVM(t, first, vm, 200)
	path := fixtureVMSitePath + "/" + vm.ID
	poll := fixtureVMHostPath + "/vm-work"
	job := poll + "/" + vm.ID
	agent := kvm.AgentIdentity{UUID: fixtureKVMUUID, Generation: 1}
	// 使用实际代理写入流程创建初始域。
	server := httptest.NewTLSServer(second)
	t.Cleanup(server.Close)
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	api := kvmapp.KVMVMAPI{Target: base.JoinPath(fixtureVMHostPath), Token: fixtureKVMAgent, Transport: server.Client().Transport}
	if vm.Address != fixtureIPv4Pool.First || vm.Subnet != fixtureIPv4Pool.Subnet || vm.Gateway != fixtureIPv4Pool.Gateway ||
		vm.MAC == "" || vm.MAC != placement.MAC {
		t.Fatalf("assignment was not carried from placement to VM: %+v", vm)
	}
	network := `<network><name>app</name><forward mode="nat"/><ip address="10.77.0.1" prefix="29"><dhcp>
		<host ip="` + vm.Address + `" mac="` + vm.MAC + `" name="fixture-vm"/>
	</dhcp></ip></network>`
	daemon := &fakeKVMDaemon{liveNetwork: network, savedNetwork: strings.ReplaceAll(network, vm.MAC, "02:00:00:00:00:01")}
	run := func(api kvmapp.KVMVMAPI, generation string) error {
		var output bytes.Buffer
		return kvmapp.RunKVMVMJob(t.Context(), api, vm.Host, generation, strings.NewReader(fixtureVMVolumes), &output, daemon.runner(t))
	}
	get := func() kvm.VM {
		t.Helper()
		var current kvm.VM
		expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "GET", path, "", "", nil), 200, &current)
		return current
	}
	if err := run(api, "1"); err == nil || get().State != "queued" || len(daemon.writes) != 0 {
		t.Fatalf("persistent network mismatch allowed domain definition: %v", err)
	}
	daemon.savedNetwork = network
	for index, state := range []string{"defining", "defined", "starting", "running"} {
		if index == 2 {
			daemon.liveNetwork = strings.ReplaceAll(network, vm.Address, "10.77.0.6")
			if err := run(api, "1"); err == nil || get().State != "defined" {
				t.Fatalf("live network mismatch allowed domain start: %v", err)
			}
			daemon.liveNetwork = network
		}
		err := run(api, "1")
		if (err != nil) != (index%2 == 0) || get().State != state {
			t.Fatalf("creation step %d: state=%s %v", index, get().State, err)
		}
	}
	vm = get()
	var configured kvmapp.KVMDomain
	if err := xml.Unmarshal(daemon.spec, &configured); err != nil || len(configured.Networks) != 1 ||
		configured.Networks[0].MAC == nil || configured.Networks[0].MAC.Address != vm.MAC {
		t.Fatalf("generated interface does not contain reserved MAC: %v %+v", err, configured.Networks)
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE cmp_kvm_placements
		SET created_at=clock_timestamp()-interval '3 minutes',expires_at=clock_timestamp()-interval '2 minutes'
		WHERE placement_id=$1`, placement.ID); err != nil {
		t.Fatal(err)
	}
	occupiedAddress := fixtureKVMPlacement()
	occupiedAddress.IPv4 = vm.Address
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "POST", fixtureVMPlacementPath,
		"", "bound-ip-outlived-hold", occupiedAddress), 409, nil)
	revision := func(v kvm.VM) string { return strconv.Quote(strconv.FormatInt(v.Revision, 10)) }
	request := func(token, rev string, handler http.Handler) *httptest.ResponseRecorder {
		return kvmRequest(t, handler, token, "POST", path+"/shutdown", rev, "", nil)
	}
	expectKVM(t, request(fixtureKVMGuest, revision(vm), first), 403, nil)
	expectKVM(t, request(fixtureKVMAgent, revision(vm), first), 403, nil)
	expectKVM(t, request(fixtureKVMSecondOperator, "", first), 412, nil)

	replies := make([]*httptest.ResponseRecorder, 8)
	var wg sync.WaitGroup
	for index := range replies {
		handler := first
		if index%2 != 0 {
			handler = second
		}
		wg.Go(func() { replies[index] = request(fixtureKVMSecondOperator, revision(vm), handler) })
	}
	wg.Wait()
	accepted := 0
	for _, response := range replies {
		if response.Code != 412 {
			expectKVM(t, response, 202, nil)
			if response.Header().Get("Location") != path {
				t.Fatal("shutdown receipt did not link its VM")
			}
			accepted++
		}
	}
	vm = get()
	if accepted != 1 || vm.State != "shutdown_requested" {
		t.Fatalf("concurrent request did not produce one pending shutdown: %d %+v", accepted, vm)
	}
	var work kvm.VMWork
	expectKVM(t, kvmRequest(t, second, fixtureKVMAgent, "POST", poll, "", "", agent), 200, &work)
	if work.Job != nil {
		t.Fatal("shutdown was visible to agent before approval")
	}
	approveKVMVM(t, second, vm, 200)
	vm = get()

	// 撤销后恢复权限应产生新代次，旧的申请授权仍然无效。
	if _, err := db.ExecContext(t.Context(), `UPDATE cmp_memberships SET active=false WHERE principal_id='operator-2'`); err != nil {
		t.Fatal(err)
	}
	cmd := kvm.VMCommand{AgentIdentity: agent, Revision: vm.Revision}
	expectKVM(t, kvmRequest(t, first, fixtureKVMAgent, "POST", job+"/arm", "", "", cmd), 403, nil)
	if _, err := db.ExecContext(t.Context(), `UPDATE cmp_memberships SET active=true WHERE principal_id='operator-2'`); err != nil {
		t.Fatal(err)
	}
	expectKVM(t, kvmRequest(t, second, fixtureKVMAgent, "POST", job+"/arm", "", "", cmd), 403, nil)
	expectKVM(t, kvmRequest(t, first, fixtureKVMReviewer, "POST", path+"/review", revision(vm), "",
		kvm.VMReview{Decision: "reject"}), 200, &vm)
	if vm.State != "running" {
		t.Fatalf("reject changed VM power state: %+v", vm)
	}
	expectKVM(t, request(fixtureKVMSecondOperator, revision(vm), second), 202, &vm)
	vm = approveKVMVM(t, first, vm, 200)

	// 宿主机换绑并进入排空后，新代理可执行针对原代次 VM 的关机。
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "PATCH", fixtureVMHostPath, `"1"`, "",
		kvm.HostChange{Action: "rebind", Agent: "replacement-agent"}), 200, nil)
	expectKVM(t, kvmRequest(t, first, fixtureKVMAgent, "POST", poll, "", "", agent), 403, nil)
	api.Token = fixtureKVMReplacement
	agent.Generation = 2
	daemon.transient = true
	if err := run(api, "2"); err == nil || get().State != "shutdown_queued" {
		t.Fatalf("transient domain obtained shutdown grant: %v", err)
	}
	daemon.transient = false
	withACPI := daemon.spec
	daemon.spec, err = xml.Marshal(kvmapp.DesiredKVMDomain(vm))
	if err != nil {
		t.Fatal(err)
	}
	err = run(api, "2")
	sampled := get()
	if err == nil || sampled.State != "shutdown_queued" || sampled.Observation != "mismatch" {
		t.Fatalf("running domain without ACPI was not blocked and recorded: %v %+v", err, sampled)
	}
	daemon.spec = withACPI
	errs := make([]error, 8)
	for i := range errs {
		wg.Go(func() { errs[i] = run(api, "2") })
	}
	wg.Wait()
	vm = get()
	if vm.State != "stopping" || !slices.Equal(daemon.writes, []string{"define", "start", "shutdown"}) {
		t.Fatalf("shutdown was missed, repeated, or completed ahead of observation: %+v %v %v", vm, daemon.writes, errs)
	}
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "PATCH", fixtureVMHostPath, `"2"`, "",
		kvm.HostChange{Action: "rebind", Agent: "host-one-agent"}), 409, nil)
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "DELETE", fixtureVMPlacementPath+"/"+placement.ID, "", "", nil), 409, nil)

	old := daemon.spec
	foreign := kvmapp.DesiredKVMDomain(vm)
	foreign.Metadata.Owner.Placement += "-foreign"
	daemon.spec, err = xml.Marshal(foreign)
	if err != nil {
		t.Fatal(err)
	}
	daemon.running = false
	if err := run(api, "2"); err != nil || get().Observation != "mismatch" {
		t.Fatalf("foreign offline domain was accepted: %v %+v", err, get())
	}
	daemon.spec = old
	if _, err := db.ExecContext(t.Context(), `ALTER TABLE cmp_kvm_events ADD CONSTRAINT reject_stopped
		CHECK (action<>'vm_stopped') NOT VALID`); err != nil {
		t.Fatal(err)
	}
	if err := run(api, "2"); err == nil || get().State != "stopping" {
		t.Fatalf("terminal state escaped audit transaction: %v %+v", err, get())
	}
	if _, err := db.ExecContext(t.Context(), `ALTER TABLE cmp_kvm_events DROP CONSTRAINT reject_stopped`); err != nil {
		t.Fatal(err)
	}
	if err := run(api, "2"); err != nil || get().State != "stopped" {
		t.Fatalf("matching persistent offline domain did not complete shutdown: %v %+v", err, get())
	}
	expectKVM(t, request(fixtureKVMSecondOperator, revision(get()), second), 409, nil)
	var retained kvm.Placement
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "GET", fixtureVMPlacementPath+"/"+placement.ID, "", "", nil), 200, &retained)
	if retained.State != "bound" || daemon.id != vm.ID || !slices.Equal(daemon.writes, []string{"define", "start", "shutdown"}) {
		t.Fatalf("shutdown released allocation or modified the domain: %+v %v", retained, daemon.writes)
	}
	var outcomeCount int
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM cmp_kvm_events
		WHERE placement_id=$1 AND action IN ('vm_shutdown','vm_stopped')`, placement.ID).Scan(&outcomeCount); err != nil || outcomeCount != 2 {
		t.Fatalf("shutdown audit not exactly once: %d %v", outcomeCount, err)
	}
	stopped := get()
	t.Run("retirement", func(t *testing.T) {
		checkKVMRetirement(t, db, first, second, api, daemon, stopped, placement)
	})
}
