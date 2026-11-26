package publiccloud

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"with-cloud-manager/internal/provider"
)

func alibabaRuleAction(rule provider.SecurityRule) (action, cidrField string) {
	if rule.Direction == "ingress" {
		return "AuthorizeSecurityGroup", "SourceCidrIp"
	}
	return "AuthorizeSecurityGroupEgress", "DestCidrIp"
}

func (c *Client) createAlibabaRule(ctx context.Context, region, group string, rule provider.SecurityRule, marker, token string) (provider.LaunchReceipt, error) {
	action, cidrField := alibabaRuleAction(rule)
	query := url.Values{"RegionId": {region}, "SecurityGroupId": {group}, "ClientToken": {token},
		"Permissions.1." + cidrField: {rule.RemoteCIDR}, "Permissions.1.PortRange": {alibabaRulePorts(rule)},
		"Permissions.1.IpProtocol": {strings.ToUpper(rule.Protocol)}, "Permissions.1.Policy": {"accept"},
		"Permissions.1.Priority": {strconv.Itoa(rulePriorityLowest)}, "Permissions.1.Description": {marker}}
	var result struct{ Code, RequestId string }
	if err := c.alibaba(ctx, "ecs."+region+".aliyuncs.com", action, "2014-05-26", query, &result); err != nil {
		return provider.LaunchReceipt{}, err
	}
	if result.Code != "" {
		return provider.LaunchReceipt{}, cloudFailure(result.Code)
	}
	return provider.LaunchReceipt{RequestID: result.RequestId}, nil
}

func (c *Client) findAlibabaRule(ctx context.Context, region, group string, rule provider.SecurityRule, marker, id string) (provider.Object, error) {
	rows, err := c.readAlibabaRuleSet(ctx, region, group, "")
	if err != nil {
		return provider.Object{}, err
	}
	var found provider.Object
	for _, row := range rows {
		if row.Description != marker {
			continue
		}
		if found.ID != "" || (id != "" && row.SecurityGroupRuleId != id) || !matchesAlibabaRule(row, rule) {
			return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		found = row.asset(group)
	}
	if found.ID == "" {
		return provider.Object{}, &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	return found, nil
}

func alibabaRulePorts(rule provider.SecurityRule) string {
	return fmt.Sprintf("%d/%d", rule.FromPort, rule.ToPort)
}

func matchesAlibabaRule(row alibabaRule, rule provider.SecurityRule) bool {
	remote, opposite := row.SourceCidrIp, row.DestCidrIp
	if rule.Direction == "egress" {
		remote, opposite = opposite, remote
	}
	return row.Direction == rule.Direction && strings.EqualFold(row.IpProtocol, rule.Protocol) &&
		row.PortRange == alibabaRulePorts(rule) && remote == rule.RemoteCIDR &&
		(opposite == "" || opposite == "0.0.0.0/0") && strings.EqualFold(row.Policy, "accept") &&
		row.Priority == strconv.Itoa(rulePriorityLowest) && row.NicType == "intranet" &&
		(row.SourcePortRange == "" || row.SourcePortRange == "-1/-1") && row.PortRangeListId == "" &&
		row.Ipv6SourceCidrIp == "" && row.Ipv6DestCidrIp == "" && row.SourceGroupId == "" && row.DestGroupId == "" &&
		row.SourcePrefixListId == "" && row.DestPrefixListId == "" && row.SourceGroupOwnerAccount == "" && row.DestGroupOwnerAccount == ""
}
