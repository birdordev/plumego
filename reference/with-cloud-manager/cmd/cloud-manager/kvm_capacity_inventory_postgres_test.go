package main

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"with-cloud-manager/internal/kvm"
)

func checkKVMResidentCapacity(t *testing.T, first, second http.Handler, api kvmVMAPI, daemon *fakeKVMDaemon, vm kvm.VM) {
	t.Helper()
	inventoryPath := fixtureVMHostPath + "/vm-inventory"
	capacityPath := fixtureVMHostPath + "/capacity"
	unknown := "b0113615-a49b-426a-9160-5742d69004ef"
	query := kvm.VMInventoryQuery{
		AgentIdentity: kvm.AgentIdentity{UUID: fixtureKVMUUID, Generation: 1},
		Domains:       []string{vm.ID, unknown},
	}
	var inventory kvm.VMInventory
	expectKVM(t, kvmRequest(t, first, fixtureKVMAgent, "POST", inventoryPath, "", "", query), 200, &inventory)
	if len(inventory.VMs) != 1 || inventory.VMs[0] != vm {
		t.Fatalf("incorrect resident domain inventory: %+v", inventory)
	}
	for _, token := range []string{fixtureKVMGuest, fixtureKVMReplacement, fixtureKVMAdmin} {
		expectKVM(t, kvmRequest(t, first, token, "POST", inventoryPath, "", "", query), 403, nil)
	}
	expectKVM(t, kvmRequest(t, first, fixtureKVMAgent, "POST", strings.Replace(inventoryPath, "/compute/", "/other/", 1),
		"", "", query), 403, nil)
	dup := query
	dup.Domains = []string{vm.ID, vm.ID}
	expectKVM(t, kvmRequest(t, second, fixtureKVMAgent, "POST", inventoryPath, "", "", dup), 400, nil)

	observe := func(generation, token string, probe kvmProbe, want kvm.Capacity) {
		t.Helper()
		var output bytes.Buffer
		err := observeKVMHost(t.Context(), api.target, vm.Host, generation, token,
			strings.NewReader(`{"vcpu":4,"memory_mib":4096,"disk_mib":16384}`), &output, api.transport, probe)
		if err != nil {
			t.Fatalf("sampling a host with a resident VM: %v", err)
		}
		var host kvm.Host
		expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "GET", fixtureVMHostPath, "", "", nil), 200, &host)
		if host.Capacity != want {
			t.Fatalf("capacity = %+v, want %+v", host.Capacity, want)
		}
	}
	budget := kvm.Capacity{VCPU: 4, MemoryMiB: 4096, DiskMiB: 16384}
	observe("1", fixtureKVMAgent, daemon.runner(t).kvmProbe, budget)
	request := fixtureKVMPlacement()
	request.VCPU = 3
	var extra kvm.Placement
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "POST", fixtureVMPlacementPath, "", "resident-room", request), 201, &extra)
	expectKVM(t, kvmRequest(t, first, fixtureKVMSecondOperator, "POST", fixtureVMPlacementPath, "", "resident-no-room",
		fixtureKVMPlacement()), 409, nil)
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "DELETE", fixtureVMPlacementPath+"/"+extra.ID, "", "", nil), 200, nil)

	observe("1", fixtureKVMAgent, fixtureKVMProbe(t, fixtureKVMUUID, vm.ID+"\n"+unknown, 64_000), kvm.Capacity{})
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "POST", fixtureVMPlacementPath, "", "resident-foreign",
		fixtureKVMPlacement()), 409, nil)
	original := daemon.spec
	daemon.spec = bytes.Replace(original, []byte("<memory "), []byte(`<maxMemory unit="KiB" slots="2">2097152</maxMemory><memory `), 1)
	if bytes.Equal(original, daemon.spec) {
		t.Fatal("fixture did not change memory ceiling")
	}
	observe("1", fixtureKVMAgent, daemon.runner(t).kvmProbe, kvm.Capacity{})
	daemon.spec = original

	var rebound kvm.Host
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "PATCH", fixtureVMHostPath, `"1"`, "",
		kvm.HostChange{Action: "rebind", Agent: "replacement-agent"}), 200, &rebound)
	expectKVM(t, kvmRequest(t, second, fixtureKVMAgent, "POST", inventoryPath, "", "", query), 403, nil)
	expectKVM(t, kvmRequest(t, second, fixtureKVMReplacement, "POST", inventoryPath, "", "", query), 412, nil)
	stale := kvm.Report{UUID: fixtureKVMUUID, Generation: rebound.Generation, Sequence: 1, Capacity: budget,
		Inventory: &kvm.CapacityInventory{HostRevision: 1, Domains: []string{vm.ID}}}
	expectKVM(t, kvmRequest(t, second, fixtureKVMReplacement, "PUT", capacityPath, "", "", stale), 412, nil)
	stale.Inventory.HostRevision = rebound.Revision
	stale.Inventory.Domains = query.Domains
	expectKVM(t, kvmRequest(t, second, fixtureKVMReplacement, "PUT", capacityPath, "", "", stale), 412, nil)
	observe("2", fixtureKVMReplacement, daemon.runner(t).kvmProbe, budget)
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "PATCH", fixtureVMHostPath, `"2"`, "", kvm.HostChange{Action: "resume"}), 200, nil)
	observe("2", fixtureKVMReplacement, daemon.runner(t).kvmProbe, budget)
	request.VCPU = 4
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "POST", fixtureVMPlacementPath, "", "resident-old-hold", request), 409, nil)
	request.VCPU = 3
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "POST", fixtureVMPlacementPath, "", "resident-new-hold", request), 201, nil)
}
