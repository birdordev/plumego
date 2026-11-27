//go:build linux || darwin

package main

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"with-cloud-manager/internal/kvm"
)

func TestAcceptanceKVMAutomaticEvacuationPlacement(t *testing.T) {
	pool := fixtureIPv4Pool
	pool.Last = pool.First
	db, first, second, _, vm := prepareKVMVMWithPool(t, &pool)
	vm = startEvacuationSourceVM(t, first, vm)
	revision := strconv.Quote(strconv.FormatInt(vm.Revision, 10))
	vmPath := fixtureVMSitePath + "/" + vm.ID
	path := vmPath + "/cold-migration"
	fencePath := fixtureVMHostPath + "/fence"
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", fencePath, `"1"`, "",
		kvm.HostFenceChange{Action: "request", UUID: fixtureKVMUUID}), 200, nil)
	assertColdMigrationEligibility(t, second, vmPath, vm, "unavailable", 0)
	expectKVM(t, kvmRequest(t, second, fixtureKVMReviewer, "POST", fencePath, `"2"`, "",
		kvm.HostFenceChange{Action: "confirm", UUID: fixtureKVMUUID, EvidenceSHA256: strings.Repeat("a", 64)}), 200, nil)
	assertColdMigrationEligibility(t, first, vmPath, vm, "evacuation", 2)
	backup := []byte("evacuation image")
	image := testKVMImage(backup)
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", fixtureKVMSite+"/images", "", "", image), 201, nil)
	input := kvm.ColdMigrationInput{ID: "automatic-first", Image: image.ID, Evidence: strings.Repeat("b", 64), SourceFenceGeneration: 2}
	request := func(handler http.Handler, token string, plan kvm.ColdMigrationInput) *httptest.ResponseRecorder {
		t.Helper()
		return kvmRequest(t, handler, token, "POST", path, revision, "", plan)
	}
	review := func(id, decision string, status int) {
		t.Helper()
		expectKVM(t, kvmRequest(t, second, fixtureKVMReviewer, "POST", path+"/review", revision, "",
			kvm.ColdMigrationReview{ID: id, Decision: decision}), status, nil)
	}

	checkSameDomainIgnored(t, db, first)
	expectKVM(t, request(second, fixtureKVMOperator, input), 403, nil)
	expectKVM(t, request(second, fixtureKVMAdmin, input), 409, nil)
	if holds := automaticEvacuationHolds(t, db, vm.ID); holds != 0 {
		t.Fatalf("no-capacity request created %d holds", holds)
	}

	const destination = fixtureKVMSite + "/hosts/" + fixtureMigrationHost
	host, report := fixtureKVMHost(), fixtureKVMReport()
	host.ID, host.UUID, host.Agent, host.FailureDomain = fixtureMigrationHost, fixtureMigrationUUID, "replacement-agent", "rack-2"
	report.UUID = host.UUID
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", fixtureKVMSite+"/hosts", "", "", host), 201, nil)
	expectKVM(t, kvmRequest(t, second, fixtureKVMReplacement, "PUT", destination+"/capacity", "", "", report), 200, nil)
	verifyLockedEvacuationTarget(t, db, func() { expectKVM(t, request(second, fixtureKVMAdmin, input), 409, nil) })
	checkAutomaticEvacuationDraftRollback(t, db, func() { expectKVM(t, request(second, fixtureKVMAdmin, input), 503, nil) })
	if holds := automaticEvacuationHolds(t, db, vm.ID); holds != 0 {
		t.Fatalf("failed request created %d holds", holds)
	}

	var replies [6]*httptest.ResponseRecorder
	var group sync.WaitGroup
	for i := range replies {
		handler := first
		if i%2 == 1 {
			handler = second
		}
		group.Go(func() { replies[i] = request(handler, fixtureKVMAdmin, input) })
	}
	group.Wait()
	var firstDraft kvm.ColdMigration
	expectKVM(t, replies[0], 202, &firstDraft)
	for _, reply := range replies[1:] {
		var draft kvm.ColdMigration
		expectKVM(t, reply, 202, &draft)
		if draft.Placement != firstDraft.Placement || !draft.RequestedAt.Equal(firstDraft.RequestedAt) {
			t.Fatalf("concurrent request allocated another target: %+v", draft)
		}
	}
	if firstDraft.TargetHost != fixtureMigrationHost || !firstDraft.AutomaticPlacement || firstDraft.Placement == "" ||
		automaticEvacuationHolds(t, db, vm.ID) != 1 {
		t.Fatalf("automatic placement chose an invalid target or created surplus holds: %+v", firstDraft)
	}
	var hold kvm.Placement
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "GET", fixtureVMPlacementPath+"/"+firstDraft.Placement, "", "", nil), 200, &hold)
	if hold.Address != "" || hold.State != "held" || hold.Capacity != vm.Capacity || hold.EvacuationVM != vm.ID {
		t.Fatalf("invalid automatic hold: %+v", hold)
	}
	explicit := input
	explicit.Placement = firstDraft.Placement
	expectKVM(t, request(first, fixtureKVMAdmin, explicit), 409, nil)

	review(firstDraft.ID, "reject", 200)
	review(firstDraft.ID, "reject", 200)
	expectKVM(t, kvmRequest(t, second, fixtureKVMAdmin, "GET", fixtureVMPlacementPath+"/"+hold.ID, "", "", nil), 200, &hold)
	if hold.State != "released" {
		t.Fatalf("rejected automatic hold still consumes capacity: %+v", hold)
	}
	input.ID = "automatic-approved"
	expectKVM(t, request(first, fixtureKVMAdmin, input), 202, nil)
	review(input.ID, "approve", 200)
	var moved kvm.VM
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "GET", fixtureVMSitePath+"/"+vm.ID, "", "", nil), 200, &moved)
	if moved.Host != fixtureMigrationHost || moved.State != "queued" || moved.Address != vm.Address || moved.MAC != vm.MAC {
		t.Fatalf("approved automatic relocation lost VM identity: %+v", moved)
	}
	assertEvacuationHistory(t, first, vmPath, input.ID, firstDraft.ID)
	checkMigrationExecution(t, first, second, destination, moved, backup)
}

func assertEvacuationHistory(t *testing.T, handler http.Handler, vmPath, latestID, olderID string) {
	t.Helper()
	path := vmPath + "/cold-migrations"
	var latest, older kvm.InventoryPage[kvm.ColdMigration]
	expectKVM(t, kvmRequest(t, handler, fixtureKVMOperator, "GET", path+"?limit=1", "", "", nil), 200, &latest)
	if len(latest.Items) != 1 || latest.Items[0].ID != latestID || latest.Items[0].Status != "approved" || latest.Next == "" {
		t.Fatalf("unexpected recent evacuation history: %+v", latest)
	}
	expectKVM(t, kvmRequest(t, handler, fixtureKVMOperator, "GET", path+"?limit=1&after="+url.QueryEscape(latest.Next), "", "", nil), 200, &older)
	if len(older.Items) != 1 || older.Items[0].ID != olderID || older.Items[0].Status != "rejected" || older.Next != "" {
		t.Fatalf("unexpected older evacuation history: %+v", older)
	}
	expectKVM(t, kvmRequest(t, handler, fixtureKVMGuest, "GET", path, "", "", nil), 403, nil)
	expectKVM(t, kvmRequest(t, handler, fixtureKVMAdmin, "GET", strings.Replace(path, "/compute/", "/other/", 1)+"/"+latestID, "", "", nil), 403, nil)
	expectKVM(t, kvmRequest(t, handler, fixtureKVMOperator, "GET", path+"?after=bad", "", "", nil), 400, nil)
}

func checkSameDomainIgnored(t *testing.T, db *sql.DB, handler http.Handler) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), `UPDATE cmp_memberships SET role='viewer' WHERE
		(tenant_id,project_id,principal_id)=('lab','compute','operator-2')`); err != nil {
		t.Fatal(err)
	}
	host, report := fixtureKVMHost(), fixtureKVMReport()
	host.ID, host.UUID, host.Agent = "host-00-same-rack", "feaa8a19-4765-4bb4-a383-3a1011ba0003", "operator-2"
	report.UUID = host.UUID
	expectKVM(t, kvmRequest(t, handler, fixtureKVMAdmin, "POST", fixtureKVMSite+"/hosts", "", "", host), 201, nil)
	expectKVM(t, kvmRequest(t, handler, fixtureKVMSecondOperator, "PUT", fixtureKVMSite+"/hosts/"+host.ID+"/capacity", "", "", report), 200, nil)
}

func verifyLockedEvacuationTarget(t *testing.T, db *sql.DB, action func()) {
	t.Helper()
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(t.Context(), `SELECT host_id FROM cmp_kvm_hosts WHERE host_id=$1 FOR UPDATE`, fixtureMigrationHost); err != nil {
		t.Fatal(err)
	}
	action()
}

func checkAutomaticEvacuationDraftRollback(t *testing.T, db *sql.DB, action func()) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), `ALTER TABLE cmp_kvm_cold_migrations ADD CONSTRAINT fixture_block_auto
		CHECK (NOT automatic_placement)`); err != nil {
		t.Fatal(err)
	}
	action()
	if _, err := db.ExecContext(t.Context(), `ALTER TABLE cmp_kvm_cold_migrations DROP CONSTRAINT fixture_block_auto`); err != nil {
		t.Fatal(err)
	}
}

func automaticEvacuationHolds(t *testing.T, db *sql.DB, vm string) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM cmp_kvm_placements WHERE evacuation_vm_id=$1::uuid`, vm).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
