package publiccloud

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"

	"with-cloud-manager/internal/provider"
)

func addAlibabaClassicDefaultMembers(group string, members []alibabaLBServer, dst *loadBalancerBackendRead) error {
	if members == nil {
		return invalidLoadBalancerBackend()
	}
	for _, row := range members {
		key, err := dst.endpointKey(row.ServerId, row.ServerIp, 0)
		if err != nil {
			return err
		}
		if err := dst.addMember(provider.LoadBalancerBackendMember{Key: key, GroupID: group, Weight: row.Weight}); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) alibabaClassicPrimarySecondary(ctx context.Context, region, parent string, cfg *provider.LoadBalancerConfiguration,
	positions map[loadBalancerListenerPort]int, dst *loadBalancerBackendRead) error {
	if err := dst.request(); err != nil {
		return err
	}
	var result struct {
		Code                    string
		MasterSlaveServerGroups struct {
			MasterSlaveServerGroup []struct {
				MasterSlaveServerGroupId, MasterSlaveServerGroupName string
				AssociatedObjects                                    struct {
					Listeners struct{ Listener []loadBalancerListenerPort }
				}
			}
		}
	}
	if err := c.alibaba(ctx, alibabaClassicLBEndpoint(region), "DescribeMasterSlaveServerGroups", "2014-05-15",
		url.Values{"RegionId": {region}, "LoadBalancerId": {parent}, "IncludeListener": {"true"}}, &result); err != nil {
		return err
	}
	if result.Code != "" {
		return cloudFailure(result.Code)
	}
	groups := result.MasterSlaveServerGroups.MasterSlaveServerGroup
	if groups == nil || len(cfg.Listeners)+len(cfg.TargetGroups)+len(groups) > provider.MaxLoadBalancerComponents {
		return invalidLoadBalancerBackend()
	}
	known := make(map[string]bool, len(cfg.TargetGroups)+len(groups))
	for _, group := range cfg.TargetGroups {
		known[group.ID] = true
	}
	for _, group := range groups {
		id := group.MasterSlaveServerGroupId
		if !validLaunchID(id, "rsp-") || known[id] {
			return invalidLoadBalancerBackend()
		}
		known[id] = true
		for _, link := range group.AssociatedObjects.Listeners.Listener {
			index, ok := positions[newLoadBalancerListenerPort(link.Port, link.Protocol)]
			if !ok {
				return invalidLoadBalancerBackend()
			}
			item := &cfg.Listeners[index]
			item.TargetGroupIDs = append(item.TargetGroupIDs, id)
		}
		cfg.TargetGroups = append(cfg.TargetGroups, provider.LoadBalancerTargetGroup{
			ID: id, Name: group.MasterSlaveServerGroupName, Kind: provider.LoadBalancerPrimarySecondaryGroup,
		})
		if err := c.alibabaClassicPrimarySecondaryMembers(ctx, region, id, dst); err != nil {
			return err
		}
	}
	return nil
}

type alibabaClassicPrimarySecondaryServer struct {
	ServerId, ServerIp, ServerType string
	Port, Weight                   json.Number
}

func (c *Client) alibabaClassicPrimarySecondaryMembers(ctx context.Context, region, id string, dst *loadBalancerBackendRead) error {
	if err := dst.request(); err != nil {
		return err
	}
	var result struct {
		Code, MasterSlaveServerGroupId string
		MasterSlaveBackendServers      struct {
			MasterSlaveBackendServer  []alibabaClassicPrimarySecondaryServer
			MasterSlaveBackendServers []alibabaClassicPrimarySecondaryServer
		}
	}
	if err := c.alibaba(ctx, alibabaClassicLBEndpoint(region), "DescribeMasterSlaveServerGroupAttribute", "2014-05-15",
		url.Values{"RegionId": {region}, "MasterSlaveServerGroupId": {id}}, &result); err != nil {
		return err
	}
	if result.Code != "" {
		return cloudFailure(result.Code)
	}
	members := result.MasterSlaveBackendServers.MasterSlaveBackendServer
	legacy := result.MasterSlaveBackendServers.MasterSlaveBackendServers
	if result.MasterSlaveServerGroupId != id || (members != nil && legacy != nil) {
		return invalidLoadBalancerBackend()
	}
	if members == nil {
		members = legacy
	}
	if len(members) != 2 {
		return invalidLoadBalancerBackend()
	}
	for _, member := range members {
		port, err := member.Port.Int64()
		if err != nil || port < 1 || port > 65535 {
			return invalidLoadBalancerBackend()
		}
		var weight *int
		if member.Weight != "" {
			value, err := member.Weight.Int64()
			if err != nil || value < 0 || value > 100 {
				return invalidLoadBalancerBackend()
			}
			weight = new(int(value))
		}
		role := ""
		switch strings.ToLower(member.ServerType) {
		case "master", "":
			role = "primary"
		case "slave":
			role = "secondary"
		default:
			return invalidLoadBalancerBackend()
		}
		key, err := dst.endpointKey(member.ServerId, member.ServerIp, int(port))
		if err != nil {
			return err
		}
		if err := dst.addMember(provider.LoadBalancerBackendMember{
			Key: key, GroupID: id, Port: int(port), Weight: weight, Role: role,
		}); err != nil {
			return err
		}
	}
	return nil
}
