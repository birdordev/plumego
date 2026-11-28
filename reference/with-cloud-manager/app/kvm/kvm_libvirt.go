package kvmapp

import (
	"context"
	"encoding/xml"
	"errors"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"with-cloud-manager/internal/kvm"
)

var ErrInvalidKVMObservation = errors.New("local libvirt response does not identify a usable KVM host")

type virshCapabilities struct {
	XMLName xml.Name `xml:"capabilities"`
	Host    struct {
		UUID  string `xml:"uuid"`
		Cells []struct {
			Memory struct {
				KiB  int64  `xml:",chardata"`
				Unit string `xml:"unit,attr"`
			} `xml:"memory"`
		} `xml:"topology>cells>cell"`
	} `xml:"host"`
	Guests []struct {
		Domains []struct {
			Type string `xml:"type,attr"`
		} `xml:"arch>domain"`
	} `xml:"guest"`
}

type KVMProbe struct {
	Read          VirshReader
	FreeMiB       func(string) (int64, error)
	SharedFreeMiB func(string) (int64, error)
	MountInfo     func(context.Context) ([]byte, error)
	shared        kvm.SharedStorageSpec
}

func (probe KVMProbe) capacity(ctx context.Context, host kvm.Host, limits kvm.Capacity) (kvm.Capacity, error) {
	document, err := probe.Read(ctx, "capabilities")
	if err != nil {
		return kvm.Capacity{}, err
	}
	memory, err := readKVMMemory(document, host.UUID)
	if err != nil {
		return kvm.Capacity{}, err
	}
	networks, err := probe.Read(ctx, "net-list", "--name")
	if err != nil {
		return kvm.Capacity{}, err
	}
	active := strings.Fields(string(networks))
	for _, network := range host.Networks {
		if !slices.Contains(active, network) {
			return kvm.Capacity{}, nil
		}
	}
	cpus, err := probe.Read(ctx, "nodecpumap")
	if err != nil {
		return kvm.Capacity{}, err
	}
	online, err := readKVMOnlineCPUs(string(cpus))
	if err != nil {
		return kvm.Capacity{}, err
	}
	space, err := probe.storage(ctx, host.StoragePool)
	if err != nil {
		return kvm.Capacity{}, err
	}
	return kvm.Capacity{VCPU: min(limits.VCPU, online), MemoryMiB: min(limits.MemoryMiB, memory),
		DiskMiB: min(limits.DiskMiB, space)}, nil
}

func readKVMMemory(data []byte, expectedUUID string) (int64, error) {
	var caps virshCapabilities
	if xml.Unmarshal(data, &caps) != nil || caps.Host.UUID != expectedUUID || expectedUUID == "" {
		return 0, ErrInvalidKVMObservation
	}
	var supported bool
	for _, guest := range caps.Guests {
		for _, domain := range guest.Domains {
			supported = supported || domain.Type == "kvm"
		}
	}
	var memory int64
	for _, cell := range caps.Host.Cells {
		if cell.Memory.Unit != "KiB" || cell.Memory.KiB < 1 || cell.Memory.KiB > (1<<50)-memory {
			return 0, ErrInvalidKVMObservation
		}
		memory += cell.Memory.KiB
	}
	if !supported || memory < 1024 {
		return 0, ErrInvalidKVMObservation
	}
	return memory / 1024, nil
}

func readKVMOnlineCPUs(data string) (int64, error) {
	for _, line := range strings.Split(data, "\n") {
		key, raw, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(key) != "CPUs online" {
			continue
		}
		if value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64); err == nil && value > 0 {
			return value, nil
		}
		break
	}
	return 0, ErrInvalidKVMObservation
}

func (probe KVMProbe) storage(ctx context.Context, pool string) (int64, error) {
	path, err := probe.poolPath(ctx, pool)
	if err != nil || path == "" {
		return 0, err
	}
	return probe.poolFreeMiB(path)
}

func (probe KVMProbe) poolFreeMiB(path string) (int64, error) {
	if probe.shared.ID == "" {
		return probe.FreeMiB(path)
	}
	if probe.SharedFreeMiB != nil {
		return probe.SharedFreeMiB(path)
	}
	return kvmSharedFreeMiB(path)
}

func (probe KVMProbe) poolPath(ctx context.Context, pool string) (string, error) {
	pools, err := probe.Read(ctx, "pool-list", "--name")
	if err != nil || !slices.Contains(strings.Fields(string(pools)), pool) {
		return "", err
	}
	document, err := probe.Read(ctx, "pool-dumpxml", "--pool", pool)
	if err != nil {
		return "", err
	}
	var volume KVMPoolXML
	if xml.Unmarshal(document, &volume) != nil || volume.Name != pool ||
		!filepath.IsAbs(volume.Path) || filepath.Clean(volume.Path) != volume.Path {
		return "", ErrInvalidKVMObservation
	}
	if probe.shared.ID != "" {
		return probe.sharedPoolPath(ctx, volume)
	}
	if volume.Type != "dir" {
		return "", ErrInvalidKVMObservation
	}
	return volume.Path, nil
}
