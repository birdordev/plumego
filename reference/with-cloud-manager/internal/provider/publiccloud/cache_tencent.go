package publiccloud

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"with-cloud-manager/internal/provider"
)

type tencentCacheRow struct {
	InstanceId, InstanceName, Engine, CurrentRedisVersion, UniqVpcId string
	Status                                                           *int
	Type, RegionId                                                   int
	Size                                                             float64
}

func (row tencentCacheRow) engineVersion() (string, string) {
	engine := strings.ToLower(strings.TrimSpace(row.Engine))
	version := row.CurrentRedisVersion
	switch row.Type {
	case 2, 5:
		engine, version = "redis", firstNonempty(version, "2.8")
	case 3, 4:
		engine, version = "ckv", firstNonempty(version, "3.2")
	case 6, 7:
		engine, version = "redis", firstNonempty(version, "4.0")
	case 8, 9:
		engine, version = "redis", firstNonempty(version, "5.0")
	case 15, 16:
		engine, version = "redis", firstNonempty(version, "6.2")
	case 17, 18:
		engine, version = "redis", firstNonempty(version, "7.0")
	case 200:
		return "memcached", "1.6"
	}
	switch engine {
	case "redis community edition", "社区版redis":
		engine = "redis"
	case "tencent cloud ckv", "腾讯云ckv":
		engine = "ckv"
	}
	return engine, version
}

func firstNonempty(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func (c *Client) tencentCaches(ctx context.Context, region string, offset int, id string) ([]provider.Object, *int, error) {
	body, err := json.Marshal(struct {
		Offset, Limit int
		InstanceId    string `json:",omitempty"`
		OrderBy       string
	}{Offset: offset, Limit: regionalInstancePageSize, InstanceId: id, OrderBy: "createtime"})
	if err != nil {
		return nil, nil, err
	}
	var result struct {
		TotalCount  *int
		InstanceSet []tencentCacheRow
	}
	if err := c.tencent(ctx, "redis", region, "DescribeInstances", "2018-04-12", body, &result); err != nil {
		return nil, nil, err
	}
	expectedRegion := tencentCacheRegionID(region)
	return projectRegionalInstances(result.InstanceSet, result.TotalCount, func(row tencentCacheRow) (provider.Object, error) {
		if row.Status == nil || (expectedRegion != 0 && row.RegionId != expectedRegion) {
			return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		engine, version := row.engineVersion()
		return cacheObject(row.InstanceId, row.InstanceName, strconv.Itoa(*row.Status),
			provider.TencentRedis, engine, version, row.UniqVpcId, row.Size)
	})
}

// tencentCacheRegionID 对照 Redis API 已公布的地域编号。其他地域使用请求的地域作用域。
func tencentCacheRegionID(region string) int {
	return map[string]int{
		"ap-guangzhou": 1, "ap-shanghai": 4, "ap-hongkong": 5, "ap-shanghai-fsi": 7,
		"ap-beijing": 8, "ap-singapore": 9, "ap-shenzhen-fsi": 11, "na-siliconvalley": 15,
		"ap-chengdu": 16, "eu-frankfurt": 17, "ap-seoul": 18, "ap-chongqing": 19,
		"na-ashburn": 22, "ap-bangkok": 23, "ap-tokyo": 25,
	}[region]
}
