package kvmapp

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
	Generation            int64
	revision              int64
}

type KVMBackupReceipt struct {
	Image          kvm.BootImageSpec `json:"image"`
	Site           string            `json:"site"`
	VM             string            `json:"vm_id"`
	SourceHostUUID string            `json:"source_host_uuid"`
	Placement      string            `json:"source_placement_id"`
	Generation     int64             `json:"source_agent_generation"`
	Revision       int64             `json:"vm_revision"`
}

func ParseKVMBackupTarget(args []string) (kvmBackupTarget, bool) {
	if len(args) != 9 || !kvmUnitName.MatchString(args[4]) ||
		!kvmMachineUUID.MatchString(args[6]) || !kvmUnitName.MatchString(args[8]) {
		return kvmBackupTarget{}, false
	}
	generation, err := strconv.ParseInt(args[5], 10, 64)
	revision, versionErr := strconv.ParseInt(args[7], 10, 64)
	if err != nil || versionErr != nil || generation < 1 || revision < 1 {
		return kvmBackupTarget{}, false
	}
	return kvmBackupTarget{site: args[3], host: args[4], vm: args[6], image: args[8], Generation: generation, revision: revision}, true
}

func RunKVMVMBackup(ctx context.Context, api KVMVMAPI, target kvmBackupTarget, input io.Reader, output io.Writer, probe KVMProbe) error {
	profile, err := readKVMAgentProfile(input)
	if err != nil {
		return err
	}
	if profile.ImageRoot == "" {
		return ErrKVMUnsafeTarget
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	var host kvm.Host
	if err := api.request(ctx, api.Target, http.MethodGet, nil, &host); err != nil {
		return err
	}
	if host.ID != target.host || host.Site != target.site || host.Generation != target.Generation || host.FenceState != "" ||
		!kvmMachineUUID.MatchString(host.UUID) {
		return ErrKVMUnsafeTarget
	}
	caps, err := probe.Read(ctx, "capabilities")
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
	plan := api.Target.JoinPath("vm-work", target.vm, "backup-plan")
	command := kvm.VMCommand{AgentIdentity: kvm.AgentIdentity{UUID: host.UUID, Generation: host.Generation}, Revision: target.revision}
	var vm kvm.VM
	if err := api.request(ctx, plan, http.MethodPost, command, &vm); err != nil {
		return err
	}
	if vm.ID != target.vm || vm.Revision != target.revision ||
		(vm.State != "retired" && vm.State != "rejected") || vm.Observation != "absent" ||
		vm.Generation < 1 || vm.Generation > host.Generation || !validKVMVMSpec(vm, host) ||
		(vm.ImageID == "" && !slices.Contains(profile.Volumes, vm.Volume)) {
		return ErrKVMUnsafeTarget
	}
	pool, err := probe.poolPath(ctx, vm.StoragePool)
	if err != nil {
		return err
	}
	if pool == "" || pool == profile.ImageRoot {
		return ErrKVMUnsafeTarget
	}
	if _, err := probe.poolFreeMiB(pool); err != nil {
		return err
	}
	size, err := readKVMRawVolume(ctx, probe.Read, vm, pool)
	if err != nil {
		return err
	}
	authorize := func() error {
		var current kvm.VM
		if err := api.request(ctx, plan, http.MethodPost, command, &current); err != nil {
			return err
		}
		if current != vm {
			return ErrKVMUnsafeTarget
		}
		return verifyKVMBackupSource(ctx, probe, vm, pool, size)
	}
	image, err := PublishKVMBackup(ctx, pool, profile.ImageRoot, vm.Volume, target.image, size, authorize)
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(KVMBackupReceipt{Image: image, Site: host.Site, VM: vm.ID,
		SourceHostUUID: host.UUID, Placement: vm.Placement, Generation: host.Generation, Revision: vm.Revision})
}

func verifyKVMBackupSource(ctx context.Context, probe KVMProbe, vm kvm.VM, pool string, size int64) error {
	path, err := probe.poolPath(ctx, vm.StoragePool)
	if err != nil {
		return err
	}
	if path != pool {
		return ErrKVMUnsafeTarget
	}
	current, err := readKVMRawVolume(ctx, probe.Read, vm, pool)
	if err != nil {
		return err
	}
	if current != size {
		return ErrKVMUnsafeTarget
	}
	if err := checkKVMBeforeWrite(ctx, probe.Read, vm, "absent"); err != nil {
		return err
	}
	return verifyKVMVolumeExclusive(ctx, probe.Read, vm, filepath.Join(pool, vm.Volume))
}
