package publiccloud

import (
	"context"
	"encoding/json"
	"net/netip"

	"with-cloud-manager/internal/provider"
)

type tencentLBTargetHealth struct {
	TargetId, IP, HealthStatusDetail string
	Port                             int
	HealthStatus                     *bool
}

type tencentLBTargetGroupMember struct {
	TargetGroupId, InstanceId, BindIP string
	PrivateIpAddresses                []string
	Port                              int
	Weight                            *int
}

func (row tencentLBTargetGroupMember) address() string {
	if row.BindIP == "" && len(row.PrivateIpAddresses) == 1 {
		return row.PrivateIpAddresses[0]
	}
	return row.BindIP
}

func (row tencentLBTargetGroupMember) editableWeight() bool {
	ip, err := netip.ParseAddr(row.address())
	return err == nil && ip.Zone() == "" && row.Port > 0 && row.Weight != nil
}

func (c *Client) tencentLoadBalancerMemberRows(ctx context.Context, region, group string, dst *loadBalancerBackendRead) ([]tencentLBTargetGroupMember, error) {
	type filter struct {
		Name   string
		Values []string
	}
	return readLoadBalancerPages(ctx, func(ctx context.Context, cursor string) ([]tencentLBTargetGroupMember, string, error) {
		if err := dst.request(); err != nil {
			return nil, "", err
		}
		offset, err := pageNumber(cursor, 0)
		if err != nil {
			return nil, "", err
		}
		body, err := json.Marshal(struct {
			Filters       []filter
			Limit, Offset int
		}{[]filter{{Name: "TargetGroupId", Values: []string{group}}}, loadBalancerPageSize, offset})
		if err != nil {
			return nil, "", err
		}
		var result struct {
			TotalCount, RealCount  *int
			TargetGroupInstanceSet []tencentLBTargetGroupMember
		}
		if err := c.tencent(ctx, "clb", region, "DescribeTargetGroupInstances", "2018-03-17", body, &result); err != nil {
			return nil, "", err
		}
		if result.RealCount != nil && (result.TotalCount == nil || *result.RealCount != *result.TotalCount) {
			return nil, "", invalidLoadBalancerBackend()
		}
		next, err := tencentNextOffset(offset, result.TotalCount, len(result.TargetGroupInstanceSet), loadBalancerPageSize)
		return result.TargetGroupInstanceSet, next, err
	})
}

func (c *Client) tencentLoadBalancerMembers(ctx context.Context, region, group string, fullListen bool, dst *loadBalancerBackendRead) error {
	rows, err := c.tencentLoadBalancerMemberRows(ctx, region, group, dst)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.TargetGroupId != group {
			return invalidLoadBalancerBackend()
		}
		key, err := dst.endpointKey(row.InstanceId, row.address(), row.Port)
		if err != nil {
			return err
		}
		if err := dst.addMember(provider.LoadBalancerBackendMember{Key: key, GroupID: group, Port: row.Port, Weight: row.Weight, WeightEditable: dst.stable && !fullListen && row.editableWeight()}); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) tencentLoadBalancerHealth(ctx context.Context, region, id string, listeners []provider.LoadBalancerListener, dst *loadBalancerBackendRead) error {
	if err := dst.request(); err != nil {
		return err
	}
	body, err := json.Marshal(struct{ LoadBalancerIds []string }{[]string{id}})
	if err != nil {
		return err
	}
	var result struct {
		LoadBalancers []struct {
			LoadBalancerId string
			Listeners      []struct {
				ListenerId string
				Targets    []tencentLBTargetHealth
				Rules      []struct {
					LocationId string
					Targets    []tencentLBTargetHealth
				}
			}
		}
	}
	if err := c.tencent(ctx, "clb", region, "DescribeTargetHealth", "2018-03-17", body, &result); err != nil {
		return err
	}
	if len(result.LoadBalancers) != 1 || result.LoadBalancers[0].LoadBalancerId != id || result.LoadBalancers[0].Listeners == nil {
		return invalidLoadBalancerBackend()
	}
	listenerIDs := make(map[string]bool, len(listeners))
	for _, listener := range listeners {
		listenerIDs[listener.ID] = true
	}
	for _, listener := range result.LoadBalancers[0].Listeners {
		if !listenerIDs[listener.ListenerId] {
			return invalidLoadBalancerBackend()
		}
		delete(listenerIDs, listener.ListenerId)
		if err := addTencentChecks(listener.ListenerId, "", listener.Targets, dst); err != nil {
			return err
		}
		for _, rule := range listener.Rules {
			if rule.LocationId != "" && !validLaunchID(rule.LocationId, "loc-") {
				return invalidLoadBalancerBackend()
			}
			if err := addTencentChecks(listener.ListenerId, rule.LocationId, rule.Targets, dst); err != nil {
				return err
			}
		}
	}
	return nil
}

func addTencentChecks(listener, rule string, targets []tencentLBTargetHealth, dst *loadBalancerBackendRead) error {
	for _, target := range targets {
		key, err := dst.endpointKey(target.TargetId, target.IP, target.Port)
		if err != nil {
			return err
		}
		if err := dst.addCheck(provider.LoadBalancerBackendCheck{Key: key, ListenerID: listener, RuleID: rule,
			Port: target.Port, Status: target.status()}); err != nil {
			return err
		}
	}
	return nil
}

func (target tencentLBTargetHealth) status() string {
	switch target.HealthStatusDetail {
	case "Alive", "":
		if target.HealthStatus != nil && *target.HealthStatus {
			return "healthy"
		}
	case "Dead":
		if target.HealthStatus == nil || !*target.HealthStatus {
			return "unhealthy"
		}
	case "Close":
		return "disabled"
	}
	return "unknown"
}

func (c *Client) tencentClassicLBMembers(ctx context.Context, region, id, group string, dst *loadBalancerBackendRead) (map[netip.Addr]string, error) {
	if err := dst.request(); err != nil {
		return nil, err
	}
	body, err := json.Marshal(struct{ LoadBalancerId string }{id})
	if err != nil {
		return nil, err
	}
	var result struct {
		Targets []struct {
			InstanceId         string
			PrivateIpAddresses []string
			Weight             *int
		}
	}
	if err := c.tencent(ctx, "clb", region, "DescribeClassicalLBTargets", "2018-03-17", body, &result); err != nil {
		return nil, err
	}
	if result.Targets == nil || len(result.Targets) > provider.MaxLoadBalancerBackendRecords {
		return nil, invalidLoadBalancerBackend()
	}
	keys := make(map[netip.Addr]string, len(result.Targets))
	for _, target := range result.Targets {
		key, err := dst.endpointKey(target.InstanceId, "", 0)
		if err != nil {
			return nil, err
		}
		if err := dst.addMember(provider.LoadBalancerBackendMember{Key: key, GroupID: group, Weight: target.Weight}); err != nil {
			return nil, err
		}
		for _, value := range target.PrivateIpAddresses {
			ip, err := netip.ParseAddr(value)
			if err != nil || ip.Zone() != "" {
				return nil, invalidLoadBalancerBackend()
			}
			ip = ip.Unmap()
			if prior, found := keys[ip]; found && prior != key {
				keys[ip] = ""
			} else {
				keys[ip] = key
			}
		}
	}
	return keys, nil
}

func (c *Client) tencentClassicLBHealth(ctx context.Context, region, id, group string, listeners []provider.LoadBalancerListener,
	keys map[netip.Addr]string, dst *loadBalancerBackendRead) error {
	if err := dst.request(); err != nil {
		return err
	}
	body, err := json.Marshal(struct{ LoadBalancerId string }{id})
	if err != nil {
		return err
	}
	var result struct {
		HealthList []struct {
			IP, Protocol       string
			Port, ListenerPort int
			HealthStatus       *int
		}
	}
	if err := c.tencent(ctx, "clb", region, "DescribeClassicalLBHealthStatus", "2018-03-17", body, &result); err != nil {
		return err
	}
	if result.HealthList == nil {
		return invalidLoadBalancerBackend()
	}
	byPort := loadBalancerListenersByPort(listeners)
	for _, target := range result.HealthList {
		listener := byPort[newLoadBalancerListenerPort(target.ListenerPort, target.Protocol)]
		if listener == "" {
			return invalidLoadBalancerBackend()
		}
		ip, err := netip.ParseAddr(target.IP)
		if err != nil || ip.Zone() != "" {
			return invalidLoadBalancerBackend()
		}
		key := keys[ip.Unmap()]
		if key == "" {
			key, err = dst.endpointKey("", target.IP, target.Port)
			if err != nil {
				return err
			}
		}
		status := "unknown"
		if target.HealthStatus != nil {
			switch *target.HealthStatus {
			case 0:
				status = "unhealthy"
			case 1:
				status = "healthy"
			default:
				return invalidLoadBalancerBackend()
			}
		}
		if err := dst.addCheck(provider.LoadBalancerBackendCheck{Key: key, ListenerID: listener, GroupID: group, Port: target.Port,
			ListenerPort: target.ListenerPort, Status: status}); err != nil {
			return err
		}
	}
	return nil
}
