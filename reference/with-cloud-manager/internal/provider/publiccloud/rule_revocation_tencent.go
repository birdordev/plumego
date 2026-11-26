package publiccloud

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"with-cloud-manager/internal/provider"
)

type tencentRuleTarget struct {
	object             provider.Object
	version, direction string
	index              *int
}

func (c *Client) readTencentRuleTarget(ctx context.Context, region string, target provider.SecurityRuleTarget) (tencentRuleTarget, error) {
	group, err := c.readTencentGroup(ctx, region, target.GroupID, "")
	if err != nil {
		return tencentRuleTarget{}, err
	}
	if *group.IsDefault {
		return tencentRuleTarget{}, &provider.Failure{Kind: provider.DeletionBlocked}
	}
	set, err := c.readTencentGroupPolicies(ctx, region, target.GroupID)
	if err != nil {
		return tencentRuleTarget{}, err
	}
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	if set.Version == nil || set.Ingress == nil || set.Egress == nil {
		return tencentRuleTarget{}, invalid
	}
	if _, err := strconv.ParseUint(*set.Version, 10, 64); err != nil {
		return tencentRuleTarget{}, invalid
	}
	match := tencentRuleTarget{version: *set.Version}
	prefix := target.ID[:strings.LastIndexByte(target.ID, '-')+1]
	similar := 0
	duplicates := make(map[string]int)
	for _, list := range []struct {
		direction string
		rows      []tencentPolicy
	}{{"ingress", set.Ingress}, {"egress", set.Egress}} {
		indexes := make(map[int]int)
		for _, row := range list.rows {
			if row.PolicyIndex != nil {
				indexes[*row.PolicyIndex]++
			}
			object, err := tencentRule(target.GroupID, list.direction, row, duplicates)
			if err != nil {
				return tencentRuleTarget{}, err
			}
			if strings.HasPrefix(object.ID, prefix) {
				similar++
			}
			if object.ID == target.ID {
				match.object, match.direction, match.index = object, list.direction, row.PolicyIndex
			}
		}
		if match.direction == list.direction && match.index != nil && indexes[*match.index] != 1 {
			return tencentRuleTarget{}, invalid
		}
	}
	if similar > 1 || (similar == 1 && match.object.ID == "") {
		return tencentRuleTarget{}, invalid
	}
	return match, nil
}

func (c *Client) prepareTencentRuleRevocation(ctx context.Context, region string, target provider.SecurityRuleTarget) (provider.SecurityRuleRevocation, error) {
	rule, err := c.readTencentRuleTarget(ctx, region, target)
	if err != nil {
		return nil, err
	}
	if !target.Matches(rule.object) || rule.index == nil || *rule.index < 0 {
		return nil, &provider.Failure{Kind: provider.DeletionBlocked}
	}
	type index struct{ PolicyIndex int }
	payload := struct {
		SecurityGroupId        string
		SecurityGroupPolicySet struct {
			Version string
			Ingress []index `json:",omitempty"`
			Egress  []index `json:",omitempty"`
		}
	}{SecurityGroupId: target.GroupID}
	payload.SecurityGroupPolicySet.Version = rule.version
	if rule.direction == "ingress" {
		payload.SecurityGroupPolicySet.Ingress = []index{{*rule.index}}
	} else {
		payload.SecurityGroupPolicySet.Egress = []index{{*rule.index}}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) (string, error) {
		var result struct{ RequestId string }
		err := c.tencent(ctx, "vpc", region, "DeleteSecurityGroupPolicies", "2017-03-12", body, &result)
		return result.RequestId, err
	}, nil
}
