package publiccloud

import (
	"context"
	"net/netip"

	"with-cloud-manager/internal/provider"
)

func (c *Client) checkSubnetParent(ctx context.Context, region string, spec provider.NetworkSpec) error {
	var cidrs []string
	switch c.name {
	case provider.TencentCVM:
		vpc, err := c.readTencentVPC(ctx, region, spec.ParentID, "")
		if err != nil {
			return err
		}
		cidrs = []string{vpc.CidrBlock}
		for _, extra := range vpc.AssistantCidrSet {
			cidrs = append(cidrs, extra.CidrBlock)
		}
	case provider.AlibabaECS:
		vpc, err := c.readAlibabaVPC(ctx, region, spec.ParentID, "")
		if err != nil {
			return err
		}
		if vpc.Status != "Available" {
			return &provider.Failure{Kind: provider.CreationBlocked}
		}
		cidrs = append([]string{vpc.CidrBlock}, vpc.SecondaryCidrBlocks.SecondaryCidrBlock...)
	default:
		vpc, err := c.readHuaweiVPC(ctx, region, spec.ParentID, "")
		if err != nil {
			return err
		}
		if vpc.Status != "ACTIVE" {
			return &provider.Failure{Kind: provider.CreationBlocked}
		}
		cidrs = append([]string{vpc.CIDR}, vpc.ExtraCIDRs...)
	}
	child, err := netip.ParsePrefix(spec.CIDR)
	if err != nil {
		return &provider.Failure{Kind: provider.CreationBlocked}
	}
	for _, cidr := range cidrs {
		parent, err := netip.ParsePrefix(cidr)
		if err == nil && parent.Addr().Is4() && parent.Bits() <= child.Bits() && parent.Contains(child.Addr()) {
			return nil
		}
	}
	return &provider.Failure{Kind: provider.CreationBlocked}
}

func (c *Client) createSubnet(ctx context.Context, region string, spec provider.NetworkSpec, token, marker string) (provider.LaunchReceipt, error) {
	switch c.name {
	case provider.TencentCVM:
		return c.createTencentSubnet(ctx, region, spec, marker)
	case provider.AlibabaECS:
		return c.createAlibabaSubnet(ctx, region, spec, token, marker)
	default:
		return c.createHuaweiSubnet(ctx, region, spec, networkIntentTag+":"+marker)
	}
}

func (c *Client) findCreatedSubnet(ctx context.Context, region string, spec provider.NetworkSpec, id, marker string) (provider.Object, error) {
	switch c.name {
	case provider.TencentCVM:
		return c.findTencentSubnet(ctx, region, spec, id, marker)
	case provider.AlibabaECS:
		return c.findAlibabaSubnet(ctx, region, spec, id, marker)
	default:
		return c.findHuaweiSubnet(ctx, region, spec, id, networkIntentTag+":"+marker)
	}
}

func subnetObject(spec provider.NetworkSpec, id, status string, tags []*tag) provider.Object {
	object := tagObject(id, spec.Name, status, tags)
	object.Link(provider.Network, spec.ParentID, "in_network")
	object.Link(provider.Zone, spec.Zone, "in_zone")
	return object
}
