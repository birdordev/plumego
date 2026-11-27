package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"with-cloud-manager/internal/kvm"
)

type kvmBackupTarget struct {
	site, host, vm, image string
	generation            int64
	revision              int64
}

type kvmBackupReceipt struct {
	Image          kvm.BootImageSpec `json:"image"`
	Site           string            `json:"site"`
	VM             string            `json:"vm_id"`
	SourceHostUUID string            `json:"source_host_uuid"`
	Placement      string            `json:"source_placement_id"`
	Generation     int64             `json:"source_agent_generation"`
	Revision       int64             `json:"vm_revision"`
}

func parseKVMBackupTarget(args []string) (kvmBackupTarget, bool) {
	if len(args) != 9 || !kvmUnitName.MatchString(args[4]) ||
		!kvmMachineUUID.MatchString(args[6]) || !kvmUnitName.MatchString(args[8]) {
		return kvmBackupTarget{}, false
	}
	generation, err := strconv.ParseInt(args[5], 10, 64)
	revision, versionErr := strconv.ParseInt(args[7], 10, 64)
	if err != nil || versionErr != nil || generation < 1 || revision < 1 {
		return kvmBackupTarget{}, false
	}
	return kvmBackupTarget{site: args[3], host: args[4], vm: args[6], image: args[8], generation: generation, revision: revision}, true
}

func runKVMVMBackup(ctx context.Context, api kvmVMAPI, target kvmBackupTarget, input io.Reader, output io.Writer, probe kvmProbe) error {
	profile, err := readKVMAgentProfile(input)
	if err != nil {
		return err
	}
	if profile.ImageRoot == "" {
		return errKVMUnsafeTarget
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	var host kvm.Host
	if err := api.request(ctx, api.target, http.MethodGet, nil, &host); err != nil {
		return err
	}
	if host.ID != target.host || host.Site != target.site || host.Generation != target.generation || host.FenceState != "" ||
		!kvmMachineUUID.MatchString(host.UUID) {
		return errKVMUnsafeTarget
	}
	caps, err := probe.read(ctx, "capabilities")
	if err != nil {
		return err
	}
	if _, err := readKVMMemory(caps, host.UUID); err != nil {
		return err
	}
	probe.shared, err = loadKVMSharedStorage(ctx, api, host)
	if err != nil {
		return err
	}
	plan := api.target.JoinPath("vm-work", target.vm, "backup-plan")
	command := kvm.VMCommand{AgentIdentity: kvm.AgentIdentity{UUID: host.UUID, Generation: host.Generation}, Revision: target.revision}
	var vm kvm.VM
	if err := api.request(ctx, plan, http.MethodPost, command, &vm); err != nil {
		return err
	}
	if vm.ID != target.vm || vm.Revision != target.revision ||
		(vm.State != "retired" && vm.State != "rejected") || vm.Observation != "absent" ||
		vm.Generation < 1 || vm.Generation > host.Generation || !validKVMVMSpec(vm, host) ||
		(vm.ImageID == "" && !slices.Contains(profile.Volumes, vm.Volume)) {
		return errKVMUnsafeTarget
	}
	pool, err := probe.poolPath(ctx, vm.StoragePool)
	if err != nil {
		return err
	}
	if pool == "" || pool == profile.ImageRoot {
		return errKVMUnsafeTarget
	}
	if _, err := probe.poolFreeMiB(pool); err != nil {
		return err
	}
	size, err := readKVMRawVolume(ctx, probe.read, vm, pool)
	if err != nil {
		return err
	}
	authorize := func() error {
		var current kvm.VM
		if err := api.request(ctx, plan, http.MethodPost, command, &current); err != nil {
			return err
		}
		if current != vm {
			return errKVMUnsafeTarget
		}
		return verifyKVMBackupSource(ctx, probe, vm, pool, size)
	}
	image, err := publishKVMBackup(ctx, pool, profile.ImageRoot, vm.Volume, target.image, size, authorize)
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(kvmBackupReceipt{Image: image, Site: host.Site, VM: vm.ID,
		SourceHostUUID: host.UUID, Placement: vm.Placement, Generation: host.Generation, Revision: vm.Revision})
}

func verifyKVMBackupSource(ctx context.Context, probe kvmProbe, vm kvm.VM, pool string, size int64) error {
	path, err := probe.poolPath(ctx, vm.StoragePool)
	if err != nil {
		return err
	}
	if path != pool {
		return errKVMUnsafeTarget
	}
	current, err := readKVMRawVolume(ctx, probe.read, vm, pool)
	if err != nil {
		return err
	}
	if current != size {
		return errKVMUnsafeTarget
	}
	if err := checkKVMBeforeWrite(ctx, probe.read, vm, "absent"); err != nil {
		return err
	}
	return verifyKVMVolumeExclusive(ctx, probe.read, vm, filepath.Join(pool, vm.Volume))
}
