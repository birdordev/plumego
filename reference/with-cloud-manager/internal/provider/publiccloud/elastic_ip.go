package publiccloud

import (
	"context"
	"encoding/json"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"with-cloud-manager/internal/provider"
)

func elasticIPObject(id, name, status string, tags []*tag, rawAddresses ...string) (provider.Object, error) {
	object := tagObject(id, name, status, tags)
	for _, value := range rawAddresses {
		if value == "" {
			continue
		}
		address, err := netip.ParseAddr(value)
		if err != nil || address.Is4In6() || address.Zone() != "" || !address.IsGlobalUnicast() {
			return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		object.PublicAddresses = append(object.PublicAddresses, address.String())
	}
	if object.Name == "" {
		object.Name = id
	}
	return object, nil
}

type tencentElasticIP struct {
	AddressId, AddressName, AddressStatus, AddressIp, AddressType, InstanceId, InstanceType string
	NetworkInterfaceId, InternetChargeType, BandwidthPackageId                              string
	IsArrears, IsBlocked                                                                    *bool
	CascadeRelease                                                                          *bool
	Bandwidth                                                                               int
	TagSet                                                                                  []*tag
}

type tencentElasticIPQuery struct {
	Offset, Limit int
	AddressIds    []string               `json:",omitempty"`
	Filters       []tencentNetworkFilter `json:",omitempty"`
}

type tencentElasticIPList struct {
	TotalCount *int
	AddressSet []tencentElasticIP
}

func (c *Client) readTencentElasticIPs(ctx context.Context, region string, query tencentElasticIPQuery) (tencentElasticIPList, error) {
	body, err := json.Marshal(query)
	if err != nil {
		return tencentElasticIPList{}, err
	}
	var result tencentElasticIPList
	if err := c.tencent(ctx, "vpc", region, "DescribeAddresses", "2017-03-12", body, &result); err != nil {
		return tencentElasticIPList{}, err
	}
	if result.AddressSet == nil {
		return tencentElasticIPList{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return result, nil
}

func (c *Client) tencentElasticIPs(ctx context.Context, region, cursor string) (provider.Page, error) {
	offset, err := pageNumber(cursor, 0)
	if err != nil {
		return provider.Page{}, err
	}
	result, err := c.readTencentElasticIPs(ctx, region, tencentElasticIPQuery{Offset: offset, Limit: pageSize})
	if err != nil {
		return provider.Page{}, err
	}
	next, err := tencentNextOffset(offset, result.TotalCount, len(result.AddressSet), pageSize)
	if err != nil {
		return provider.Page{}, err
	}
	page := provider.Page{Objects: make([]provider.Object, 0, len(result.AddressSet)), Next: next}
	for _, row := range result.AddressSet {
		switch row.AddressType {
		case "EIP", "AnycastEIP", "HighQualityEIP", "AntiDDoSEIP", "ResidentialEIP":
		default:
			return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		if !validLaunchID(row.AddressId, "eip-") {
			return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		object, err := elasticIPObject(row.AddressId, row.AddressName, row.AddressStatus, row.TagSet, row.AddressIp)
		if err != nil {
			return provider.Page{}, err
		}
		if row.AddressStatus == "BIND" && row.InstanceType == "CVM" && validLaunchID(row.InstanceId, "ins-") {
			object.Link(provider.Instance, row.InstanceId, "bound_to")
		}
		page.Objects = append(page.Objects, object)
	}
	return page, nil
}

type alibabaElasticIP struct {
	AllocationId, Name, Status, IpAddress, RegionId, InstanceType, InstanceId, InstanceRegionId, VpcId string
	ChargeType, InternetChargeType, Bandwidth, BandwidthPackageId                                      string
	SegmentInstanceId, BusinessStatus                                                                  string
	DeletionProtection                                                                                 *bool
	ServiceManaged                                                                                     *int
	Tags                                                                                               struct{ Tag []*tag }
}

type alibabaElasticIPList struct {
	alibabaNumberedPage
	Code         string
	EipAddresses struct{ EipAddress []alibabaElasticIP }
}

func (c *Client) readAlibabaElasticIPs(ctx context.Context, region string, query url.Values) (alibabaElasticIPList, error) {
	var result alibabaElasticIPList
	query.Set("RegionId", region)
	if err := c.alibaba(ctx, alibabaVPCHost(region), "DescribeEipAddresses", "2016-04-28", query, &result); err != nil {
		return alibabaElasticIPList{}, err
	}
	if result.Code != "" {
		return alibabaElasticIPList{}, cloudFailure(result.Code)
	}
	if result.EipAddresses.EipAddress == nil {
		return alibabaElasticIPList{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return result, nil
}

func (c *Client) alibabaElasticIPs(ctx context.Context, region, cursor string) (provider.Page, error) {
	number, err := pageNumber(cursor, 1)
	if err != nil {
		return provider.Page{}, err
	}
	query := url.Values{"PageNumber": {strconv.Itoa(number)}, "PageSize": {strconv.Itoa(pageSize)}}
	result, err := c.readAlibabaElasticIPs(ctx, region, query)
	if err != nil {
		return provider.Page{}, err
	}
	rows := result.EipAddresses.EipAddress
	next, err := result.next(number, pageSize, len(rows))
	if err != nil {
		return provider.Page{}, err
	}
	page := provider.Page{Objects: make([]provider.Object, 0, len(rows)), Next: next}
	for _, row := range rows {
		if row.RegionId != region {
			return provider.Page{}, &provider.Failure{Kind: provider.Unauthorized}
		}
		if !validLaunchID(row.AllocationId, "eip-") {
			return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		object, err := elasticIPObject(row.AllocationId, row.Name, row.Status, row.Tags.Tag, row.IpAddress)
		if err != nil {
			return provider.Page{}, err
		}
		if row.Status == "InUse" {
			if row.InstanceType == "EcsInstance" && strings.TrimSpace(row.InstanceRegionId) == region && validLaunchID(row.InstanceId, "i-") {
				object.Link(provider.Instance, row.InstanceId, "bound_to")
			}
			if row.InstanceType == "IpAddress" && validLaunchID(row.VpcId, "vpc-") {
				object.Link(provider.Network, row.VpcId, "in_network")
			}
		}
		page.Objects = append(page.Objects, object)
	}
	return page, nil
}
