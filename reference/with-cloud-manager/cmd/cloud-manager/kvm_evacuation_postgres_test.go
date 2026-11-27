//go:build linux || darwin

package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"with-cloud-manager/internal/kvm"
)

func TestAcceptanceKVMEvacuation(t *testing.T) {
	pool := fixtureIPv4Pool
	pool.Last = pool.First
	db, first, second, source, vm := prepareKVMVMWithPool(t, &pool)
	if _, err := db.ExecContext(t.Context(), `INSERT INTO cmp_principals (tenant_id,principal_id,kind,issuer,subject)
		VALUES ('lab','spare-agent','service','kvm-test','spare-agent');
		INSERT INTO cmp_memberships (tenant_id,project_id,principal_id,role) VALUES ('lab','compute','spare-agent','viewer')`); err != nil {
		t.Fatal(err)
	}
	const destination = fixtureKVMSite + "/hosts/" + fixtureMigrationHost
	host, report := fixtureKVMHost(), fixtureKVMReport()
	host.ID, host.UUID, host.Agent = fixtureMigrationHost, fixtureMigrationUUID, "replacement-agent"
	report.UUID = host.UUID
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", fixtureKVMSite+"/hosts", "", "", host), 201, nil)
	expectKVM(t, kvmRequest(t, second, fixtureKVMReplacement, "PUT", destination+"/capacity", "", "", report), 200, nil)
	vm = startEvacuationSourceVM(t, first, vm)
	revision := strconv.Quote(strconv.FormatInt(vm.Revision, 10))
	vmPath := fixtureVMSitePath + "/" + vm.ID
	migrationPath, fencePath := vmPath+"/cold-migration", fixtureVMHostPath+"/fence"

	spec := fixtureKVMPlacement()
	spec.RequiredHost = fixtureMigrationHost
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", fixtureVMPlacementPath, "", "ordinary-no-address", spec), 409, nil)
	spec.EvacuationVM = vm.ID
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", fixtureVMPlacementPath, "", "before-fence", spec), 409, nil)
	fence := func(actor, version, action, evidence, replacement string, status int) {
		t.Helper()
		expectKVM(t, kvmRequest(t, first, actor, "POST", fencePath, version, "",
			kvm.HostFenceChange{Action: action, UUID: fixtureKVMUUID, EvidenceSHA256: evidence, ReplacementAgent: replacement}), status, nil)
	}
	fence(fixtureKVMAdmin, `"1"`, "request", "", "", 200)
	expectKVM(t, kvmRequest(t, second, fixtureKVMAdmin, "POST", fixtureVMPlacementPath, "", "pending-fence", spec), 409, nil)
	fence(fixtureKVMReviewer, `"2"`, "confirm", strings.Repeat("a", 64), "", 200)
	var target kvm.Placement
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "POST", fixtureVMPlacementPath, "", "operator-evacuation", spec), 403, nil)
	expectKVM(t, kvmRequest(t, second, fixtureKVMAdmin, "POST", fixtureVMPlacementPath, "", "evacuation-hold", spec), 201, &target)
	if target.Address != "" || target.EvacuationVM != vm.ID {
		t.Fatalf("evacuation preallocation changed IP ownership: %+v", target)
	}
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", fixtureVMSitePath, "", "",
		kvm.VMInput{Placement: target.ID, Volume: "boot-unused"}), 409, nil)

	backup := []byte("preexisting recovery backup")
	image := testKVMImage(backup)
	expectKVM(t, kvmRequest(t, second, fixtureKVMAdmin, "POST", fixtureKVMSite+"/images", "", "", image), 201, nil)
	input := kvm.ColdMigrationInput{ID: "evacuation-1", Placement: target.ID, Image: image.ID,
		Evidence: strings.Repeat("b", 64), SourceFenceGeneration: 1}
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", migrationPath, revision, "", input), 412, nil)
	input.SourceFenceGeneration = 2
	expectKVM(t, kvmRequest(t, second, fixtureKVMAdmin, "POST", migrationPath, revision, "", input), 202, nil)
	review := func() *httptest.ResponseRecorder {
		t.Helper()
		return kvmRequest(t, second, fixtureKVMReviewer, "POST", migrationPath+"/review", revision, "",
			kvm.ColdMigrationReview{ID: input.ID, Decision: "approve"})
	}
	checkMigrationRollback(t, db, first, vm, target, review)
	assertEvacuationSource(t, second, source, "bound", false)
	expectKVM(t, review(), 200, nil)
	expectKVM(t, review(), 200, nil)
	assertEvacuationSource(t, first, source, "released", true)
	spec.EvacuationVM = ""
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", fixtureVMPlacementPath, "", "transferred-address-unavailable", spec), 409, nil)

	var runningOnTarget kvm.VM
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "GET", vmPath, "", "", nil), 200, &runningOnTarget)
	if runningOnTarget.ID != vm.ID || runningOnTarget.Address != vm.Address || runningOnTarget.MAC != vm.MAC ||
		runningOnTarget.Host != fixtureMigrationHost || runningOnTarget.State != "queued" {
		t.Fatalf("evacuation lost VM or IPv4 identity: %+v", runningOnTarget)
	}
	checkMigrationExecution(t, first, second, destination, runningOnTarget, backup)
	expectKVM(t, kvmRequest(t, second, fixtureKVMAgent, "POST", fixtureVMHostPath+"/vm-work", "", "",
		kvm.AgentIdentity{UUID: fixtureKVMUUID, Generation: 1}), 412, nil)

	fence(fixtureKVMAdmin, `"3"`, "request-rejoin", strings.Repeat("c", 64), "spare-agent", 200)
	fence(fixtureKVMReviewer, `"4"`, "approve-rejoin", "", "", 409)
	var work kvm.HostFenceWorkPage
	expectKVM(t, kvmRequest(t, second, fixtureKVMReviewer, "GET", fencePath+"/work", "", "", nil), 200, &work)
	if len(work.Items) != 1 || work.Items[0].State != "cleanup_required" ||
		work.Items[0].VM != vm.ID || work.Items[0].EvacuationID != input.ID {
		t.Fatalf("evacuation omitted the cleanup blocker: %+v", work)
	}
	proof := kvm.EvacuationCleanup{VM: vm.ID, ID: input.ID, Evidence: strings.Repeat("d", 64)}
	expectKVM(t, kvmRequest(t, first, fixtureKVMReviewer, "POST", fencePath+"/cleanup", `"3"`, "", proof), 412, nil)
	expectKVM(t, kvmRequest(t, second, fixtureKVMAdmin, "POST", fencePath+"/cleanup", `"4"`, "", proof), 403, nil)
	confirmEvacuationViaCLI(t, second, proof)
	expectKVM(t, kvmRequest(t, first, fixtureKVMReviewer, "POST", fencePath+"/cleanup", `"4"`, "", proof), 200, nil)
	expectKVM(t, kvmRequest(t, first, fixtureKVMReviewer, "GET", fencePath+"/work", "", "", nil), 200, &work)
	if len(work.Items) != 0 {
		t.Fatalf("source cleanup still prevents rejoin: %+v", work)
	}
	fence(fixtureKVMReviewer, `"4"`, "approve-rejoin", "", "", 200)
	expectKVM(t, kvmRequest(t, second, fixtureKVMReviewer, "POST", fencePath+"/cleanup", `"4"`, "", proof), 412, nil)
}

func startEvacuationSourceVM(t *testing.T, handler http.Handler, vm kvm.VM) kvm.VM {
	t.Helper()
	vm = approveKVMVM(t, handler, vm, 200)
	path := fixtureVMHostPath + "/vm-work/" + vm.ID
	agent := kvm.AgentIdentity{UUID: fixtureKVMUUID, Generation: 1}
	for _, state := range []string{"defined", "running"} {
		expectKVM(t, kvmRequest(t, handler, fixtureKVMAgent, "POST", path+"/arm", "", "",
			kvm.VMCommand{AgentIdentity: agent, Revision: vm.Revision}), 200, &vm)
		expectKVM(t, kvmRequest(t, handler, fixtureKVMAgent, "PUT", path+"/observation", "", "",
			kvm.VMObservation{VMCommand: kvm.VMCommand{AgentIdentity: agent, Revision: vm.Revision}, Observed: state}), 200, &vm)
	}
	return vm
}

func assertEvacuationSource(t *testing.T, handler http.Handler, source kvm.Placement, state string, retained bool) {
	t.Helper()
	var actual kvm.Placement
	expectKVM(t, kvmRequest(t, handler, fixtureKVMOperator, "GET", fixtureVMPlacementPath+"/"+source.ID, "", "", nil), 200, &actual)
	if actual.State != state || actual.Address != source.Address || actual.BootVolumeRetained != retained {
		t.Fatalf("unexpected source hold: %+v", actual)
	}
}

func confirmEvacuationViaCLI(t *testing.T, handler http.Handler, proof kvm.EvacuationCleanup) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	raw, err := json.Marshal(proof)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	args := []string{"confirm-evacuation-cleanup", "lab", "compute", "site-a", "host-one", "4"}
	if err := runKVMClient(t.Context(), args, server.URL, fixtureKVMReviewer, bytes.NewReader(raw), &output, server.Client().Transport); err != nil {
		t.Fatal(err)
	}
	var result kvm.ColdMigration
	if err := json.Unmarshal(output.Bytes(), &result); err != nil || result.CleanupAt == nil ||
		result.CleanupSHA256 != proof.Evidence || result.CleanupReviewer != "reviewer" {
		t.Fatalf("invalid CLI cleanup receipt: %s, %v", output.String(), err)
	}
}
