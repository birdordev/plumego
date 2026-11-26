package publiccloud

import (
	"context"
	"strconv"
	"strings"

	"with-cloud-manager/internal/provider"
)

const ruleIntentPrefix = "plumego-cmp-rule:"
const rulePriorityLowest = 100

func (c *Client) validRuleGroup(id string) bool {
	if c.name == provider.HuaweiECS {
		return huaweiResourceID.MatchString(id)
	}
	return validSecurityGroupID(id)
}

func (c *Client) prepareRuleCreation(ctx context.Context, scope provider.Scope, spec provider.NetworkSpec, token string) (func(context.Context) (provider.LaunchReceipt, error), error) {
	blocked := &provider.Failure{Kind: provider.CreationBlocked}
	if !c.profile.AllowSecurityGroupRules {
		return nil, blocked
	}
	region, group, rule := scope.Region, spec.ParentID, *spec.Rule
	marker := ruleIntentPrefix + vpcMarker(scope, token)
	switch c.name {
	case provider.TencentCVM:
		row, err := c.readTencentGroup(ctx, region, group, "")
		if err != nil {
			return nil, err
		}
		if *row.IsDefault {
			return nil, blocked
		}
		set, err := c.readTencentGroupPolicies(ctx, region, group)
		if err != nil {
			return nil, err
		}
		if set.Version == nil || *set.Version == "" {
			return nil, blocked
		}
		return func(ctx context.Context) (provider.LaunchReceipt, error) {
			return c.createTencentRule(ctx, region, group, rule, marker, *set.Version)
		}, nil
	case provider.AlibabaECS:
		row, err := c.readAlibabaGroup(ctx, region, "", group, "")
		if err != nil {
			return nil, err
		}
		if !validLaunchID(row.VpcId, "vpc-") || row.SecurityGroupType != "enterprise" {
			return nil, blocked
		}
		return func(ctx context.Context) (provider.LaunchReceipt, error) {
			return c.createAlibabaRule(ctx, region, group, rule, marker, token)
		}, nil
	default:
		row, err := c.readHuaweiGroup(ctx, region, group)
		if err != nil {
			return nil, err
		}
		if strings.EqualFold(row.Name, "default") {
			return nil, blocked
		}
		return func(ctx context.Context) (provider.LaunchReceipt, error) {
			return c.createHuaweiRule(ctx, region, group, rule, marker)
		}, nil
	}
}

func (c *Client) findCreatedRule(ctx context.Context, region string, spec provider.NetworkSpec, id, marker string) (provider.Object, error) {
	group, rule := spec.ParentID, *spec.Rule
	marker = ruleIntentPrefix + marker
	switch c.name {
	case provider.TencentCVM:
		return c.findTencentRule(ctx, region, group, rule, marker, id)
	case provider.AlibabaECS:
		return c.findAlibabaRule(ctx, region, group, rule, marker, id)
	default:
		return c.findHuaweiRule(ctx, region, group, rule, marker, id)
	}
}

func contiguousRulePorts(rule provider.SecurityRule) string {
	from := strconv.FormatInt(int64(rule.FromPort), 10)
	if rule.FromPort == rule.ToPort {
		return from
	}
	return from + "-" + strconv.FormatInt(int64(rule.ToPort), 10)
}

func matchesContiguousRulePorts(ports string, rule provider.SecurityRule) bool {
	expected := contiguousRulePorts(rule)
	return ports == expected || (rule.FromPort == rule.ToPort && ports == expected+"-"+expected)
}
