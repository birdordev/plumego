package publiccloud

import (
	"context"
	"net/url"
	"strconv"

	"with-cloud-manager/internal/provider"
)

type alibabaRDSRow struct {
	DBInstanceId, DBInstanceDescription, DBInstanceStatus, Engine, EngineVersion, RegionId, VpcId string
}

func (c *Client) alibabaRDS(ctx context.Context, region string, offset int, id string) ([]provider.Object, *int, error) {
	number := offset/regionalInstancePageSize + 1
	query := url.Values{
		"RegionId": {region}, "PageNumber": {strconv.Itoa(number)}, "PageSize": {strconv.Itoa(regionalInstancePageSize)},
	}
	if id != "" {
		query.Set("DBInstanceId", id)
	}
	var result struct {
		Items                       struct{ DBInstance []alibabaRDSRow }
		PageNumber, PageRecordCount *int
		TotalRecordCount            *int
	}
	if err := c.alibaba(ctx, alibabaRDSEndpoint(region), "DescribeDBInstances", "2014-08-15", query, &result); err != nil {
		return nil, nil, err
	}
	if result.PageNumber == nil || *result.PageNumber != number || result.PageRecordCount == nil ||
		*result.PageRecordCount != len(result.Items.DBInstance) {
		return nil, nil, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return projectRegionalInstances(result.Items.DBInstance, result.TotalRecordCount, func(row alibabaRDSRow) (provider.Object, error) {
		if row.RegionId != region {
			return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		return databaseObject(row.DBInstanceId, row.DBInstanceDescription, row.DBInstanceStatus, provider.AlibabaRDS, row.Engine, row.EngineVersion, row.VpcId)
	})
}

func alibabaRDSEndpoint(region string) string {
	if region == "cn-hangzhou" || region == "cn-hangzhou-finance" {
		return "rds.aliyuncs.com"
	}
	return "rds." + region + ".aliyuncs.com"
}

type huaweiRDSRow struct {
	ID, Name, Status, Region string
	Network                  string `json:"vpc_id"`
	Project                  string `json:"project_id"`
	Datastore                struct{ Type, Version string }
}

func (c *Client) huaweiRDS(ctx context.Context, region string, offset int, id string) ([]provider.Object, *int, error) {
	query := url.Values{"limit": {strconv.Itoa(regionalInstancePageSize)}, "offset": {strconv.Itoa(offset)}}
	if id != "" {
		query.Set("id", id)
	}
	endpoint := url.URL{
		Scheme: "https", Host: "rds." + region + ".myhuaweicloud.com",
		Path: "/v3/" + c.profile.ProjectID + "/instances", RawQuery: query.Encode(),
	}
	var result struct {
		Instances []huaweiRDSRow
		Total     *int `json:"total_count"`
	}
	if err := c.readHuawei(ctx, endpoint, &result); err != nil {
		return nil, nil, err
	}
	return projectRegionalInstances(result.Instances, result.Total, func(row huaweiRDSRow) (provider.Object, error) {
		if row.Project != "" && row.Project != c.profile.ProjectID {
			return provider.Object{}, &provider.Failure{Kind: provider.Unauthorized}
		}
		if row.Region != region {
			return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		return databaseObject(row.ID, row.Name, row.Status, provider.HuaweiRDS, row.Datastore.Type, row.Datastore.Version, row.Network)
	})
}
