package publiccloud

import (
	"context"
	"net/http"
	"net/url"

	"with-cloud-manager/internal/provider"
)

func (c *Client) tencentZones(ctx context.Context, region string) (provider.Page, error) {
	zones, err := c.readTencentZones(ctx, region)
	if err != nil {
		return provider.Page{}, err
	}
	page := provider.Page{Objects: make([]provider.Object, 0, len(zones))}
	for _, row := range zones {
		page.Objects = append(page.Objects, provider.Object{ID: row.Zone, Name: row.ZoneName, Status: row.ZoneState})
	}
	return page, nil
}

type tencentZone struct{ Zone, ZoneId, ZoneName, ZoneState string }

func (c *Client) readTencentZones(ctx context.Context, region string) ([]tencentZone, error) {
	var result struct {
		TotalCount *int
		ZoneSet    []tencentZone
	}
	if err := c.tencent(ctx, "cvm", region, "DescribeZones", "2017-03-12", []byte("{}"), &result); err != nil {
		return nil, err
	}
	if result.ZoneSet == nil || result.TotalCount == nil || *result.TotalCount != len(result.ZoneSet) {
		return nil, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return result.ZoneSet, nil
}

func (c *Client) alibabaZones(ctx context.Context, region string) (provider.Page, error) {
	var result struct {
		Code  string
		Zones struct {
			Zone []struct{ ZoneId, LocalName string }
		}
	}
	query := url.Values{"RegionId": {region}}
	if err := c.alibaba(ctx, "ecs."+region+".aliyuncs.com", "DescribeZones", "2014-05-26", query, &result); err != nil {
		return provider.Page{}, err
	}
	if result.Code != "" {
		return provider.Page{}, cloudFailure(result.Code)
	}
	rows := result.Zones.Zone
	if rows == nil {
		return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	page := provider.Page{Objects: make([]provider.Object, 0, len(rows))}
	for _, row := range rows {
		page.Objects = append(page.Objects, provider.Object{ID: row.ZoneId, Name: row.LocalName, Status: "observed"})
	}
	return page, nil
}

func (c *Client) huaweiZones(ctx context.Context, region string) (provider.Page, error) {
	endpoint := url.URL{
		Scheme: "https", Host: "ecs." + region + ".myhuaweicloud.com",
		Path: "/v1/" + c.profile.ProjectID + "/availability-zones",
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	req.Header.Set("X-Auth-Token", c.profile.IAMToken)
	var result struct {
		Zones []struct {
			ID    string `json:"availability_zone_id"`
			Alias string
		} `json:"availability_zones"`
	}
	if err := c.request(req, &result); err != nil {
		return provider.Page{}, err
	}
	if result.Zones == nil {
		return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	page := provider.Page{Objects: make([]provider.Object, 0, len(result.Zones))}
	for _, row := range result.Zones {
		page.Objects = append(page.Objects, provider.Object{ID: row.ID, Name: row.Alias, Status: "observed"})
	}
	return page, nil
}
