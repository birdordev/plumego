package publiccloud

import (
	"context"
	"net/url"
	"slices"
	"strconv"

	"with-cloud-manager/internal/provider"
)

type huaweiELBResourceRef struct{ ID string }

type huaweiELBResource struct {
	ID, Name, Protocol string
	ProjectID          string                 `json:"project_id"`
	LoadBalancers      []huaweiELBResourceRef `json:"loadbalancers"`
}

type huaweiELBListener struct {
	huaweiELBResource
	Port        int    `json:"protocol_port"`
	DefaultPool string `json:"default_pool_id"`
}

type huaweiELBPool struct {
	huaweiELBResource
	Algorithm string                 `json:"lb_algorithm"`
	Listeners []huaweiELBResourceRef `json:"listeners"`
}

func (c *Client) huaweiLoadBalancerConfiguration(ctx context.Context, region, id string, backends *loadBalancerBackendRead) (provider.LoadBalancerConfiguration, error) {
	listeners, err := huaweiELBPages(ctx, c, region, id, "listeners", func(result huaweiELBPage) []huaweiELBListener { return result.Listeners })
	if err != nil {
		return provider.LoadBalancerConfiguration{}, err
	}
	cfg := provider.LoadBalancerConfiguration{Listeners: make([]provider.LoadBalancerListener, 0, len(listeners))}
	positions := make(map[string]int, len(listeners))
	directGroups := make(map[string]bool)
	for _, row := range listeners {
		if !containsHuaweiELBResource(row.LoadBalancers, id) {
			return provider.LoadBalancerConfiguration{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		listener := provider.LoadBalancerListener{ID: row.ID, Name: row.Name, Protocol: row.Protocol, Port: row.Port}
		if row.DefaultPool != "" {
			listener.TargetGroupIDs = []string{row.DefaultPool}
			directGroups[row.DefaultPool] = true
		}
		positions[row.ID] = len(cfg.Listeners)
		cfg.Listeners = append(cfg.Listeners, listener)
	}
	pools, err := huaweiELBPages(ctx, c, region, id, "pools", func(result huaweiELBPage) []huaweiELBPool { return result.Pools })
	if err != nil {
		return provider.LoadBalancerConfiguration{}, err
	}
	for _, row := range pools {
		associated := directGroups[row.ID] || containsHuaweiELBResource(row.LoadBalancers, id)
		for _, ref := range row.Listeners {
			if i, ok := positions[ref.ID]; ok {
				associated = true
				cfg.Listeners[i].TargetGroupIDs = append(cfg.Listeners[i].TargetGroupIDs, row.ID)
			}
		}
		if !associated {
			return provider.LoadBalancerConfiguration{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		cfg.TargetGroups = append(cfg.TargetGroups, provider.LoadBalancerTargetGroup{ID: row.ID, Name: row.Name, Protocol: row.Protocol, Algorithm: row.Algorithm, Exclusive: slices.Equal(row.LoadBalancers, []huaweiELBResourceRef{{ID: id}})})
	}
	if backends != nil {
		for _, row := range pools {
			if err := c.huaweiLoadBalancerMembers(ctx, region, id, row, cfg.Listeners, positions, backends); err != nil {
				return provider.LoadBalancerConfiguration{}, err
			}
		}
	}
	return cfg, nil
}

type huaweiELBPage struct {
	Listeners []huaweiELBListener
	Pools     []huaweiELBPool
	Page      *huaweiPageInfo `json:"page_info"`
}

type huaweiELBRow interface {
	elbResource() huaweiELBResource
}

func (row huaweiELBResource) elbResource() huaweiELBResource { return row }

func huaweiELBPages[T huaweiELBRow](ctx context.Context, c *Client, region, id, leaf string, selectRows func(huaweiELBPage) []T) ([]T, error) {
	return readLoadBalancerPages(ctx, func(ctx context.Context, cursor string) ([]T, string, error) {
		query := url.Values{"loadbalancer_id": {id}, "limit": {strconv.Itoa(loadBalancerPageSize)}}
		if cursor != "" {
			query.Set("marker", cursor)
		}
		endpoint := url.URL{Scheme: "https", Host: "elb." + region + ".myhuaweicloud.com",
			Path: "/v3/" + c.profile.ProjectID + "/elb/" + leaf, RawQuery: query.Encode()}
		var result huaweiELBPage
		if err := c.readHuawei(ctx, endpoint, &result); err != nil {
			return nil, "", err
		}
		rows := selectRows(result)
		if result.Page == nil || result.Page.Count == nil || *result.Page.Count != len(rows) {
			return nil, "", &provider.Failure{Kind: provider.InvalidResponse}
		}
		for _, row := range rows {
			item := row.elbResource()
			if item.ProjectID != c.profile.ProjectID {
				return nil, "", &provider.Failure{Kind: provider.Unauthorized}
			}
			if !huaweiResourceID.MatchString(item.ID) || item.ID == cursor {
				return nil, "", &provider.Failure{Kind: provider.InvalidResponse}
			}
		}
		if result.Page.Next != "" && (len(rows) == 0 || rows[len(rows)-1].elbResource().ID != result.Page.Next) {
			return nil, "", &provider.Failure{Kind: provider.InvalidResponse}
		}
		return rows, result.Page.Next, nil
	})
}

func containsHuaweiELBResource(values []huaweiELBResourceRef, id string) bool {
	return slices.ContainsFunc(values, func(ref huaweiELBResourceRef) bool { return ref.ID == id })
}
