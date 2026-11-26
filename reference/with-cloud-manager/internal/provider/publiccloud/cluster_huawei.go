package publiccloud

import (
	"context"
	"net/url"
	"strconv"
	"strings"

	"with-cloud-manager/internal/provider"
)

type huaweiClusterRow struct {
	Metadata struct {
		UID, Name, ProjectID string
		Annotations          struct{ TotalNodesNumber string }
	}
	Spec struct {
		Type, Version, LegacyVersion string
		HostNetwork                  struct{ VPC string }
	}
	Status struct{ Phase string }
}

func (c *Client) huaweiClusters(ctx context.Context, region string) ([]provider.Object, error) {
	endpoint := url.URL{
		Scheme: "https", Host: "cce." + region + ".myhuaweicloud.com",
		Path: "/api/v3/projects/" + c.profile.ProjectID + "/clusters", RawQuery: "detail=true",
	}
	var result struct {
		Kind, APIVersion string
		Items            []huaweiClusterRow
	}
	if err := c.readHuawei(ctx, endpoint, &result); err != nil {
		return nil, err
	}
	if !strings.EqualFold(result.Kind, "Cluster") || result.APIVersion != "v3" ||
		result.Items == nil || len(result.Items) > provider.MaxPageObjects {
		return nil, &provider.Failure{Kind: provider.InvalidResponse}
	}
	items := make([]provider.Object, 0, len(result.Items))
	for _, row := range result.Items {
		item, err := c.huaweiCluster(row)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (c *Client) huaweiCluster(row huaweiClusterRow) (provider.Object, error) {
	if row.Metadata.ProjectID != "" && row.Metadata.ProjectID != c.profile.ProjectID {
		return provider.Object{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	if !huaweiResourceID.MatchString(row.Metadata.UID) {
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	details := provider.ClusterDetails{Product: provider.HuaweiCCE, Type: row.Spec.Type, Version: row.Spec.LegacyVersion}
	if details.Version == "" {
		details.Version = row.Spec.Version
	}
	if text := row.Metadata.Annotations.TotalNodesNumber; text != "" {
		count, err := strconv.Atoi(text)
		if err != nil || strconv.Itoa(count) != text {
			return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		details.NodeCount = &count
	}
	return clusterObject(row.Metadata.UID, row.Metadata.Name, row.Status.Phase, row.Spec.HostNetwork.VPC, details)
}
