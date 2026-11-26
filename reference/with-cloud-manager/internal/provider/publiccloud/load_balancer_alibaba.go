package publiccloud

import (
	"context"
	"net/url"
	"strconv"
	"strings"

	"with-cloud-manager/internal/provider"
)

type alibabaLBProduct string

const (
	alibabaALB alibabaLBProduct = "alb"
	alibabaNLB alibabaLBProduct = "nlb"
	alibabaCLB alibabaLBProduct = "clb"
)

func (c *Client) alibabaLoadBalancers(ctx context.Context, region, cursor, id string) (provider.Page, error) {
	product, token := alibabaALB, ""
	if id != "" {
		switch {
		case validLaunchID(id, "alb-"):
			product = alibabaALB
		case validLaunchID(id, "nlb-"):
			product = alibabaNLB
		case validLaunchID(id, "lb-"):
			product = alibabaCLB
		default:
			return provider.Page{}, &provider.Failure{Kind: provider.Unsupported}
		}
	} else if cursor != "" {
		service, position, ok := strings.Cut(cursor, ":")
		product, token = alibabaLBProduct(service), position
		if !ok || (product != alibabaALB && product != alibabaNLB && product != alibabaCLB) ||
			(product == alibabaALB && token == "") {
			return provider.Page{}, &provider.Failure{Kind: provider.ExpiredCursor}
		}
	}
	var page provider.Page
	var err error
	if product == alibabaCLB {
		page, err = c.alibabaClassicLoadBalancers(ctx, region, token, id)
	} else {
		page, err = c.alibabaModernLoadBalancers(ctx, region, product, token, id)
	}
	if err != nil {
		return provider.Page{}, err
	}
	if id == "" {
		switch {
		case page.Next != "":
			page.Next = string(product) + ":" + page.Next
		case product == alibabaALB:
			page.Next = string(alibabaNLB) + ":"
		case product == alibabaNLB:
			page.Next = string(alibabaCLB) + ":"
		}
	}
	return page, nil
}

type alibabaModernLB struct {
	RegionId, LoadBalancerId, LoadBalancerName, LoadBalancerStatus, AddressType string
	Ipv6AddressType, VpcId, DNSName                                             string
	SecurityGroupIds                                                            []string
	Tags                                                                        []*tag
	ZoneMappings                                                                []struct {
		ZoneId, VSwitchId     string
		LoadBalancerAddresses []struct {
			AllocationId, PrivateIPv4Address, PublicIPv4Address, Ipv6Address string
		}
	}
}

func (c *Client) alibabaModernLoadBalancers(ctx context.Context, region string, product alibabaLBProduct, token, id string) (provider.Page, error) {
	query := url.Values{"MaxResults": {strconv.Itoa(loadBalancerPageSize)}}
	host, version := alibabaModernLBEndpoint(region, product)
	if product == alibabaNLB {
		query.Set("RegionId", region)
	}
	if token != "" {
		query.Set("NextToken", token)
	}
	if id != "" {
		query.Set("LoadBalancerIds.1", id)
	}
	var result struct {
		Code, NextToken string
		TotalCount      *int
		LoadBalancers   []alibabaModernLB
	}
	if err := c.alibaba(ctx, host, "ListLoadBalancers", version, query, &result); err != nil {
		return provider.Page{}, err
	}
	if result.Code != "" {
		return provider.Page{}, cloudFailure(result.Code)
	}
	rows := result.LoadBalancers
	if rows == nil || len(rows) > loadBalancerPageSize || len(result.NextToken) > provider.MaxCursorBytes-4 || strings.ContainsRune(result.NextToken, 0) ||
		(result.NextToken != "" && (result.NextToken == token || len(rows) == 0)) ||
		(id != "" && (result.TotalCount == nil || *result.TotalCount != len(rows))) {
		return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	page := provider.Page{Objects: make([]provider.Object, 0, len(rows)), Next: result.NextToken}
	for _, row := range rows {
		if (product == alibabaNLB || row.RegionId != "") && row.RegionId != region {
			return provider.Page{}, &provider.Failure{Kind: provider.Unauthorized}
		}
		object, err := row.asset(product)
		if err != nil {
			return provider.Page{}, err
		}
		page.Objects = append(page.Objects, object)
	}
	return page, nil
}

func (row alibabaModernLB) asset(product alibabaLBProduct) (provider.Object, error) {
	if !validLaunchID(row.LoadBalancerId, string(product)+"-") || !validLaunchID(row.VpcId, "vpc-") {
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	addressType, ok := alibabaLBAddressType(row.AddressType)
	if !ok {
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	ipv6Type := ""
	if row.Ipv6AddressType != "" {
		ipv6Type, ok = alibabaLBAddressType(row.Ipv6AddressType)
		if !ok {
			return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		if ipv6Type != addressType {
			addressType = "dual"
		}
	}
	var private, public, eips []string
	for _, zone := range row.ZoneMappings {
		for _, addresses := range zone.LoadBalancerAddresses {
			if addresses.PrivateIPv4Address != "" {
				private = append(private, addresses.PrivateIPv4Address)
			}
			if addresses.PublicIPv4Address != "" {
				public = append(public, addresses.PublicIPv4Address)
			}
			if addresses.Ipv6Address != "" {
				if ipv6Type == "" {
					return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
				}
				if ipv6Type == "public" {
					public = append(public, addresses.Ipv6Address)
				} else {
					private = append(private, addresses.Ipv6Address)
				}
			}
			if addresses.AllocationId != "" {
				if !validLaunchID(addresses.AllocationId, "eip-") {
					return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
				}
				eips = append(eips, addresses.AllocationId)
			}
		}
	}
	if addressType == "public" && len(private) != 0 {
		addressType = "dual"
	}
	object, err := loadBalancerObject(row.LoadBalancerId, row.LoadBalancerName, row.LoadBalancerStatus,
		"alibaba-"+string(product), addressType, row.DNSName, private, public, row.Tags)
	if err != nil {
		return provider.Object{}, err
	}
	object.Link(provider.Network, row.VpcId, "in_network")
	for _, group := range row.SecurityGroupIds {
		if !validSecurityGroupID(group) {
			return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		object.Link(provider.SecurityGroup, group, "uses_security_group")
	}
	for _, zone := range row.ZoneMappings {
		if zone.VSwitchId != "" && !validLaunchID(zone.VSwitchId, "vsw-") {
			return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		object.Link(provider.Zone, zone.ZoneId, "in_zone")
		object.Link(provider.Subnet, zone.VSwitchId, "in_subnet")
	}
	for _, id := range eips {
		object.Link(provider.ElasticIP, id, "uses_elastic_ip")
	}
	return object, nil
}

func alibabaLBAddressType(value string) (string, bool) {
	switch strings.ToLower(value) {
	case "internet":
		return "public", true
	case "intranet":
		return "private", true
	default:
		return "", false
	}
}

type alibabaClassicLB struct {
	RegionId, LoadBalancerId, LoadBalancerName, LoadBalancerStatus, AddressType string
	VpcId, VSwitchId, Address, MasterZoneId, SlaveZoneId                        string
	Tags                                                                        alibabaECSTags
}

func (c *Client) alibabaClassicLoadBalancers(ctx context.Context, region, cursor, id string) (provider.Page, error) {
	number, err := pageNumber(cursor, 1)
	if err != nil {
		return provider.Page{}, err
	}
	query := url.Values{"RegionId": {region}, "PageNumber": {strconv.Itoa(number)}, "PageSize": {strconv.Itoa(loadBalancerPageSize)}}
	if id != "" {
		query.Set("LoadBalancerId", id)
	}
	host := alibabaClassicLBEndpoint(region)
	var result struct {
		alibabaNumberedPage
		Code          string
		LoadBalancers struct{ LoadBalancer []alibabaClassicLB }
	}
	if err := c.alibaba(ctx, host, "DescribeLoadBalancers", "2014-05-15", query, &result); err != nil {
		return provider.Page{}, err
	}
	if result.Code != "" {
		return provider.Page{}, cloudFailure(result.Code)
	}
	rows := result.LoadBalancers.LoadBalancer
	if rows == nil {
		return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	next, err := result.next(number, loadBalancerPageSize, len(rows))
	if err != nil {
		return provider.Page{}, err
	}
	page := provider.Page{Objects: make([]provider.Object, 0, len(rows)), Next: next}
	for _, row := range rows {
		if row.RegionId != region {
			return provider.Page{}, &provider.Failure{Kind: provider.Unauthorized}
		}
		object, err := row.asset()
		if err != nil {
			return provider.Page{}, err
		}
		page.Objects = append(page.Objects, object)
	}
	return page, nil
}

func (row alibabaClassicLB) asset() (provider.Object, error) {
	addressType, ok := alibabaLBAddressType(row.AddressType)
	if !ok || !validLaunchID(row.LoadBalancerId, "lb-") || (row.VpcId != "" && !validLaunchID(row.VpcId, "vpc-")) ||
		(row.VSwitchId != "" && !validLaunchID(row.VSwitchId, "vsw-")) {
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	var private, public []string
	if addressType == "private" {
		private = []string{row.Address}
	} else {
		public = []string{row.Address}
	}
	object, err := loadBalancerObject(row.LoadBalancerId, row.LoadBalancerName, row.LoadBalancerStatus,
		"alibaba-clb", addressType, "", private, public, row.Tags.normalized())
	if err != nil {
		return provider.Object{}, err
	}
	object.Link(provider.Network, row.VpcId, "in_network")
	object.Link(provider.Subnet, row.VSwitchId, "in_subnet")
	object.Link(provider.Zone, row.MasterZoneId, "in_zone")
	object.Link(provider.Zone, row.SlaveZoneId, "in_zone")
	return object, nil
}
