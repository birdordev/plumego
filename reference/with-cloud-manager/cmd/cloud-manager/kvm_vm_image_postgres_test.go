//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"with-cloud-manager/internal/kvm"
)

func TestAcceptanceKVMImageVolume(t *testing.T) {
	db, first, second := prepareKVM(t)
	source, pool := imageFixtureDir(t), imageFixtureDir(t)
	contents := []byte("local raw image")
	image := testKVMImage(contents)
	imagePath := filepath.Join(source, image.SHA256+".raw")
	write := func(path string, contents []byte) {
		t.Helper()
		if err := os.WriteFile(path, contents, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(imagePath, make([]byte, len(contents)))
	images := fixtureKVMSite + "/images"
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "POST", images, "", "", image), 403, nil)
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", images, "", "", image), 201, nil)
	expectKVM(t, kvmRequest(t, second, fixtureKVMAdmin, "POST", images, "", "", image), 409, nil)
	expectKVM(t, kvmRequest(t, second, fixtureKVMGuest, "GET", images+"/"+image.ID, "", "", nil), 403, nil)
	var got kvm.BootImage
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "GET", images+"/"+image.ID, "", "", nil), 200, &got)
	if got.BootImageSpec != image {
		t.Fatalf("catalog did not retain image contract: %+v", got)
	}
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "POST", fixtureKVMSite+"/hosts", "", "", fixtureKVMHost()), 201, nil)
	expectKVM(t, kvmRequest(t, first, fixtureKVMAgent, "PUT", fixtureVMHostPath+"/capacity", "", "", fixtureKVMReport()), 200, nil)
	var placement kvm.Placement
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "POST", fixtureVMPlacementPath, "", "image-vm", fixtureKVMPlacement()), 201, &placement)
	volume := kvm.ImageVolumeName(placement.ID)
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "POST", fixtureVMSitePath, "", "",
		kvm.VMInput{Placement: placement.ID, Volume: volume}), 400, nil)
	input := kvm.VMInput{Placement: placement.ID, ImageID: image.ID}
	var vm, replay kvm.VM
	expectKVM(t, kvmRequest(t, second, fixtureKVMOperator, "POST", fixtureVMSitePath, "", "", input), 202, &vm)
	expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "POST", fixtureVMSitePath, "", "", input), 202, &replay)
	if vm != replay || vm.Volume != volume || vm.ImageSHA256 != image.SHA256 {
		t.Fatalf("image was not immutably pinned: %+v %+v", vm, replay)
	}
	path := fixtureVMSitePath + "/" + vm.ID
	stage := fixtureVMHostPath + "/vm-work/" + vm.ID + "/image-plan"
	identity := kvm.AgentIdentity{UUID: fixtureKVMUUID, Generation: 1}
	expectKVM(t, kvmRequest(t, first, fixtureKVMAgent, "POST", stage, "", "",
		kvm.VMCommand{AgentIdentity: identity, Revision: vm.Revision}), 409, nil)
	vm = approveKVMVM(t, first, vm, 200)
	server := httptest.NewTLSServer(second)
	t.Cleanup(server.Close)
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	api := kvmVMAPI{target: base.JoinPath(fixtureVMHostPath), token: fixtureKVMAgent, transport: server.Client().Transport}
	profile, err := json.Marshal(kvmAgentProfile{ImageRoot: source})
	if err != nil {
		t.Fatal(err)
	}
	daemon := &fakeKVMDaemon{expectedVolume: volume}
	runner := daemon.runner(t)
	target := filepath.Join(pool, volume)
	runner.freeMiB = func(path string) (int64, error) {
		if path != pool {
			return 0, errKVMUnsafeTarget
		}
		return 64_000, nil
	}
	probe := runner.read
	runner.read = func(ctx context.Context, args ...string) ([]byte, error) {
		switch strings.Join(args, " ") {
		case "pool-dumpxml --pool local":
			return []byte(`<pool type="dir"><name>local</name><target><path>` + pool + `</path></target></pool>`), nil
		case "vol-dumpxml --pool local --vol " + volume:
			info, err := os.Stat(target)
			if err != nil {
				return nil, err
			}
			return []byte(fmt.Sprintf(`<volume><name>%s</name><capacity unit="bytes">%d</capacity><target><path>%s</path><format type="raw"/></target></volume>`,
				volume, info.Size(), target)), nil
		default:
			return probe(ctx, args...)
		}
	}
	runner.bootFile = func(path string, bytes int64) error {
		if path != target || bytes != image.SizeBytes {
			return errKVMUnsafeTarget
		}
		return nil
	}
	mutate, refresh := runner.write, 0
	runner.write = func(ctx context.Context, args ...string) ([]byte, error) {
		if strings.Join(args, " ") == "pool-refresh local" {
			refresh++
			if refresh == 1 {
				return nil, errors.New("fixture dropped volume refresh acknowledgment")
			}
			return nil, nil
		}
		return mutate(ctx, args...)
	}
	get := func() kvm.VM {
		t.Helper()
		var current kvm.VM
		expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "GET", path, "", "", nil), 200, &current)
		return current
	}
	run := func(phase string, failed bool) {
		t.Helper()
		var output bytes.Buffer
		err := runKVMVMJob(t.Context(), api, vm.Host, "1", bytes.NewReader(profile), &output, runner)
		if current := get(); current.State != phase || (err != nil) != failed {
			t.Fatalf("image stage: state=%+v need=%s err=%v", current, phase, err)
		}
	}
	run("staging", true)
	if got := get(); got.Observation != "mismatch" {
		t.Fatalf("invalid image was not recorded for the next poll: %+v", got)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("wrong image produced a boot volume: %v", err)
	}
	expectKVM(t, kvmRequest(t, first, fixtureKVMReplacement, "POST", stage, "", "",
		kvm.VMCommand{AgentIdentity: identity, Revision: get().Revision}), 403, nil)
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "PATCH", fixtureVMHostPath, `"1"`, "",
		kvm.HostChange{Action: "rebind", Agent: "replacement-agent"}), 409, nil)
	write(imagePath, contents)
	run("staging", true)
	if err := os.Remove(imagePath); err != nil {
		t.Fatal(err)
	}
	run("staged", false)
	write(target, make([]byte, len(contents)))
	run("staged", true)
	if len(daemon.writes) != 0 {
		t.Fatalf("corrupted staged volume led to domain writes: %v", daemon.writes)
	}
	write(target, contents)
	run("defining", true)
	run("defined", false)
	run("starting", true)
	run("running", false)
	var staged int
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM cmp_kvm_events
		WHERE placement_id=$1 AND action IN ('vm_stage','vm_staged')`, placement.ID).Scan(&staged); err != nil || staged != 2 || refresh != 2 {
		t.Fatalf("image stage did not converge as expected: audit=%d refresh=%d %v", staged, refresh, err)
	}
}
