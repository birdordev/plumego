package publiccloud

import (
	"context"
	"encoding/json"
	"net/url"

	"with-cloud-manager/internal/provider"
)

func (c *Client) createAlibabaSubnet(ctx context.Context, region string, spec provider.NetworkSpec, token, marker string) (provider.LaunchReceipt, error) {
	query := url.Values{"RegionId": {region}, "ZoneId": {spec.Zone}, "VpcId": {spec.ParentID},
		"VSwitchName": {spec.Name}, "CidrBlock": {spec.CIDR}, "ClientToken": {token}, "Tag.1.Key": {networkIntentTag}, "Tag.1.Value": {marker}}
	var result struct{ RequestId, VSwitchId, Code string }
	if err := c.alibaba(ctx, alibabaVPCHost(region), "CreateVSwitch", "2016-04-28", query, &result); err != nil {
		return provider.LaunchReceipt{}, err
	}
	if result.Code != "" {
		return provider.LaunchReceipt{}, cloudFailure(result.Code)
	}
	return creationReceipt([]string{result.VSwitchId}, result.RequestId, "vsw-")
}

func (c *Client) findAlibabaSubnet(ctx context.Context, region string, spec provider.NetworkSpec, id, marker string) (provider.Object, error) {
	query := url.Values{"RegionId": {region}, "VpcId": {spec.ParentID}, "VSwitchOwnerId": {c.profile.AccountID}, "PageNumber": {"1"}, "PageSize": {"10"}}
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	if id == "" {
		query.Set("Tag.1.Key", networkIntentTag)
		query.Set("Tag.1.Value", marker)
	} else {
		if !validLaunchID(id, "vsw-") {
			return provider.Object{}, invalid
		}
		query.Set("VSwitchId", id)
	}
	var result struct {
		alibabaNumberedPage
		Code      string
		VSwitches struct {
			VSwitch []struct {
				VSwitchId, VSwitchName, VpcId, ZoneId, RegionId, Status, CidrBlock string
				OwnerId                                                            json.Number
				Tags                                                               struct{ Tag []*tag }
			}
		}
	}
	if err := c.alibaba(ctx, alibabaVPCHost(region), "DescribeVSwitches", "2016-04-28", query, &result); err != nil {
		return provider.Object{}, err
	}
	if result.Code != "" {
		return provider.Object{}, cloudFailure(result.Code)
	}
	rows := result.VSwitches.VSwitch
	next, err := result.next(1, 10, len(rows))
	if err != nil || next != "" || rows == nil || len(rows) > 1 {
		return provider.Object{}, invalid
	}
	if len(rows) == 0 {
		return provider.Object{}, &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	row := rows[0]
	if row.OwnerId.String() != c.profile.AccountID || (row.RegionId != "" && row.RegionId != region) {
		return provider.Object{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	if !validLaunchID(row.VSwitchId, "vsw-") || (id != "" && row.VSwitchId != id) || row.VpcId != spec.ParentID {
		return provider.Object{}, invalid
	}
	if row.Status == "Pending" {
		return provider.Object{}, &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	if row.Status != "Available" || row.VSwitchName != spec.Name ||
		row.CidrBlock != spec.CIDR || row.ZoneId != spec.Zone || !matchingTag(row.Tags.Tag, networkIntentTag, marker) {
		return provider.Object{}, invalid
	}
	return subnetObject(spec, row.VSwitchId, row.Status, row.Tags.Tag), nil
}
