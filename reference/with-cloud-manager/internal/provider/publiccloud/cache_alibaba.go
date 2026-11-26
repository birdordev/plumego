package publiccloud

import (
	"context"
	"net/url"
	"strconv"
	"strings"

	"with-cloud-manager/internal/provider"
)

type alibabaCacheRow struct {
	InstanceId, InstanceName, InstanceStatus, InstanceType, EngineVersion, RegionId, VpcId string
	Capacity                                                                               int64
}

func (c *Client) alibabaCaches(ctx context.Context, region string, offset int, id string) ([]provider.Object, *int, error) {
	number := offset/regionalInstancePageSize + 1
	query := url.Values{
		"RegionId": {region}, "PageNumber": {strconv.Itoa(number)}, "PageSize": {strconv.Itoa(regionalInstancePageSize)},
	}
	if id != "" {
		query.Set("InstanceIds", id)
	}
	var result struct {
		Instances                        struct{ KVStoreInstance []alibabaCacheRow }
		PageNumber, PageSize, TotalCount *int
	}
	if err := c.alibaba(ctx, alibabaCacheEndpoint(region), "DescribeInstances", "2015-01-01", query, &result); err != nil {
		return nil, nil, err
	}
	if result.PageNumber == nil || *result.PageNumber != number || result.PageSize == nil || *result.PageSize != regionalInstancePageSize {
		return nil, nil, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return projectRegionalInstances(result.Instances.KVStoreInstance, result.TotalCount, func(row alibabaCacheRow) (provider.Object, error) {
		if row.RegionId != region {
			return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		engine := strings.ToLower(row.InstanceType)
		switch engine {
		case "tair":
			engine = "redis"
		case "memcache":
			engine = "memcached"
		}
		return cacheObject(row.InstanceId, row.InstanceName, row.InstanceStatus, provider.AlibabaTair,
			engine, row.EngineVersion, row.VpcId, float64(row.Capacity))
	})
}

func alibabaCacheEndpoint(region string) string {
	if region == "cn-hangzhou" || region == "cn-hangzhou-finance" {
		return "r-kvstore.aliyuncs.com"
	}
	return "r-kvstore." + region + ".aliyuncs.com"
}
