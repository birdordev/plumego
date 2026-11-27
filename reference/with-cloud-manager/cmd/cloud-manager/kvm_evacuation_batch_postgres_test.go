//go:build linux || darwin

package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"with-cloud-manager/internal/kvm"
)

func TestAcceptanceKVMEvacuationBatch(t *testing.T) {
	db, first, second, _, vm1 := prepareKVMVMWithPool(t, &fixtureIPv4Pool)
	var hold kvm.Placement
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "POST", fixtureVMPlacementPath, "", "batch-source-two", fixtureKVMPlacement()), 201, &hold)
	var vm2 kvm.VM
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "POST", fixtureVMSitePath, "", "",
		kvm.VMInput{Placement: hold.ID, Volume: "boot-second"}), 202, &vm2)
	vm1 = startEvacuationSourceVM(t, first, vm1)
	vm2 = startEvacuationSourceVM(t, second, vm2)
	path := fixtureVMHostPath + "/fence/evacuation-batches"
	fence := fixtureVMHostPath + "/fence"
	backup := []byte("batch recovery image")
	image := testKVMImage(backup)
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", fixtureKVMSite+"/images", "", "", image), 201, nil)
	input := kvm.EvacuationBatchInput{ID: "rack-failover", SourceFenceGeneration: 2, Items: []kvm.EvacuationBatchItemInput{
		{VM: vm1.ID, Revision: vm1.Revision, Image: image.ID, Evidence: strings.Repeat("a", 64)},
		{VM: vm2.ID, Revision: vm2.Revision, Image: image.ID, Evidence: strings.Repeat("b", 64)},
	}}
	post := func(handler http.Handler, token, revision string, draft kvm.EvacuationBatchInput) *httptest.ResponseRecorder {
		t.Helper()
		return kvmRequest(t, handler, token, "POST", path, revision, "", draft)
	}
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", fence, `"1"`, "",
		kvm.HostFenceChange{Action: "request", UUID: fixtureKVMUUID}), 200, nil)
	expectKVM(t, post(second, fixtureKVMAdmin, `"2"`, input), 409, nil)
	expectKVM(t, kvmRequest(t, second, fixtureKVMReviewer, "POST", fence, `"2"`, "",
		kvm.HostFenceChange{Action: "confirm", UUID: fixtureKVMUUID, EvidenceSHA256: strings.Repeat("c", 64)}), 200, nil)
	expectKVM(t, post(first, fixtureKVMAdmin, `"2"`, input), 412, nil)
	expectKVM(t, post(first, fixtureKVMOperator, `"3"`, input), 403, nil)
	duplicate := input
	duplicate.Items = []kvm.EvacuationBatchItemInput{input.Items[0], input.Items[0]}
	expectKVM(t, post(first, fixtureKVMAdmin, `"3"`, duplicate), 400, nil)

	const destination = fixtureKVMSite + "/hosts/" + fixtureMigrationHost
	host, report := fixtureKVMHost(), fixtureKVMReport()
	host.ID, host.UUID, host.Agent, host.FailureDomain = fixtureMigrationHost, fixtureMigrationUUID, "replacement-agent", "rack-2"
	report.UUID, report.Capacity.VCPU = host.UUID, 1
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", fixtureKVMSite+"/hosts", "", "", host), 201, nil)
	expectKVM(t, kvmRequest(t, second, fixtureKVMReplacement, "PUT", destination+"/capacity", "", "", report), 200, nil)
	expectKVM(t, post(second, fixtureKVMAdmin, `"3"`, input), 409, nil)
	assertNoEvacuationBatch(t, db, vm1, vm2)

	report.Sequence++
	report.Capacity.VCPU = 2
	expectKVM(t, kvmRequest(t, first, fixtureKVMReplacement, "PUT", destination+"/capacity", "", "", report), 200, nil)
	checkEvacuationBatchRollback(t, db, func() { expectKVM(t, post(first, fixtureKVMAdmin, `"3"`, input), 503, nil) })
	assertNoEvacuationBatch(t, db, vm1, vm2)
	stale := input
	stale.Items = append([]kvm.EvacuationBatchItemInput(nil), input.Items...)
	stale.Items[1].Revision--
	expectKVM(t, post(second, fixtureKVMAdmin, `"3"`, stale), 412, nil)
	assertNoEvacuationBatch(t, db, vm1, vm2)

	var replies [4]*httptest.ResponseRecorder
	var group sync.WaitGroup
	for i := range replies {
		handler := first
		if i%2 == 1 {
			handler = second
		}
		group.Go(func() { replies[i] = post(handler, fixtureKVMAdmin, `"3"`, input) })
	}
	group.Wait()
	var batch kvm.EvacuationBatch
	expectKVM(t, replies[0], 202, &batch)
	location := path + "/" + input.ID
	if replies[0].Header().Get("Location") != location || batch.HostRevision != 3 || batch.SourceFenceGeneration != 2 ||
		len(batch.Items) != 2 || batch.Items[0].VM != vm1.ID || batch.Items[1].VM != vm2.ID {
		t.Fatalf("invalid batch receipt: %+v", batch)
	}
	for _, reply := range replies[1:] {
		var replay kvm.EvacuationBatch
		expectKVM(t, reply, 202, &replay)
		if !replay.RequestedAt.Equal(batch.RequestedAt) || len(replay.Items) != 2 ||
			replay.Items[0].Placement != batch.Items[0].Placement || replay.Items[1].Placement != batch.Items[1].Placement {
			t.Fatalf("batch replay allocated another target: %+v", replay)
		}
	}
	for _, vm := range []kvm.VM{vm1, vm2} {
		if automaticEvacuationHolds(t, db, vm.ID) != 1 {
			t.Fatalf("batch did not create exactly one hold for %s", vm.ID)
		}
	}
	expectKVM(t, post(second, fixtureKVMAdmin, `"3"`, stale), 409, nil)
	expectKVM(t, kvmRequest(t, first, fixtureKVMGuest, "GET", location, "", "", nil), 403, nil)
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "GET", strings.Replace(location, "/compute/", "/other/", 1), "", "", nil), 403, nil)
	reviewBatchItem(t, second, batch.Items[0], "approve")
	reviewBatchItem(t, first, batch.Items[1], "reject")
	verifyEvacuationBatchCLI(t, second, input, batch)

	var moved kvm.VM
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "GET", fixtureVMSitePath+"/"+vm1.ID, "", "", nil), 200, &moved)
	if moved.Host != fixtureMigrationHost || moved.Address != vm1.Address || moved.State != "queued" {
		t.Fatalf("approved VM was not queued on the target: %+v", moved)
	}
	var unchanged kvm.VM
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "GET", fixtureVMSitePath+"/"+vm2.ID, "", "", nil), 200, &unchanged)
	if vm2 != unchanged {
		t.Fatalf("review of another batch item changed the rejected VM: %+v", unchanged)
	}
	checkMigrationExecution(t, first, second, destination, moved, backup)
}

func assertNoEvacuationBatch(t *testing.T, db *sql.DB, vms ...kvm.VM) {
	t.Helper()
	var count int
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM cmp_kvm_evacuation_batches`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed batch left a receipt: count=%d err=%v", count, err)
	}
	for _, vm := range vms {
		if count := automaticEvacuationHolds(t, db, vm.ID); count != 0 {
			t.Fatalf("failed batch left %d holds for %s", count, vm.ID)
		}
	}
}

func checkEvacuationBatchRollback(t *testing.T, db *sql.DB, action func()) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), `ALTER TABLE cmp_kvm_evacuation_batch_items
		ADD CONSTRAINT fixture_block_second CHECK (position<>2)`); err != nil {
		t.Fatal(err)
	}
	action()
	if _, err := db.ExecContext(t.Context(), `ALTER TABLE cmp_kvm_evacuation_batch_items DROP CONSTRAINT fixture_block_second`); err != nil {
		t.Fatal(err)
	}
}

func reviewBatchItem(t *testing.T, handler http.Handler, m kvm.ColdMigration, decision string) {
	t.Helper()
	revision := strconv.Quote(strconv.FormatInt(m.VMRevision, 10))
	path := fixtureVMSitePath + "/" + m.VM + "/cold-migration/review"
	expectKVM(t, kvmRequest(t, handler, fixtureKVMReviewer, "POST", path, revision, "",
		kvm.ColdMigrationReview{ID: m.ID, Decision: decision}), 200, nil)
}

func verifyEvacuationBatchCLI(t *testing.T, handler http.Handler, input kvm.EvacuationBatchInput, original kvm.EvacuationBatch) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range []struct {
		command, last, token string
	}{
		{"request-evacuation-batch", "3", fixtureKVMAdmin},
		{"get-evacuation-batch", input.ID, fixtureKVMOperator},
	} {
		var output bytes.Buffer
		args := []string{call.command, "lab", "compute", "site-a", "host-one", call.last}
		if err := runKVMClient(t.Context(), args, server.URL, call.token, bytes.NewReader(body), &output, server.Client().Transport); err != nil {
			t.Fatal(err)
		}
		var batch kvm.EvacuationBatch
		if err := json.Unmarshal(output.Bytes(), &batch); err != nil || len(batch.Items) != 2 ||
			!batch.RequestedAt.Equal(original.RequestedAt) || batch.Items[0].Status != "approved" || batch.Items[1].Status != "rejected" {
			t.Fatalf("unexpected batch CLI response: %s, %v", output.String(), err)
		}
	}
}
