package publiccloud

import (
	"context"
	"encoding/json"
	"net/url"
	"slices"

	"with-cloud-manager/internal/provider"
)

func (c *Client) checkVolumeSnapshotDependencies(ctx context.Context, region, id string) error {
	switch c.name {
	case provider.TencentCVM:
		body, err := json.Marshal(struct {
			Filters []tencentNetworkFilter
			Limit   int
		}{[]tencentNetworkFilter{{"disk-id", []string{id}}}, 1})
		if err != nil {
			return err
		}
		var result struct {
			TotalCount  *int
			SnapshotSet []json.RawMessage
		}
		if err := c.tencent(ctx, "cbs", region, "DescribeSnapshots", "2017-03-12", body, &result); err != nil {
			return err
		}
		if result.SnapshotSet == nil || result.TotalCount == nil || *result.TotalCount < len(result.SnapshotSet) {
			return &provider.Failure{Kind: provider.InvalidResponse}
		}
		return requireEmptySnapshotList(*result.TotalCount, false)
	case provider.AlibabaECS:
		query := url.Values{"RegionId": {region}, "DiskId": {id}, "MaxResults": {"10"}}
		var result struct {
			Code, NextToken string
			Snapshots       struct{ Snapshot []json.RawMessage }
		}
		if err := c.alibaba(ctx, "ecs."+region+".aliyuncs.com", "DescribeSnapshots", "2014-05-26", query, &result); err != nil {
			return err
		}
		if result.Code != "" {
			return cloudFailure(result.Code)
		}
		if result.Snapshots.Snapshot == nil {
			return &provider.Failure{Kind: provider.InvalidResponse}
		}
		return requireEmptySnapshotList(len(result.Snapshots.Snapshot), result.NextToken != "")
	default:
		for _, class := range []string{"flash", "standard"} {
			if err := c.checkHuaweiVolumeSnapshots(ctx, region, id, class); err != nil {
				return err
			}
		}
		return nil
	}
}

func (c *Client) checkHuaweiVolumeSnapshots(ctx context.Context, region, id, class string) error {
	endpoint := huaweiEVS(region, c.huaweiSnapshotCollection(class)+"/detail")
	query := url.Values{"volume_id": {id}, "limit": {"1"}}
	if class == "flash" {
		query.Set("enterprise_project_id", "all_granted_eps")
	}
	endpoint.RawQuery = query.Encode()
	var result struct {
		Count     *int
		Snapshots []json.RawMessage
		Links     []struct{ Rel string } `json:"snapshots_links"`
	}
	if err := c.readHuawei(ctx, endpoint, &result); err != nil {
		return err
	}
	if result.Snapshots == nil || (class == "flash" && result.Count == nil) || (result.Count != nil && *result.Count < len(result.Snapshots)) {
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	if result.Count != nil && *result.Count > 0 {
		return &provider.Failure{Kind: provider.DeletionBlocked}
	}
	return requireEmptySnapshotList(len(result.Snapshots), slices.ContainsFunc(result.Links, func(link struct{ Rel string }) bool {
		return link.Rel == "next"
	}))
}

func requireEmptySnapshotList(count int, hasMore bool) error {
	if count > 0 || hasMore {
		return &provider.Failure{Kind: provider.DeletionBlocked}
	}
	return nil
}
