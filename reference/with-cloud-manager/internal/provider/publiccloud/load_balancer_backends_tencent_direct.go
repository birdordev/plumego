package publiccloud

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"with-cloud-manager/internal/provider"
)

type tencentDirectBackend struct {
	InstanceId         string
	PrivateIpAddresses []string
	Port               int
	Weight             *int
}

type tencentDirectListener struct {
	ListenerId, Protocol string
	Port, EndPort        int
	Targets              []tencentDirectBackend
	Rules                []struct {
		LocationId string
		Targets    []tencentDirectBackend
	}
}

func (c *Client) tencentLoadBalancerDirectMembers(ctx context.Context, region, id string, cfg *provider.LoadBalancerConfiguration,
	listeners []tencentLBListener, dst *loadBalancerBackendRead) error {
	positions := make(map[string]int, len(listeners))
	ids := make([]string, 0, len(listeners))
	for i, listener := range listeners {
		if _, duplicate := positions[listener.ListenerId]; duplicate {
			return invalidLoadBalancerBackend()
		}
		positions[listener.ListenerId] = i
		directRule := slices.ContainsFunc(listener.Rules, func(rule tencentLBRule) bool { return !rule.hasGroups() })
		if directRule || (len(listener.Rules) == 0 && !listener.hasGroups()) {
			ids = append(ids, listener.ListenerId)
		}
	}
	for batch := range slices.Chunk(ids, 20) {
		if err := dst.request(); err != nil {
			return err
		}
		rows, err := c.tencentDirectListeners(ctx, region, id, batch)
		if err != nil {
			return err
		}
		seen := make(map[string]bool, len(batch))
		for _, row := range rows {
			if seen[row.ListenerId] || !slices.Contains(batch, row.ListenerId) {
				return invalidLoadBalancerBackend()
			}
			seen[row.ListenerId] = true
			i := positions[row.ListenerId]
			if err := projectTencentDirectListener(cfg, i, listeners[i], row, dst); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *Client) tencentDirectListeners(ctx context.Context, region, id string, listenerIDs []string) ([]tencentDirectListener, error) {
	body, err := json.Marshal(struct {
		LoadBalancerId string
		ListenerIds    []string
	}{id, listenerIDs})
	if err != nil {
		return nil, err
	}
	var result struct{ Listeners []tencentDirectListener }
	if err := c.tencent(ctx, "clb", region, "DescribeTargets", "2018-03-17", body, &result); err != nil {
		return nil, err
	}
	if result.Listeners == nil || len(result.Listeners) > len(listenerIDs) {
		return nil, invalidLoadBalancerBackend()
	}
	return result.Listeners, nil
}

func projectTencentDirectListener(cfg *provider.LoadBalancerConfiguration, index int, source tencentLBListener, row tencentDirectListener,
	dst *loadBalancerBackendRead) error {
	if !strings.EqualFold(row.Protocol, source.Protocol) || row.Port != source.Port || row.EndPort != source.EndPort {
		return invalidLoadBalancerBackend()
	}
	if !source.hasGroups() {
		if err := addTencentDirectBackendGroup(cfg, index, "", row.Targets, dst); err != nil {
			return err
		}
	}
	knownRules := make(map[string]bool, len(source.Rules))
	for _, rule := range source.Rules {
		if !validLaunchID(rule.LocationId, "loc-") {
			return invalidLoadBalancerBackend()
		}
		if _, duplicate := knownRules[rule.LocationId]; duplicate {
			return invalidLoadBalancerBackend()
		}
		knownRules[rule.LocationId] = rule.hasGroups()
	}
	for _, rule := range row.Rules {
		grouped, known := knownRules[rule.LocationId]
		if !known {
			return invalidLoadBalancerBackend()
		}
		delete(knownRules, rule.LocationId)
		if !grouped {
			if err := addTencentDirectBackendGroup(cfg, index, rule.LocationId, rule.Targets, dst); err != nil {
				return err
			}
		}
	}
	return nil
}

func addTencentDirectBackendGroup(cfg *provider.LoadBalancerConfiguration, listener int, rule string, targets []tencentDirectBackend,
	dst *loadBalancerBackendRead) error {
	if len(targets) == 0 {
		return nil
	}
	if len(cfg.TargetGroups)+len(cfg.Listeners) >= provider.MaxLoadBalancerComponents {
		return invalidLoadBalancerBackend()
	}
	listenerID := cfg.Listeners[listener].ID
	id := listenerID + ".direct"
	if rule != "" {
		id = listenerID + "." + rule + ".direct"
	}
	for _, target := range targets {
		address := ""
		if len(target.PrivateIpAddresses) == 1 {
			address = target.PrivateIpAddresses[0]
		}
		key, err := dst.endpointKey(target.InstanceId, address, target.Port)
		if err != nil {
			return err
		}
		if err := dst.addMember(provider.LoadBalancerBackendMember{Key: key, GroupID: id, Port: target.Port, Weight: target.Weight}); err != nil {
			return err
		}
	}
	cfg.TargetGroups = append(cfg.TargetGroups, provider.LoadBalancerTargetGroup{ID: id, Kind: provider.LoadBalancerDirectGroup})
	cfg.Listeners[listener].TargetGroupIDs = append(cfg.Listeners[listener].TargetGroupIDs, id)
	return nil
}
