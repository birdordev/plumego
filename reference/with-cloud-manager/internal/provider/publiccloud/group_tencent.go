package publiccloud

import (
	"context"
	"encoding/json"

	"with-cloud-manager/internal/provider"
)

func (c *Client) createTencentGroup(ctx context.Context, region string, spec provider.NetworkSpec, marker string) (provider.LaunchReceipt, error) {
	input := struct {
		GroupName, GroupDescription string
		Tags                        []tag
	}{spec.Name, "Managed by Plumego", []tag{{Key: networkIntentTag, Value: marker}}}
	body, err := json.Marshal(input)
	if err != nil {
		return provider.LaunchReceipt{}, err
	}
	var response struct {
		RequestId     string
		SecurityGroup struct{ SecurityGroupId string }
	}
	if err := c.tencent(ctx, "vpc", region, "CreateSecurityGroup", "2017-03-12", body, &response); err != nil {
		return provider.LaunchReceipt{}, err
	}
	return creationReceipt([]string{response.SecurityGroup.SecurityGroupId}, response.RequestId, "sg-")
}

type tencentGroupObservation struct {
	SecurityGroupId, SecurityGroupName, Region string
	ServiceManaged                             *bool
	IsDefault                                  *bool
	TagSet                                     []*tag
}

func (c *Client) readTencentGroup(ctx context.Context, region, id, marker string) (tencentGroupObservation, error) {
	input := struct {
		Limit            string
		SecurityGroupIds []string               `json:",omitempty"`
		Filters          []tencentNetworkFilter `json:",omitempty"`
	}{Limit: "2"}
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	if id == "" {
		input.Filters = []tencentNetworkFilter{{"tag:" + networkIntentTag, []string{marker}}}
	} else {
		if !validSecurityGroupID(id) {
			return tencentGroupObservation{}, invalid
		}
		input.SecurityGroupIds = []string{id}
	}
	body, err := json.Marshal(input)
	if err != nil {
		return tencentGroupObservation{}, err
	}
	var result struct {
		TotalCount       *int
		SecurityGroupSet []tencentGroupObservation
	}
	if err := c.tencent(ctx, "vpc", region, "DescribeSecurityGroups", "2017-03-12", body, &result); err != nil {
		return tencentGroupObservation{}, err
	}
	rows := result.SecurityGroupSet
	if rows == nil || result.TotalCount == nil || *result.TotalCount != len(rows) || len(rows) > 1 {
		return tencentGroupObservation{}, invalid
	}
	if len(rows) == 0 {
		return tencentGroupObservation{}, emptyNetworkList
	}
	row := rows[0]
	if row.Region != "" && row.Region != region {
		return tencentGroupObservation{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	if !validSecurityGroupID(row.SecurityGroupId) || (id != "" && row.SecurityGroupId != id) || row.IsDefault == nil {
		return tencentGroupObservation{}, invalid
	}
	return row, nil
}

func (c *Client) findTencentGroup(ctx context.Context, region string, spec provider.NetworkSpec, id, marker string) (provider.Object, error) {
	row, err := c.readTencentGroup(ctx, region, id, marker)
	if err != nil {
		return provider.Object{}, err
	}
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	if *row.IsDefault || row.SecurityGroupName != spec.Name || !matchingTag(row.TagSet, networkIntentTag, marker) {
		return provider.Object{}, invalid
	}
	policies, err := c.readTencentGroupPolicies(ctx, region, row.SecurityGroupId)
	if err != nil {
		return provider.Object{}, err
	}
	if len(policies.Ingress) != 0 || len(policies.Egress) != 0 {
		return provider.Object{}, invalid
	}
	return tagObject(row.SecurityGroupId, row.SecurityGroupName, "available", row.TagSet), nil
}
