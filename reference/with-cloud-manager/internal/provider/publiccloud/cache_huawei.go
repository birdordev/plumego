package publiccloud

import (
	"context"
	"net/url"
	"strconv"
	"strings"

	"with-cloud-manager/internal/provider"
)

type huaweiCacheRow struct {
	ID                           string `json:"instance_id"`
	Version                      string `json:"engine_version"`
	Network                      string `json:"vpc_id"`
	Project                      string `json:"project_id"`
	Memory                       int64  `json:"max_memory"`
	Name, Status, Engine, Region string
}

func (c *Client) huaweiCaches(ctx context.Context, region string, offset int, id string) ([]provider.Object, *int, error) {
	query := url.Values{
		"offset": {strconv.Itoa(offset)}, "limit": {strconv.Itoa(regionalInstancePageSize)},
		"include_failure": {"true"},
	}
	if id != "" {
		query.Set("instance_id", id)
	}
	endpoint := url.URL{
		Scheme: "https", Host: "dcs." + region + ".myhuaweicloud.com",
		Path: "/v2/" + c.profile.ProjectID + "/instances", RawQuery: query.Encode(),
	}
	var result struct {
		Instances []huaweiCacheRow
		Count     *int `json:"instance_num"`
	}
	if err := c.readHuawei(ctx, endpoint, &result); err != nil {
		return nil, nil, err
	}
	return projectRegionalInstances(result.Instances, result.Count, func(row huaweiCacheRow) (provider.Object, error) {
		if row.Project != "" && row.Project != c.profile.ProjectID {
			return provider.Object{}, &provider.Failure{Kind: provider.Unauthorized}
		}
		if (row.Region != "" && row.Region != region) || !cacheID(provider.HuaweiECS, row.ID) {
			return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		return cacheObject(row.ID, row.Name, row.Status, provider.HuaweiDCS,
			strings.ToLower(row.Engine), row.Version, row.Network, float64(row.Memory))
	})
}

func (c *Client) discoverHuaweiCaches(ctx context.Context, region, cursor string) (provider.Page, error) {
	if strings.ContainsRune(cursor, ':') {
		return discoverRegionalInstances(ctx, region, cursor, regionalInstancePageSize, []regionalInstanceReader{c.huaweiCaches})
	}
	offset, err := pageNumber(cursor, 0)
	if err != nil || offset > maxRegionalInstances || offset%regionalInstancePageSize != 0 {
		return provider.Page{}, &provider.Failure{Kind: provider.ExpiredCursor}
	}
	rows, count, err := c.huaweiCaches(ctx, region, offset, "")
	if err != nil {
		return provider.Page{}, err
	}
	next, err := huaweiCacheNext(offset, count, len(rows))
	if err != nil {
		return provider.Page{}, err
	}
	return provider.Page{Objects: rows, Next: next}, nil
}

func huaweiCacheNext(offset int, count *int, size int) (string, error) {
	if count == nil || *count < size || *count > maxRegionalInstances || offset+size > maxRegionalInstances {
		return "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	// 厂商未区分满页条数和总量相等的情况，满页时补读边界确认终点。
	end := offset + size
	if *count == size {
		if size == regionalInstancePageSize {
			return strconv.Itoa(end), nil
		}
		return "", nil
	}
	if end == *count {
		return "", nil
	}
	if size == regionalInstancePageSize && end < *count {
		return "0:" + strconv.Itoa(end) + ":" + strconv.Itoa(*count), nil
	}
	return "", &provider.Failure{Kind: provider.ExpiredCursor}
}
