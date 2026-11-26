package publiccloud

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"with-cloud-manager/internal/provider"
)

type tencentSubnet struct{ SubnetId, VpcId, Zone string }

func (c *Client) tencentLaunchSubnet(ctx context.Context, region, id string) (tencentSubnet, error) {
	body, err := json.Marshal(struct{ SubnetIds []string }{[]string{id}})
	if err != nil {
		return tencentSubnet{}, err
	}
	var result struct {
		TotalCount *int
		SubnetSet  []tencentSubnet
	}
	if err := c.tencent(ctx, "vpc", region, "DescribeSubnets", "2017-03-12", body, &result); err != nil {
		return tencentSubnet{}, err
	}
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	if result.TotalCount == nil || *result.TotalCount != 1 || len(result.SubnetSet) != 1 {
		return tencentSubnet{}, invalid
	}
	subnet := result.SubnetSet[0]
	if subnet.SubnetId != id || !validLaunchID(subnet.VpcId, "vpc-") ||
		!strings.HasPrefix(subnet.Zone, region+"-") || !regionID.MatchString(subnet.Zone) {
		return tencentSubnet{}, invalid
	}
	return subnet, nil
}

func (c *Client) createTencentInstance(ctx context.Context, region string, spec provider.InstanceSpec, token, marker string, subnet tencentSubnet) (provider.LaunchReceipt, error) {
	type tagGroup struct {
		ResourceType string
		Tags         []tag
	}
	input := struct {
		InstanceName, ImageId, InstanceType, InstanceChargeType, ClientToken string
		InstanceCount                                                        int
		Placement                                                            struct{ Zone string }
		VirtualPrivateCloud                                                  struct{ VpcId, SubnetId string }
		SecurityGroupIds                                                     []string
		InternetAccessible                                                   struct {
			PublicIpAssigned        bool
			InternetMaxBandwidthOut int
		}
		LoginSettings    struct{ KeepImageLogin string }
		TagSpecification []tagGroup
	}{
		InstanceName: spec.Name, ImageId: spec.ImageID, InstanceType: spec.Size,
		InstanceChargeType: "POSTPAID_BY_HOUR", ClientToken: token, InstanceCount: 1,
		SecurityGroupIds: []string{spec.SecurityGroup},
		TagSpecification: []tagGroup{{ResourceType: "instance", Tags: []tag{{Key: launchTag, Value: marker}}}},
	}
	input.Placement.Zone = subnet.Zone
	input.VirtualPrivateCloud.VpcId, input.VirtualPrivateCloud.SubnetId = subnet.VpcId, subnet.SubnetId
	input.LoginSettings.KeepImageLogin = "true"
	body, err := json.Marshal(input)
	if err != nil {
		return provider.LaunchReceipt{}, err
	}
	var result struct {
		InstanceIdSet []string
		RequestId     string
	}
	if err := c.tencent(ctx, "cvm", region, "RunInstances", "2017-03-12", body, &result); err != nil {
		return provider.LaunchReceipt{}, err
	}
	return creationReceipt(result.InstanceIdSet, result.RequestId, "ins-")
}

func (c *Client) findTencentCreation(ctx context.Context, region string, spec provider.InstanceSpec, marker, instanceID string) (provider.Object, error) {
	type filter struct {
		Name   string
		Values []string
	}
	body, err := json.Marshal(struct {
		Filters []filter
		Limit   int
	}{[]filter{{"tag:" + launchTag, []string{marker}}}, 2})
	if err != nil {
		return provider.Object{}, err
	}
	var result struct {
		TotalCount  *int
		InstanceSet []tencentInstanceObservation
	}
	if err := c.tencent(ctx, "cvm", region, "DescribeInstances", "2017-03-12", body, &result); err != nil {
		return provider.Object{}, err
	}
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	if result.TotalCount == nil || result.InstanceSet == nil || *result.TotalCount != len(result.InstanceSet) || len(result.InstanceSet) > 1 {
		return provider.Object{}, invalid
	}
	if len(result.InstanceSet) == 0 {
		return provider.Object{}, &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	row := result.InstanceSet[0]
	if !validLaunchID(row.InstanceId, "ins-") || (instanceID != "" && row.InstanceId != instanceID) ||
		!matchingLaunchTag(row.Tags, marker) || row.InstanceName != spec.Name || row.ImageId != spec.ImageID ||
		row.InstanceType != spec.Size || row.InstanceChargeType != "POSTPAID_BY_HOUR" || row.VirtualPrivateCloud.SubnetId != spec.NetworkID ||
		!slices.Equal(row.SecurityGroupIds, []string{spec.SecurityGroup}) || row.InstanceState == "" {
		return provider.Object{}, invalid
	}
	return row.object(), nil
}
