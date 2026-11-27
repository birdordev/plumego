//go:build linux || darwin

package main

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"with-cloud-manager/internal/kvm"
)

const evacuationCandidatesPath = evacuationFixtureFencePath + "/evacuation-candidates"
const evacuationBatchesPath = evacuationFixtureFencePath + "/evacuation-batches"

func checkEvacuationCandidates(t *testing.T, first, second http.Handler, vm1, vm2 kvm.VM) {
	t.Helper()
	var initial, next kvm.EvacuationCandidatePage
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "GET", evacuationCandidatesPath+"?limit=1", "", "", nil), 200, &initial)
	if initial.Mode != "plan" || initial.Revision != 1 || initial.Generation != 2 || len(initial.Items) != 1 || initial.Next != initial.Items[0].VM {
		t.Fatalf("invalid evacuation candidate page: %+v", initial)
	}
	expectKVM(t, kvmRequest(t, second, fixtureKVMAdmin, "GET", evacuationCandidatesPath+"?limit=1&after="+url.QueryEscape(initial.Next), "", "", nil), 200, &next)
	if len(next.Items) != 1 || next.Next != "" || next.Mode != initial.Mode || next.Revision != initial.Revision || next.Generation != initial.Generation {
		t.Fatalf("invalid evacuation candidate continuation: %+v", next)
	}
	for _, vm := range []kvm.VM{vm1, vm2} {
		got := initial.Items[0]
		if got.VM != vm.ID {
			got = next.Items[0]
		}
		if got.VM != vm.ID || got.Revision != vm.Revision || got.Address != vm.Address || got.Network != vm.Network || got.DiskMiB != vm.Capacity.DiskMiB {
			t.Fatalf("invalid snapshot for %s: %+v", vm.ID, got)
		}
	}
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "GET", evacuationCandidatesPath, "", "", nil), 403, nil)
	for _, path := range []string{evacuationCandidatesPath, evacuationBatchesPath, evacuationFixturePlansPath} {
		expectKVM(t, kvmRequest(t, first, fixtureKVMGuest, "GET", path, "", "", nil), 403, nil)
		other := strings.Replace(path, "/projects/compute/", "/projects/other/", 1)
		expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "GET", other, "", "", nil), 403, nil)
		for _, query := range []string{"?limit=101", "?limit=1&limit=2", "?after=invalid", "?site=site-a"} {
			expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "GET", path+query, "", "", nil), 400, nil)
		}
	}
}

func assertEvacuationInventory(t *testing.T, handler http.Handler, mode string, revision int64, candidates int, batch, status, plan string) {
	t.Helper()
	var sources kvm.EvacuationCandidatePage
	expectKVM(t, kvmRequest(t, handler, fixtureKVMAdmin, "GET", evacuationCandidatesPath, "", "", nil), 200, &sources)
	if sources.Mode != mode || sources.Revision != revision || sources.Generation != 2 || sources.Items == nil || len(sources.Items) != candidates {
		t.Fatalf("unexpected source candidates: %+v", sources)
	}
	var plans, batches kvm.InventoryPage[kvm.EvacuationRecord]
	expectKVM(t, kvmRequest(t, handler, fixtureKVMOperator, "GET", evacuationFixturePlansPath, "", "", nil), 200, &plans)
	if len(plans.Items) != 1 || plans.Items[0].ID != plan || plans.Items[0].Status != status || plans.Items[0].BatchID != batch {
		t.Fatalf("unexpected plan history: %+v", plans)
	}
	expectKVM(t, kvmRequest(t, handler, fixtureKVMOperator, "GET", evacuationBatchesPath, "", "", nil), 200, &batches)
	if batch == "" {
		if batches.Items == nil || len(batches.Items) != 0 {
			t.Fatalf("unexpected batch before dispatch: %+v", batches)
		}
		return
	}
	if len(batches.Items) != 1 || batches.Items[0].ID != batch {
		t.Fatalf("unexpected batch history: %+v", batches)
	}
	record := batches.Items[0]
	after := url.QueryEscape(record.Date.UTC().Format(time.RFC3339Nano) + "|" + record.ID)
	expectKVM(t, kvmRequest(t, handler, fixtureKVMOperator, "GET", evacuationBatchesPath+"?after="+after, "", "", nil), 200, &batches)
	if batches.Items == nil || len(batches.Items) != 0 {
		t.Fatalf("unexpected history before the first batch: %+v", batches)
	}
}
