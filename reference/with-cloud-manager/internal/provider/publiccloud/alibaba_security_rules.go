package publiccloud

import (
	"context"
	"net/url"
	"strconv"
	"strings"

	"with-cloud-manager/internal/provider"
)

func (c *Client) alibabaSecurityRules(ctx context.Context, region, cursor string) (provider.Page, error) {
	return walkSecurityRules(ctx, region, cursor, c.alibabaSecurityGroups, c.alibabaGroupRules)
}

type alibabaRule struct {
	SecurityGroupRuleId, Direction, IpProtocol, PortRange, SourcePortRange, PortRangeListId    string
	SourceCidrIp, Ipv6SourceCidrIp, SourceGroupId, SourcePrefixListId, SourceGroupOwnerAccount string
	DestCidrIp, Ipv6DestCidrIp, DestGroupId, DestPrefixListId, DestGroupOwnerAccount           string
	Policy, Priority, NicType, Description                                                     string
}

type alibabaRulePage struct {
	VpcId, Next string
	Rules       []alibabaRule
}

func (c *Client) readAlibabaGroupRules(ctx context.Context, region, group, direction, cursor string) (alibabaRulePage, error) {
	query := url.Values{
		"RegionId": {region}, "SecurityGroupId": {group}, "Direction": {direction},
		"MaxResults": {strconv.Itoa(pageSize)},
	}
	if cursor != "" {
		query.Set("NextToken", cursor)
	}
	var result struct {
		Code, RegionId, SecurityGroupId, VpcId, NextToken string
		Permissions                                       struct{ Permission []alibabaRule }
	}
	if err := c.alibaba(ctx, "ecs."+region+".aliyuncs.com", "DescribeSecurityGroupAttribute", "2014-05-26", query, &result); err != nil {
		return alibabaRulePage{}, err
	}
	if result.Code != "" {
		return alibabaRulePage{}, cloudFailure(result.Code)
	}
	if result.RegionId != region || result.SecurityGroupId != group {
		return alibabaRulePage{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	rows := result.Permissions.Permission
	if rows == nil || len(rows) > pageSize || len(result.NextToken) > provider.MaxCursorBytes ||
		(len(rows) == 0 && result.NextToken != "") {
		return alibabaRulePage{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	for _, row := range rows {
		if !validLaunchID(row.SecurityGroupRuleId, "sgr-") || !validRuleDirection(row.Direction) {
			return alibabaRulePage{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
	}
	return alibabaRulePage{Rules: rows, Next: result.NextToken, VpcId: result.VpcId}, nil
}

func (c *Client) readAlibabaRuleSet(ctx context.Context, region, group, vpc string) ([]alibabaRule, error) {
	const maxPages = 20
	var rows []alibabaRule
	var cursor string
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	tokens := make(map[string]struct{})
	ids := make(map[string]struct{})
	for range maxPages {
		page, err := c.readAlibabaGroupRules(ctx, region, group, "all", cursor)
		if err != nil {
			return nil, err
		}
		if !validLaunchID(page.VpcId, "vpc-") || (vpc != "" && page.VpcId != vpc) {
			return nil, invalid
		}
		vpc = page.VpcId
		for _, row := range page.Rules {
			if _, repeated := ids[row.SecurityGroupRuleId]; repeated {
				return nil, invalid
			}
			ids[row.SecurityGroupRuleId] = struct{}{}
		}
		rows = append(rows, page.Rules...)
		if page.Next == "" {
			return rows, nil
		}
		if _, repeated := tokens[page.Next]; repeated {
			return nil, invalid
		}
		tokens[page.Next] = struct{}{}
		cursor = page.Next
	}
	return nil, invalid
}

func (c *Client) alibabaGroupRules(ctx context.Context, region, group, cursor string) (provider.Page, error) {
	rows, err := c.readAlibabaGroupRules(ctx, region, group, "all", cursor)
	if err != nil {
		return provider.Page{}, err
	}
	page := provider.Page{Next: rows.Next, Objects: make([]provider.Object, 0, len(rows.Rules))}
	for _, row := range rows.Rules {
		page.Objects = append(page.Objects, row.asset(group))
	}
	return page, nil
}

func (r alibabaRule) asset(group string) provider.Object {
	source := ruleDetail("source_cidr", r.SourceCidrIp) + ruleDetail("source_ipv6", r.Ipv6SourceCidrIp) +
		ruleDetail("source_group", r.SourceGroupId) + ruleDetail("source_owner", r.SourceGroupOwnerAccount) +
		ruleDetail("source_prefix_list", r.SourcePrefixListId)
	destination := ruleDetail("dest_cidr", r.DestCidrIp) + ruleDetail("dest_ipv6", r.Ipv6DestCidrIp) +
		ruleDetail("dest_group", r.DestGroupId) + ruleDetail("dest_owner", r.DestGroupOwnerAccount) +
		ruleDetail("dest_prefix_list", r.DestPrefixListId)
	rule := provider.ObservedSecurityRule(r.SecurityGroupRuleId, group, r.Direction, strings.ToLower(r.IpProtocol),
		"", nil, nil, strings.TrimSpace(source+destination))
	rule.Name += ruleDetail("ports", r.PortRange) + ruleDetail("source_ports", r.SourcePortRange) +
		ruleDetail("port_list", r.PortRangeListId) + ruleDetail("action", strings.ToLower(r.Policy)) +
		ruleDetail("priority", r.Priority) + ruleDetail("network", r.NicType)
	return rule
}
