//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
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
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/mirror/"+image.SHA256+".raw" {
			if r.Header.Get("Authorization") != "" {
				t.Error("site mirror received the control plane token")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if _, err := w.Write(contents); err != nil {
				t.Error(err)
			}
			return
		}
		second.ServeHTTP(w, r)
	}))
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
	get := func(t *testing.T) kvm.VM {
		t.Helper()
		var current kvm.VM
		expectKVM(t, kvmRequest(t, first, fixtureKVMOperator, "GET", path, "", "", nil), 200, &current)
		return current
	}
	run := func(t *testing.T, phase string, failed bool) {
		t.Helper()
		var output bytes.Buffer
		err := runKVMVMJob(t.Context(), api, vm.Host, "1", bytes.NewReader(profile), &output, runner)
		if current := get(t); current.State != phase || (err != nil) != failed {
			t.Fatalf("image stage: state=%+v need=%s err=%v", current, phase, err)
		}
	}
	t.Run("contended spool", func(t *testing.T) {
		root, err := openKVMImageRoot(pool)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := root.Close(); err != nil {
				t.Error(err)
			}
		})
		lock, err := lockKVMImageSpool(root, volume)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := lock.Close(); err != nil {
				t.Error(err)
			}
		})
		run(t, "staging", true)
		if got := get(t); got.Observation == "mismatch" {
			t.Fatalf("spool contention reported an image mismatch: %+v", got)
		}
	})
	run(t, "staging", true)
	if got := get(t); got.Observation != "mismatch" {
		t.Fatalf("invalid image was not recorded for the next poll: %+v", got)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("wrong image produced a boot volume: %v", err)
	}
	expectKVM(t, kvmRequest(t, first, fixtureKVMReplacement, "POST", stage, "", "",
		kvm.VMCommand{AgentIdentity: identity, Revision: get(t).Revision}), 403, nil)
	expectKVM(t, kvmRequest(t, first, fixtureKVMAdmin, "PATCH", fixtureVMHostPath, `"1"`, "",
		kvm.HostChange{Action: "rebind", Agent: "replacement-agent"}), 409, nil)
	if err := os.Remove(imagePath); err != nil {
		t.Fatal(err)
	}
	mirror, err := json.Marshal(kvmImageMirrorProfile{MirrorURL: server.URL + "/mirror", ImageRoot: source})
	if err != nil {
		t.Fatal(err)
	}
	if err := runKVMClient(t.Context(), []string{"mirror-image", "lab", "compute", "site-a", image.ID},
		server.URL, fixtureKVMAgent, bytes.NewReader(mirror), io.Discard, server.Client().Transport); err != nil {
		t.Fatal(err)
	}
	run(t, "staging", true)
	if err := os.Remove(imagePath); err != nil {
		t.Fatal(err)
	}
	run(t, "staged", false)
	write(target, make([]byte, len(contents)))
	run(t, "staged", true)
	if len(daemon.writes) != 0 {
		t.Fatalf("corrupted staged volume led to domain writes: %v", daemon.writes)
	}
	write(target, contents)
	run(t, "defining", true)
	run(t, "defined", false)
	run(t, "starting", true)
	run(t, "running", false)
	var staged int
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM cmp_kvm_events
		WHERE placement_id=$1 AND action IN ('vm_stage','vm_staged')`, placement.ID).Scan(&staged); err != nil || staged != 2 || refresh != 2 {
		t.Fatalf("image stage did not converge as expected: audit=%d refresh=%d %v", staged, refresh, err)
	}
}
