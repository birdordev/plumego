package publiccloud

import (
	"context"
	"encoding/json"
	"net/netip"
	"net/url"
	"strconv"

	"with-cloud-manager/internal/provider"
)

const tencentElasticIPCharge = "TRAFFIC_POSTPAID_BY_HOUR"

func (c *Client) prepareElasticIPCreation(scope provider.Scope, spec provider.NetworkSpec, token string) (func(context.Context) (provider.LaunchReceipt, error), error) {
	limit := c.profile.ElasticIPBandwidthLimit
	if limit < spec.BandwidthMbps || limit > 200 ||
		(c.name == provider.HuaweiECS && c.profile.HuaweiElasticIPPool != spec.AddressPool) {
		return nil, &provider.Failure{Kind: provider.CreationBlocked}
	}
	marker := vpcMarker(scope, token)
	return func(ctx context.Context) (provider.LaunchReceipt, error) {
		switch c.name {
		case provider.TencentCVM:
			return c.createTencentElasticIP(ctx, scope.Region, spec, token, marker)
		case provider.AlibabaECS:
			return c.createAlibabaElasticIP(ctx, scope.Region, spec, token, marker)
		default:
			return c.createHuaweiElasticIP(ctx, scope.Region, spec, networkIntentTag+":"+marker)
		}
	}, nil
}

func (c *Client) findCreatedElasticIP(ctx context.Context, region string, spec provider.NetworkSpec, id, marker string) (provider.Object, error) {
	switch c.name {
	case provider.TencentCVM:
		return c.findTencentElasticIP(ctx, region, spec, id, marker)
	case provider.AlibabaECS:
		return c.findAlibabaElasticIP(ctx, region, spec, id, marker)
	default:
		return c.findHuaweiElasticIP(ctx, region, spec, id, networkIntentTag+":"+marker)
	}
}

func observedIPv4Allocation(id, name, status string, tags []*tag, address string) (provider.Object, error) {
	if address == "" {
		return provider.Object{}, &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	ip, err := netip.ParseAddr(address)
	if err != nil || !ip.Is4() {
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	object, err := elasticIPObject(id, name, status, tags, address)
	if err != nil {
		return provider.Object{}, err
	}
	page := provider.Page{Objects: []provider.Object{object}}
	return object, page.Validate("")
}

func (c *Client) createTencentElasticIP(ctx context.Context, region string, spec provider.NetworkSpec, token, marker string) (provider.LaunchReceipt, error) {
	payload := struct {
		AddressCount, InternetMaxBandwidthOut                                              int
		AddressName, AddressType, InternetServiceProvider, InternetChargeType, ClientToken string
		IsDedicatedAddressPool                                                             bool
		Tags                                                                               []tag
	}{
		AddressCount: 1, InternetMaxBandwidthOut: spec.BandwidthMbps, AddressName: spec.Name, AddressType: "EIP",
		InternetServiceProvider: "BGP", InternetChargeType: tencentElasticIPCharge, ClientToken: token,
		Tags: []tag{{Key: networkIntentTag, Value: marker}},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return provider.LaunchReceipt{}, err
	}
	var receipt struct {
		RequestId  string
		AddressSet []string
	}
	if err := c.tencent(ctx, "vpc", region, "AllocateAddresses", "2017-03-12", body, &receipt); err != nil {
		return provider.LaunchReceipt{}, err
	}
	return creationReceipt(receipt.AddressSet, receipt.RequestId, "eip-")
}

func (c *Client) findTencentElasticIP(ctx context.Context, region string, spec provider.NetworkSpec, id, marker string) (provider.Object, error) {
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	query := tencentElasticIPQuery{Limit: 2}
	if id == "" {
		query.Filters = []tencentNetworkFilter{{"tag:" + networkIntentTag, []string{marker}}}
	} else {
		if !validLaunchID(id, "eip-") {
			return provider.Object{}, invalid
		}
		query.AddressIds = []string{id}
	}
	result, err := c.readTencentElasticIPs(ctx, region, query)
	if err != nil {
		return provider.Object{}, err
	}
	rows := result.AddressSet
	if len(rows) > 1 || result.TotalCount == nil || *result.TotalCount != len(rows) {
		return provider.Object{}, invalid
	}
	if len(rows) == 0 {
		return provider.Object{}, &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	row := rows[0]
	if !validLaunchID(row.AddressId, "eip-") || (id != "" && id != row.AddressId) {
		return provider.Object{}, invalid
	}
	if row.AddressStatus == "CREATING" {
		return provider.Object{}, &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	if !matchingTag(row.TagSet, networkIntentTag, marker) || row.AddressName != spec.Name || row.AddressType != "EIP" ||
		row.AddressStatus != "UNBIND" || row.InstanceId != "" || row.NetworkInterfaceId != "" ||
		row.InternetChargeType != tencentElasticIPCharge || row.Bandwidth != spec.BandwidthMbps {
		return provider.Object{}, invalid
	}
	return observedIPv4Allocation(row.AddressId, row.AddressName, row.AddressStatus, row.TagSet, row.AddressIp)
}

func (c *Client) createAlibabaElasticIP(ctx context.Context, region string, spec provider.NetworkSpec, token, marker string) (provider.LaunchReceipt, error) {
	query := url.Values{"RegionId": {region}, "Name": {spec.Name}, "Bandwidth": {strconv.Itoa(spec.BandwidthMbps)},
		"ISP": {"BGP"}, "InstanceChargeType": {"PostPaid"}, "InternetChargeType": {"PayByTraffic"},
		"ClientToken": {token}, "Tag.1.Key": {networkIntentTag}, "Tag.1.Value": {marker}}
	var result struct{ Code, RequestId, AllocationId string }
	if err := c.alibaba(ctx, alibabaVPCHost(region), "AllocateEipAddress", "2016-04-28", query, &result); err != nil {
		return provider.LaunchReceipt{}, err
	}
	if result.Code != "" {
		return provider.LaunchReceipt{}, cloudFailure(result.Code)
	}
	return creationReceipt([]string{result.AllocationId}, result.RequestId, "eip-")
}

func (c *Client) findAlibabaElasticIP(ctx context.Context, region string, spec provider.NetworkSpec, id, marker string) (provider.Object, error) {
	const size = 10
	query := url.Values{"PageNumber": {"1"}, "PageSize": {"10"}}
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	if id == "" {
		query.Set("Tag.1.Key", networkIntentTag)
		query.Set("Tag.1.Value", marker)
	} else {
		if !validLaunchID(id, "eip-") {
			return provider.Object{}, invalid
		}
		query.Set("AllocationId", id)
	}
	list, err := c.readAlibabaElasticIPs(ctx, region, query)
	if err != nil {
		return provider.Object{}, err
	}
	rows := list.EipAddresses.EipAddress
	next, err := list.next(1, size, len(rows))
	if err != nil || next != "" || len(rows) > 1 {
		return provider.Object{}, invalid
	}
	if len(rows) == 0 {
		return provider.Object{}, &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	row := rows[0]
	if row.RegionId != region {
		return provider.Object{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	if !validLaunchID(row.AllocationId, "eip-") || (id != "" && id != row.AllocationId) ||
		!matchingTag(row.Tags.Tag, networkIntentTag, marker) || row.Name != spec.Name || row.Status != "Available" ||
		row.InstanceId != "" || row.VpcId != "" || row.BandwidthPackageId != "" ||
		row.InternetChargeType != "PayByTraffic" || row.ChargeType != "PostPaid" || row.Bandwidth != strconv.Itoa(spec.BandwidthMbps) {
		return provider.Object{}, invalid
	}
	return observedIPv4Allocation(row.AllocationId, row.Name, row.Status, row.Tags.Tag, row.IpAddress)
}
