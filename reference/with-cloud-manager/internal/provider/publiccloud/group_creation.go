package publiccloud

import (
	"context"

	"with-cloud-manager/internal/provider"
)

func (c *Client) prepareGroupCreation(ctx context.Context, scope provider.Scope, spec provider.NetworkSpec, token string) (func(context.Context) (provider.LaunchReceipt, error), error) {
	if !c.profile.AllowSecurityGroupCreation {
		return nil, &provider.Failure{Kind: provider.CreationBlocked}
	}
	if c.name == provider.AlibabaECS {
		vpc, err := c.readAlibabaVPC(ctx, scope.Region, spec.ParentID, "")
		if err != nil {
			return nil, err
		}
		if vpc.Status != "Available" {
			return nil, &provider.Failure{Kind: provider.CreationBlocked}
		}
	}
	marker := vpcMarker(scope, token)
	return func(ctx context.Context) (provider.LaunchReceipt, error) {
		switch c.name {
		case provider.TencentCVM:
			return c.createTencentGroup(ctx, scope.Region, spec, marker)
		case provider.AlibabaECS:
			return c.createAlibabaGroup(ctx, scope.Region, spec, token, marker)
		default:
			return c.createHuaweiGroup(ctx, scope.Region, spec, networkIntentTag+":"+marker)
		}
	}, nil
}

func (c *Client) findCreatedGroup(ctx context.Context, region string, spec provider.NetworkSpec, id, marker string) (provider.Object, error) {
	switch c.name {
	case provider.TencentCVM:
		return c.findTencentGroup(ctx, region, spec, id, marker)
	case provider.AlibabaECS:
		return c.findAlibabaGroup(ctx, region, spec, id, marker)
	default:
		return c.findHuaweiGroup(ctx, region, spec, id, networkIntentTag+":"+marker)
	}
}
