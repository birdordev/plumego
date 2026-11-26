package publiccloud

import (
	"context"
	"encoding/json"
	"strconv"

	"with-cloud-manager/internal/provider"
)

func (c *Client) createTencentSubnet(ctx context.Context, region string, spec provider.NetworkSpec, marker string) (provider.LaunchReceipt, error) {
	payload := struct {
		SubnetName, VpcId, CidrBlock, Zone string
		Tags                               []tag
	}{spec.Name, spec.ParentID, spec.CIDR, spec.Zone, []tag{{Key: networkIntentTag, Value: marker}}}
	body, err := json.Marshal(payload)
	if err != nil {
		return provider.LaunchReceipt{}, err
	}
	var result struct {
		RequestId string
		Subnet    struct{ SubnetId string }
	}
	if err := c.tencent(ctx, "vpc", region, "CreateSubnet", "2017-03-12", body, &result); err != nil {
		return provider.LaunchReceipt{}, err
	}
	return creationReceipt([]string{result.Subnet.SubnetId}, result.RequestId, "subnet-")
}

func (c *Client) findTencentSubnet(ctx context.Context, region string, spec provider.NetworkSpec, id, marker string) (provider.Object, error) {
	payload := struct {
		Limit     string
		SubnetIds []string               `json:",omitempty"`
		Filters   []tencentNetworkFilter `json:",omitempty"`
	}{Limit: "2"}
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	if id == "" {
		payload.Filters = []tencentNetworkFilter{{"tag:" + networkIntentTag, []string{marker}}, {"vpc-id", []string{spec.ParentID}}}
	} else {
		if !validLaunchID(id, "subnet-") {
			return provider.Object{}, invalid
		}
		payload.SubnetIds = []string{id}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return provider.Object{}, err
	}
	var result struct {
		TotalCount *int
		SubnetSet  []struct {
			SubnetId, SubnetName, VpcId, CidrBlock, Zone string
			TagSet                                       []*tag
		}
	}
	if err := c.tencent(ctx, "vpc", region, "DescribeSubnets", "2017-03-12", body, &result); err != nil {
		return provider.Object{}, err
	}
	if result.TotalCount == nil || result.SubnetSet == nil || *result.TotalCount != len(result.SubnetSet) || len(result.SubnetSet) > 1 {
		return provider.Object{}, invalid
	}
	if len(result.SubnetSet) == 0 {
		return provider.Object{}, &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	row := result.SubnetSet[0]
	if !validLaunchID(row.SubnetId, "subnet-") || (id != "" && row.SubnetId != id) || row.VpcId != spec.ParentID ||
		row.SubnetName != spec.Name || row.CidrBlock != spec.CIDR || !matchingTag(row.TagSet, networkIntentTag, marker) {
		return provider.Object{}, invalid
	}
	if err := c.checkTencentSubnetZone(ctx, region, row.Zone, spec.Zone); err != nil {
		return provider.Object{}, err
	}
	return subnetObject(spec, row.SubnetId, "available", row.TagSet), nil
}

func (c *Client) checkTencentSubnetZone(ctx context.Context, region, actual, expected string) error {
	if actual == expected {
		return nil
	}
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	if _, err := strconv.ParseUint(actual, 10, 32); err != nil {
		return invalid
	}
	zones, err := c.readTencentZones(ctx, region)
	if err != nil {
		return err
	}
	matched := ""
	for _, zone := range zones {
		if zone.ZoneId == actual {
			if matched != "" || zone.Zone == "" {
				return invalid
			}
			matched = zone.Zone
		}
	}
	if matched != expected {
		return invalid
	}
	return nil
}
