package publiccloud

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"with-cloud-manager/internal/provider"
)

const ackClusterPageSize = 20

type alibabaClusterRow struct {
	ID          string `json:"cluster_id"`
	Region      string `json:"region_id"`
	Type        string `json:"cluster_type"`
	Version     string `json:"current_version"`
	Network     string `json:"vpc_id"`
	Nodes       *int   `json:"size"`
	Name, State string
}

func (c *Client) alibabaClusters(ctx context.Context, region string, offset int, id string) ([]provider.Object, *int, error) {
	number := offset/ackClusterPageSize + 1
	query := url.Values{
		"region_id": {region}, "page_number": {strconv.Itoa(number)}, "page_size": {strconv.Itoa(ackClusterPageSize)},
	}
	if id != "" {
		query.Set("cluster_id", id)
	}
	var result struct {
		Clusters []alibabaClusterRow
		Page     struct {
			Number *int `json:"page_number"`
			Size   *int `json:"page_size"`
			Total  *int `json:"total_count"`
		} `json:"page_info"`
		Code string
	}
	host := "cs." + region + ".aliyuncs.com"
	if err := c.alibabaSigned(ctx, http.MethodGet, host, "/api/v1/clusters", "DescribeClustersV1", "2015-12-15", query, "", &result); err != nil {
		return nil, nil, err
	}
	if result.Code != "" {
		return nil, nil, cloudFailure(result.Code)
	}
	if result.Page.Number == nil || *result.Page.Number != number ||
		result.Page.Size == nil || *result.Page.Size != ackClusterPageSize {
		return nil, nil, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return projectRegionalInstances(result.Clusters, result.Page.Total, func(row alibabaClusterRow) (provider.Object, error) {
		if row.Region != region {
			return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		return clusterObject(row.ID, row.Name, row.State, row.Network,
			provider.ClusterDetails{Product: provider.AlibabaACK, Type: row.Type, Version: row.Version, NodeCount: row.Nodes})
	})
}
