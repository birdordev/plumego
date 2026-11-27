//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"with-cloud-manager/internal/kvm"
)

func exportMigrationBackup(t *testing.T, handler http.Handler, vm kvm.VM, contents []byte, image kvm.BootImageSpec) ([]byte, string) {
	t.Helper()
	pool, destination := imageFixtureDir(t), imageFixtureDir(t)
	source := filepath.Join(pool, vm.Volume)
	if err := os.WriteFile(source, contents, 0600); err != nil {
		t.Fatal(err)
	}
	const generation = "1"
	rev := strconv.FormatInt(vm.Revision, 10)
	args := []string{"backup-vm", "lab", "compute", "site-a", vm.Host, generation, vm.ID, rev, image.ID}
	target, ok := parseKVMBackupTarget(args)
	if !ok {
		t.Fatal("valid backup arguments were rejected")
	}
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	clientPlan, err := planKVMRequest(base, args)
	if err != nil || clientPlan.target.String() != base.String()+fixtureVMHostPath {
		t.Fatalf("invalid backup CLI target: %+v, %v", clientPlan, err)
	}
	plan := fixtureVMHostPath + "/vm-work/" + vm.ID + "/backup-plan"
	command := kvm.VMCommand{AgentIdentity: kvm.AgentIdentity{UUID: fixtureKVMUUID, Generation: target.generation}, Revision: vm.Revision}
	expectKVM(t, kvmRequest(t, handler, fixtureKVMOperator, "POST", plan, "", "", command), 403, nil)
	expectKVM(t, kvmRequest(t, handler, fixtureKVMReplacement, "POST", plan, "", "", command), 403, nil)
	command.Revision--
	expectKVM(t, kvmRequest(t, handler, fixtureKVMAgent, "POST", plan, "", "", command), 412, nil)
	probe := backupMigrationProbe(t, vm, pool, image.SizeBytes)
	api := kvmVMAPI{target: clientPlan.target, token: fixtureKVMAgent, transport: server.Client().Transport}
	profile := fmt.Sprintf(`{"image_root":%q,"boot_volumes":[%q]}`, destination, vm.Volume)
	var output bytes.Buffer
	if err := runKVMVMBackup(t.Context(), api, target, strings.NewReader(profile), &output, probe); err != nil {
		t.Fatal(err)
	}
	var receipt kvmBackupReceipt
	if err := json.Unmarshal(output.Bytes(), &receipt); err != nil || receipt.Image != image || receipt.VM != vm.ID ||
		receipt.SourceHostUUID != fixtureKVMUUID || receipt.Placement != vm.Placement || receipt.Revision != vm.Revision ||
		receipt.Site != "site-a" || receipt.Generation != 1 {
		t.Fatalf("invalid source backup receipt: %s, %v", output.String(), err)
	}
	copied, err := os.ReadFile(filepath.Join(destination, image.SHA256+".raw"))
	if err != nil || !bytes.Equal(copied, contents) {
		t.Fatalf("invalid backup content: %v", err)
	}
	evidence := sha256.Sum256(output.Bytes())
	return copied, hex.EncodeToString(evidence[:])
}

func backupMigrationProbe(t *testing.T, vm kvm.VM, pool string, size int64) kvmProbe {
	t.Helper()
	probe := fixtureKVMProbe(t, fixtureKVMUUID, "", 64_000)
	read := probe.read
	probe.read = func(ctx context.Context, args ...string) ([]byte, error) {
		switch strings.Join(args, " ") {
		case "pool-dumpxml --pool local":
			return []byte(`<pool type="dir"><name>local</name><target><path>` + pool + `</path></target></pool>`), nil
		case "vol-dumpxml --pool local --vol " + vm.Volume:
			return []byte(fmt.Sprintf(`<volume><name>%s</name><capacity unit="bytes">%d</capacity><target><path>%s</path><format type="raw"/></target></volume>`,
				vm.Volume, size, filepath.Join(pool, vm.Volume))), nil
		default:
			return read(ctx, args...)
		}
	}
	probe.freeMiB = func(path string) (int64, error) {
		if path != pool {
			return 0, errKVMUnsafeTarget
		}
		return 64_000, nil
	}
	return probe
}
