package publiccloud

import (
	"context"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"with-cloud-manager/internal/provider"
)

type alibabaLBServer struct {
	ServerGroupId, ServerId, ServerIp, ServerType, Status string
	Port                                                  int
	Weight                                                *int
}

type alibabaLBHealthGroup struct {
	ServerGroupId    string
	NonNormalServers []struct {
		alibabaLBServer
		Status string
	}
}

type alibabaLBListenerHealth struct {
	ListenerId       string
	ListenerPort     int
	ServerGroupInfos []alibabaLBHealthGroup
}

type alibabaLBRuleHealth struct {
	RuleId           string
	ServerGroupInfos []alibabaLBHealthGroup
}

type alibabaLBBackendPage struct {
	Code, NextToken      string
	Servers              []alibabaLBServer
	ListenerHealthStatus []alibabaLBListenerHealth
	RuleHealthStatus     []alibabaLBRuleHealth
}

func alibabaLBBackendPages[T any](ctx context.Context, c *Client, region string, product alibabaLBProduct,
	action string, query url.Values, dst *loadBalancerBackendRead, selectRows func(alibabaLBBackendPage) []T) ([]T, error) {
	host, version := alibabaModernLBEndpoint(region, product)
	if product == alibabaNLB {
		query.Set("RegionId", region)
	}
	call := c.alibaba
	if product == alibabaNLB && action == "ListServerGroupServers" {
		call = c.alibabaForm
	}
	return readLoadBalancerPages(ctx, func(ctx context.Context, cursor string) ([]T, string, error) {
		if err := dst.request(); err != nil {
			return nil, "", err
		}
		if cursor != "" {
			query.Set("NextToken", cursor)
		}
		var response alibabaLBBackendPage
		if err := call(ctx, host, action, version, query, &response); err != nil {
			return nil, "", err
		}
		if response.Code != "" {
			return nil, "", cloudFailure(response.Code)
		}
		if product == alibabaNLB && action == "GetListenerHealthStatus" && response.NextToken != "" {
			return nil, "", invalidLoadBalancerBackend()
		}
		return selectRows(response), response.NextToken, nil
	})
}

func (r alibabaLBServer) editableWeight() bool {
	if r.Port < 1 || r.Weight == nil {
		return false
	}
	switch r.ServerType {
	case "Ecs", "Eni", "Eci":
		return resourceID.MatchString(r.ServerId)
	case "Ip":
		ip, err := netip.ParseAddr(r.ServerId)
		return err == nil && ip.Zone() == ""
	}
	return false
}

func (c *Client) alibabaLoadBalancerMemberRows(ctx context.Context, region string, product alibabaLBProduct, id string, dst *loadBalancerBackendRead) ([]alibabaLBServer, error) {
	return alibabaLBBackendPages(ctx, c, region, product, "ListServerGroupServers",
		url.Values{"MaxResults": {strconv.Itoa(loadBalancerPageSize)}, "ServerGroupId": {id}}, dst,
		func(page alibabaLBBackendPage) []alibabaLBServer { return page.Servers })
}

func (c *Client) alibabaLoadBalancerMembers(ctx context.Context, region string, product alibabaLBProduct, id string, dst *loadBalancerBackendRead) error {
	rows, err := c.alibabaLoadBalancerMemberRows(ctx, region, product, id, dst)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.ServerGroupId != id {
			return invalidLoadBalancerBackend()
		}
		key, err := dst.endpointKey(row.ServerId, row.ServerIp, row.Port)
		if err != nil {
			return err
		}
		if err := dst.addMember(provider.LoadBalancerBackendMember{Key: key, GroupID: id, Port: row.Port, Weight: row.Weight, WeightEditable: dst.stable && row.editableWeight()}); err != nil {
			return err
		}
	}
	return nil
}

type alibabaBackendHealthScope struct {
	listenerID string
	groups     []string
}

type alibabaLBHealthEntry struct {
	listener *alibabaLBListenerHealth
	rule     *alibabaLBRuleHealth
}

func (c *Client) alibabaLoadBalancerHealth(ctx context.Context, region string, product alibabaLBProduct, listener provider.LoadBalancerListener,
	defaultGroups []string, rules map[string]alibabaBackendHealthScope, dst *loadBalancerBackendRead) error {
	query := url.Values{"ListenerId": {listener.ID}}
	if product == alibabaALB {
		query.Set("IncludeRule", "true")
		query.Set("MaxResults", "30")
	}
	entries, err := alibabaLBBackendPages(ctx, c, region, product, "GetListenerHealthStatus", query, dst,
		func(page alibabaLBBackendPage) []alibabaLBHealthEntry {
			if page.ListenerHealthStatus == nil && page.RuleHealthStatus == nil {
				return nil
			}
			entries := make([]alibabaLBHealthEntry, 0, len(page.ListenerHealthStatus)+len(page.RuleHealthStatus))
			for i := range page.ListenerHealthStatus {
				entries = append(entries, alibabaLBHealthEntry{listener: &page.ListenerHealthStatus[i]})
			}
			for i := range page.RuleHealthStatus {
				entries = append(entries, alibabaLBHealthEntry{rule: &page.RuleHealthStatus[i]})
			}
			return entries
		})
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if row := entry.listener; row != nil {
			if !matchesAlibabaHealthListener(product, listener.ID, row) {
				return invalidLoadBalancerBackend()
			}
			if row.ListenerPort != listener.Port && (listener.EndPort == 0 || row.ListenerPort < listener.Port || row.ListenerPort > listener.EndPort) {
				return invalidLoadBalancerBackend()
			}
			if err := addAlibabaHealthGroups(listener.ID, "", row.ListenerPort, defaultGroups, row.ServerGroupInfos, dst); err != nil {
				return err
			}
			continue
		}
		rule := entry.rule
		scope, ok := rules[rule.RuleId]
		if product != alibabaALB || !ok || scope.listenerID != listener.ID {
			return invalidLoadBalancerBackend()
		}
		if err := addAlibabaHealthGroups(listener.ID, rule.RuleId, listener.Port, scope.groups, rule.ServerGroupInfos, dst); err != nil {
			return err
		}
	}
	return nil
}

func matchesAlibabaHealthListener(product alibabaLBProduct, listenerID string, row *alibabaLBListenerHealth) bool {
	if product != alibabaNLB {
		return row.ListenerId == listenerID
	}
	expected, _, _ := strings.Cut(listenerID, "@")
	actual, suffix, hasSuffix := strings.Cut(row.ListenerId, "@")
	return actual == expected && (!hasSuffix || suffix == strconv.Itoa(row.ListenerPort))
}

func addAlibabaHealthGroups(listener, rule string, listenerPort int, allowed []string, groups []alibabaLBHealthGroup, dst *loadBalancerBackendRead) error {
	for _, group := range groups {
		if !slices.Contains(allowed, group.ServerGroupId) {
			return invalidLoadBalancerBackend()
		}
		for _, row := range group.NonNormalServers {
			key, err := dst.endpointKey(row.ServerId, row.ServerIp, row.Port)
			if err != nil {
				return err
			}
			if err := dst.addCheck(provider.LoadBalancerBackendCheck{Key: key, ListenerID: listener, GroupID: group.ServerGroupId,
				RuleID: rule, Port: row.Port, ListenerPort: listenerPort, Status: backendHealth(row.Status)}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *Client) alibabaClassicLBMembers(ctx context.Context, region, group string, dst *loadBalancerBackendRead) error {
	if err := dst.request(); err != nil {
		return err
	}
	var result struct {
		Code, VServerGroupId string
		BackendServers       struct{ BackendServer []alibabaLBServer }
	}
	if err := c.alibaba(ctx, alibabaClassicLBEndpoint(region), "DescribeVServerGroupAttribute", "2014-05-15",
		url.Values{"RegionId": {region}, "VServerGroupId": {group}}, &result); err != nil {
		return err
	}
	if result.Code != "" {
		return cloudFailure(result.Code)
	}
	if result.VServerGroupId != group || result.BackendServers.BackendServer == nil {
		return invalidLoadBalancerBackend()
	}
	for _, row := range result.BackendServers.BackendServer {
		key, err := dst.endpointKey(row.ServerId, row.ServerIp, row.Port)
		if err != nil {
			return err
		}
		if err := dst.addMember(provider.LoadBalancerBackendMember{Key: key, GroupID: group, Port: row.Port, Weight: row.Weight}); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) alibabaClassicLBHealth(ctx context.Context, region, parent string, listener provider.LoadBalancerListener, dst *loadBalancerBackendRead) error {
	if err := dst.request(); err != nil {
		return err
	}
	var result struct {
		Code           string
		BackendServers struct {
			BackendServer []struct {
				alibabaLBServer
				ListenerPort                 int
				Protocol, ServerHealthStatus string
			}
		}
	}
	query := url.Values{"RegionId": {region}, "LoadBalancerId": {parent},
		"ListenerPort": {strconv.Itoa(listener.Port)}, "ListenerProtocol": {strings.ToLower(listener.Protocol)}}
	if err := c.alibaba(ctx, alibabaClassicLBEndpoint(region), "DescribeHealthStatus", "2014-05-15", query, &result); err != nil {
		return err
	}
	if result.Code != "" {
		return cloudFailure(result.Code)
	}
	if result.BackendServers.BackendServer == nil {
		return invalidLoadBalancerBackend()
	}
	for _, row := range result.BackendServers.BackendServer {
		if row.ListenerPort != listener.Port || (row.Protocol != "" && !strings.EqualFold(row.Protocol, listener.Protocol)) {
			return invalidLoadBalancerBackend()
		}
		key, err := dst.endpointKey(row.ServerId, row.ServerIp, row.Port)
		if err != nil {
			return err
		}
		if err := dst.addCheck(provider.LoadBalancerBackendCheck{Key: key, ListenerID: listener.ID, Port: row.Port,
			ListenerPort: row.ListenerPort, Status: backendHealth(row.ServerHealthStatus)}); err != nil {
			return err
		}
	}
	return nil
}
