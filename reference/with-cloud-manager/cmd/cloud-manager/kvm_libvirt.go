package main

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

var errInvalidKVMObservation = errors.New("local libvirt response does not identify a usable KVM host")

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

type kvmProbe struct {
	read          virshReader
	freeMiB       func(string) (int64, error)
	sharedFreeMiB func(string) (int64, error)
	mountInfo     func(context.Context) ([]byte, error)
	shared        kvm.SharedStorageSpec
}

func (probe kvmProbe) capacity(ctx context.Context, host kvm.Host, limits kvm.Capacity) (kvm.Capacity, error) {
	document, err := probe.read(ctx, "capabilities")
	if err != nil {
		return kvm.Capacity{}, err
	}
	memory, err := readKVMMemory(document, host.UUID)
	if err != nil {
		return kvm.Capacity{}, err
	}
	networks, err := probe.read(ctx, "net-list", "--name")
	if err != nil {
		return kvm.Capacity{}, err
	}
	active := strings.Fields(string(networks))
	for _, network := range host.Networks {
		if !slices.Contains(active, network) {
			return kvm.Capacity{}, nil
		}
	}
	cpus, err := probe.read(ctx, "nodecpumap")
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
		return 0, errInvalidKVMObservation
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
			return 0, errInvalidKVMObservation
		}
		memory += cell.Memory.KiB
	}
	if !supported || memory < 1024 {
		return 0, errInvalidKVMObservation
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
	return 0, errInvalidKVMObservation
}

func (probe kvmProbe) storage(ctx context.Context, pool string) (int64, error) {
	path, err := probe.poolPath(ctx, pool)
	if err != nil || path == "" {
		return 0, err
	}
	return probe.poolFreeMiB(path)
}

func (probe kvmProbe) poolFreeMiB(path string) (int64, error) {
	if probe.shared.ID == "" {
		return probe.freeMiB(path)
	}
	if probe.sharedFreeMiB != nil {
		return probe.sharedFreeMiB(path)
	}
	return kvmSharedFreeMiB(path)
}

func (probe kvmProbe) poolPath(ctx context.Context, pool string) (string, error) {
	pools, err := probe.read(ctx, "pool-list", "--name")
	if err != nil || !slices.Contains(strings.Fields(string(pools)), pool) {
		return "", err
	}
	document, err := probe.read(ctx, "pool-dumpxml", "--pool", pool)
	if err != nil {
		return "", err
	}
	var volume kvmPoolXML
	if xml.Unmarshal(document, &volume) != nil || volume.Name != pool ||
		!filepath.IsAbs(volume.Path) || filepath.Clean(volume.Path) != volume.Path {
		return "", errInvalidKVMObservation
	}
	if probe.shared.ID != "" {
		return probe.sharedPoolPath(ctx, volume)
	}
	if volume.Type != "dir" {
		return "", errInvalidKVMObservation
	}
	return volume.Path, nil
}
