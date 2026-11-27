//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"with-cloud-manager/internal/kvm"
)

const testCleanupID = "cleanup-1"
const testCleanupPoolUUID = "feaa8a19-4765-4bb4-a383-3a1011ba0070"

func TestAcceptanceKVMVolumeCleanup(t *testing.T) {
	db, first, second, storage, vm := prepareLiveMigration(t)
	data := []byte("shared live guest disk")
	backup := testKVMImage(data)
	backup.ID = "cleanup-backup"
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", fixtureKVMSite+"/images", "", "", backup), 201, nil)
	path := fixtureVMPlacementPath + "/" + vm.Placement + "/volume-cleanups"
	input := kvm.VolumeCleanupInput{ID: testCleanupID, Backup: backup.ID, Evidence: strings.Repeat("a", 64)}
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", path, `"1"`, "", input), 409, nil)
	vm = retireVolumeCleanupFixture(t, first, vm)
	register := kvmRequest(t, second, fixtureKVMAdmin, "POST", path, `"1"`, "", input)
	var firstReceipt kvm.VolumeCleanup
	expectKVM(t, register, 202, &firstReceipt)
	location := path + "/" + input.ID
	if register.Header().Get("Location") != location || firstReceipt.VM != vm.ID || firstReceipt.Placement != vm.Placement {
		t.Fatalf("request did not freeze the retained disk: %+v", firstReceipt)
	}
	var replay kvm.VolumeCleanup
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", path, `"1"`, "", input), 202, &replay)
	if !replay.RequestedAt.Equal(firstReceipt.RequestedAt) {
		t.Fatal("idempotent cleanup submission created a new receipt")
	}
	expectKVM(t, kvmRequest(t, first, fixtureKVMGuest, "GET", location, "", "", nil), 403, nil)
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", location+"/review", `"1"`, "",
		kvm.VMReview{Decision: "approve"}), 403, nil)
	expectKVM(t, kvmRequest(t, second, fixtureKVMReviewer, "POST", location+"/review", `"1"`, "",
		kvm.VMReview{Decision: "approve"}), 200, nil)
	backupPlan := fixtureVMHostPath + "/vm-work/" + vm.ID + "/backup-plan"
	expectKVM(t, kvmRequest(t, first, fixtureKVMAgent, "POST", backupPlan, "", "",
		kvm.VMCommand{AgentIdentity: kvm.AgentIdentity{UUID: fixtureKVMUUID, Generation: 1}, Revision: vm.Revision}), 409, nil)

	fixture, run := volumeCleanupAgentFixture(t, first, storage, vm, backup, data)
	get := func(status string) kvm.VolumeCleanup {
		t.Helper()
		var current kvm.VolumeCleanup
		expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "GET", location, "", "", nil), 200, &current)
		if current.Status != status {
			t.Fatalf("unexpected cleanup status: %+v want %s", current, status)
		}
		return current
	}
	if err := run(); err == nil || fixture.deletions != 0 {
		t.Fatalf("missing backup still allowed deletion: %v", err)
	}
	if err := os.WriteFile(fixture.backup, data, 0600); err != nil {
		t.Fatal(err)
	}
	fixture.referenced = true
	if err := run(); err == nil || fixture.deletions != 0 {
		t.Fatalf("referenced volume still allowed deletion: %v", err)
	}
	fixture.referenced = false
	wrong := bytes.Repeat([]byte{'x'}, len(data))
	if err := os.WriteFile(fixture.disk, wrong, 0600); err != nil {
		t.Fatal(err)
	}
	if err := run(); err == nil || fixture.deletions != 0 {
		t.Fatalf("mismatched volume still allowed deletion: %v", err)
	}
	if err := os.WriteFile(fixture.disk, data, 0600); err != nil {
		t.Fatal(err)
	}
	get("approved")
	fixture.refreshFails = true
	var parallel sync.WaitGroup
	start := make(chan struct{})
	for range 8 {
		parallel.Go(func() {
			<-start
			_ = run()
		})
	}
	close(start)
	parallel.Wait()
	if fixture.deletions != 1 || get("dispatched").FirstAbsentAt != nil {
		t.Fatalf("expected one deletion and no absence receipt, got %d", fixture.deletions)
	}
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "PATCH", fixtureVMHostPath, `"1"`, "",
		kvm.HostChange{Action: "rebind", Agent: "replacement-agent"}), 409, nil)
	assertKVMSharedUsage(t, second, storage, 0, 1024)
	fixture.refreshFails = false
	if err := run(); err != nil {
		t.Fatal(err)
	}
	if get("dispatched").FirstAbsentAt == nil {
		t.Fatal("did not store first absence")
	}
	fixture.poolUUID = fixtureMigrationUUID
	if err := run(); err == nil {
		t.Fatal("pool UUID drift accepted as second absence")
	}
	fixture.poolUUID = testCleanupPoolUUID
	if err := os.WriteFile(fixture.disk, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := run(); err != nil {
		t.Fatal(err)
	}
	if get("dispatched").FirstAbsentAt != nil {
		t.Fatal("reappearance did not reset absence confirmation")
	}
	if err := os.Remove(fixture.disk); err != nil {
		t.Fatal(err)
	}
	if err := run(); err != nil {
		t.Fatal(err)
	}
	execEvacuationPlanSQL(t, db, `ALTER TABLE cmp_kvm_volume_cleanups ADD CONSTRAINT fixture_block_cleanup CHECK (status<>'completed')`)
	if err := run(); err == nil {
		t.Fatal("simulated commit failure was silently accepted")
	}
	assertKVMSharedUsage(t, second, storage, 0, 1024)
	get("dispatched")
	execEvacuationPlanSQL(t, db, `ALTER TABLE cmp_kvm_volume_cleanups DROP CONSTRAINT fixture_block_cleanup`)
	if err := run(); err != nil {
		t.Fatal(err)
	}
	get("completed")
	assertKVMSharedUsage(t, second, storage, 0, 0)
	if err := run(); err != nil || fixture.deletions != 1 {
		t.Fatalf("completion replay wrote again: %v", err)
	}
	preserved, err := os.ReadFile(fixture.backup)
	if err != nil || !bytes.Equal(data, preserved) {
		t.Fatalf("independent backup changed: %v", err)
	}
	checkVolumeCleanupCLI(t, second, vm.Placement)
}

func retireVolumeCleanupFixture(t *testing.T, handler http.Handler, vm kvm.VM) kvm.VM {
	t.Helper()
	path := fixtureVMSitePath + "/" + vm.ID
	job := fixtureVMHostPath + "/vm-work/" + vm.ID
	agent := kvm.AgentIdentity{UUID: fixtureKVMUUID, Generation: 1}
	for _, action := range []struct {
		path, actor  string
		observations []string
	}{
		{"shutdown", fixtureKVMOperator, []string{"defined"}},
		{"retirement", fixtureKVMAdmin, []string{"absent", "absent"}},
	} {
		header := strconv.Quote(strconv.FormatInt(vm.Revision, 10))
		expectKVM(t, kvmRequest(t, handler, action.actor, "POST", path+"/"+action.path, header, "", nil), 202, &vm)
		vm = approveKVMVM(t, handler, vm, 200)
		expectKVM(t, kvmRequest(t, handler, fixtureKVMAgent, "POST", job+"/arm", "", "",
			kvm.VMCommand{AgentIdentity: agent, Revision: vm.Revision}), 200, &vm)
		for _, sample := range action.observations {
			expectKVM(t, kvmRequest(t, handler, fixtureKVMAgent, "PUT", job+"/observation", "", "",
				kvm.VMObservation{VMCommand: kvm.VMCommand{AgentIdentity: agent, Revision: vm.Revision}, Observed: sample}), 200, &vm)
		}
	}
	return vm
}

func checkVolumeCleanupCLI(t *testing.T, handler http.Handler, placement string) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	var output bytes.Buffer
	args := []string{"get-volume-cleanup", "lab", "compute", "site-a", placement, testCleanupID}
	if err := runKVMClient(t.Context(), args, server.URL, fixtureKVMOperator, nil, &output, server.Client().Transport); err != nil {
		t.Fatal(err)
	}
	var result kvm.VolumeCleanup
	if err := json.Unmarshal(output.Bytes(), &result); err != nil || result.Status != "completed" || result.FinishedAt == nil {
		t.Fatalf("unexpected CLI output: %s %v", output.String(), err)
	}
}

type volumeCleanupFixture struct {
	mu           sync.Mutex
	disk, backup string
	poolUUID     string
	referenced   bool
	refreshFails bool
	deletions    int
}

func volumeCleanupAgentFixture(t *testing.T, handler http.Handler, storage kvm.SharedStorageSpec, vm kvm.VM,
	backup kvm.BootImageSpec, data []byte) (*volumeCleanupFixture, func() error) {
	t.Helper()
	pool, imageRoot := imageFixtureDir(t), imageFixtureDir(t)
	f := &volumeCleanupFixture{disk: filepath.Join(pool, vm.Volume), backup: filepath.Join(imageRoot, backup.SHA256+".raw"), poolUUID: testCleanupPoolUUID}
	if err := os.WriteFile(f.disk, data, 0600); err != nil {
		t.Fatal(err)
	}
	probe := fixtureKVMProbe(t, fixtureKVMUUID, "", 4096)
	setSharedKVMProbe(t, &probe, pool, storage, vm.StoragePool)
	original := probe.read
	probe.read = func(ctx context.Context, args ...string) ([]byte, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch command := strings.Join(args, " "); command {
		case "pool-uuid " + vm.StoragePool:
			return []byte(f.poolUUID), nil
		case "vol-dumpxml --pool " + vm.StoragePool + " --vol " + vm.Volume:
			info, err := os.Stat(f.disk)
			if err != nil {
				return nil, err
			}
			return []byte(fmt.Sprintf(`<volume><name>%s</name><capacity unit="bytes">%d</capacity>
				<target><path>%s</path><format type="raw"/></target></volume>`, vm.Volume, info.Size(), f.disk)), nil
		case "vol-list --pool " + f.poolUUID:
			return []byte(" Name Path\n -------------------------\n"), nil
		case "list --all --uuid":
			if f.referenced {
				return []byte(vm.ID), nil
			}
			return nil, nil
		case "dumpxml " + vm.ID, "dumpxml " + vm.ID + " --inactive":
			return []byte(fmt.Sprintf(`<domain><devices><disk><source pool="%s" volume="%s"/></disk></devices></domain>`,
				vm.StoragePool, vm.Volume)), nil
		default:
			return original(ctx, args...)
		}
	}
	write := func(_ context.Context, args ...string) ([]byte, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch strings.Join(args, " ") {
		case "pool-refresh " + f.poolUUID:
			if f.refreshFails {
				return nil, errors.New("fixture lost pool refresh")
			}
			return nil, nil
		case "vol-delete --pool " + testCleanupPoolUUID + " " + vm.Volume:
			f.deletions++
			if err := os.Remove(f.disk); err != nil {
				return nil, err
			}
			return nil, errors.New("fixture lost libvirt deletion reply")
		default:
			t.Errorf("unexpected libvirt write %v", args)
			return nil, errKVMUnsafeTarget
		}
	}
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"run-volume-cleanup", "lab", "compute", "site-a", "host-one", "1", vm.Placement, testCleanupID}
	target, ok := parseKVMVolumeCleanupTarget(args)
	if !ok {
		t.Fatal("invalid CLI cleanup target")
	}
	plan, err := planKVMRequest(base, args)
	if err != nil {
		t.Fatal(err)
	}
	api := kvmVMAPI{target: plan.target, token: fixtureKVMAgent, transport: server.Client().Transport}
	profile, err := json.Marshal(kvmAgentProfile{ImageRoot: imageRoot})
	if err != nil {
		t.Fatal(err)
	}
	return f, func() error {
		var output bytes.Buffer
		return runKVMVolumeCleanup(t.Context(), api, target, bytes.NewReader(profile), &output, probe, write)
	}
}
