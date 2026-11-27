package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"net"
	"path/filepath"
	"slices"
	"strings"

	"with-cloud-manager/internal/kvm"
)

func liveKVMNetwork(ctx context.Context, read virshReader, name string) (string, error) {
	networks, err := read(ctx, "net-list", "--name")
	if err != nil {
		return "", err
	}
	if !slices.Contains(strings.Fields(string(networks)), name) {
		return "", errKVMNetworkMapping
	}
	var digest string
	for _, flags := range [][]string{nil, {"--inactive"}} {
		raw, err := read(ctx, append([]string{"net-dumpxml", "--network", name}, flags...)...)
		if err != nil {
			return "", err
		}
		value, err := liveKVMBridgeDigest(raw, name)
		if err != nil || (digest != "" && value != digest) {
			return "", errKVMNetworkMapping
		}
		digest = value
	}
	return digest, nil
}

func liveKVMBridgeDigest(raw []byte, name string) (string, error) {
	var network struct {
		XMLName xml.Name `xml:"network"`
		Name    string   `xml:"name"`
		UUID    string   `xml:"uuid"`
		Forward []struct {
			Mode string `xml:"mode,attr"`
		} `xml:"forward"`
		Bridges []struct {
			Name string `xml:"name,attr"`
		} `xml:"bridge"`
		Addresses  []struct{} `xml:"ip"`
		VLANs      []struct{} `xml:"vlan"`
		PortGroups []struct{} `xml:"portgroup"`
	}
	if xml.Unmarshal(raw, &network) != nil || network.Name != name || !kvmMachineUUID.MatchString(network.UUID) ||
		len(network.Forward) != 1 || network.Forward[0].Mode != "bridge" || len(network.Bridges) != 1 ||
		!kvmUnitName.MatchString(network.Bridges[0].Name) || len(network.Addresses) != 0 || len(network.VLANs) != 0 ||
		len(network.PortGroups) != 0 {
		return "", errKVMNetworkMapping
	}
	hash := sha256.Sum256([]byte(name + "/" + network.UUID + "/" + network.Bridges[0].Name))
	return hex.EncodeToString(hash[:]), nil
}

func liveKVMVolume(ctx context.Context, runner kvmLiveMigrationRunner, vm kvm.VM) error {
	if runner.shared.ID == "" || runner.shared.ID != vm.SharedStoragePool {
		return errKVMUnsafeTarget
	}
	pool, err := runner.poolPath(ctx, vm.StoragePool)
	if err != nil {
		return err
	}
	if pool == "" {
		return errKVMUnsafeTarget
	}
	if _, err := runner.poolFreeMiB(pool); err != nil {
		return err
	}
	path := filepath.Join(pool, vm.Volume)
	if err := runner.bootFile(path, vm.ImageSizeBytes); err != nil {
		return err
	}
	return verifyKVMVolumeExclusive(ctx, runner.read, vm, path)
}

func liveKVMDomainMAC(ctx context.Context, read virshReader, vm kvm.VM) (string, error) {
	persistent, err := localKVMUUIDs(ctx, read, "--persistent")
	if err != nil {
		return "", err
	}
	if !slices.Contains(persistent, vm.ID) {
		return "", errKVMUnsafeTarget
	}
	state, err := read(ctx, "domstate", vm.ID)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(string(state)) != "running" {
		return "", errKVMUnsafeTarget
	}
	var expected string
	for _, inactive := range []bool{true, false} {
		domain, err := readDomain(ctx, read, vm.ID, inactive)
		if err != nil {
			return "", err
		}
		if !domain.matches(vm) || domain.OS.NVRAM != nil || domain.OS.Varstore != nil || len(domain.TPMDevices) != 0 ||
			domain.Disks[0].Driver.Cache != "none" || domain.Networks[0].MAC == nil {
			return "", errKVMUnsafeTarget
		}
		address, err := net.ParseMAC(domain.Networks[0].MAC.Address)
		if err != nil || len(address) != 6 || address[0]&1 != 0 || (expected != "" && address.String() != expected) {
			return "", errKVMUnsafeTarget
		}
		expected = address.String()
	}
	return expected, nil
}

func liveKVMAbsent(ctx context.Context, read virshReader, id string) (bool, error) {
	ids, err := localKVMUUIDs(ctx, read)
	return !slices.Contains(ids, id), err
}
