package publiccloud

import (
	"context"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"with-cloud-manager/internal/provider"
)

type alibabaLBGroupTuple struct{ ServerGroupId string }

type alibabaLBAction struct {
	ForwardGroupConfig  struct{ ServerGroupTuples []alibabaLBGroupTuple }
	TrafficMirrorConfig struct {
		MirrorGroupConfig struct{ ServerGroupTuples []alibabaLBGroupTuple }
	}
}

func appendAlibabaGroupIDs(dst []string, actions []alibabaLBAction) []string {
	for _, action := range actions {
		for _, group := range action.ForwardGroupConfig.ServerGroupTuples {
			dst = append(dst, group.ServerGroupId)
		}
		for _, group := range action.TrafficMirrorConfig.MirrorGroupConfig.ServerGroupTuples {
			dst = append(dst, group.ServerGroupId)
		}
	}
	return dst
}

func alibabaModernLBEndpoint(region string, product alibabaLBProduct) (string, string) {
	hostRegion := region
	if region == "cn-hangzhou-finance" {
		hostRegion = "cn-hangzhou"
	}
	version := "2020-06-16"
	if product == alibabaNLB {
		version = "2022-04-30"
	}
	return string(product) + "." + hostRegion + ".aliyuncs.com", version
}

func alibabaClassicLBEndpoint(region string) string {
	if region == "cn-hangzhou" || region == "cn-hangzhou-finance" {
		return "slb.aliyuncs.com"
	}
	return "slb." + region + ".aliyuncs.com"
}

type alibabaLBListener struct {
	RegionId, LoadBalancerId, ListenerId, ListenerDescription, ListenerProtocol string
	ServerGroupId, StartPort, EndPort                                           string
	ListenerPort                                                                int
	DefaultActions                                                              []alibabaLBAction
}

type alibabaLBRule struct {
	RuleId, LoadBalancerId, ListenerId string
	RuleActions                        []alibabaLBAction
}

type alibabaLBGroup struct {
	VpcId, ServerGroupType                                                           string
	AnyPortEnabled                                                                   bool
	RegionId, ServerGroupId, ServerGroupName, Protocol, Scheduler, ServerGroupStatus string
	RelatedLoadBalancerIds                                                           []string
}

type alibabaModernLBConfigurationPage struct {
	Code, NextToken string
	Listeners       []alibabaLBListener
	Rules           []alibabaLBRule
	ServerGroups    []alibabaLBGroup
}

func alibabaLBConfigurationPages[T any](ctx context.Context, c *Client, region string, product alibabaLBProduct,
	action string, filter url.Values, selectRows func(alibabaModernLBConfigurationPage) []T) ([]T, error) {
	host, version := alibabaModernLBEndpoint(region, product)
	filter.Set("MaxResults", strconv.Itoa(loadBalancerPageSize))
	if product == alibabaNLB {
		filter.Set("RegionId", region)
	}
	call := c.alibaba
	if product == alibabaNLB && action == "ListServerGroups" {
		call = c.alibabaForm
	}
	return readLoadBalancerPages(ctx, func(ctx context.Context, cursor string) ([]T, string, error) {
		if cursor != "" {
			filter.Set("NextToken", cursor)
		}
		var result alibabaModernLBConfigurationPage
		if err := call(ctx, host, action, version, filter, &result); err != nil {
			return nil, "", err
		}
		if result.Code != "" {
			return nil, "", cloudFailure(result.Code)
		}
		return selectRows(result), result.NextToken, nil
	})
}

func validAlibabaLoadBalancerListenerID(product alibabaLBProduct, id string) bool {
	if product == alibabaNLB {
		base, _, _ := strings.Cut(id, "@")
		return validLaunchID(base, "lsn-") && provider.ValidLoadBalancerListenerID(id)
	}
	return validLaunchID(id, "lsn-")
}

func (c *Client) alibabaLoadBalancerConfiguration(ctx context.Context, region, id, productName string, backends *loadBalancerBackendRead) (provider.LoadBalancerConfiguration, error) {
	if productName == "alibaba-clb" {
		return c.alibabaClassicLBConfiguration(ctx, region, id, backends)
	}
	product := alibabaLBProduct(strings.TrimPrefix(productName, "alibaba-"))
	if product != alibabaALB && product != alibabaNLB {
		return provider.LoadBalancerConfiguration{}, &provider.Failure{Kind: provider.Unsupported}
	}
	rows, err := alibabaLBConfigurationPages(ctx, c, region, product, "ListListeners", url.Values{"LoadBalancerIds.1": {id}},
		func(page alibabaModernLBConfigurationPage) []alibabaLBListener { return page.Listeners })
	if err != nil {
		return provider.LoadBalancerConfiguration{}, err
	}
	cfg := provider.LoadBalancerConfiguration{Listeners: make([]provider.LoadBalancerListener, 0, len(rows))}
	positions := make(map[string]int, len(rows))
	var defaultGroups map[string][]string
	var ruleGroups map[string]alibabaBackendHealthScope
	if backends != nil {
		defaultGroups = make(map[string][]string, len(rows))
		ruleGroups = make(map[string]alibabaBackendHealthScope)
	}
	for _, row := range rows {
		if row.LoadBalancerId != id || !validAlibabaLoadBalancerListenerID(product, row.ListenerId) {
			return provider.LoadBalancerConfiguration{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		if row.RegionId != "" && row.RegionId != region {
			return provider.LoadBalancerConfiguration{}, &provider.Failure{Kind: provider.Unauthorized}
		}
		item := provider.LoadBalancerListener{ID: row.ListenerId, Name: row.ListenerDescription, Protocol: row.ListenerProtocol, Port: row.ListenerPort}
		if product == alibabaNLB && row.ListenerPort == 0 {
			var startErr, endErr error
			item.Port, startErr = strconv.Atoi(row.StartPort)
			item.EndPort, endErr = strconv.Atoi(row.EndPort)
			if startErr != nil || endErr != nil || item.EndPort == 0 {
				return provider.LoadBalancerConfiguration{}, &provider.Failure{Kind: provider.InvalidResponse}
			}
		}
		if row.ServerGroupId != "" {
			item.TargetGroupIDs = []string{row.ServerGroupId}
		}
		item.TargetGroupIDs = appendAlibabaGroupIDs(item.TargetGroupIDs, row.DefaultActions)
		if backends != nil {
			defaultGroups[row.ListenerId] = slices.Clone(item.TargetGroupIDs)
		}
		positions[row.ListenerId] = len(cfg.Listeners)
		cfg.Listeners = append(cfg.Listeners, item)
	}
	if product == alibabaALB && len(rows) != 0 {
		rules, err := alibabaLBConfigurationPages(ctx, c, region, product, "ListRules", url.Values{"LoadBalancerIds.1": {id}},
			func(page alibabaModernLBConfigurationPage) []alibabaLBRule { return page.Rules })
		if err != nil {
			return provider.LoadBalancerConfiguration{}, err
		}
		seen := make(map[string]bool, len(rules))
		for _, rule := range rules {
			i, found := positions[rule.ListenerId]
			if !found || rule.LoadBalancerId != id || !validLaunchID(rule.RuleId, "rule-") || seen[rule.RuleId] {
				return provider.LoadBalancerConfiguration{}, &provider.Failure{Kind: provider.InvalidResponse}
			}
			seen[rule.RuleId] = true
			item := &cfg.Listeners[i]
			groups := appendAlibabaGroupIDs(nil, rule.RuleActions)
			item.TargetGroupIDs = append(item.TargetGroupIDs, groups...)
			if backends != nil {
				ruleGroups[rule.RuleId] = alibabaBackendHealthScope{listenerID: rule.ListenerId, groups: groups}
			}
		}
	}
	ids := configurationGroupIDs(cfg.Listeners)
	if len(ids)+len(cfg.Listeners) > provider.MaxLoadBalancerComponents {
		return provider.LoadBalancerConfiguration{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	for batch := range slices.Chunk(ids, 20) {
		groups, err := c.alibabaModernLBGroups(ctx, region, id, product, batch)
		if err != nil {
			return provider.LoadBalancerConfiguration{}, err
		}
		cfg.TargetGroups = append(cfg.TargetGroups, groups...)
	}
	if backends != nil {
		for _, group := range cfg.TargetGroups {
			if err := c.alibabaLoadBalancerMembers(ctx, region, product, group.ID, backends); err != nil {
				return provider.LoadBalancerConfiguration{}, err
			}
		}
		for _, listener := range cfg.Listeners {
			if err := c.alibabaLoadBalancerHealth(ctx, region, product, listener, defaultGroups[listener.ID], ruleGroups, backends); err != nil {
				return provider.LoadBalancerConfiguration{}, err
			}
		}
	}
	return cfg, nil
}

func (c *Client) alibabaModernLBGroupRows(ctx context.Context, region string, product alibabaLBProduct, ids []string) ([]alibabaLBGroup, error) {
	query := make(url.Values, len(ids)+2)
	for i, id := range ids {
		if !validLaunchID(id, "sgp-") {
			return nil, &provider.Failure{Kind: provider.InvalidResponse}
		}
		query.Set("ServerGroupIds."+strconv.Itoa(i+1), id)
	}
	rows, err := alibabaLBConfigurationPages(ctx, c, region, product, "ListServerGroups", query,
		func(page alibabaModernLBConfigurationPage) []alibabaLBGroup { return page.ServerGroups })
	if err != nil {
		return nil, err
	}
	if len(rows) != len(ids) {
		return nil, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return rows, nil
}

func (c *Client) alibabaModernLBGroups(ctx context.Context, region, parent string, product alibabaLBProduct, ids []string) ([]provider.LoadBalancerTargetGroup, error) {
	rows, err := c.alibabaModernLBGroupRows(ctx, region, product, ids)
	if err != nil {
		return nil, err
	}
	groups := make([]provider.LoadBalancerTargetGroup, 0, len(ids))
	for _, group := range rows {
		if _, found := slices.BinarySearch(ids, group.ServerGroupId); !found ||
			!slices.Contains(group.RelatedLoadBalancerIds, parent) {
			return nil, &provider.Failure{Kind: provider.InvalidResponse}
		}
		if group.RegionId != "" && group.RegionId != region {
			return nil, &provider.Failure{Kind: provider.Unauthorized}
		}
		groups = append(groups, provider.LoadBalancerTargetGroup{ID: group.ServerGroupId, Name: group.ServerGroupName, Protocol: group.Protocol, Algorithm: group.Scheduler, Exclusive: slices.Equal(group.RelatedLoadBalancerIds, []string{parent})})
	}
	return groups, nil
}

type alibabaClassicLBListener struct {
	ListenerPort                                    int
	ListenerProtocol, ListenerProtocal, Description string
}

func (c *Client) alibabaClassicLBConfiguration(ctx context.Context, region, id string, backends *loadBalancerBackendRead) (provider.LoadBalancerConfiguration, error) {
	host := alibabaClassicLBEndpoint(region)
	var parent struct {
		Code, RegionId, LoadBalancerId string
		ListenerPortsAndProtocol       struct{ ListenerPortAndProtocol []alibabaClassicLBListener }
		ListenerPortsAndProtocal       struct{ ListenerPortAndProtocal []alibabaClassicLBListener }
		BackendServers                 struct{ BackendServer []alibabaLBServer }
	}
	if err := c.alibaba(ctx, host, "DescribeLoadBalancerAttribute", "2014-05-15", url.Values{"RegionId": {region}, "LoadBalancerId": {id}}, &parent); err != nil {
		return provider.LoadBalancerConfiguration{}, err
	}
	if parent.Code != "" {
		return provider.LoadBalancerConfiguration{}, cloudFailure(parent.Code)
	}
	if parent.LoadBalancerId != id || parent.RegionId != region {
		return provider.LoadBalancerConfiguration{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	listeners := parent.ListenerPortsAndProtocol.ListenerPortAndProtocol
	if listeners == nil {
		listeners = parent.ListenerPortsAndProtocal.ListenerPortAndProtocal
	}
	if listeners == nil || len(listeners) > provider.MaxLoadBalancerComponents {
		return provider.LoadBalancerConfiguration{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	defaultGroup := id + ".default"
	cfg := provider.LoadBalancerConfiguration{
		Listeners:    make([]provider.LoadBalancerListener, 0, len(listeners)),
		TargetGroups: []provider.LoadBalancerTargetGroup{{ID: defaultGroup, Kind: provider.LoadBalancerDefaultGroup}},
	}
	positions := make(map[loadBalancerListenerPort]int, len(listeners))
	for _, row := range listeners {
		protocol := strings.ToUpper(row.ListenerProtocol)
		if protocol == "" {
			protocol = strings.ToUpper(row.ListenerProtocal)
		}
		key := newLoadBalancerListenerPort(row.ListenerPort, protocol)
		positions[key] = len(cfg.Listeners)
		cfg.Listeners = append(cfg.Listeners, provider.LoadBalancerListener{
			ID:       id + "." + strings.ToLower(protocol) + "." + strconv.Itoa(row.ListenerPort),
			Protocol: protocol, Port: row.ListenerPort, Name: row.Description,
		})
	}
	var result struct {
		Code          string
		VServerGroups struct {
			VServerGroup []struct {
				VServerGroupId, VServerGroupName string
				AssociatedObjects                struct {
					Listeners struct{ Listener []loadBalancerListenerPort }
					Rules     struct{ Rule []loadBalancerListenerPort }
				}
			}
		}
	}
	if err := c.alibaba(ctx, host, "DescribeVServerGroups", "2014-05-15", url.Values{
		"RegionId": {region}, "LoadBalancerId": {id}, "IncludeRule": {"true"}, "IncludeListener": {"true"},
	}, &result); err != nil {
		return provider.LoadBalancerConfiguration{}, err
	}
	if result.Code != "" {
		return provider.LoadBalancerConfiguration{}, cloudFailure(result.Code)
	}
	if result.VServerGroups.VServerGroup == nil || len(result.VServerGroups.VServerGroup)+len(cfg.Listeners)+len(cfg.TargetGroups) > provider.MaxLoadBalancerComponents {
		return provider.LoadBalancerConfiguration{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	for _, group := range result.VServerGroups.VServerGroup {
		if !validLaunchID(group.VServerGroupId, "rsp-") {
			return provider.LoadBalancerConfiguration{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		for _, link := range slices.Concat(group.AssociatedObjects.Listeners.Listener, group.AssociatedObjects.Rules.Rule) {
			link.Protocol = strings.ToUpper(link.Protocol)
			if i, found := positions[link]; found {
				cfg.Listeners[i].TargetGroupIDs = append(cfg.Listeners[i].TargetGroupIDs, group.VServerGroupId)
			}
		}
		cfg.TargetGroups = append(cfg.TargetGroups, provider.LoadBalancerTargetGroup{
			ID: group.VServerGroupId, Name: group.VServerGroupName, Kind: provider.LoadBalancerVirtualGroup,
		})
	}
	if backends != nil {
		if err := addAlibabaClassicDefaultMembers(defaultGroup, parent.BackendServers.BackendServer, backends); err != nil {
			return provider.LoadBalancerConfiguration{}, err
		}
		for _, group := range cfg.TargetGroups[1:] {
			if err := c.alibabaClassicLBMembers(ctx, region, group.ID, backends); err != nil {
				return provider.LoadBalancerConfiguration{}, err
			}
		}
		if err := c.alibabaClassicPrimarySecondary(ctx, region, id, &cfg, positions, backends); err != nil {
			return provider.LoadBalancerConfiguration{}, err
		}
		for _, listener := range cfg.Listeners {
			if err := c.alibabaClassicLBHealth(ctx, region, id, listener, backends); err != nil {
				return provider.LoadBalancerConfiguration{}, err
			}
		}
	}
	return cfg, nil
}
