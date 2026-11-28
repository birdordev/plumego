package publiccloud

import (
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strconv"

	"with-cloud-manager/internal/provider"
)

const maxInstanceResizeRows = 4000

func (c *Client) InstanceResizeOptions(ctx context.Context, scope provider.Scope, id, current string) (provider.InstanceResizeOptions, error) {
	if err := c.checkInstanceScope(scope, id); err != nil {
		return provider.InstanceResizeOptions{}, err
	}
	if !provider.ValidInstanceType(current) || len(c.profile.InstanceResizeSizes) == 0 {
		return provider.InstanceResizeOptions{}, &provider.Failure{Kind: provider.Unsupported}
	}
	zone, err := c.checkInstanceResize(ctx, scope.Region, id, current)
	if err != nil {
		return provider.InstanceResizeOptions{}, err
	}
	if !resourceID.MatchString(zone) {
		return provider.InstanceResizeOptions{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	var available []string
	switch c.name {
	case provider.TencentCVM:
		available, err = c.tencentResizeTypes(ctx, scope.Region, id, zone)
	case provider.AlibabaECS:
		available, err = c.alibabaResizeTypes(ctx, scope.Region, id, zone)
	case provider.HuaweiECS:
		available, err = c.huaweiResizeTypes(ctx, scope.Region, id)
	}
	if err != nil {
		return provider.InstanceResizeOptions{}, err
	}
	allowed := make([]string, 0, len(c.profile.InstanceResizeSizes))
	for _, size := range available {
		if size != current && slices.Contains(c.profile.InstanceResizeSizes, size) {
			allowed = append(allowed, size)
		}
	}
	slices.Sort(allowed)
	return provider.InstanceResizeOptions{Zone: zone, Types: slices.Compact(allowed)}, nil
}

func (c *Client) tencentResizeTypes(ctx context.Context, region, id, zone string) ([]string, error) {
	body, err := json.Marshal(struct {
		InstanceIds []string
		Filters     []struct {
			Name   string
			Values []string
		}
	}{
		InstanceIds: []string{id},
		Filters: []struct {
			Name   string
			Values []string
		}{{Name: "status", Values: []string{"SELL"}}},
	})
	if err != nil {
		return nil, err
	}
	var response struct {
		TotalCount                  *int
		InstanceTypeConfigStatusSet []struct {
			Status             string
			InstanceTypeConfig struct{ InstanceType, Zone string }
		}
	}
	if err := c.tencent(ctx, "cvm", region, "DescribeInstancesModification", "2017-03-12", body, &response); err != nil {
		return nil, err
	}
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	rows := response.InstanceTypeConfigStatusSet
	if response.TotalCount == nil || rows == nil || *response.TotalCount != len(rows) || len(rows) > maxInstanceResizeRows {
		return nil, invalid
	}
	result := make([]string, 0, len(rows))
	for _, row := range rows {
		item := row.InstanceTypeConfig
		if !provider.ValidInstanceType(item.InstanceType) || !resourceID.MatchString(item.Zone) ||
			(row.Status != "SELL" && row.Status != "UNAVAILABLE") {
			return nil, invalid
		}
		if row.Status == "SELL" && item.Zone == zone {
			result = append(result, item.InstanceType)
		}
	}
	return result, nil
}

func (c *Client) alibabaResizeTypes(ctx context.Context, region, id, zone string) ([]string, error) {
	query := url.Values{"RegionId": {region}, "ResourceId": {id}, "DestinationResource": {"InstanceType"},
		"OperationType": {"Upgrade"}, "MigrateAcrossZone": {"false"}}
	var response struct {
		Code           string
		AvailableZones struct {
			AvailableZone []struct {
				ZoneId, RegionId, Status string
				AvailableResources       struct {
					AvailableResource []struct {
						Type               string
						SupportedResources struct {
							SupportedResource []struct{ Status, Value string }
						}
					}
				}
			}
		}
	}
	if err := c.alibaba(ctx, "ecs."+region+".aliyuncs.com", "DescribeResourcesModification", "2014-05-26", query, &response); err != nil {
		return nil, err
	}
	if response.Code != "" {
		return nil, cloudFailure(response.Code)
	}
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	zones := response.AvailableZones.AvailableZone
	if zones == nil || len(zones) > 128 {
		return nil, invalid
	}
	result := make([]string, 0)
	matched := false
	for _, item := range zones {
		if item.ZoneId != zone {
			continue
		}
		if matched || item.RegionId != region || (item.Status != "Available" && item.Status != "SoldOut") {
			return nil, invalid
		}
		matched = true
		if item.Status == "SoldOut" {
			continue
		}
		for _, resource := range item.AvailableResources.AvailableResource {
			if resource.Type != "InstanceType" {
				continue
			}
			rows := resource.SupportedResources.SupportedResource
			if len(rows) > maxInstanceResizeRows {
				return nil, invalid
			}
			for _, row := range rows {
				if !provider.ValidInstanceType(row.Value) || (row.Status != "Available" && row.Status != "SoldOut") {
					return nil, invalid
				}
				if row.Status == "Available" {
					result = append(result, row.Value)
					if len(result) > maxInstanceResizeRows {
						return nil, invalid
					}
				}
			}
		}
	}
	return result, nil
}

func (c *Client) huaweiResizeTypes(ctx context.Context, region, id string) ([]string, error) {
	const limit = 100
	endpoint := url.URL{Scheme: "https", Host: "ecs." + region + ".myhuaweicloud.com",
		Path: "/v1/" + c.profile.ProjectID + "/cloudservers/resize_flavors"}
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	query := url.Values{"instance_uuid": {id}, "limit": {strconv.Itoa(limit)}, "sort_key": {"flavorid"}, "sort_dir": {"asc"}}
	result := make([]string, 0)
	seen := make(map[string]struct{})
	for range maxInstanceResizeRows/limit + 1 {
		endpoint.RawQuery = query.Encode()
		var response struct {
			Flavors []struct {
				ID       string
				Disabled bool `json:"OS-FLV-DISABLED:disabled"`
			}
		}
		if err := c.readHuawei(ctx, endpoint, &response); err != nil {
			return nil, err
		}
		if response.Flavors == nil || len(response.Flavors) > limit || len(seen)+len(response.Flavors) > maxInstanceResizeRows {
			return nil, invalid
		}
		for _, row := range response.Flavors {
			if _, duplicate := seen[row.ID]; duplicate || !provider.ValidInstanceType(row.ID) {
				return nil, invalid
			}
			seen[row.ID] = struct{}{}
			if !row.Disabled {
				result = append(result, row.ID)
			}
		}
		if len(response.Flavors) < limit {
			return result, nil
		}
		query.Set("marker", response.Flavors[limit-1].ID)
	}
	return nil, invalid
}
