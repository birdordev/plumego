package publiccloud

import (
	"context"
	"regexp"
	"slices"

	"with-cloud-manager/internal/provider"
)

var tencentRuleID = regexp.MustCompile(`^tc-sgr-[0-9a-f]{64}-[1-9][0-9]{0,7}$`)

func (c *Client) checkRuleRevocationScope(scope provider.Scope, target provider.SecurityRuleTarget) error {
	if scope.Provider != c.name || scope.RemoteIdentity != c.identity {
		return &provider.Failure{Kind: provider.Unauthorized}
	}
	if scope.Kind != provider.SecurityGroupRule || !slices.Contains(c.profile.Regions, scope.Region) ||
		!c.validRuleGroup(target.GroupID) || target.Name == "" {
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	var valid bool
	switch c.name {
	case provider.TencentCVM:
		valid = tencentRuleID.MatchString(target.ID)
	case provider.AlibabaECS:
		valid = validLaunchID(target.ID, "sgr-")
	case provider.HuaweiECS:
		valid = huaweiResourceID.MatchString(target.ID)
	}
	if !valid {
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	return nil
}

func (c *Client) PrepareSecurityRuleRevocation(ctx context.Context, scope provider.Scope, target provider.SecurityRuleTarget) (provider.SecurityRuleRevocation, error) {
	if !c.profile.AllowSecurityRuleRevocation {
		return nil, &provider.Failure{Kind: provider.DeletionDisabled}
	}
	if err := c.checkRuleRevocationScope(scope, target); err != nil {
		return nil, err
	}
	switch c.name {
	case provider.TencentCVM:
		return c.prepareTencentRuleRevocation(ctx, scope.Region, target)
	case provider.AlibabaECS:
		return c.prepareAlibabaRuleRevocation(ctx, scope.Region, target)
	default:
		return c.prepareHuaweiRuleRevocation(ctx, scope.Region, target)
	}
}

func (c *Client) ObserveSecurityRuleRevocation(ctx context.Context, scope provider.Scope, target provider.SecurityRuleTarget) (provider.ActionResult, error) {
	if err := c.checkRuleRevocationScope(scope, target); err != nil {
		return provider.ActionPending, err
	}
	var present bool
	var err error
	switch c.name {
	case provider.TencentCVM:
		rule, readErr := c.readTencentRuleTarget(ctx, scope.Region, target)
		present, err = rule.object.ID != "", readErr
	case provider.AlibabaECS:
		rule, readErr := c.readAlibabaRuleTarget(ctx, scope.Region, target)
		present, err = rule.SecurityGroupRuleId != "", readErr
	default:
		present, err = c.huaweiRulePresent(ctx, scope.Region, target)
	}
	if err != nil || present {
		return provider.ActionPending, err
	}
	return provider.ActionSucceeded, nil
}
