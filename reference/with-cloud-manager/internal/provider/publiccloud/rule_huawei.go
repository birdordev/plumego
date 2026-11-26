package publiccloud

import (
	"context"
	"net/url"

	"with-cloud-manager/internal/provider"
)

type huaweiRuleResponse struct {
	RequestID string     `json:"request_id"`
	Rule      huaweiRule `json:"security_group_rule"`
}

func (c *Client) createHuaweiRule(ctx context.Context, region, group string, rule provider.SecurityRule, marker string) (provider.LaunchReceipt, error) {
	type entry struct {
		GroupID     string `json:"security_group_id"`
		Description string `json:"description"`
		Direction   string `json:"direction"`
		EtherType   string `json:"ethertype"`
		Protocol    string `json:"protocol"`
		Multiport   string `json:"multiport"`
		RemoteCIDR  string `json:"remote_ip_prefix"`
		Action      string `json:"action"`
		Priority    int    `json:"priority"`
		Enabled     bool   `json:"enabled"`
	}
	payload := struct {
		Rule entry `json:"security_group_rule"`
	}{entry{group, marker, rule.Direction, "IPv4", rule.Protocol, contiguousRulePorts(rule), rule.RemoteCIDR, "allow", rulePriorityLowest, true}}
	var result huaweiRuleResponse
	if err := c.postHuaweiNetwork(ctx, region, "/v3/"+c.profile.ProjectID+"/vpc/security-group-rules", payload, &result); err != nil {
		return provider.LaunchReceipt{}, err
	}
	if result.Rule.GroupID != "" && result.Rule.GroupID != group {
		return provider.LaunchReceipt{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return c.huaweiNetworkReceipt(result.RequestID, huaweiNetworkReceipt{ID: result.Rule.ID, Tenant: result.Rule.ProjectID})
}

func (c *Client) huaweiRuleEndpoint(region, id string) url.URL {
	return url.URL{Scheme: "https", Host: "vpc." + region + ".myhuaweicloud.com",
		Path: "/v3/" + c.profile.ProjectID + "/vpc/security-group-rules/" + id}
}

func (c *Client) readHuaweiRule(ctx context.Context, region, group, id string) (huaweiRule, error) {
	var result huaweiRuleResponse
	if err := c.readHuawei(ctx, c.huaweiRuleEndpoint(region, id), &result); err != nil {
		return huaweiRule{}, err
	}
	row := result.Rule
	if row.ProjectID != c.profile.ProjectID || row.GroupID != group {
		return huaweiRule{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	if row.ID != id || !validRuleDirection(row.Direction) {
		return huaweiRule{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return row, nil
}

func (c *Client) findHuaweiRule(ctx context.Context, region, group string, rule provider.SecurityRule, marker, id string) (provider.Object, error) {
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	var rows []huaweiRule
	if id == "" {
		details, err := c.readHuaweiGroup(ctx, region, group)
		if err != nil {
			return provider.Object{}, err
		}
		rows = details.Rules
	} else {
		if !huaweiResourceID.MatchString(id) {
			return provider.Object{}, invalid
		}
		row, err := c.readHuaweiRule(ctx, region, group, id)
		if err != nil {
			return provider.Object{}, err
		}
		if row.Description != marker {
			return provider.Object{}, invalid
		}
		rows = []huaweiRule{row}
	}
	var found provider.Object
	for _, row := range rows {
		if row.ProjectID != c.profile.ProjectID || row.GroupID != group || !huaweiResourceID.MatchString(row.ID) {
			return provider.Object{}, invalid
		}
		if row.Description != marker {
			continue
		}
		if found.ID != "" || !matchesHuaweiRule(row, rule) {
			return provider.Object{}, invalid
		}
		found = row.asset()
	}
	if found.ID == "" {
		return provider.Object{}, &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	return found, nil
}

func matchesHuaweiRule(row huaweiRule, rule provider.SecurityRule) bool {
	return row.Direction == rule.Direction && row.Protocol == rule.Protocol && row.EtherType == "IPv4" &&
		matchesContiguousRulePorts(row.Multiport, rule) && row.RemoteCIDR == rule.RemoteCIDR &&
		row.RemoteGroup == "" && row.RemoteAddressGroup == "" && row.Action == "allow" &&
		row.Enabled != nil && *row.Enabled && row.Priority != nil && *row.Priority == rulePriorityLowest
}
