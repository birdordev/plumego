package provider

import (
	"net/netip"
	"slices"
	"strings"
)

const MaxLoadBalancerAddresses = 128

type LoadBalancerDetails struct {
	Product          string   `json:"product"`
	AddressType      string   `json:"address_type"`
	DNSName          string   `json:"dns_name,omitempty"`
	PrivateAddresses []string `json:"private_addresses,omitempty"`
}

func (details *LoadBalancerDetails) valid() bool {
	if details == nil {
		return false
	}
	switch details.Product {
	case "tencent-clb", "alibaba-alb", "alibaba-nlb", "alibaba-clb", "huawei-elb":
	default:
		return false
	}
	switch details.AddressType {
	case "public", "private", "dual":
	default:
		return false
	}
	return validLoadBalancerDNS(details.DNSName) && validLoadBalancerAddresses(details.PrivateAddresses)
}

func (o Object) ValidLoadBalancer() bool {
	details := o.LoadBalancer
	return details.valid() && (details.AddressType != "private" || len(o.PublicAddresses) == 0) &&
		validLoadBalancerAddresses(o.PublicAddresses)
}

func validLoadBalancerDNS(name string) bool {
	if name == "" {
		return true
	}
	if len(name) > 253 {
		return false
	}
	for label := range strings.SplitSeq(name, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
				return false
			}
		}
	}
	return true
}

func validLoadBalancerAddresses(values []string) bool {
	if len(values) > MaxLoadBalancerAddresses {
		return false
	}
	var previous netip.Addr
	for _, value := range values {
		address, err := netip.ParseAddr(value)
		if err != nil || address.Is4In6() || address.Zone() != "" || !address.IsGlobalUnicast() ||
			address.String() != value || (previous.IsValid() && previous.Compare(address) >= 0) {
			return false
		}
		previous = address
	}
	return true
}

func NormalizeLoadBalancerAddresses(values []string) ([]string, error) {
	addresses := make([]netip.Addr, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		address, err := netip.ParseAddr(value)
		if err != nil || address.Zone() != "" || !address.IsGlobalUnicast() {
			return nil, &Failure{Kind: InvalidResponse}
		}
		addresses = append(addresses, address.Unmap())
	}
	slices.SortFunc(addresses, netip.Addr.Compare)
	addresses = slices.Compact(addresses)
	if len(addresses) > MaxLoadBalancerAddresses {
		return nil, &Failure{Kind: InvalidResponse}
	}
	result := make([]string, len(addresses))
	for i, address := range addresses {
		result[i] = address.String()
	}
	return result, nil
}
