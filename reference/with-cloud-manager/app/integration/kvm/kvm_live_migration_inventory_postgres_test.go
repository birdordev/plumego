package kvmtest

import (
	"net/url"
	"strconv"
	"testing"

	"with-cloud-manager/internal/kvm"
)

func TestAcceptanceKVMLiveMigrationDiscovery(t *testing.T) {
	db, first, second, _, vm := prepareLiveMigration(t)
	path := fixtureVMSitePath + "/" + vm.ID
	targets, history := path+"/live-migration-targets", path+"/live-migrations"
	var page kvm.InventoryPage[kvm.LiveMigrationTarget]
	response := kvmRequest(t, second, fixtureKVMAdmin, "GET", targets+"?limit=1", "", "", nil)
	expectKVM(t, response, 200, &page)
	if response.Header().Get("Cache-Control") != "no-store" || len(page.Items) != 1 ||
		page.Items[0].ID != fixtureMigrationHost || page.Items[0].FailureDomain == "" || page.Next != "" {
		t.Fatalf("unexpected target page: %+v", page)
	}
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "GET", targets, "", "", nil), 403, nil)
	expectKVM(t, kvmRequest(t, first, fixtureKVMGuest, "GET", history, "", "", nil), 403, nil)
	otherSite := fixtureKVMProject + "/sites/site-b/vms/" + vm.ID
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "GET", otherSite+"/live-migrations", "", "", nil), 403, nil)
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "GET", otherSite+"/live-migration-targets", "", "", nil), 403, nil)
	for _, query := range []string{"?limit=0", "?after=x", "?site=site-a", "?after=a&after=b"} {
		expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "GET", history+query, "", "", nil), 400, nil)
	}
	var noHistory kvm.InventoryPage[kvm.LiveMigration]
	expectKVM(t, kvmRequest(t, first, fixtureKVMAgent, "GET", history, "", "", nil), 200, &noHistory)
	if noHistory.Items == nil || len(noHistory.Items) != 0 {
		t.Fatalf("unexpected empty history: %+v", noHistory)
	}

	setTargetReport := func(stale bool) {
		t.Helper()
		if _, err := db.ExecContext(t.Context(), `UPDATE cmp_kvm_hosts SET reported_at=clock_timestamp()-
			CASE WHEN $1::boolean THEN interval '2 minutes' ELSE interval '1 second' END WHERE host_id=$2`, stale, fixtureMigrationHost); err != nil {
			t.Fatal(err)
		}
	}
	setTargetReport(true)
	var expired kvm.InventoryPage[kvm.LiveMigrationTarget]
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "GET", targets, "", "", nil), 200, &expired)
	if expired.Items == nil || len(expired.Items) != 0 {
		t.Fatalf("expired report offered a candidate: %+v", expired)
	}
	setTargetReport(false)
	version := strconv.Quote(strconv.FormatInt(vm.Revision, 10))
	for _, id := range []string{"maintenance-a", "maintenance-b"} {
		expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", history, version, "", kvm.LiveMigrationInput{ID: id, Target: fixtureMigrationHost}), 202, nil)
		if id == "maintenance-a" {
			expectKVM(t, kvmRequest(t, second, fixtureKVMReviewer, "POST", history+"/"+id+"/review", version, "",
				kvm.VMReview{Decision: "reject"}), 200, nil)
		}
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE cmp_kvm_live_migrations SET requested_at=statement_timestamp() WHERE vm_id=$1`, vm.ID); err != nil {
		t.Fatal(err)
	}
	var latest kvm.InventoryPage[kvm.LiveMigration]
	expectKVM(t, kvmRequest(t, second, fixtureKVMReviewer, "GET", history+"?limit=1", "", "", nil), 200, &latest)
	if len(latest.Items) != 1 || latest.Items[0].ID != "maintenance-b" || latest.Items[0].Status != "pending" || latest.Next == "" {
		t.Fatalf("incorrect first migration page: %+v", latest)
	}
	var older kvm.InventoryPage[kvm.LiveMigration]
	expectKVM(t, kvmRequest(t, first, fixtureKVMAgent, "GET", history+"?limit=1&after="+url.QueryEscape(latest.Next), "", "", nil), 200, &older)
	if len(older.Items) != 1 || older.Items[0].ID != "maintenance-a" || older.Items[0].Status != "rejected" || older.Next != "" {
		t.Fatalf("incorrect second migration page: %+v", older)
	}
}
