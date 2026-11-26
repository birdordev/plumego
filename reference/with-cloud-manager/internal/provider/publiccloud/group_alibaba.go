package publiccloud

import (
	"context"
	"net/url"

	"with-cloud-manager/internal/provider"
)

func (c *Client) createAlibabaGroup(ctx context.Context, region string, spec provider.NetworkSpec, token, marker string) (provider.LaunchReceipt, error) {
	query := url.Values{"RegionId": {region}, "VpcId": {spec.ParentID}, "SecurityGroupName": {spec.Name},
		"SecurityGroupType": {"enterprise"}, "ClientToken": {token}, "Tag.1.Key": {networkIntentTag}, "Tag.1.Value": {marker}}
	var response struct{ RequestId, SecurityGroupId, Code string }
	if err := c.alibaba(ctx, "ecs."+region+".aliyuncs.com", "CreateSecurityGroup", "2014-05-26", query, &response); err != nil {
		return provider.LaunchReceipt{}, err
	}
	if response.Code != "" {
		return provider.LaunchReceipt{}, cloudFailure(response.Code)
	}
	return creationReceipt([]string{response.SecurityGroupId}, response.RequestId, "sg-")
}

type alibabaGroupObservation struct {
	ServiceManaged                                               *bool
	SecurityGroupId, SecurityGroupName, VpcId, SecurityGroupType string
	Tags                                                         alibabaECSTags
}

func (c *Client) readAlibabaGroup(ctx context.Context, region, vpc, id, marker string) (alibabaGroupObservation, error) {
	query := url.Values{"RegionId": {region}, "MaxResults": {"10"}}
	if vpc != "" {
		query.Set("VpcId", vpc)
	}
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	if id == "" {
		query.Set("Tag.1.Key", networkIntentTag)
		query.Set("Tag.1.Value", marker)
	} else {
		if !validSecurityGroupID(id) {
			return alibabaGroupObservation{}, invalid
		}
		query.Set("SecurityGroupId", id)
	}
	var result struct {
		Code, RegionId, NextToken string
		SecurityGroups            struct {
			SecurityGroup []alibabaGroupObservation
		}
	}
	if err := c.alibaba(ctx, "ecs."+region+".aliyuncs.com", "DescribeSecurityGroups", "2014-05-26", query, &result); err != nil {
		return alibabaGroupObservation{}, err
	}
	if result.Code != "" {
		return alibabaGroupObservation{}, cloudFailure(result.Code)
	}
	if result.RegionId != region {
		return alibabaGroupObservation{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	rows := result.SecurityGroups.SecurityGroup
	if rows == nil || len(rows) > 1 || result.NextToken != "" {
		return alibabaGroupObservation{}, invalid
	}
	if len(rows) == 0 {
		return alibabaGroupObservation{}, emptyNetworkList
	}
	row := rows[0]
	if !validSecurityGroupID(row.SecurityGroupId) || (id != "" && row.SecurityGroupId != id) || (vpc != "" && row.VpcId != vpc) {
		return alibabaGroupObservation{}, invalid
	}
	return row, nil
}

func (c *Client) findAlibabaGroup(ctx context.Context, region string, spec provider.NetworkSpec, id, marker string) (provider.Object, error) {
	row, err := c.readAlibabaGroup(ctx, region, spec.ParentID, id, marker)
	if err != nil {
		return provider.Object{}, err
	}
	tags := row.Tags.normalized()
	if row.SecurityGroupName != spec.Name || row.SecurityGroupType != "enterprise" || !matchingTag(tags, networkIntentTag, marker) {
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	if err := c.checkAlibabaGroupIsolation(ctx, region, row.SecurityGroupId, spec); err != nil {
		return provider.Object{}, err
	}
	object := tagObject(row.SecurityGroupId, row.SecurityGroupName, "available", tags)
	object.Link(provider.Network, row.VpcId, "in_network")
	return object, nil
}

func (c *Client) checkAlibabaGroupIsolation(ctx context.Context, region, group string, spec provider.NetworkSpec) error {
	query := url.Values{"RegionId": {region}, "SecurityGroupId": {group}, "Direction": {"ingress"}, "MaxResults": {"10"}}
	var result struct {
		Code, RegionId, SecurityGroupId, VpcId, NextToken, InnerAccessPolicy string
		Permissions                                                          struct{ Permission []alibabaRule }
	}
	if err := c.alibaba(ctx, "ecs."+region+".aliyuncs.com", "DescribeSecurityGroupAttribute", "2014-05-26", query, &result); err != nil {
		return err
	}
	if result.Code != "" {
		return cloudFailure(result.Code)
	}
	if result.RegionId != region || result.SecurityGroupId != group || result.VpcId != spec.ParentID {
		return &provider.Failure{Kind: provider.Unauthorized}
	}
	if result.InnerAccessPolicy != "Drop" || result.NextToken != "" || result.Permissions.Permission == nil ||
		len(result.Permissions.Permission) != 0 {
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	return nil
}
