package publiccloud

import (
	"context"
	"encoding/json"
	"slices"

	"with-cloud-manager/internal/provider"
)

type tencentLBGroupRef struct{ TargetGroupId string }

type tencentLBGroupRefs struct {
	TargetGroup     *tencentLBGroupRef
	TargetGroupList []tencentLBGroupRef
}

type tencentLBListener struct {
	ListenerId, ListenerName, Protocol string
	Port, ListenerPort, EndPort        int
	tencentLBGroupRefs
	Rules []tencentLBRule
}

type tencentLBRule struct {
	LocationId string
	tencentLBGroupRefs
}

func (refs tencentLBGroupRefs) hasGroups() bool {
	return refs.TargetGroup != nil || len(refs.TargetGroupList) != 0
}

func (refs tencentLBGroupRefs) ids(dst []string) []string {
	if refs.TargetGroup != nil {
		dst = append(dst, refs.TargetGroup.TargetGroupId)
	}
	for _, group := range refs.TargetGroupList {
		dst = append(dst, group.TargetGroupId)
	}
	return dst
}

func (c *Client) tencentLoadBalancerConfiguration(ctx context.Context, region, id string, backends *loadBalancerBackendRead) (provider.LoadBalancerConfiguration, error) {
	if !validLaunchID(id, "lb-") {
		return provider.LoadBalancerConfiguration{}, &provider.Failure{Kind: provider.Unsupported}
	}
	rows, next, err := c.tencentLoadBalancerRows(ctx, region, "", id)
	if err != nil {
		return provider.LoadBalancerConfiguration{}, err
	}
	if next != "" || len(rows) > 1 {
		return provider.LoadBalancerConfiguration{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	if len(rows) == 0 {
		return provider.LoadBalancerConfiguration{}, &provider.Failure{Kind: provider.Absent}
	}
	if rows[0].LoadBalancerId != id || rows[0].Forward == nil || (*rows[0].Forward != 0 && *rows[0].Forward != 1) {
		return provider.LoadBalancerConfiguration{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	classic := *rows[0].Forward == 0
	action := "DescribeListeners"
	defaultGroup := id + ".default"
	if classic {
		action = "DescribeClassicalLBListeners"
	}
	body, err := json.Marshal(struct{ LoadBalancerId string }{id})
	if err != nil {
		return provider.LoadBalancerConfiguration{}, err
	}
	var result struct {
		TotalCount *int
		Listeners  []tencentLBListener
	}
	if err := c.tencent(ctx, "clb", region, action, "2018-03-17", body, &result); err != nil {
		return provider.LoadBalancerConfiguration{}, err
	}
	if result.Listeners == nil || len(result.Listeners) > provider.MaxLoadBalancerComponents ||
		(!classic && (result.TotalCount == nil || *result.TotalCount != len(result.Listeners))) ||
		(result.TotalCount != nil && *result.TotalCount != len(result.Listeners)) {
		return provider.LoadBalancerConfiguration{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	cfg := provider.LoadBalancerConfiguration{Listeners: make([]provider.LoadBalancerListener, 0, len(result.Listeners))}
	linkCount := 0
	for _, row := range result.Listeners {
		if !validLaunchID(row.ListenerId, "lbl-") {
			return provider.LoadBalancerConfiguration{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		item := provider.LoadBalancerListener{ID: row.ListenerId, Name: row.ListenerName, Protocol: row.Protocol, Port: row.Port, EndPort: row.EndPort}
		if classic {
			item.Port = row.ListenerPort
			item.TargetGroupIDs = []string{defaultGroup}
		} else {
			item.TargetGroupIDs = row.ids(nil)
			for _, rule := range row.Rules {
				item.TargetGroupIDs = rule.ids(item.TargetGroupIDs)
			}
		}
		linkCount += len(item.TargetGroupIDs)
		if linkCount > 8*provider.MaxLoadBalancerComponents {
			return provider.LoadBalancerConfiguration{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		cfg.Listeners = append(cfg.Listeners, item)
	}
	if classic {
		cfg.TargetGroups = []provider.LoadBalancerTargetGroup{{ID: defaultGroup, Kind: provider.LoadBalancerDefaultGroup}}
		if backends == nil {
			return cfg, nil
		}
		keys, err := c.tencentClassicLBMembers(ctx, region, id, defaultGroup, backends)
		if err != nil {
			return provider.LoadBalancerConfiguration{}, err
		}
		if len(cfg.Listeners) != 0 {
			return cfg, c.tencentClassicLBHealth(ctx, region, id, defaultGroup, cfg.Listeners, keys, backends)
		}
		return cfg, nil
	}
	ids := configurationGroupIDs(cfg.Listeners)
	if len(ids)+len(cfg.Listeners) > provider.MaxLoadBalancerComponents {
		return provider.LoadBalancerConfiguration{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	for idBatch := range slices.Chunk(ids, loadBalancerPageSize) {
		groups, err := c.tencentLoadBalancerGroups(ctx, region, id, idBatch)
		if err != nil {
			return provider.LoadBalancerConfiguration{}, err
		}
		cfg.TargetGroups = append(cfg.TargetGroups, groups...)
	}
	if backends != nil && len(cfg.Listeners) != 0 {
		for _, group := range cfg.TargetGroups {
			if err := c.tencentLoadBalancerMembers(ctx, region, group.ID, group.AllPorts, backends); err != nil {
				return provider.LoadBalancerConfiguration{}, err
			}
		}
		if err := c.tencentLoadBalancerDirectMembers(ctx, region, id, &cfg, result.Listeners, backends); err != nil {
			return provider.LoadBalancerConfiguration{}, err
		}
		return cfg, c.tencentLoadBalancerHealth(ctx, region, id, cfg.Listeners, backends)
	}
	return cfg, nil
}

type tencentLBTargetGroup struct {
	VpcId                                                       string
	TargetGroupId, TargetGroupName, Protocol, ScheduleAlgorithm string
	FullListenSwitch                                            bool
	AssociatedRule                                              []struct{ LoadBalancerId string }
}

func (g tencentLBTargetGroup) exclusiveTo(parent string) bool {
	return len(g.AssociatedRule) != 0 && !slices.ContainsFunc(g.AssociatedRule, func(rule struct{ LoadBalancerId string }) bool {
		return rule.LoadBalancerId != parent
	})
}

func (c *Client) tencentLoadBalancerGroupRows(ctx context.Context, region string, ids []string) ([]tencentLBTargetGroup, error) {
	for _, id := range ids {
		if !validLaunchID(id, "lbtg-") {
			return nil, &provider.Failure{Kind: provider.InvalidResponse}
		}
	}
	body, err := json.Marshal(struct {
		TargetGroupIds []string
		Limit          int
	}{ids, len(ids)})
	if err != nil {
		return nil, err
	}
	var result struct {
		TotalCount     *int
		TargetGroupSet []tencentLBTargetGroup
	}
	if err := c.tencent(ctx, "clb", region, "DescribeTargetGroups", "2018-03-17", body, &result); err != nil {
		return nil, err
	}
	if result.TotalCount == nil || *result.TotalCount != len(ids) || len(result.TargetGroupSet) != len(ids) {
		return nil, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return result.TargetGroupSet, nil
}

func (c *Client) tencentLoadBalancerGroups(ctx context.Context, region, parent string, ids []string) ([]provider.LoadBalancerTargetGroup, error) {
	rows, err := c.tencentLoadBalancerGroupRows(ctx, region, ids)
	if err != nil {
		return nil, err
	}
	groups := make([]provider.LoadBalancerTargetGroup, 0, len(ids))
	for _, row := range rows {
		if _, found := slices.BinarySearch(ids, row.TargetGroupId); !found ||
			!slices.ContainsFunc(row.AssociatedRule, func(ref struct{ LoadBalancerId string }) bool { return ref.LoadBalancerId == parent }) {
			return nil, &provider.Failure{Kind: provider.InvalidResponse}
		}
		groups = append(groups, provider.LoadBalancerTargetGroup{ID: row.TargetGroupId, Name: row.TargetGroupName, Protocol: row.Protocol, Algorithm: row.ScheduleAlgorithm, Exclusive: row.exclusiveTo(parent), AllPorts: row.FullListenSwitch})
	}
	return groups, nil
}
