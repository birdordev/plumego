package kvmtest

import (
	"net/http/httptest"
	"testing"

	"with-cloud-manager/internal/kvm"
)

func TestAcceptanceKVMHostLifecycle(t *testing.T) {
	db, first, second := prepareKVM(t)
	hosts := fixtureKVMSite + "/hosts"
	hostPath := hosts + "/host-one"
	placementPath := fixtureKVMSite + "/placements"
	call := func(token, method, path, match, key string, body any, want int, data any) *httptest.ResponseRecorder {
		t.Helper()
		response := kvmRequest(t, first, token, method, path, match, key, body)
		expectKVM(t, response, want, data)
		return response
	}
	spec := fixtureKVMHost()
	call(fixtureKVMOperator, "POST", hosts, "", "", spec, 403, nil)
	var initial kvm.Host
	registered := call(fixtureKVMAdmin, "POST", hosts, "", "", spec, 201, &initial)
	if registered.Header().Get("ETag") != `"1"` || registered.Header().Get("Location") != hostPath || initial.Generation != 1 {
		t.Fatalf("host registration receipt: %v %+v", registered.Header(), initial)
	}
	duplicate := spec
	duplicate.ID, duplicate.Agent = "duplicate", "replacement-agent"
	call(fixtureKVMAdmin, "POST", hosts, "", "", duplicate, 409, nil)
	call(fixtureKVMGuest, "GET", hostPath, "", "", nil, 403, nil)
	call(fixtureKVMAdmin, "GET", "/v1/tenants/lab/projects/other/kvm/sites/site-a/hosts/host-one", "", "", nil, 403, nil)
	call(fixtureKVMOperator, "POST", placementPath, "", "no-sample", fixtureKVMPlacement(), 409, nil)

	sample := fixtureKVMReport()
	call(fixtureKVMReplacement, "PUT", hostPath+"/capacity", "", "", sample, 403, nil)
	wrongHost := sample
	wrongHost.UUID = "feaa8a19-4765-4bb4-a383-3a1011ba0002"
	call(fixtureKVMAgent, "PUT", hostPath+"/capacity", "", "", wrongHost, 403, nil)
	var observed, replay kvm.Host
	call(fixtureKVMAgent, "PUT", hostPath+"/capacity", "", "", sample, 200, &observed)
	expectKVM(t, kvmRequest(t, second, fixtureKVMAgent, "PUT", hostPath+"/capacity", "", "", sample), 200, &replay)
	if observed.ReportedAt == nil || replay.ReportedAt == nil || !observed.ReportedAt.Equal(*replay.ReportedAt) {
		t.Fatalf("report replay renewed freshness: %+v %+v", observed, replay)
	}
	mutated := sample
	mutated.Capacity.VCPU++
	call(fixtureKVMAgent, "PUT", hostPath+"/capacity", "", "", mutated, 412, nil)
	budget := fixtureKVMPlacement()
	budget.RequiredHost, budget.FailureDomain, budget.ExcludedFailureDomain = spec.ID, "rack-1", "rack-2"
	budget.GPU, budget.GPUModel = 2, "a10"
	for _, mismatch := range []func(*kvm.PlacementInput){
		func(p *kvm.PlacementInput) { p.RequiredHost = "somewhere-else" },
		func(p *kvm.PlacementInput) { p.Network = "isolated" },
		func(p *kvm.PlacementInput) { p.StoragePool = "another-pool" },
		func(p *kvm.PlacementInput) { p.GPUModel = "l40" },
		func(p *kvm.PlacementInput) { p.FailureDomain = "rack-3" },
		func(p *kvm.PlacementInput) { p.FailureDomain, p.ExcludedFailureDomain = "", "rack-1" },
		func(p *kvm.PlacementInput) { p.MemoryMiB = 4097 },
		func(p *kvm.PlacementInput) { p.DiskMiB = 16385 },
	} {
		impossible := budget
		mismatch(&impossible)
		call(fixtureKVMOperator, "POST", placementPath, "", "misfit", impossible, 409, nil)
	}
	var hold kvm.Placement
	response := call(fixtureKVMOperator, "POST", placementPath, "", "valid", budget, 201, &hold)
	path := placementPath + "/" + hold.ID
	if hold.Host != spec.ID || hold.State != "held" || hold.Generation != 1 || response.Header().Get("Location") != path {
		t.Fatalf("invalid placement receipt: %+v", hold)
	}
	other := budget
	other.VCPU++
	call(fixtureKVMOperator, "POST", placementPath, "", "valid", other, 409, nil)
	call(fixtureKVMOperator, "POST", placementPath, "", "gpu-exhausted", budget, 409, nil)
	call(fixtureKVMGuest, "GET", path, "", "", nil, 403, nil)
	call(fixtureKVMSecondOperator, "DELETE", path, "", "", nil, 403, nil)
	call(fixtureKVMAdmin, "PATCH", hostPath, `"2"`, "", kvm.HostChange{Action: "drain"}, 412, nil)
	call(fixtureKVMAdmin, "PATCH", hostPath, `"1"`, "", kvm.HostChange{Action: "drain"}, 200, nil)
	call(fixtureKVMOperator, "POST", placementPath, "", "drained", fixtureKVMPlacement(), 409, nil)
	call(fixtureKVMAdmin, "PATCH", hostPath, `"2"`, "", kvm.HostChange{Action: "rebind", Agent: "replacement-agent"}, 200, nil)
	call(fixtureKVMAgent, "PUT", hostPath+"/capacity", "", "", sample, 403, nil)
	call(fixtureKVMReplacement, "PUT", hostPath+"/capacity", "", "", sample, 412, nil)
	call(fixtureKVMAdmin, "PATCH", hostPath, `"3"`, "", kvm.HostChange{Action: "resume"}, 200, nil)
	call(fixtureKVMOperator, "POST", placementPath, "", "requires-report", fixtureKVMPlacement(), 409, nil)
	sample.Generation = 2
	call(fixtureKVMReplacement, "PUT", hostPath+"/capacity", "", "", sample, 200, nil)
	call(fixtureKVMOperator, "POST", placementPath, "", "retained-gpu-hold", budget, 409, nil)
	if _, err := db.ExecContext(t.Context(), `UPDATE cmp_kvm_placements SET
		created_at=statement_timestamp()-interval '3 minutes',expires_at=statement_timestamp()-interval '1 minute'
		WHERE placement_id=$1`, hold.ID); err != nil {
		t.Fatal(err)
	}
	var expired kvm.Placement
	call(fixtureKVMOperator, "POST", placementPath, "", "valid", budget, 201, &expired)
	if expired.ID != hold.ID || expired.State != "expired" {
		t.Fatalf("expired key reallocated capacity: %+v", expired)
	}
	var moved kvm.Placement
	call(fixtureKVMOperator, "POST", placementPath, "", "new", budget, 201, &moved)
	if moved.Generation != 2 || moved.ID == hold.ID {
		t.Fatalf("new generation not recorded: %+v", moved)
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE cmp_kvm_placements SET
		expires_at=statement_timestamp()+interval '3 minutes' WHERE placement_id=$1`, hold.ID); err != nil {
		t.Fatal(err)
	}
	call(fixtureKVMOperator, "GET", path, "", "", nil, 200, &expired)
	if expired.State != "expired" {
		t.Fatalf("reclaimed hold revived after clock shift: %+v", expired)
	}
	call(fixtureKVMAdmin, "DELETE", placementPath+"/"+moved.ID, "", "", nil, 200, nil)
	if _, err := db.ExecContext(t.Context(), "UPDATE cmp_kvm_hosts SET reported_at=clock_timestamp()-interval '91 seconds'"); err != nil {
		t.Fatal(err)
	}
	call(fixtureKVMOperator, "POST", placementPath, "", "expired-report", fixtureKVMPlacement(), 409, nil)
	sample.Sequence++
	call(fixtureKVMReplacement, "PUT", hostPath+"/capacity", "", "", sample, 200, nil)
	if _, err := db.ExecContext(t.Context(), "UPDATE cmp_memberships SET active=false WHERE principal_id='replacement-agent'"); err != nil {
		t.Fatal(err)
	}
	call(fixtureKVMOperator, "POST", placementPath, "", "revoked-agent", fixtureKVMPlacement(), 409, nil)
	sample.Sequence++
	call(fixtureKVMReplacement, "PUT", hostPath+"/capacity", "", "", sample, 403, nil)
}
