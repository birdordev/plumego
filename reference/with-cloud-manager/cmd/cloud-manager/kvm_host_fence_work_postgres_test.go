package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"with-cloud-manager/internal/kvm"
)

func TestAcceptanceKVMHostFenceWork(t *testing.T) {
	_, first, second, original, defining := prepareKVMVM(t)
	const path = fixtureVMHostPath + "/fence/work"
	createVM := func(key string) kvm.VM {
		t.Helper()
		var hold kvm.Placement
		expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "POST", fixtureVMPlacementPath, "", key, fixtureKVMPlacement()), 201, &hold)
		var vm kvm.VM
		expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "POST", fixtureVMSitePath, "", "",
			kvm.VMInput{Placement: hold.ID, Volume: key}), 202, &vm)
		return vm
	}
	awaiting := createVM("boot-awaiting")
	queued := approveKVMVM(t, first, createVM("boot-queued"), 200)
	defining = approveKVMVM(t, first, defining, 200)
	expectKVM(t, kvmRequest(t, second, fixtureKVMAgent, "POST", fixtureVMHostPath+"/vm-work/"+defining.ID+"/arm", "", "",
		kvm.VMCommand{AgentIdentity: kvm.AgentIdentity{UUID: fixtureKVMUUID, Generation: 1}, Revision: defining.Revision}), 200, &defining)

	get := func(handler http.Handler, query string) kvm.HostFenceWorkPage {
		t.Helper()
		var page kvm.HostFenceWorkPage
		expectKVM(t, kvmRequest(t, handler, fixtureKVMReviewer, "GET", path+query, "", "", nil), 200, &page)
		if page.Host != "host-one" || page.HostUUID != fixtureKVMUUID || page.Items == nil {
			t.Fatalf("invalid fence work page: %+v", page)
		}
		return page
	}
	if page := get(first, ""); len(page.Items) != 0 || page.FenceState != "" {
		t.Fatalf("unexpected blockers before fence: %+v", page)
	}
	for _, query := range []string{"?limit=101", "?limit=0", "?limit=1&limit=2", "?after=", "?after=2.invalid",
		"?after=" + fixtureKVMUUID + ".01." + defining.ID, "?page=2"} {
		expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "GET", path+query, "", "", nil), 400, nil)
	}
	expectKVM(t, kvmRequest(t, second, fixtureKVMGuest, "GET", path, "", "", nil), 403, nil)
	expectKVM(t, kvmRequest(t, second, fixtureKVMAdmin, "GET", strings.Replace(path, "/compute/", "/other/", 1), "", "", nil), 403, nil)

	fence := func(actor, version, action, evidence, replacement string, status int) {
		t.Helper()
		expectKVM(t, kvmRequest(t, first, actor, "POST", fixtureVMHostPath+"/fence", version, "",
			kvm.HostFenceChange{Action: action, UUID: fixtureKVMUUID, EvidenceSHA256: evidence, ReplacementAgent: replacement}), status, nil)
	}
	fence(fixtureKVMAdmin, `"1"`, "request", "", "", 200)
	pending := get(second, "")
	if pending.FenceState != "pending" || pending.Generation != 2 || len(pending.Items) != 3 {
		t.Fatalf("fence request hid pending work: %+v", pending)
	}
	fence(fixtureKVMReviewer, `"2"`, "confirm", strings.Repeat("a", 64), "", 200)
	api := httptest.NewTLSServer(second)
	defer api.Close()
	verifyKVMFenceWorkPaging(t, api, get(first, ""), get(second, "?limit=1"))
	firstPage := get(first, "?limit=1")
	mismatchedHost := strings.Replace(firstPage.Next, fixtureKVMUUID, "feaa8a19-4765-4bb4-a383-3a1011ba0002", 1)
	expectKVM(t, kvmRequest(t, second, fixtureKVMReviewer, "GET", path+"?after="+mismatchedHost, "", "", nil), 412, nil)

	want := map[string]string{awaiting.ID: "awaiting_approval", queued.ID: "queued", defining.ID: "defining"}
	for _, item := range get(second, "").Items {
		if item.State != want[item.VM] || item.Generation != 1 {
			t.Fatalf("unexpected blocker: %+v", item)
		}
		delete(want, item.VM)
	}
	if len(want) != 0 {
		t.Fatalf("omitted blockers: %+v", want)
	}
	recoveryPath := fixtureVMSitePath + "/" + defining.ID + "/fence-recovery"
	revision := strconv.Quote(strconv.FormatInt(defining.Revision, 10))
	proof := kvm.FencedVMRecoveryInput{ID: "site-proof-1", HostUUID: fixtureKVMUUID, FenceGeneration: 2,
		Observed: "absent", EvidenceSHA256: strings.Repeat("b", 64), SecondEvidenceSHA256: strings.Repeat("c", 64)}
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", recoveryPath, revision, "", proof), 202, nil)
	for _, item := range get(second, "").Items {
		if item.VM == defining.ID && (item.PendingRecovery != proof.ID || item.Revision != defining.Revision || item.Placement != original.ID) {
			t.Fatalf("incorrect recovery linkage: %+v", item)
		}
	}

	fence(fixtureKVMAdmin, `"3"`, "request-rejoin", strings.Repeat("d", 64), "replacement-agent", 200)
	fence(fixtureKVMReviewer, `"4"`, "approve-rejoin", "", "", 409)
	fence(fixtureKVMReviewer, `"4"`, "reject-rejoin", "", "", 200)
	for _, vm := range []kvm.VM{awaiting, queued} {
		match := strconv.Quote(strconv.FormatInt(vm.Revision, 10))
		expectKVM(t, kvmRequest(t, second, fixtureKVMReviewer, "POST", fixtureVMSitePath+"/"+vm.ID+"/review",
			match, "", kvm.VMReview{Decision: "reject"}), 200, nil)
	}
	if page := get(first, ""); len(page.Items) != 1 || page.Items[0].PendingRecovery != proof.ID {
		t.Fatalf("expected just the in-flight VM after rejecting undispatched tasks: %+v", page)
	}
	expectKVM(t, kvmRequest(t, second, fixtureKVMReviewer, "POST", recoveryPath+"/review", revision, "",
		kvm.FencedVMRecoveryReview{ID: proof.ID, Decision: "approve"}), 200, nil)
	if page := get(first, ""); len(page.Items) != 0 || page.Next != "" {
		t.Fatalf("closed work still blocks rejoin: %+v", page)
	}
	fence(fixtureKVMAdmin, `"5"`, "request-rejoin", strings.Repeat("e", 64), "replacement-agent", 200)
	fence(fixtureKVMReviewer, `"6"`, "approve-rejoin", "", "", 200)
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "GET", path+"?after="+firstPage.Next, "", "", nil), 412, nil)
	if page := get(second, ""); page.Generation != 3 || page.FenceState != "" || len(page.Items) != 0 {
		t.Fatalf("incorrect rejoined host status: %+v", page)
	}
}

func verifyKVMFenceWorkPaging(t *testing.T, api *httptest.Server, all, first kvm.HostFenceWorkPage) {
	t.Helper()
	if len(all.Items) != 3 || len(first.Items) != 1 || first.Next == "" || first.Items[0] != all.Items[0] {
		t.Fatalf("unexpected first page: %+v %+v", first, all)
	}
	var output bytes.Buffer
	tail := []string{"list-host-fence-work", "lab", "compute", "site-a", "host-one", first.Next}
	if err := runKVMClient(t.Context(), tail, api.URL, fixtureKVMOperator, nil, &output, api.Client().Transport); err != nil {
		t.Fatal(err)
	}
	var next kvm.HostFenceWorkPage
	if err := json.Unmarshal(output.Bytes(), &next); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(next.Items, all.Items[1:]) || next.Next != "" || next.Generation != first.Generation {
		t.Fatalf("wrong CLI continuation: %+v", next)
	}
}
