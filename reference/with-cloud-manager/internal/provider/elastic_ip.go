package provider

import (
	"context"
	"net/netip"
	"regexp"
)

type ElasticIPTarget struct {
	ID      string
	Address string
}

func (target ElasticIPTarget) ValidIPv4() bool {
	address, err := netip.ParseAddr(target.Address)
	return err == nil && address.Is4() && address.IsGlobalUnicast() && address.String() == target.Address
}

type ElasticIPRelease func(context.Context) (string, error)

type ElasticIPReleaser interface {
	PrepareElasticIPRelease(context.Context, Scope, ElasticIPTarget) (ElasticIPRelease, error)
	ObserveElasticIPRelease(context.Context, Scope, ElasticIPTarget) (ActionResult, error)
}

type ElasticIPBinding struct {
	ElasticIPTarget
	InstanceID  string
	PortID      string
	ClientToken string
	Bind        bool
}

type ElasticIPBinder interface {
	PrepareElasticIPBinding(context.Context, Scope, ElasticIPBinding) (func(context.Context) (string, error), error)
	ObserveElasticIPBinding(context.Context, Scope, ElasticIPBinding) (Object, error)
}

var elasticIPIdentifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

func (spec NetworkSpec) ValidPublicCloudElasticIP(vendor string) bool {
	if !elasticIPIdentifier.MatchString(spec.Name) || spec.BandwidthMbps < 5 || spec.BandwidthMbps > 200 ||
		spec.ParentID != "" || spec.CIDR != "" || spec.Zone != "" || spec.Rule != nil {
		return false
	}
	switch vendor {
	case TencentCVM, AlibabaECS:
		return spec.AddressPool == ""
	case HuaweiECS:
		return elasticIPIdentifier.MatchString(spec.AddressPool)
	default:
		return false
	}
}

func validPublicAddresses(addresses []string) bool {
	if len(addresses) > 2 {
		return false
	}
	var previous netip.Addr
	for _, value := range addresses {
		address, err := netip.ParseAddr(value)
		if err != nil || address.Is4In6() || address.Zone() != "" || !address.IsGlobalUnicast() || address.String() != value ||
			(previous.IsValid() && (!previous.Is4() || !address.Is6())) {
			return false
		}
		previous = address
	}
	return true
}
