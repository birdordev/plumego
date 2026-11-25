package awsec2

import (
	"context"
	"net/netip"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	smithymiddleware "github.com/aws/smithy-go/middleware"

	"with-cloud-manager/internal/provider"
)

func (c *Client) inventorySecurityRule(row types.SecurityGroupRule) (provider.Object, error) {
	id, group := aws.ToString(row.SecurityGroupRuleId), aws.ToString(row.GroupId)
	protocol := aws.ToString(row.IpProtocol)
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	if !validResourceID(id, "sgr-") || !validResourceID(group, "sg-") || row.IsEgress == nil ||
		protocol == "" || len(protocol) > 32 || strings.ContainsAny(protocol, "\x00 \r\n\t") {
		return provider.Object{}, invalid
	}
	if aws.ToString(row.GroupOwnerId) != c.account {
		return provider.Object{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	direction := "ingress"
	if *row.IsEgress {
		direction = "egress"
	}
	remote, family, targets := "", "", 0
	for _, cidr := range []struct {
		value *string
		ipv4  bool
	}{{row.CidrIpv4, true}, {row.CidrIpv6, false}} {
		if cidr.value == nil {
			continue
		}
		prefix, err := netip.ParsePrefix(*cidr.value)
		if err != nil || prefix.Addr().Is4() != cidr.ipv4 {
			return provider.Object{}, invalid
		}
		remote = prefix.Masked().String()
		if !cidr.ipv4 {
			family = "IPv6"
		}
		targets++
	}
	if row.PrefixListId != nil {
		if !validResourceID(*row.PrefixListId, "pl-") {
			return provider.Object{}, invalid
		}
		remote, targets = "prefix-list:"+*row.PrefixListId, targets+1
	}
	if row.ReferencedGroupInfo != nil {
		id := aws.ToString(row.ReferencedGroupInfo.GroupId)
		if !validResourceID(id, "sg-") {
			return provider.Object{}, invalid
		}
		remote, targets = "security-group:"+id, targets+1
	}
	if targets != 1 {
		return provider.Object{}, invalid
	}
	return provider.ObservedSecurityRule(id, group, direction, protocol, family, row.FromPort, row.ToPort, remote), nil
}

func (c *Client) createSecurityRule(ctx context.Context, region string, spec provider.NetworkSpec, token string) (provider.LaunchReceipt, error) {
	blocked := &provider.Failure{Kind: provider.CreationBlocked}
	if !c.allowSecurityGroupRules {
		return provider.LaunchReceipt{}, blocked
	}
	group, err := c.ec2.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{GroupIds: []string{spec.ParentID}}, func(o *ec2.Options) { o.Region = region })
	if err != nil {
		return provider.LaunchReceipt{}, blocked
	}
	if len(group.SecurityGroups) != 1 || aws.ToString(group.NextToken) != "" {
		return provider.LaunchReceipt{}, blocked
	}
	parent := group.SecurityGroups[0]
	if aws.ToString(parent.GroupId) != spec.ParentID || aws.ToString(parent.OwnerId) != c.account ||
		aws.ToString(parent.GroupName) == "" || strings.EqualFold(aws.ToString(parent.GroupName), "default") {
		return provider.LaunchReceipt{}, blocked
	}
	rule := *spec.Rule
	permissions := []types.IpPermission{{IpProtocol: aws.String(rule.Protocol), FromPort: aws.Int32(rule.FromPort), ToPort: aws.Int32(rule.ToPort),
		IpRanges: []types.IpRange{{CidrIp: aws.String(rule.RemoteCIDR)}}}}
	tags := []types.TagSpecification{{ResourceType: types.ResourceTypeSecurityGroupRule,
		Tags: []types.Tag{{Key: aws.String(networkIntentTag), Value: aws.String(token)}}}}
	sendOnce := func(o *ec2.Options) { o.Region, o.Retryer = region, aws.NopRetryer{} }
	var metadata smithymiddleware.Metadata
	var rows []types.SecurityGroupRule
	var accepted bool
	if rule.Direction == "egress" {
		out, err := c.ec2.AuthorizeSecurityGroupEgress(ctx, &ec2.AuthorizeSecurityGroupEgressInput{
			GroupId: aws.String(spec.ParentID), IpPermissions: permissions, TagSpecifications: tags,
		}, sendOnce)
		if err != nil {
			return provider.LaunchReceipt{}, translate(ctx, err)
		}
		metadata, rows, accepted = out.ResultMetadata, out.SecurityGroupRules, aws.ToBool(out.Return)
	} else {
		out, err := c.ec2.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
			GroupId: aws.String(spec.ParentID), IpPermissions: permissions, TagSpecifications: tags,
		}, sendOnce)
		if err != nil {
			return provider.LaunchReceipt{}, translate(ctx, err)
		}
		metadata, rows, accepted = out.ResultMetadata, out.SecurityGroupRules, aws.ToBool(out.Return)
	}
	request, _ := middleware.GetRequestIDMetadata(metadata)
	receipt := provider.LaunchReceipt{RequestID: request}
	if accepted && len(rows) == 1 && validResourceID(aws.ToString(rows[0].SecurityGroupRuleId), "sgr-") {
		receipt.ResourceID = aws.ToString(rows[0].SecurityGroupRuleId)
	}
	return receipt, nil
}

func (c *Client) findCreatedSecurityRule(ctx context.Context, spec provider.NetworkSpec, token, id string, filters []types.Filter, options func(*ec2.Options)) (provider.Object, error) {
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	filters = append(filters, types.Filter{Name: aws.String("group-id"), Values: []string{spec.ParentID}})
	input := &ec2.DescribeSecurityGroupRulesInput{Filters: filters, MaxResults: aws.Int32(10)}
	if id != "" {
		if !validResourceID(id, "sgr-") {
			return provider.Object{}, invalid
		}
		input = &ec2.DescribeSecurityGroupRulesInput{SecurityGroupRuleIds: []string{id}}
	}
	out, err := c.ec2.DescribeSecurityGroupRules(ctx, input, options)
	if err != nil {
		return provider.Object{}, translate(ctx, err)
	}
	if len(out.SecurityGroupRules) == 0 && aws.ToString(out.NextToken) == "" {
		return provider.Object{}, &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	if len(out.SecurityGroupRules) != 1 || aws.ToString(out.NextToken) != "" {
		return provider.Object{}, invalid
	}
	row, rule := out.SecurityGroupRules[0], *spec.Rule
	actual := aws.ToString(row.SecurityGroupRuleId)
	if !validResourceID(actual, "sgr-") || (id != "" && actual != id) || aws.ToString(row.GroupId) != spec.ParentID ||
		aws.ToString(row.GroupOwnerId) != c.account || row.IsEgress == nil || *row.IsEgress != (rule.Direction == "egress") ||
		aws.ToString(row.IpProtocol) != rule.Protocol || aws.ToInt32(row.FromPort) != rule.FromPort || aws.ToInt32(row.ToPort) != rule.ToPort ||
		aws.ToString(row.CidrIpv4) != rule.RemoteCIDR || aws.ToString(row.CidrIpv6) != "" || aws.ToString(row.PrefixListId) != "" ||
		row.ReferencedGroupInfo != nil || !matchesNetworkTag(row.Tags, token) {
		return provider.Object{}, invalid
	}
	return rule.Asset(actual, spec.ParentID), nil
}
