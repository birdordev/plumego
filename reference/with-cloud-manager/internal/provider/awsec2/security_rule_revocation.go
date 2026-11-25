package awsec2

import (
	"context"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	smithymiddleware "github.com/aws/smithy-go/middleware"

	"with-cloud-manager/internal/provider"
)

func (c *Client) securityRuleRevocationScope(ctx context.Context, scope provider.Scope, target provider.SecurityRuleTarget) (types.SecurityGroup, error) {
	if scope.Provider != Name || scope.RemoteIdentity != c.identity {
		return types.SecurityGroup{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	if scope.Kind != provider.SecurityGroupRule || !regionName.MatchString(scope.Region) ||
		!validResourceID(target.ID, "sgr-") || !validResourceID(target.GroupID, "sg-") {
		return types.SecurityGroup{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	groups, err := c.ec2.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{GroupIds: []string{target.GroupID}},
		func(o *ec2.Options) { o.Region = scope.Region })
	if err != nil {
		return types.SecurityGroup{}, translate(ctx, err)
	}
	if len(groups.SecurityGroups) != 1 || aws.ToString(groups.NextToken) != "" || aws.ToString(groups.SecurityGroups[0].GroupId) != target.GroupID {
		return types.SecurityGroup{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	group := groups.SecurityGroups[0]
	if aws.ToString(group.OwnerId) != c.account {
		return types.SecurityGroup{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	return group, nil
}

func (c *Client) PrepareSecurityRuleRevocation(ctx context.Context, scope provider.Scope, target provider.SecurityRuleTarget) (provider.SecurityRuleRevocation, error) {
	if !c.allowSecurityRuleRevocation {
		return nil, &provider.Failure{Kind: provider.DeletionDisabled}
	}
	group, err := c.securityRuleRevocationScope(ctx, scope, target)
	if err != nil {
		return nil, err
	}
	if name := aws.ToString(group.GroupName); name == "" || strings.EqualFold(name, "default") {
		return nil, &provider.Failure{Kind: provider.DeletionBlocked}
	}
	object, err := c.Observe(ctx, scope, target.ID)
	if err != nil {
		return nil, err
	}
	if !target.Matches(object) {
		return nil, &provider.Failure{Kind: provider.DeletionBlocked}
	}
	return func(ctx context.Context) (string, error) {
		return c.revokeSecurityRule(ctx, scope.Region, target, strings.HasPrefix(object.Name, "egress "))
	}, nil
}

func (c *Client) revokeSecurityRule(ctx context.Context, region string, target provider.SecurityRuleTarget, egress bool) (string, error) {
	sendOnce := func(o *ec2.Options) { o.Region, o.Retryer = region, aws.NopRetryer{} }
	if egress {
		out, err := c.ec2.RevokeSecurityGroupEgress(ctx, &ec2.RevokeSecurityGroupEgressInput{
			GroupId: aws.String(target.GroupID), SecurityGroupRuleIds: []string{target.ID},
		}, sendOnce)
		if err != nil {
			return "", translate(ctx, err)
		}
		return ec2RevocationReceipt(out.ResultMetadata, out.Return, out.UnknownIpPermissions)
	}
	out, err := c.ec2.RevokeSecurityGroupIngress(ctx, &ec2.RevokeSecurityGroupIngressInput{
		GroupId: aws.String(target.GroupID), SecurityGroupRuleIds: []string{target.ID},
	}, sendOnce)
	if err != nil {
		return "", translate(ctx, err)
	}
	return ec2RevocationReceipt(out.ResultMetadata, out.Return, out.UnknownIpPermissions)
}

func ec2RevocationReceipt(metadata smithymiddleware.Metadata, accepted *bool, unknown []types.IpPermission) (string, error) {
	id, _ := middleware.GetRequestIDMetadata(metadata)
	if !aws.ToBool(accepted) || len(unknown) != 0 {
		return id, &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	return id, nil
}

func (c *Client) ObserveSecurityRuleRevocation(ctx context.Context, scope provider.Scope, target provider.SecurityRuleTarget) (provider.ActionResult, error) {
	if _, err := c.securityRuleRevocationScope(ctx, scope, target); err != nil {
		return provider.ActionPending, err
	}
	query := &ec2.DescribeSecurityGroupRulesInput{MaxResults: aws.Int32(5), Filters: []types.Filter{
		{Name: aws.String("group-id"), Values: []string{target.GroupID}},
		{Name: aws.String("security-group-rule-id"), Values: []string{target.ID}},
	}}
	page, err := c.ec2.DescribeSecurityGroupRules(ctx, query, func(o *ec2.Options) { o.Region = scope.Region })
	if err != nil {
		return provider.ActionPending, translate(ctx, err)
	}
	if aws.ToString(page.NextToken) != "" || len(page.SecurityGroupRules) > 1 {
		return provider.ActionPending, &provider.Failure{Kind: provider.InvalidResponse}
	}
	if len(page.SecurityGroupRules) == 1 {
		row := page.SecurityGroupRules[0]
		if aws.ToString(row.GroupOwnerId) != c.account || aws.ToString(row.GroupId) != target.GroupID || aws.ToString(row.SecurityGroupRuleId) != target.ID {
			return provider.ActionPending, &provider.Failure{Kind: provider.InvalidResponse}
		}
		return provider.ActionPending, nil
	}
	object, err := c.Observe(ctx, scope, target.ID)
	if provider.IsFailure(err, provider.OutcomeUnknown) {
		return provider.ActionSucceeded, nil
	}
	if err == nil {
		if parent, ok := provider.SecurityRuleParent(object); !ok || parent != target.GroupID {
			return provider.ActionPending, &provider.Failure{Kind: provider.InvalidResponse}
		}
	}
	return provider.ActionPending, err
}
