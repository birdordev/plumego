package kvmapp

import (
	"context"
	"encoding/xml"
	"errors"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"with-cloud-manager/internal/kvm"
)

var kvmUnitName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
var kvmMachineUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var ErrKVMUnsafeTarget = errors.New("local libvirt VM or boot volume does not match the approved request")

type KVMDomain struct {
	XMLName       xml.Name       `xml:"domain"`
	Type          string         `xml:"type,attr"`
	Name          string         `xml:"name"`
	UUID          string         `xml:"uuid"`
	Memory        kvmMemory      `xml:"memory"`
	CurrentMemory *kvmMemory     `xml:"currentMemory,omitempty"`
	MaxMemory     *kvmMemory     `xml:"maxMemory,omitempty"`
	VCPU          int64          `xml:"vcpu"`
	Metadata      kvmMetadata    `xml:"metadata"`
	OS            *kvmDomainOS   `xml:"os,omitempty"`
	Features      *kvmFeatures   `xml:"features,omitempty"`
	Disks         []kvmDisk      `xml:"devices>disk"`
	Networks      []kvmInterface `xml:"devices>interface"`
	MemoryDevices []struct{}     `xml:"devices>memory"`
	HostDevices   []struct{}     `xml:"devices>hostdev"`
	TPMDevices    []struct{}     `xml:"devices>tpm"`
}

type kvmMetadata struct {
	Owner *kvmOwner `xml:"https://plumego.dev/cmp/kvm/1 ownership"`
}

type kvmOwner struct {
	Placement string `xml:"placement,attr"`
}

type kvmMemory struct {
	Unit   string `xml:"unit,attr"`
	Amount int64  `xml:",chardata"`
}

type kvmFeatures struct {
	ACPI *struct{} `xml:"acpi,omitempty"`
}

type kvmDomainOS struct {
	Type     string        `xml:"type"`
	Boot     kvmBootDevice `xml:"boot"`
	NVRAM    *struct{}     `xml:"nvram,omitempty"`
	Varstore *struct{}     `xml:"varstore,omitempty"`
}

type kvmBootDevice struct {
	Device string `xml:"dev,attr"`
}

type kvmDisk struct {
	Type    string        `xml:"type,attr"`
	Device  string        `xml:"device,attr"`
	Driver  kvmDiskDriver `xml:"driver"`
	Source  kvmDiskSource `xml:"source"`
	Target  kvmDiskTarget `xml:"target"`
	Backing *kvmBacking   `xml:"backingStore,omitempty"`
}

type kvmBacking struct {
	Source kvmDiskSource `xml:"source"`
	Parent *kvmBacking   `xml:"backingStore"`
}

type kvmDiskDriver struct {
	Name   string `xml:"name,attr"`
	Format string `xml:"type,attr"`
	Cache  string `xml:"cache,attr,omitempty"`
}

type kvmDiskSource struct {
	Pool   string `xml:"pool,attr,omitempty"`
	Volume string `xml:"volume,attr,omitempty"`
	File   string `xml:"file,attr,omitempty"`
}

type kvmDiskTarget struct {
	Device string `xml:"dev,attr"`
	Bus    string `xml:"bus,attr"`
}

type kvmInterface struct {
	Type   string           `xml:"type,attr"`
	Source kvmNetworkSource `xml:"source"`
	MAC    *KVMMAC          `xml:"mac,omitempty"`
}

type KVMMAC struct {
	Address string `xml:"address,attr"`
}

type kvmNetworkSource struct {
	Network string `xml:"network,attr"`
}

func DesiredKVMDomain(vm kvm.VM) KVMDomain {
	link := kvmInterface{Type: "network", Source: kvmNetworkSource{Network: vm.Network}}
	if vm.MAC != "" {
		link.MAC = &KVMMAC{Address: vm.MAC}
	}
	driver := kvmDiskDriver{Name: "qemu", Format: "raw"}
	if vm.SharedStoragePool != "" {
		driver.Cache = "none"
	}
	return KVMDomain{
		Type: "kvm", Name: "cmp-" + vm.ID, UUID: vm.ID,
		VCPU: vm.Capacity.VCPU, Memory: kvmMemory{Unit: "KiB", Amount: vm.Capacity.MemoryMiB * 1024},
		Metadata: kvmMetadata{Owner: &kvmOwner{Placement: vm.Placement}}, OS: &kvmDomainOS{Type: "hvm", Boot: kvmBootDevice{Device: "hd"}},
		Disks: []kvmDisk{{Type: "volume", Device: "disk", Driver: driver,
			Source: kvmDiskSource{Pool: vm.StoragePool, Volume: vm.Volume}, Target: kvmDiskTarget{Device: "vda", Bus: "virtio"}}},
		Networks: []kvmInterface{link},
	}
}

func (domain KVMDomain) matches(vm kvm.VM) bool {
	want := DesiredKVMDomain(vm)
	return domain.Type == want.Type && domain.Name == want.Name && domain.UUID == want.UUID &&
		domain.OS != nil && domain.OS.Type == want.OS.Type && domain.OS.Boot == want.OS.Boot &&
		domain.Memory == want.Memory && (domain.CurrentMemory == nil ||
		((domain.CurrentMemory.Unit == "" || domain.CurrentMemory.Unit == "KiB") &&
			domain.CurrentMemory.Amount > 0 && domain.CurrentMemory.Amount <= want.Memory.Amount)) &&
		domain.MaxMemory == nil && len(domain.MemoryDevices) == 0 && len(domain.HostDevices) == 0 &&
		domain.VCPU == want.VCPU && domain.Metadata.Owner != nil && *domain.Metadata.Owner == *want.Metadata.Owner &&
		len(domain.Disks) == 1 && (domain.Disks[0].Backing == nil || *domain.Disks[0].Backing == (kvmBacking{})) &&
		domain.Disks[0].Type == want.Disks[0].Type && domain.Disks[0].Device == want.Disks[0].Device &&
		domain.Disks[0].Driver.matches(want.Disks[0].Driver) && domain.Disks[0].Source == want.Disks[0].Source &&
		domain.Disks[0].Target == want.Disks[0].Target && len(domain.Networks) == 1 &&
		domain.Networks[0].matches(want.Networks[0])
}

func (driver kvmDiskDriver) matches(want kvmDiskDriver) bool {
	// 常规生命周期沿用已有比较，实时迁移另行要求显式 cache=none。
	return driver.Name == want.Name && driver.Format == want.Format
}

func (iface kvmInterface) matches(want kvmInterface) bool {
	return iface.Type == want.Type && iface.Source == want.Source &&
		(want.MAC == nil || (iface.MAC != nil && strings.EqualFold(iface.MAC.Address, want.MAC.Address)))
}

func localKVMUUIDs(ctx context.Context, read VirshReader, filters ...string) ([]string, error) {
	data, err := read(ctx, append([]string{"list", "--all", "--uuid"}, filters...)...)
	if err != nil {
		return nil, err
	}
	ids := strings.Fields(string(data))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		_, duplicate := seen[id]
		if duplicate || !kvmMachineUUID.MatchString(id) {
			return nil, ErrKVMUnsafeTarget
		}
		seen[id] = struct{}{}
	}
	return ids, nil
}

type kvmDomainObservation struct {
	state           string
	acpi            bool
	retireProtected bool
}

func inspectLocalVM(ctx context.Context, read VirshReader, vm kvm.VM) (kvmDomainObservation, error) {
	ids, err := localKVMUUIDs(ctx, read)
	if err != nil {
		return kvmDomainObservation{}, err
	}
	if !slices.Contains(ids, vm.ID) {
		return kvmDomainObservation{state: "absent"}, nil
	}
	return inspectPresentKVMVM(ctx, read, vm)
}

func inspectPersistentKVMVM(ctx context.Context, read VirshReader, vm kvm.VM) (kvmDomainObservation, error) {
	observed, err := inspectLocalVM(ctx, read, vm)
	if err != nil || (observed.state != "defined" && observed.state != "running") {
		return observed, err
	}
	persistent, err := localKVMUUIDs(ctx, read, "--persistent")
	if err != nil {
		return kvmDomainObservation{}, err
	}
	if !slices.Contains(persistent, vm.ID) {
		return kvmDomainObservation{state: "mismatch"}, nil
	}
	return observed, nil
}

func inspectPresentKVMVM(ctx context.Context, read VirshReader, vm kvm.VM) (kvmDomainObservation, error) {
	domain, err := readDomain(ctx, read, vm.ID, true)
	if err != nil {
		return kvmDomainObservation{}, err
	}
	if !domain.matches(vm) {
		return kvmDomainObservation{state: "mismatch"}, nil
	}
	state, err := read(ctx, "domstate", vm.ID)
	if err != nil {
		return kvmDomainObservation{}, err
	}
	switch strings.TrimSpace(string(state)) {
	case "shut off":
		return kvmDomainObservation{state: "defined",
			retireProtected: domain.OS.NVRAM != nil || domain.OS.Varstore != nil || len(domain.TPMDevices) > 0}, nil
	case "running":
		live, err := readDomain(ctx, read, vm.ID, false)
		if err != nil {
			return kvmDomainObservation{}, err
		}
		if live.matches(vm) {
			return kvmDomainObservation{state: "running", acpi: live.Features != nil && live.Features.ACPI != nil}, nil
		}
	}
	return kvmDomainObservation{state: "mismatch"}, nil
}

func readDomain(ctx context.Context, read VirshReader, id string, inactive bool) (KVMDomain, error) {
	args := []string{"dumpxml", id}
	if inactive {
		args = append(args, "--inactive")
	}
	data, err := read(ctx, args...)
	if err != nil {
		return KVMDomain{}, err
	}
	var domain KVMDomain
	if xml.Unmarshal(data, &domain) != nil || domain.UUID != id {
		return KVMDomain{}, ErrKVMUnsafeTarget
	}
	return domain, nil
}

func validateKVMVolume(ctx context.Context, runner KVMVMRunner, vm kvm.VM) error {
	probe := runner.KVMProbe
	path, err := probe.poolPath(ctx, vm.StoragePool)
	if err != nil {
		return err
	}
	if path == "" {
		return ErrKVMUnsafeTarget
	}
	if _, err := probe.poolFreeMiB(path); err != nil {
		return err
	}
	networks, err := probe.Read(ctx, "net-list", "--name")
	if err != nil {
		return err
	}
	if !slices.Contains(strings.Fields(string(networks)), vm.Network) {
		return ErrKVMUnsafeTarget
	}
	if err := validateKVMNetwork(ctx, probe.Read, vm); err != nil {
		return err
	}
	size, err := readKVMRawVolume(ctx, probe.Read, vm, path)
	if err != nil {
		return err
	}
	target := filepath.Join(path, vm.Volume)
	if err := runner.BootFile(target, size); err != nil {
		return err
	}
	return verifyKVMVolumeExclusive(ctx, probe.Read, vm, target)
}

func readKVMRawVolume(ctx context.Context, read VirshReader, vm kvm.VM, pool string) (int64, error) {
	info, err := read(ctx, "vol-dumpxml", "--pool", vm.StoragePool, "--vol", vm.Volume)
	if err != nil {
		return 0, err
	}
	var volume struct {
		XMLName    xml.Name  `xml:"volume"`
		Name       string    `xml:"name"`
		Capacity   kvmMemory `xml:"capacity"`
		TargetPath string    `xml:"target>path"`
		Format     struct {
			Type string `xml:"type,attr"`
		} `xml:"target>format"`
		Backing *struct {
			Path string `xml:"path"`
		} `xml:"backingStore"`
	}
	if xml.Unmarshal(info, &volume) != nil || volume.Name != vm.Volume ||
		volume.TargetPath != filepath.Join(pool, vm.Volume) || volume.Format.Type != "raw" ||
		(volume.Capacity.Unit != "" && volume.Capacity.Unit != "bytes") ||
		volume.Capacity.Amount < 1 || volume.Capacity.Amount > vm.Capacity.DiskMiB*(1<<20) ||
		(vm.ImageID != "" && volume.Capacity.Amount != vm.ImageSizeBytes) ||
		(volume.Backing != nil && volume.Backing.Path != "") {
		return 0, ErrKVMUnsafeTarget
	}
	return volume.Capacity.Amount, nil
}

func verifyKVMVolumeExclusive(ctx context.Context, read VirshReader, vm kvm.VM, path string) error {
	ids, err := localKVMUUIDs(ctx, read)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if id == vm.ID {
			continue
		}
		for _, inactive := range []bool{true, false} {
			domain, err := readDomain(ctx, read, id, inactive)
			if err != nil {
				return err
			}
			for _, disk := range domain.Disks {
				if namedKVMVolume(disk.Source, vm, path) {
					return ErrKVMUnsafeTarget
				}
				for backing := disk.Backing; backing != nil; backing = backing.Parent {
					if namedKVMVolume(backing.Source, vm, path) {
						return ErrKVMUnsafeTarget
					}
				}
			}
		}
	}
	return nil
}

func namedKVMVolume(source kvmDiskSource, vm kvm.VM, path string) bool {
	return (source.Pool == vm.StoragePool && source.Volume == vm.Volume) || source.File == path
}
