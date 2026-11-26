package publiccloud

import (
	"context"
	"net/url"

	"with-cloud-manager/internal/provider"
)

func (c *Client) readAlibabaRuleTarget(ctx context.Context, region string, target provider.SecurityRuleTarget) (alibabaRule, error) {
	group, err := c.readAlibabaGroup(ctx, region, "", target.GroupID, "")
	if err != nil {
		return alibabaRule{}, err
	}
	if group.SecurityGroupType != "enterprise" || !validLaunchID(group.VpcId, "vpc-") {
		return alibabaRule{}, &provider.Failure{Kind: provider.DeletionBlocked}
	}
	rules, err := c.readAlibabaRuleSet(ctx, region, target.GroupID, group.VpcId)
	if err != nil {
		return alibabaRule{}, err
	}
	for _, rule := range rules {
		if rule.SecurityGroupRuleId == target.ID {
			return rule, nil
		}
	}
	return alibabaRule{}, nil
}

func (c *Client) prepareAlibabaRuleRevocation(ctx context.Context, region string, target provider.SecurityRuleTarget) (provider.SecurityRuleRevocation, error) {
	rule, err := c.readAlibabaRuleTarget(ctx, region, target)
	if err != nil {
		return nil, err
	}
	if !target.Matches(rule.asset(target.GroupID)) {
		return nil, &provider.Failure{Kind: provider.DeletionBlocked}
	}
	action := "RevokeSecurityGroup"
	if rule.Direction == "egress" {
		action = "RevokeSecurityGroupEgress"
	}
	query := url.Values{"RegionId": {region}, "SecurityGroupId": {target.GroupID}, "SecurityGroupRuleId.1": {target.ID}}
	return func(ctx context.Context) (string, error) {
		var result struct{ RequestId, Code string }
		if err := c.alibaba(ctx, "ecs."+region+".aliyuncs.com", action, "2014-05-26", query, &result); err != nil {
			return "", err
		}
		if result.Code != "" {
			return result.RequestId, cloudFailure(result.Code)
		}
		return result.RequestId, nil
	}, nil
}
