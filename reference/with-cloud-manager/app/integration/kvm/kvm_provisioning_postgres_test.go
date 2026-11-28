package kvmtest

import (
	"net/url"
	"strings"
	"testing"

	"with-cloud-manager/internal/kvm"
)

func TestAcceptanceKVMProvisioningRecovery(t *testing.T) {
	db, first, second := prepareKVM(t)
	const targets = fixtureKVMProject + "/provisioning-hosts"
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", fixtureKVMSite+"/hosts", "", "", fixtureKVMHost()), 201, nil)
	var page kvm.InventoryPage[kvm.ProvisioningHost]
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "GET", targets, "", "", nil), 200, &page)
	if page.Items == nil || len(page.Items) != 0 {
		t.Fatalf("host without a report was advertised: %+v", page)
	}
	expectKVM(t, kvmRequest(t, second, fixtureKVMAgent, "PUT", fixtureVMHostPath+"/capacity", "", "", fixtureKVMReport()), 200, nil)
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "GET", targets+"?site=site-a", "", "", nil), 200, &page)
	if len(page.Items) != 1 || page.Items[0].ID != "host-one" || len(page.Items[0].Networks) != 2 || page.Items[0].StoragePool != "local" {
		t.Fatalf("unexpected provisioning target: %+v", page)
	}
	expectKVM(t, kvmRequest(t, first, fixtureKVMReviewer, "GET", targets, "", "", nil), 403, nil)
	if _, err := db.ExecContext(t.Context(), `UPDATE cmp_kvm_hosts SET reported_at=clock_timestamp()-interval '2 minutes'`); err != nil {
		t.Fatal(err)
	}
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "GET", targets, "", "", nil), 200, &page)
	if len(page.Items) != 0 {
		t.Fatalf("expired host was advertised: %+v", page)
	}
	report := fixtureKVMReport()
	report.Sequence = 2
	expectKVM(t, kvmRequest(t, second, fixtureKVMAgent, "PUT", fixtureVMHostPath+"/capacity", "", "", report), 200, nil)

	images := fixtureKVMSite + "/images"
	image := kvm.BootImageSpec{ID: "image-a", SHA256: strings.Repeat("a", 64), SizeBytes: 2048}
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", images, "", "", image), 201, nil)
	other := image
	other.ID = "image-b"
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", images, "", "", other), 201, nil)
	var firstPage, later kvm.InventoryPage[kvm.BootImage]
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "GET", images+"?limit=1", "", "", nil), 200, &firstPage)
	if len(firstPage.Items) != 1 || firstPage.Items[0].BootImageSpec != image || firstPage.Next == "" {
		t.Fatalf("unexpected first image page: %+v", firstPage)
	}
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "GET", images+"?limit=1&after="+url.QueryEscape(firstPage.Next), "", "", nil), 200, &later)
	if len(later.Items) != 1 || later.Items[0].BootImageSpec != other || later.Next != "" {
		t.Fatalf("unexpected second image page: %+v", later)
	}
	expectKVM(t, kvmRequest(t, first, fixtureKVMGuest, "GET", images, "", "", nil), 403, nil)

	reserve := fixtureKVMPlacement()
	reserve.RequiredHost = "host-one"
	var hold, replay kvm.Placement
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "POST", fixtureVMPlacementPath, "", "provisioning-recovery", reserve), 201, &hold)
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "POST", fixtureVMPlacementPath, "", "provisioning-recovery", reserve), 201, &replay)
	if replay.ID != hold.ID || hold.State != "held" {
		t.Fatalf("reservation retry created a second hold: %+v %+v", hold, replay)
	}
	statusPath := fixtureVMPlacementPath + "/" + hold.ID + "/provisioning"
	var pending kvm.ProvisioningStatus
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "GET", statusPath, "", "", nil), 200, &pending)
	if pending.Placement.ID != hold.ID || pending.Placement.State != "held" || pending.VM != "" {
		t.Fatalf("unexpected new hold status: %+v", pending)
	}
	expectKVM(t, kvmRequest(t, second, fixtureKVMSecondOperator, "GET", statusPath, "", "", nil), 403, nil)
	var vm kvm.VM
	input := kvm.VMInput{Placement: hold.ID, ImageID: image.ID}
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "POST", fixtureVMSitePath, "", "", input), 202, &vm)
	var recovered kvm.ProvisioningStatus
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "GET", statusPath, "", "", nil), 200, &recovered)
	if recovered.VM != vm.ID || recovered.Placement.State != "bound" {
		t.Fatalf("lost submit did not recover original VM: %+v", recovered)
	}
	expectKVM(t, kvmRequest(t, second, fixtureKVMReviewer, "POST", fixtureVMSitePath+"/"+vm.ID+"/review", `"1"`, "",
		kvm.VMReview{Decision: "reject"}), 200, nil)
	var terminated kvm.ProvisioningStatus
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "GET", statusPath, "", "", nil), 200, &terminated)
	if terminated.VM != vm.ID || terminated.Placement.State != "released" {
		t.Fatalf("a rejected VM was confused with an unused hold: %+v", terminated)
	}
}
