package provider

import "testing"

func TestPublicCloudVPCRanges(t *testing.T) {
	for _, test := range []struct {
		vendor, name, cidr string
		want               bool
	}{
		{TencentCVM, "vpc", "10.0.0.0/12", true},
		{TencentCVM, "vpc", "10.16.0.0/16", false},
		{TencentCVM, "vpc", "172.16.0.0/12", true},
		{TencentCVM, "vpc", "192.168.255.240/28", true},
		{TencentCVM, "vpc", "192.168.0.0/29", false},
		{AlibabaECS, "vpc", "10.16.0.0/16", true},
		{AlibabaECS, "vpc", "10.0.0.0/15", false},
		{HuaweiECS, "vpc", "10.0.0.0/8", true},
		{HuaweiECS, "vpc.space", "192.168.0.0/24", true},
		{HuaweiECS, "vpc space", "192.168.0.0/24", false},
		{HuaweiECS, "vpc", "192.168.0.0/25", false},
		{HuaweiECS, "vpc", "172.16.0.1/24", false},
	} {
		spec := NetworkSpec{Name: test.name, CIDR: test.cidr}
		if got := spec.ValidPublicCloudVPC(test.vendor); got != test.want {
			t.Errorf("%s %q %s accepted %t, want %t", test.vendor, test.name, test.cidr, got, test.want)
		}
	}
}

func TestPublicCloudSubnetCoordinates(t *testing.T) {
	for _, test := range []struct {
		vendor, region, zone, cidr string
		want                       bool
	}{
		{TencentCVM, "ap-guangzhou", "ap-guangzhou-3", "10.0.0.0/28", true},
		{TencentCVM, "ap-guangzhou", "ap-guangzhou-3", "10.0.0.0/29", false},
		{TencentCVM, "ap-guangzhou", "ap-guangzhou-", "10.0.0.0/24", false},
		{TencentCVM, "ap-guangzhou", "", "10.0.0.0/24", false},
		{AlibabaECS, "cn-hangzhou", "cn-hangzhou-b", "10.0.0.0/29", true},
		{AlibabaECS, "cn-hangzhou", "cn-shanghai-b", "10.0.0.0/24", false},
		{HuaweiECS, "cn-north-4", "", "10.0.0.0/29", true},
		{HuaweiECS, "cn-north-4", "cn-north-4a", "10.0.0.0/30", false},
	} {
		spec := NetworkSpec{Name: "subnet", ParentID: "parent", CIDR: test.cidr, Zone: test.zone}
		if got := spec.ValidPublicCloudSubnet(test.vendor, test.region); got != test.want {
			t.Errorf("%s %q %s accepted %t, want %t", test.vendor, test.zone, test.cidr, got, test.want)
		}
	}
}
