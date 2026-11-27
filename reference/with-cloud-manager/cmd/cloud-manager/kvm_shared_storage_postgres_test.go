//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"with-cloud-manager/internal/kvm"
)

const fixtureSharedStoragePath = fixtureKVMSite + "/shared-storage-pools"

var fixtureSharedStorage = kvm.SharedStorageSpec{ID: "shared-data", CapacityMiB: 1024,
	SharedStorageSource: kvm.SharedStorageSource{Server: "10.24.0.9", Export: "/kvm/compute"}}

func TestAcceptanceKVMSharedStorageBudget(t *testing.T) {
	db, first, second := prepareKVM(t)
	storage := fixtureSharedStorage
	registerSharedStorageFixture(t, first, storage)
	poolPath := fixtureSharedStoragePath + "/" + storage.ID
	expectKVM(t, kvmRequest(t, first, fixtureKVMGuest, "GET", poolPath, "", "", nil), 403, nil)
	other := strings.Replace(fixtureSharedStoragePath, "/compute/", "/other/", 1)
	overlap := storage
	overlap.ID, overlap.Export = "overlap", storage.Export+"/more"
	expectKVM(t, kvmRequest(t, second, fixtureKVMAdmin, "POST", other, "", "", overlap), 409, nil)
	overlap.Export = "/kvm"
	expectKVM(t, kvmRequest(t, second, fixtureKVMAdmin, "POST", other, "", "", overlap), 409, nil)

	one, two := fixtureKVMHost(), fixtureKVMHost()
	one.SharedStoragePool, two.SharedStoragePool = storage.ID, storage.ID
	two.ID, two.UUID, two.Agent, two.StoragePool = "host-two", fixtureMigrationUUID, "replacement-agent", "shared-local-name"
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", fixtureKVMSite+"/hosts", "", "", one), 201, nil)
	expectKVM(t, kvmRequest(t, second, fixtureKVMAdmin, "POST", fixtureKVMSite+"/hosts", "", "", two), 201, nil)
	report := fixtureKVMReport()
	expectKVM(t, kvmRequest(t, first, fixtureKVMAgent, "PUT", fixtureVMHostPath+"/capacity", "", "", report), 409, nil)
	report.SharedStorage = &kvm.SharedStorageEvidence{ID: storage.ID, SharedStorageSource: overlap.SharedStorageSource}
	expectKVM(t, kvmRequest(t, first, fixtureKVMAgent, "PUT", fixtureVMHostPath+"/capacity", "", "", report), 409, nil)
	report.SharedStorage.SharedStorageSource = storage.SharedStorageSource
	expectKVM(t, kvmRequest(t, second, fixtureKVMAgent, "PUT", fixtureVMHostPath+"/capacity", "", "", report), 200, nil)
	report.UUID = two.UUID
	expectKVM(t, kvmRequest(t, first, fixtureKVMReplacement, "PUT", fixtureKVMSite+"/hosts/"+two.ID+"/capacity", "", "", report), 200, nil)
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "POST", fixtureVMPlacementPath, "", "wrong-budget-path", fixtureKVMPlacement()), 409, nil)

	hold := func(handler http.Handler, host, key string, disk int64, status int) kvm.Placement {
		t.Helper()
		input := sharedStoragePlacement(storage.ID, host, disk)
		var placement kvm.Placement
		response := kvmRequest(t, handler, fixtureKVMOperator, "POST", fixtureVMPlacementPath, "", key, input)
		if status == 201 {
			expectKVM(t, response, status, &placement)
		} else {
			expectKVM(t, response, status, nil)
		}
		return placement
	}
	left := hold(first, one.ID, "half-on-first", 512, 201)
	right := hold(second, two.ID, "half-on-second", 512, 201)
	if left.SharedStoragePool != storage.ID || right.SharedStoragePool != storage.ID || right.StoragePool != two.StoragePool {
		t.Fatalf("placement did not preserve shared pool and local alias: %+v %+v", left, right)
	}
	hold(second, one.ID, "oversold", 1, 409)
	assertKVMSharedUsage(t, first, storage, 1024, 0)
	var metadata kvm.SharedStorage
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "GET", poolPath+"?usage=false", "", "", nil), 200, &metadata)
	if metadata.SharedStorageSpec != storage || metadata.ReservedMiB != 0 {
		t.Fatalf("unexpected metadata-only response: %+v", metadata)
	}
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "GET", poolPath+"?usage=false&usage=true", "", "", nil), 400, nil)
	release := func(p kvm.Placement) {
		t.Helper()
		expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "DELETE", fixtureVMPlacementPath+"/"+p.ID, "", "", nil), 200, nil)
	}
	release(left)
	release(right)

	var requests [16]*httptest.ResponseRecorder
	var group sync.WaitGroup
	start := make(chan struct{})
	for i := range requests {
		handler, host := first, one.ID
		if i%2 == 1 {
			handler, host = second, two.ID
		}
		group.Go(func() {
			<-start
			requests[i] = kvmRequest(t, handler, fixtureKVMOperator, "POST", fixtureVMPlacementPath, "", strconv.Itoa(i)+"-shared",
				sharedStoragePlacement(storage.ID, host, 1024))
		})
	}
	close(start)
	group.Wait()
	var winner kvm.Placement
	accepted := 0
	for _, response := range requests {
		if response.Code == 201 {
			expectKVM(t, response, 201, &winner)
			accepted++
		} else {
			expectKVM(t, response, 409, nil)
		}
	}
	if accepted != 1 || winner.SharedStoragePool != storage.ID {
		t.Fatalf("unexpected number of concurrent reservations: %d %+v", accepted, winner)
	}
	expireKVMSharedHold(t, db, winner.ID)
	newHost := one.ID
	if winner.Host == newHost {
		newHost = two.ID
	}
	hold(second, newHost, "reclaim-from-other-host", 1024, 201)
	var expired kvm.Placement
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "GET", fixtureVMPlacementPath+"/"+winner.ID, "", "", nil), 200, &expired)
	if expired.State != "expired" {
		t.Fatalf("stale shared hold was not sealed: %+v", expired)
	}
	assertKVMSharedUsage(t, first, storage, 1024, 0)
}

func TestAcceptanceKVMSharedStorageVM(t *testing.T) {
	_, first, second := prepareKVM(t)
	storage := fixtureSharedStorage
	registerSharedStorageFixture(t, first, storage)
	host := fixtureKVMHost()
	host.SharedStoragePool = storage.ID
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", fixtureKVMSite+"/hosts", "", "", host), 201, nil)
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", fixtureKVMSite+"/networks", "", "", fixtureIPv4Pool), 201, nil)
	server := httptest.NewTLSServer(second)
	defer server.Close()
	base, err := operationClientBase(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	api := kvmVMAPI{target: base.JoinPath(fixtureVMHostPath), token: fixtureKVMAgent, transport: server.Client().Transport}
	probe := fixtureKVMProbe(t, host.UUID, "", 8192)
	setSharedKVMProbe(t, &probe, imageFixtureDir(t), storage, host.StoragePool)
	observe := func(probe kvmProbe) error {
		var output bytes.Buffer
		return observeKVMHost(t.Context(), api.target, host.ID, "1", fixtureKVMAgent,
			strings.NewReader(`{"vcpu":4,"memory_mib":4096,"disk_mib":4096}`), &output, api.transport, probe)
	}
	if err := observe(probe); err != nil {
		t.Fatal(err)
	}
	badProbe := probe
	badProbe.mountInfo = func(context.Context) ([]byte, error) { return nil, nil }
	if err := observe(badProbe); err == nil {
		t.Fatal("missing mount produced a healthy report")
	}
	var hostAfter kvm.Host
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "GET", fixtureVMHostPath, "", "", nil), 200, &hostAfter)
	if hostAfter.Capacity != (kvm.Capacity{}) || hostAfter.Sequence != 2 {
		t.Fatalf("unmounted pool left capacity eligible: %+v", hostAfter)
	}
	placement := sharedStoragePlacement(storage.ID, host.ID, 1024)
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "POST", fixtureVMPlacementPath, "", "not-mounted", placement), 409, nil)
	if err := observe(probe); err != nil {
		t.Fatal(err)
	}
	var hold kvm.Placement
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "POST", fixtureVMPlacementPath, "", "shared-vm", placement), 201, &hold)
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "POST", fixtureVMSitePath, "", "",
		kvm.VMInput{Placement: hold.ID, Volume: "unmanaged"}), 409, nil)
	data := []byte("managed shared raw boot disk")
	image := testKVMImage(data)
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", fixtureKVMSite+"/images", "", "", image), 201, nil)
	var vm kvm.VM
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "POST", fixtureVMSitePath, "", "",
		kvm.VMInput{Placement: hold.ID, ImageID: image.ID}), 202, &vm)
	vm = approveKVMVM(t, first, vm, 200)
	source := imageFixtureDir(t)
	if err := os.WriteFile(filepath.Join(source, image.SHA256+".raw"), data, 0600); err != nil {
		t.Fatal(err)
	}
	daemon, runner, api, profile := recoveryAgentFixture(t, server, fixtureVMHostPath, host.UUID, fixtureKVMAgent, source, vm)
	document, err := runner.read(t.Context(), "pool-dumpxml", "--pool", host.StoragePool)
	if err != nil {
		t.Fatal(err)
	}
	var local kvmPoolXML
	if err := xml.Unmarshal(document, &local); err != nil {
		t.Fatal(err)
	}
	setSharedKVMProbe(t, &runner.kvmProbe, local.Path, storage, host.StoragePool)
	vm = runKVMSharedVMCreation(t, first, runner, api, profile, vm)
	retireKVMSharedVM(t, first, second, vm, hold, daemon, runner, api, profile)
	if retained, err := os.ReadFile(filepath.Join(local.Path, vm.Volume)); err != nil || !bytes.Equal(retained, data) {
		t.Fatalf("retirement lost the shared boot volume: %v", err)
	}
	assertKVMSharedUsage(t, second, storage, 0, 1024)
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "POST", fixtureVMPlacementPath, "", "retained-still-charged", placement), 409, nil)
}

func TestAcceptanceKVMSharedStorageColdMigration(t *testing.T) {
	_, first, second, source, vm := prepareKVMVMWithPool(t, &fixtureIPv4Pool)
	storage := fixtureSharedStorage
	registerSharedStorageFixture(t, first, storage)
	host, report := fixtureKVMHost(), fixtureKVMReport()
	host.ID, host.UUID, host.Agent, host.SharedStoragePool = fixtureMigrationHost, fixtureMigrationUUID, "replacement-agent", storage.ID
	destination := fixtureKVMSite + "/hosts/" + host.ID
	report.UUID = host.UUID
	report.SharedStorage = &kvm.SharedStorageEvidence{ID: storage.ID, SharedStorageSource: storage.SharedStorageSource}
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", fixtureKVMSite+"/hosts", "", "", host), 201, nil)
	expectKVM(t, kvmRequest(t, second, fixtureKVMReplacement, "PUT", destination+"/capacity", "", "", report), 200, nil)
	vm = retireMigrationSource(t, first, vm)
	input := sharedStoragePlacement(storage.ID, host.ID, source.Capacity.DiskMiB)
	input.IPv4 = source.Address
	var target kvm.Placement
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", fixtureVMPlacementPath, "", "shared-target", input), 201, &target)
	image := testKVMImage([]byte("backup for shared storage migration"))
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", fixtureKVMSite+"/images", "", "", image), 201, nil)
	revision := strconv.Quote(strconv.FormatInt(vm.Revision, 10))
	path := fixtureVMSitePath + "/" + vm.ID + "/cold-migration"
	migration := kvm.ColdMigrationInput{ID: "shared-cold", Image: image.ID, Placement: target.ID, Evidence: strings.Repeat("a", 64)}
	var requested kvm.ColdMigration
	expectKVM(t, kvmRequest(t, second, fixtureKVMAdmin, "POST", path, revision, "", migration), 202, &requested)
	offline := kvm.Report{UUID: host.UUID, Generation: 1, Sequence: 2}
	expectKVM(t, kvmRequest(t, first, fixtureKVMReplacement, "PUT", destination+"/capacity", "", "", offline), 200, nil)
	expectKVM(t, kvmRequest(t, second, fixtureKVMReviewer, "POST", path+"/review", revision, "",
		kvm.ColdMigrationReview{ID: requested.ID, Decision: "approve"}), 409, nil)
	var untouched kvm.VM
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "GET", fixtureVMSitePath+"/"+vm.ID, "", "", nil), 200, &untouched)
	if untouched != vm {
		t.Fatalf("offline shared storage changed VM binding: %+v", untouched)
	}
	report.Sequence = 3
	expectKVM(t, kvmRequest(t, first, fixtureKVMReplacement, "PUT", destination+"/capacity", "", "", report), 200, nil)
	reviewBatchItem(t, second, requested, "approve")
	var moved kvm.VM
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "GET", fixtureVMSitePath+"/"+vm.ID, "", "", nil), 200, &moved)
	if moved.ID != vm.ID || moved.State != "queued" || moved.Address != source.Address || moved.SharedStoragePool != storage.ID ||
		moved.Host != target.Host || moved.Placement != target.ID || moved.Volume != kvm.ImageVolumeName(target.ID) {
		t.Fatalf("cold migration did not bind the shared target: %+v", moved)
	}
	assertKVMSharedUsage(t, second, storage, 1024, 0)
}

func registerSharedStorageFixture(t *testing.T, handler http.Handler, spec kvm.SharedStorageSpec) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	body, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	args := []string{"register-shared-storage", "lab", "compute", "site-a"}
	if err := runKVMClient(t.Context(), args, server.URL, fixtureKVMAdmin, bytes.NewReader(body), &output, server.Client().Transport); err != nil {
		t.Fatal(err)
	}
	var pool kvm.SharedStorage
	if err := json.Unmarshal(output.Bytes(), &pool); err != nil || pool.SharedStorageSpec != spec {
		t.Fatalf("unexpected shared storage receipt: %s %v", output.String(), err)
	}
}

func sharedStoragePlacement(pool, host string, disk int64) kvm.PlacementInput {
	input := fixtureKVMPlacement()
	input.StoragePool, input.SharedStoragePool, input.RequiredHost, input.DiskMiB = "", pool, host, disk
	return input
}

func assertKVMSharedUsage(t *testing.T, handler http.Handler, spec kvm.SharedStorageSpec, reserved, retained int64) {
	t.Helper()
	var pool kvm.SharedStorage
	expectKVM(t, kvmRequest(t, handler, fixtureKVMOperator, "GET", fixtureSharedStoragePath+"/"+spec.ID, "", "", nil), 200, &pool)
	if pool.SharedStorageSpec != spec || pool.ReservedMiB != reserved || pool.RetainedMiB != retained {
		t.Fatalf("unexpected shared usage: %+v", pool)
	}
}

func expireKVMSharedHold(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), `UPDATE cmp_kvm_placements
		SET created_at=clock_timestamp()-interval '10 minutes',expires_at=clock_timestamp()-interval '1 minute' WHERE placement_id=$1`, id); err != nil {
		t.Fatal(err)
	}
}

func setSharedKVMProbe(t *testing.T, probe *kvmProbe, root string, storage kvm.SharedStorageSpec, local string) {
	t.Helper()
	raw := fmt.Sprintf(`<pool type="netfs"><name>%s</name><source><host name="%s"/><dir path="%s"/><format type="nfs"/></source><target><path>%s</path></target></pool>`,
		local, storage.Server, storage.Export, root)
	read := probe.read
	probe.read = func(ctx context.Context, args ...string) ([]byte, error) {
		if strings.Join(args, " ") == "pool-dumpxml --pool "+local {
			return []byte(raw), nil
		}
		return read(ctx, args...)
	}
	probe.mountInfo = func(context.Context) ([]byte, error) {
		return []byte(fmt.Sprintf("64 25 0:41 / %s rw,relatime - nfs4 %s:%s rw,vers=4.2,proto=tcp,addr=%s,local_lock=none\n",
			root, storage.Server, storage.Export, storage.Server)), nil
	}
	probe.sharedFreeMiB = func(path string) (int64, error) {
		if path != root {
			t.Fatalf("statfs called for unverified path %q", path)
		}
		return 4096, nil
	}
}

func runKVMSharedVMCreation(t *testing.T, handler http.Handler, runner kvmVMRunner, api kvmVMAPI, profile string, vm kvm.VM) kvm.VM {
	t.Helper()
	for i, want := range []string{"staged", "defining", "defined", "starting", "running"} {
		var output bytes.Buffer
		err := runKVMVMJob(t.Context(), api, vm.Host, "1", strings.NewReader(profile), &output, runner)
		expectKVM(t, kvmRequest(t, handler, fixtureKVMOperator, "GET", fixtureVMSitePath+"/"+vm.ID, "", "", nil), 200, &vm)
		if (err != nil) != (i == 1 || i == 3) || vm.State != want {
			t.Fatalf("unexpected shared VM step %s (wanted %s): %v", vm.State, want, err)
		}
	}
	return vm
}

func retireKVMSharedVM(t *testing.T, first, second http.Handler, vm kvm.VM, placement kvm.Placement, daemon *fakeKVMDaemon,
	runner kvmVMRunner, api kvmVMAPI, profile string) {
	t.Helper()
	path := fixtureVMSitePath + "/" + vm.ID
	step := func(want string, lostReply bool) {
		t.Helper()
		var output bytes.Buffer
		err := runKVMVMJob(t.Context(), api, vm.Host, "1", strings.NewReader(profile), &output, runner)
		expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "GET", path, "", "", nil), 200, &vm)
		if (err != nil) != lostReply || vm.State != want {
			t.Fatalf("unexpected shared retirement step %s (wanted %s): %v", vm.State, want, err)
		}
	}
	approve := func(operation, token string) {
		t.Helper()
		revision := strconv.Quote(strconv.FormatInt(vm.Revision, 10))
		expectKVM(t, kvmRequest(t, first, token, "POST", path+"/"+operation, revision, "", nil), 202, &vm)
		vm = approveKVMVM(t, first, vm, 200)
	}
	approve("shutdown", fixtureKVMOperator)
	step("stopping", true)
	daemon.running = false
	step("stopped", false)
	approve("retirement", fixtureKVMAdmin)
	step("undefining", true)
	step("undefining", false)
	step("retired", false)
	var retained kvm.Placement
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "GET", fixtureVMPlacementPath+"/"+placement.ID, "", "", nil), 200, &retained)
	if !retained.BootVolumeRetained || retained.State != "released" || daemon.id != "" {
		t.Fatalf("retired VM did not preserve its shared volume: %+v %+v", vm, retained)
	}
}
