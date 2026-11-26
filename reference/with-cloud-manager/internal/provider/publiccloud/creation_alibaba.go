package publiccloud

import (
	"context"
	"net/url"
	"slices"

	"with-cloud-manager/internal/provider"
)

func (c *Client) createAlibabaInstance(ctx context.Context, region string, spec provider.InstanceSpec, token, marker string) (provider.LaunchReceipt, error) {
	query := url.Values{
		"RegionId": {region}, "InstanceName": {spec.Name}, "ImageId": {spec.ImageID}, "InstanceType": {spec.Size},
		"VSwitchId": {spec.NetworkID}, "SecurityGroupId": {spec.SecurityGroup}, "ClientToken": {token},
		"InstanceChargeType": {"PostPaid"}, "Amount": {"1"}, "InternetMaxBandwidthOut": {"0"},
		"PasswordInherit": {"false"}, "HttpTokens": {"required"}, "Tag.1.Key": {launchTag}, "Tag.1.Value": {marker},
	}
	var response struct {
		RequestId, Code string
		InstanceIdSets  struct{ InstanceIdSet []string }
	}
	if err := c.alibaba(ctx, "ecs."+region+".aliyuncs.com", "RunInstances", "2014-05-26", query, &response); err != nil {
		return provider.LaunchReceipt{}, err
	}
	if response.Code != "" {
		return provider.LaunchReceipt{}, cloudFailure(response.Code)
	}
	return creationReceipt(response.InstanceIdSets.InstanceIdSet, response.RequestId, "i-")
}

func (c *Client) findAlibabaCreation(ctx context.Context, region string, spec provider.InstanceSpec, marker, instanceID string) (provider.Object, error) {
	query := url.Values{"RegionId": {region}, "MaxResults": {"10"}, "Tag.1.Key": {launchTag}, "Tag.1.Value": {marker}}
	var response struct {
		Code, NextToken string
		Instances       struct{ Instance []alibabaInstanceObservation }
	}
	if err := c.alibaba(ctx, "ecs."+region+".aliyuncs.com", "DescribeInstances", "2014-05-26", query, &response); err != nil {
		return provider.Object{}, err
	}
	if response.Code != "" {
		return provider.Object{}, cloudFailure(response.Code)
	}
	rows := response.Instances.Instance
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	if rows == nil || response.NextToken != "" || len(rows) > 1 {
		return provider.Object{}, invalid
	}
	if len(rows) == 0 {
		return provider.Object{}, &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	row := rows[0]
	if row.RegionId != region || !validLaunchID(row.InstanceId, "i-") || (instanceID != "" && row.InstanceId != instanceID) ||
		!matchingLaunchTag(row.Tags.normalized(), marker) || row.InstanceName != spec.Name || row.ImageId != spec.ImageID ||
		row.InstanceType != spec.Size || row.InstanceChargeType != "PostPaid" || row.VpcAttributes.VSwitchId != spec.NetworkID ||
		!slices.Equal(row.SecurityGroupIds.SecurityGroupId, []string{spec.SecurityGroup}) || row.Status == "" {
		return provider.Object{}, invalid
	}
	return row.object(), nil
}
