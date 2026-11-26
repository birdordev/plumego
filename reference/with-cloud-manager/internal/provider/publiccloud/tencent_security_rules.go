package publiccloud

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"with-cloud-manager/internal/provider"
)

type tencentPolicyMatch struct {
	Protocol, Port, CidrBlock, Ipv6CidrBlock, SecurityGroupId, Action, PolicyDescription string
	AddressTemplate                                                                      struct{ AddressId, AddressGroupId string }
	ServiceTemplate                                                                      struct{ ServiceId, ServiceGroupId string }
}

type tencentPolicy struct {
	tencentPolicyMatch
	PolicyIndex, Priority *int
}

type tencentPolicySet struct {
	Ingress, Egress []tencentPolicy
	Version         *string
}

func (c *Client) tencentSecurityRules(ctx context.Context, region, cursor string) (provider.Page, error) {
	return walkSecurityRules(ctx, region, cursor, c.tencentSecurityGroups, c.tencentGroupRules)
}

func (c *Client) readTencentGroupPolicies(ctx context.Context, region, group string) (*tencentPolicySet, error) {
	body, err := json.Marshal(struct{ SecurityGroupId string }{group})
	if err != nil {
		return nil, err
	}
	var result struct {
		SecurityGroupPolicySet *tencentPolicySet
	}
	if err := c.tencent(ctx, "vpc", region, "DescribeSecurityGroupPolicies", "2017-03-12", body, &result); err != nil {
		return nil, err
	}
	set := result.SecurityGroupPolicySet
	if set == nil || (set.Version == nil && set.Ingress == nil && set.Egress == nil) {
		return nil, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return set, nil
}

func (c *Client) tencentGroupRules(ctx context.Context, region, group, cursor string) (provider.Page, error) {
	set, err := c.readTencentGroupPolicies(ctx, region, group)
	if err != nil {
		return provider.Page{}, err
	}
	objects := make([]provider.Object, 0, len(set.Ingress)+len(set.Egress))
	duplicates := make(map[string]int)
	for _, list := range []struct {
		direction string
		rules     []tencentPolicy
	}{{"ingress", set.Ingress}, {"egress", set.Egress}} {
		for _, row := range list.rules {
			rule, err := tencentRule(group, list.direction, row, duplicates)
			if err != nil {
				return provider.Page{}, err
			}
			objects = append(objects, rule)
		}
	}
	if cursor == "" && len(objects) <= provider.MaxPageObjects {
		return provider.Page{Objects: objects}, nil
	}
	return tencentPolicyPage(objects, cursor)
}

func tencentRule(group, direction string, row tencentPolicy, duplicates map[string]int) (provider.Object, error) {
	row.Protocol, row.Action = strings.ToLower(row.Protocol), strings.ToLower(row.Action)
	if row.Action != "accept" && row.Action != "drop" {
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	key, err := json.Marshal(row.tencentPolicyMatch)
	if err != nil {
		return provider.Object{}, err
	}
	// 规则没有厂商稳定 ID。用匹配条件、动作及描述区分规则，避免排序改变资产 ID。
	fingerprint := sha([]byte(group + ":" + direction + ":" + string(key)))
	duplicates[fingerprint]++
	id := "tc-sgr-" + fingerprint + "-" + strconv.Itoa(duplicates[fingerprint])
	family := ""
	if row.Ipv6CidrBlock != "" {
		family = "IPv6"
	}
	remote := strings.TrimSpace(ruleDetail("cidr", row.CidrBlock) + ruleDetail("ipv6_cidr", row.Ipv6CidrBlock) +
		ruleDetail("remote_group", row.SecurityGroupId) + ruleDetail("address", row.AddressTemplate.AddressId) +
		ruleDetail("address_group", row.AddressTemplate.AddressGroupId))
	protocol := row.Protocol
	if protocol == "" && (row.ServiceTemplate.ServiceId != "" || row.ServiceTemplate.ServiceGroupId != "") {
		protocol = "template"
	}
	rule := provider.ObservedSecurityRule(id, group, direction, protocol, family, nil, nil, remote)
	rule.Name += ruleDetail("ports", row.Port) + ruleDetail("service", row.ServiceTemplate.ServiceId) +
		ruleDetail("service_group", row.ServiceTemplate.ServiceGroupId) + ruleDetail("action", row.Action)
	if row.Priority != nil {
		rule.Name += rulePriority(row.Priority)
	} else if row.PolicyIndex != nil {
		rule.Name += ruleDetail("index", strconv.Itoa(*row.PolicyIndex))
	}
	return rule, nil
}

func tencentPolicyPage(objects []provider.Object, cursor string) (provider.Page, error) {
	encoded, err := json.Marshal(objects)
	if err != nil {
		return provider.Page{}, err
	}
	revision := sha(encoded)
	offset := 0
	if cursor != "" {
		digest, position, found := strings.Cut(cursor, ":")
		offset, err = pageNumber(position, 0)
		if !found || err != nil || digest != revision || offset == 0 ||
			offset%provider.MaxPageObjects != 0 || offset >= len(objects) {
			return provider.Page{}, &provider.Failure{Kind: provider.ExpiredCursor}
		}
	}
	end := min(offset+provider.MaxPageObjects, len(objects))
	page := provider.Page{Objects: objects[offset:end]}
	if end < len(objects) {
		page.Next = revision + ":" + strconv.Itoa(end)
	}
	return page, nil
}
