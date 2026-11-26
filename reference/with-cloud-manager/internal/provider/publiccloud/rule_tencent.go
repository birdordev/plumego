package publiccloud

import (
	"context"
	"encoding/json"
	"strings"

	"with-cloud-manager/internal/provider"
)

func (c *Client) createTencentRule(ctx context.Context, region, group string, rule provider.SecurityRule, marker, version string) (provider.LaunchReceipt, error) {
	type entry struct{ Protocol, Port, CidrBlock, Action, PolicyDescription string }
	payload := struct {
		SecurityGroupId        string
		SecurityGroupPolicySet struct {
			Version string
			Ingress []entry `json:",omitempty"`
			Egress  []entry `json:",omitempty"`
		}
	}{SecurityGroupId: group}
	row := entry{strings.ToUpper(rule.Protocol), contiguousRulePorts(rule), rule.RemoteCIDR, "ACCEPT", marker}
	payload.SecurityGroupPolicySet.Version = version
	if rule.Direction == "ingress" {
		payload.SecurityGroupPolicySet.Ingress = []entry{row}
	} else {
		payload.SecurityGroupPolicySet.Egress = []entry{row}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return provider.LaunchReceipt{}, err
	}
	var response struct{ RequestId string }
	if err := c.tencent(ctx, "vpc", region, "CreateSecurityGroupPolicies", "2017-03-12", body, &response); err != nil {
		return provider.LaunchReceipt{}, err
	}
	return provider.LaunchReceipt{RequestID: response.RequestId}, nil
}

func (c *Client) findTencentRule(ctx context.Context, region, group string, rule provider.SecurityRule, marker, id string) (provider.Object, error) {
	set, err := c.readTencentGroupPolicies(ctx, region, group)
	if err != nil {
		return provider.Object{}, err
	}
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	var found provider.Object
	for _, list := range []struct {
		direction string
		rows      []tencentPolicy
	}{{"ingress", set.Ingress}, {"egress", set.Egress}} {
		for _, row := range list.rows {
			if row.PolicyDescription != marker {
				continue
			}
			if found.ID != "" || list.direction != rule.Direction || !matchesTencentRule(row, rule) {
				return provider.Object{}, invalid
			}
			found, err = tencentRule(group, list.direction, row, make(map[string]int, 1))
			if err != nil || (id != "" && found.ID != id) {
				return provider.Object{}, invalid
			}
		}
	}
	if found.ID == "" {
		return provider.Object{}, &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	return found, nil
}

func matchesTencentRule(row tencentPolicy, rule provider.SecurityRule) bool {
	return strings.EqualFold(row.Protocol, rule.Protocol) && matchesContiguousRulePorts(row.Port, rule) &&
		row.CidrBlock == rule.RemoteCIDR && strings.EqualFold(row.Action, "ACCEPT") &&
		row.Ipv6CidrBlock == "" && row.SecurityGroupId == "" &&
		row.AddressTemplate.AddressId == "" && row.AddressTemplate.AddressGroupId == "" &&
		row.ServiceTemplate.ServiceId == "" && row.ServiceTemplate.ServiceGroupId == ""
}
