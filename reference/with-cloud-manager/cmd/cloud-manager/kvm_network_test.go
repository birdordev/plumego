package main

import (
	"strings"
	"testing"

	"with-cloud-manager/internal/kvm"
)

func TestKVMStaticDHCPMapping(t *testing.T) {
	vm := kvm.VM{IPv4Assignment: kvm.IPv4Assignment{Address: "10.77.0.2", MAC: "02:00:0a:4d:00:02"},
		Network: "app", Subnet: "10.77.0.0/29", Gateway: "10.77.0.1"}
	if !validKVMNetworkAddress(vm) {
		t.Fatal("the reserved network assignment should be valid")
	}
	entry := `<host mac="02:00:0a:4d:00:02" ip="10.77.0.2" name="guest"/>`
	base := `<network><name>app</name><ip address="10.77.0.1" netmask="255.255.255.248"><dhcp>` + entry + `</dhcp></ip></network>`
	for _, tc := range []struct {
		name, find, substitution string
		ok                       bool
	}{
		{name: "netmask", ok: true},
		{name: "prefix", find: `netmask="255.255.255.248"`, substitution: `family="ipv4" prefix="29"`, ok: true},
		{name: "distinct-explicit-range", find: entry, substitution: entry + `<range start="10.77.0.5" end="10.77.0.6"/>`, ok: true},
		{name: "overlapping-dynamic-range", find: entry, substitution: entry + `<range start="10.77.0.2" end="10.77.0.5"/>`},
		{name: "duplicate-ip", find: entry, substitution: entry + strings.ReplaceAll(entry, vm.MAC, "02:00:00:00:00:01")},
		{name: "duplicate-mac", find: entry, substitution: entry + strings.ReplaceAll(entry, vm.Address, "10.77.0.3")},
		{name: "missing-reservation", find: entry},
		{name: "wrong-subnet", find: "255.255.255.248", substitution: "255.255.255.0"},
		{name: "wrong-network", find: "<name>app</name>", substitution: "<name>backup</name>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			document := base
			if tc.find != "" {
				document = strings.ReplaceAll(base, tc.find, tc.substitution)
			}
			if matched := matchesKVMNetwork([]byte(document), vm); matched != tc.ok {
				t.Fatalf("network match=%t, want %t", matched, tc.ok)
			}
		})
	}
}
