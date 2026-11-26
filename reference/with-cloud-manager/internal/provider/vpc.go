package provider

import (
	"net/netip"
	"regexp"
	"strings"
)

var huaweiVPCName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)
var subnetZone = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9_-]{0,62}[A-Za-z0-9])?$`)
var alibabaGroupName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9:_.-]{1,127}$`)

func (spec NetworkSpec) ValidPublicCloudSecurityGroup(name string) bool {
	if spec.Name == "" || strings.TrimSpace(spec.Name) != spec.Name || strings.EqualFold(spec.Name, "default") ||
		strings.ContainsRune(spec.Name, 0) || spec.CIDR != "" || spec.Rule != nil || spec.Zone != "" {
		return false
	}
	switch name {
	case TencentCVM:
		return len(spec.Name) <= 60 && spec.ParentID == ""
	case AlibabaECS:
		return alibabaGroupName.MatchString(spec.Name) && spec.ParentID != ""
	case HuaweiECS:
		return huaweiVPCName.MatchString(spec.Name) && spec.ParentID == ""
	}
	return false
}

func (spec NetworkSpec) ValidPublicCloudVPC(name string) bool {
	if spec.Name == "" || strings.TrimSpace(spec.Name) != spec.Name ||
		strings.ContainsRune(spec.Name, 0) || spec.ParentID != "" || spec.Rule != nil || spec.Zone != "" {
		return false
	}
	prefix, err := netip.ParsePrefix(spec.CIDR)
	if err != nil || !prefix.Addr().Is4() || prefix.Masked().String() != spec.CIDR || !prefix.Addr().IsPrivate() {
		return false
	}
	bits, addr := prefix.Bits(), prefix.Addr().As4()
	switch name {
	case TencentCVM:
		return len(spec.Name) <= 60 && bits >= 12 && bits <= 28 &&
			(addr[0] != 10 || addr[1] < 16)
	case AlibabaECS:
		return len(spec.Name) <= 128 && bits >= 16 && bits <= 28 &&
			!strings.HasPrefix(spec.Name, "http://") && !strings.HasPrefix(spec.Name, "https://")
	case HuaweiECS:
		if !huaweiVPCName.MatchString(spec.Name) || bits > 24 {
			return false
		}
		switch addr[0] {
		case 10:
			return bits >= 8
		case 172:
			return bits >= 12
		case 192:
			return bits >= 16
		}
	}
	return false
}

func (spec NetworkSpec) ValidPublicCloudSubnet(name, region string) bool {
	if spec.Name == "" || strings.TrimSpace(spec.Name) != spec.Name || strings.ContainsRune(spec.Name, 0) ||
		spec.ParentID == "" || spec.Rule != nil || (spec.Zone != "" && !subnetZone.MatchString(spec.Zone)) {
		return false
	}
	prefix, err := netip.ParsePrefix(spec.CIDR)
	if err != nil || !prefix.Addr().Is4() || prefix.Masked().String() != spec.CIDR {
		return false
	}
	bits := prefix.Bits()
	switch name {
	case TencentCVM:
		return len(spec.Name) <= 60 && bits >= 16 && bits <= 28 && strings.HasPrefix(spec.Zone, region+"-")
	case AlibabaECS:
		return len(spec.Name) <= 128 && bits >= 16 && bits <= 29 && strings.HasPrefix(spec.Zone, region+"-") &&
			!strings.HasPrefix(spec.Name, "http://") && !strings.HasPrefix(spec.Name, "https://")
	case HuaweiECS:
		return huaweiVPCName.MatchString(spec.Name) && bits >= 1 && bits <= 29
	}
	return false
}
