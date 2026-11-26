package main

import (
	"context"
	"encoding/xml"
	"net/netip"
	"slices"
	"strings"

	"with-cloud-manager/internal/kvm"
)

const kvmDHCPBatch = 16

type kvmDHCPDesired struct {
	spec   kvm.IPv4PoolSpec
	first  netip.Addr
	last   netip.Addr
	hosts  []kvmDHCPHost
	byIP   map[string]kvmDHCPHost
	byMAC  map[string]kvmDHCPHost
	byName map[string]kvmDHCPHost
}

func newKVMDHCPDesired(spec kvm.IPv4PoolSpec) kvmDHCPDesired {
	// 调用方已校验并限制地址池范围。
	first := netip.MustParseAddr(spec.First)
	last := netip.MustParseAddr(spec.Last)
	desired := kvmDHCPDesired{spec: spec, first: first, last: last, byIP: make(map[string]kvmDHCPHost),
		byMAC: make(map[string]kvmDHCPHost), byName: make(map[string]kvmDHCPHost)}
	for ip := first; ip.Compare(last) <= 0; ip = ip.Next() {
		text := ip.String()
		host := kvmDHCPHost{IP: text, MAC: kvm.IPv4MAC(text), Name: "cmp-ip-" + strings.ReplaceAll(text, ".", "-")}
		desired.hosts = append(desired.hosts, host)
		desired.byIP[host.IP], desired.byMAC[host.MAC], desired.byName[host.Name] = host, host, host
	}
	return desired
}

type kvmDHCPChange struct {
	kvmDHCPHost
	Live   bool `json:"live"`
	Config bool `json:"config"`
}

type kvmDHCPPlan struct {
	liveIndex   int
	configIndex int
	missing     []kvmDHCPChange
}

func (desired kvmDHCPDesired) observe(ctx context.Context, read virshReader, uuid string) (kvmDHCPPlan, error) {
	active, err := read(ctx, "net-list", "--uuid", "--persistent")
	if err != nil {
		return kvmDHCPPlan{}, err
	}
	if !slices.Contains(strings.Fields(string(active)), uuid) {
		return kvmDHCPPlan{}, errKVMNetworkMapping
	}
	live, liveIndex, liveMode, err := desired.readConfig(ctx, read, uuid, false)
	if err != nil {
		return kvmDHCPPlan{}, err
	}
	config, configIndex, configMode, err := desired.readConfig(ctx, read, uuid, true)
	if err != nil {
		return kvmDHCPPlan{}, err
	}
	if liveMode != configMode {
		return kvmDHCPPlan{}, errKVMNetworkMapping
	}
	leases, err := read(ctx, "net-dhcp-leases", uuid)
	if err != nil {
		return kvmDHCPPlan{}, err
	}
	if !desired.compatibleLeases(string(leases)) {
		return kvmDHCPPlan{}, errKVMNetworkMapping
	}
	plan := kvmDHCPPlan{liveIndex: liveIndex, configIndex: configIndex}
	for _, host := range desired.hosts {
		addLive, addConfig := !live[host.IP], !config[host.IP]
		if addLive || addConfig {
			plan.missing = append(plan.missing, kvmDHCPChange{kvmDHCPHost: host, Live: addLive, Config: addConfig})
		}
	}
	return plan, nil
}

func (desired kvmDHCPDesired) readConfig(ctx context.Context, read virshReader, uuid string, inactive bool) (map[string]bool, int, string, error) {
	args := []string{"net-dumpxml", "--network", uuid}
	if inactive {
		args = append(args, "--inactive")
	}
	data, err := read(ctx, args...)
	if err != nil {
		return nil, 0, "", err
	}
	var network kvmNetwork
	if xml.Unmarshal(data, &network) != nil || network.UUID != uuid {
		return nil, 0, "", errKVMNetworkMapping
	}
	index, ok := network.ipv4Index(desired.spec.Network, desired.spec.Subnet, desired.spec.Gateway)
	if !ok {
		return nil, 0, "", errKVMNetworkMapping
	}
	address := network.Addresses[index]
	for _, span := range address.Ranges {
		first, last, valid := span.parsed()
		if !valid || (first.Compare(desired.last) <= 0 && last.Compare(desired.first) >= 0) {
			return nil, 0, "", errKVMNetworkMapping
		}
	}
	installed := make(map[string]bool, len(desired.hosts))
	for i, subnet := range network.Addresses {
		for _, host := range subnet.Hosts {
			ip := desired.byIP[host.IP]
			mac := desired.byMAC[strings.ToLower(host.MAC)]
			name := desired.byName[strings.ToLower(host.Name)]
			if ip.IP == "" && mac.IP == "" && name.IP == "" {
				continue
			}
			if i != index || ip.IP == "" || ip.IP != mac.IP || (name.IP != "" && name.IP != ip.IP) ||
				host.Name == "" || installed[host.IP] {
				return nil, 0, "", errKVMNetworkMapping
			}
			installed[host.IP] = true
		}
	}
	return installed, index, network.Forward.Mode, nil
}

func (desired kvmDHCPDesired) compatibleLeases(table string) bool {
	lines := strings.Split(strings.TrimSpace(table), "\n")
	if len(lines) < 2 || strings.Join(strings.Fields(lines[0]), " ") !=
		"Expiry Time MAC address Protocol IP address Hostname Client ID or DUID" ||
		strings.Trim(strings.TrimSpace(lines[1]), "-") != "" {
		return false
	}
	for _, line := range lines[2:] {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			return false
		}
		prefix, err := netip.ParsePrefix(fields[4])
		if err != nil {
			return false
		}
		if fields[3] == "ipv6" && prefix.Addr().Is6() {
			continue
		}
		if fields[3] != "ipv4" || !prefix.Addr().Is4() {
			return false
		}
		ip := desired.byIP[prefix.Addr().String()]
		mac := desired.byMAC[strings.ToLower(fields[2])]
		if ip.IP != mac.IP {
			return false
		}
	}
	return true
}
