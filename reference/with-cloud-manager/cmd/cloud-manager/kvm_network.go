package main

import (
	"context"
	"encoding/xml"
	"errors"
	"net"
	"net/netip"
	"strings"

	"with-cloud-manager/internal/kvm"
)

var errKVMNetworkMapping = errors.New("local libvirt network does not match the reserved IPv4 and MAC mapping")

type kvmNetwork struct {
	XMLName xml.Name `xml:"network"`
	Name    string   `xml:"name"`
	UUID    string   `xml:"uuid"`
	Forward struct {
		Mode string `xml:"mode,attr"`
	} `xml:"forward"`
	Addresses []kvmNetworkAddress `xml:"ip"`
}

type kvmNetworkAddress struct {
	Family  string         `xml:"family,attr"`
	Address string         `xml:"address,attr"`
	Netmask string         `xml:"netmask,attr"`
	Prefix  *int           `xml:"prefix,attr"`
	Hosts   []kvmDHCPHost  `xml:"dhcp>host"`
	Ranges  []kvmDHCPRange `xml:"dhcp>range"`
}

type kvmDHCPHost struct {
	XMLName xml.Name `xml:"host" json:"-"`
	IP      string   `xml:"ip,attr" json:"ipv4"`
	MAC     string   `xml:"mac,attr" json:"mac"`
	Name    string   `xml:"name,attr" json:"name"`
}

type kvmDHCPRange struct {
	Start string `xml:"start,attr"`
	End   string `xml:"end,attr"`
}

func (span kvmDHCPRange) parsed() (netip.Addr, netip.Addr, bool) {
	start, startErr := netip.ParseAddr(span.Start)
	end, endErr := netip.ParseAddr(span.End)
	return start, end, startErr == nil && endErr == nil && start.Is4() && end.Is4() && start.Compare(end) <= 0
}

func validKVMNetworkAddress(vm kvm.VM) bool {
	if vm.Address == "" {
		return vm.MAC == "" && vm.Subnet == "" && vm.Gateway == ""
	}
	ip, err := netip.ParseAddr(vm.Address)
	gateway, gatewayErr := netip.ParseAddr(vm.Gateway)
	subnet, subnetErr := netip.ParsePrefix(vm.Subnet)
	return err == nil && gatewayErr == nil && subnetErr == nil && ip.Is4() && gateway.Is4() && ip != gateway &&
		subnet == subnet.Masked() && subnet.Contains(ip) && subnet.Contains(gateway) &&
		vm.MAC != "" && vm.MAC == kvm.IPv4MAC(vm.Address)
}

func validateKVMNetwork(ctx context.Context, read virshReader, vm kvm.VM) error {
	if vm.Address == "" {
		return nil
	}
	for _, inactive := range []bool{false, true} {
		args := []string{"net-dumpxml", "--network", vm.Network}
		if inactive {
			args = append(args, "--inactive")
		}
		data, err := read(ctx, args...)
		if err != nil {
			return err
		}
		if !matchesKVMNetwork(data, vm) {
			return errKVMNetworkMapping
		}
	}
	return nil
}

func matchesKVMNetwork(data []byte, vm kvm.VM) bool {
	var network kvmNetwork
	if xml.Unmarshal(data, &network) != nil {
		return false
	}
	index, ok := network.ipv4Index(vm.Network, vm.Subnet, vm.Gateway)
	return ok && network.Addresses[index].matchesIPv4(vm)
}

func (network kvmNetwork) ipv4Index(name, subnet, gateway string) (int, bool) {
	if network.Name != name {
		return 0, false
	}
	switch network.Forward.Mode {
	case "", "nat", "route":
	default:
		return 0, false
	}
	index := -1
	for i, address := range network.Addresses {
		if address.Family == "ipv6" {
			continue
		}
		if index >= 0 || (address.Family != "" && address.Family != "ipv4") || !address.matchesSubnet(subnet, gateway) {
			return 0, false
		}
		index = i
	}
	return index, index >= 0
}

func (address kvmNetworkAddress) matchesSubnet(subnet, gatewayIP string) bool {
	gateway, err := netip.ParseAddr(address.Address)
	if err != nil || !gateway.Is4() || gateway.String() != gatewayIP {
		return false
	}
	var bits int
	switch {
	case address.Prefix != nil && address.Netmask == "":
		bits = *address.Prefix
	case address.Prefix == nil && address.Netmask != "":
		mask := net.ParseIP(address.Netmask).To4()
		var size int
		bits, size = net.IPMask(mask).Size()
		if size != 32 {
			return false
		}
	default:
		return false
	}
	return bits >= 16 && bits <= 30 && netip.PrefixFrom(gateway, bits).Masked().String() == subnet
}

func (address kvmNetworkAddress) matchesIPv4(vm kvm.VM) bool {
	ip, err := netip.ParseAddr(vm.Address)
	if err != nil {
		return false
	}
	for _, span := range address.Ranges {
		start, end, valid := span.parsed()
		if !valid || (start.Compare(ip) <= 0 && end.Compare(ip) >= 0) {
			return false
		}
	}
	var matched bool
	for _, host := range address.Hosts {
		ipMatches, macMatches := host.IP == vm.Address, strings.EqualFold(host.MAC, vm.MAC)
		if ipMatches || macMatches {
			if matched || !ipMatches || !macMatches || host.Name == "" {
				return false
			}
			matched = true
		}
	}
	return matched
}
