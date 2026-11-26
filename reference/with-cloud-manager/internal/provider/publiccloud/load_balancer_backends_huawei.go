package publiccloud

import (
	"context"
	"net/url"
	"slices"
	"strconv"

	"with-cloud-manager/internal/provider"
)

type huaweiELBMember struct {
	Address        string `json:"address"`
	Subnet         string `json:"subnet_cidr_id"`
	ID             string
	ProjectID      string `json:"project_id"`
	Port           int    `json:"protocol_port"`
	Weight         *int
	Status         string `json:"operating_status"`
	ListenerStatus []struct {
		ListenerID string `json:"listener_id"`
		Status     string `json:"operating_status"`
	} `json:"status"`
}

func huaweiWeightedAlgorithm(algorithm string) bool {
	return algorithm == "ROUND_ROBIN" || algorithm == "LEAST_CONNECTIONS"
}

func (c *Client) huaweiLoadBalancerMemberRows(ctx context.Context, region, poolID string, dst *loadBalancerBackendRead) ([]huaweiELBMember, error) {
	return readLoadBalancerPages(ctx, func(ctx context.Context, marker string) ([]huaweiELBMember, string, error) {
		if err := dst.request(); err != nil {
			return nil, "", err
		}
		query := url.Values{"limit": {strconv.Itoa(loadBalancerPageSize)}}
		if marker != "" {
			query.Set("marker", marker)
		}
		endpoint := url.URL{Scheme: "https", Host: "elb." + region + ".myhuaweicloud.com",
			Path: "/v3/" + c.profile.ProjectID + "/elb/pools/" + poolID + "/members", RawQuery: query.Encode()}
		var result struct {
			Members []huaweiELBMember
			Page    *huaweiPageInfo `json:"page_info"`
		}
		if err := c.readHuawei(ctx, endpoint, &result); err != nil {
			return nil, "", err
		}
		rows := result.Members
		if result.Page == nil || result.Page.Count == nil || *result.Page.Count != len(rows) ||
			(result.Page.Next != "" && (len(rows) == 0 || rows[len(rows)-1].ID != result.Page.Next)) {
			return nil, "", invalidLoadBalancerBackend()
		}
		for _, row := range rows {
			if row.ProjectID != c.profile.ProjectID {
				return nil, "", &provider.Failure{Kind: provider.Unauthorized}
			}
			if !huaweiResourceID.MatchString(row.ID) || row.ID == marker {
				return nil, "", invalidLoadBalancerBackend()
			}
		}
		return rows, result.Page.Next, nil
	})
}

func (c *Client) huaweiLoadBalancerMembers(ctx context.Context, region, parent string, pool huaweiELBPool,
	listeners []provider.LoadBalancerListener, positions map[string]int, dst *loadBalancerBackendRead) error {
	rows, err := c.huaweiLoadBalancerMemberRows(ctx, region, pool.ID, dst)
	if err != nil {
		return err
	}
	for _, row := range rows {
		key, err := dst.endpointKey(row.ID, "", row.Port)
		if err != nil {
			return err
		}
		if err := dst.addMember(provider.LoadBalancerBackendMember{Key: key, GroupID: pool.ID, Port: row.Port, Weight: row.Weight, WeightEditable: dst.stable && row.Port > 0 && row.Weight != nil && huaweiWeightedAlgorithm(pool.Algorithm)}); err != nil {
			return err
		}
		canUseGroupStatus := len(row.ListenerStatus) == 0 && row.Status != "" &&
			len(pool.LoadBalancers) == 1 && pool.LoadBalancers[0].ID == parent && len(pool.Listeners) == 1
		if canUseGroupStatus {
			id := pool.Listeners[0].ID
			if index, ok := positions[id]; ok && slices.Contains(listeners[index].TargetGroupIDs, pool.ID) {
				if err := dst.addCheck(provider.LoadBalancerBackendCheck{
					Key: key, ListenerID: id, GroupID: pool.ID, Port: row.Port, Status: backendHealth(row.Status),
				}); err != nil {
					return err
				}
			}
		}
		for _, status := range row.ListenerStatus {
			if !huaweiResourceID.MatchString(status.ListenerID) {
				return invalidLoadBalancerBackend()
			}
			index, local := positions[status.ListenerID]
			if !local {
				continue
			}
			if !slices.Contains(listeners[index].TargetGroupIDs, pool.ID) {
				return invalidLoadBalancerBackend()
			}
			if err := dst.addCheck(provider.LoadBalancerBackendCheck{
				Key: key, ListenerID: status.ListenerID, GroupID: pool.ID, Port: row.Port, Status: backendHealth(status.Status),
			}); err != nil {
				return err
			}
		}
	}
	return nil
}
