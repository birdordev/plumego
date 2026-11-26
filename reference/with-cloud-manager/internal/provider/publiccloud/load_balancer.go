package publiccloud

import (
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"with-cloud-manager/internal/provider"
)

const loadBalancerPageSize = 50

func loadBalancerObject(id, name, status, product, addressType, dns string, private, public []string, tags []*tag) (provider.Object, error) {
	inside, err := provider.NormalizeLoadBalancerAddresses(private)
	if err != nil {
		return provider.Object{}, err
	}
	outside, err := provider.NormalizeLoadBalancerAddresses(public)
	if err != nil {
		return provider.Object{}, err
	}
	object := tagObject(id, name, status, tags)
	object.LoadBalancer = &provider.LoadBalancerDetails{
		Product: product, AddressType: addressType, DNSName: strings.TrimSuffix(strings.ToLower(dns), "."), PrivateAddresses: inside,
	}
	object.PublicAddresses = outside
	return object, nil
}

func (c *Client) observeLoadBalancer(ctx context.Context, scope provider.Scope, id string) (provider.Object, error) {
	if scope.Provider != c.name || scope.RemoteIdentity != c.identity {
		return provider.Object{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	if id == "" || !slices.Contains(c.profile.Regions, scope.Region) {
		return provider.Object{}, &provider.Failure{Kind: provider.Unsupported}
	}
	var page provider.Page
	var err error
	switch c.name {
	case provider.TencentCVM:
		page, err = c.tencentLoadBalancers(ctx, scope.Region, "", id)
	case provider.AlibabaECS:
		page, err = c.alibabaLoadBalancers(ctx, scope.Region, "", id)
	case provider.HuaweiECS:
		page, err = c.huaweiLoadBalancers(ctx, scope.Region, "", id)
	default:
		err = &provider.Failure{Kind: provider.Unsupported}
	}
	if err != nil {
		return provider.Object{}, err
	}
	if page.Next != "" || len(page.Objects) > 1 || page.Validate("") != nil {
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	if len(page.Objects) == 0 {
		return provider.Object{}, &provider.Failure{Kind: provider.Absent}
	}
	item := page.Objects[0]
	if item.ID != id || item.Status == "" || strings.ContainsRune(item.Name, 0) || strings.ContainsRune(item.Status, 0) {
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return item, nil
}

type tencentLoadBalancer struct {
	LoadBalancerId, LoadBalancerName, LoadBalancerType, LoadBalancerDomain, Domain, AddressIPv6 string
	VpcId, SubnetId                                                                             string
	Status, Forward                                                                             *int
	LoadBalancerVips, SecureGroups                                                              []string
	Tags                                                                                        []struct{ TagKey, TagValue string }
	MasterZone                                                                                  tencentLBZone
	BackupZoneSet                                                                               []tencentLBZone
	TargetRegionInfo                                                                            struct{ Region string }
}

type tencentLBZone struct{ Zone, ZoneRegion string }

func (c *Client) tencentLoadBalancers(ctx context.Context, region, cursor, id string) (provider.Page, error) {
	rows, next, err := c.tencentLoadBalancerRows(ctx, region, cursor, id)
	if err != nil {
		return provider.Page{}, err
	}
	page := provider.Page{Objects: make([]provider.Object, 0, len(rows)), Next: next}
	for _, row := range rows {
		object, err := row.asset(region)
		if err != nil {
			return provider.Page{}, err
		}
		page.Objects = append(page.Objects, object)
	}
	return page, nil
}

func (c *Client) tencentLoadBalancerRows(ctx context.Context, region, cursor, id string) ([]tencentLoadBalancer, string, error) {
	offset, err := pageNumber(cursor, 0)
	if err != nil {
		return nil, "", err
	}
	input := struct {
		Offset, Limit   int
		LoadBalancerIds []string `json:",omitempty"`
	}{Offset: offset, Limit: loadBalancerPageSize}
	if id != "" {
		if !validLaunchID(id, "lb-") {
			return nil, "", &provider.Failure{Kind: provider.Unsupported}
		}
		input.LoadBalancerIds = []string{id}
		input.Limit = 2
	}
	body, err := json.Marshal(input)
	if err != nil {
		return nil, "", err
	}
	var result struct {
		TotalCount      *int
		LoadBalancerSet []tencentLoadBalancer
	}
	if err := c.tencent(ctx, "clb", region, "DescribeLoadBalancers", "2018-03-17", body, &result); err != nil {
		return nil, "", err
	}
	if result.LoadBalancerSet == nil {
		return nil, "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	next, err := tencentNextOffset(offset, result.TotalCount, len(result.LoadBalancerSet), input.Limit)
	return result.LoadBalancerSet, next, err
}

func (row tencentLoadBalancer) asset(region string) (provider.Object, error) {
	if !validLaunchID(row.LoadBalancerId, "lb-") || row.Status == nil || *row.Status < 0 ||
		(row.VpcId != "" && !validLaunchID(row.VpcId, "vpc-")) ||
		(row.SubnetId != "" && !validLaunchID(row.SubnetId, "subnet-")) {
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	vips := append(row.LoadBalancerVips, row.AddressIPv6)
	var private, public []string
	addressType := ""
	switch row.LoadBalancerType {
	case "OPEN":
		addressType, public = "public", vips
	case "INTERNAL":
		addressType, private = "private", vips
	default:
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	dns := row.LoadBalancerDomain
	if dns == "" {
		dns = row.Domain
	}
	var tags []*tag
	if row.Tags != nil {
		tags = make([]*tag, 0, len(row.Tags))
		for _, value := range row.Tags {
			tags = append(tags, &tag{Key: value.TagKey, Value: value.TagValue})
		}
	}
	object, err := loadBalancerObject(row.LoadBalancerId, row.LoadBalancerName, strconv.Itoa(*row.Status),
		"tencent-clb", addressType, dns, private, public, tags)
	if err != nil {
		return provider.Object{}, err
	}
	if row.TargetRegionInfo.Region == "" || row.TargetRegionInfo.Region == region {
		object.Link(provider.Network, row.VpcId, "in_network")
		object.Link(provider.Subnet, row.SubnetId, "in_subnet")
	}
	for _, zone := range append(row.BackupZoneSet, row.MasterZone) {
		if zone.ZoneRegion == region {
			object.Link(provider.Zone, zone.Zone, "in_zone")
		}
	}
	for _, id := range row.SecureGroups {
		if !validSecurityGroupID(id) {
			return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		object.Link(provider.SecurityGroup, id, "uses_security_group")
	}
	return object, nil
}

type huaweiLBPublicIP struct {
	ID      string `json:"publicip_id"`
	Address string `json:"publicip_address"`
}

type huaweiLBEIP struct {
	ID      string `json:"eip_id"`
	Address string `json:"eip_address"`
}

type huaweiLoadBalancer struct {
	ID, Name   string
	Project    string             `json:"project_id"`
	VPC        string             `json:"vpc_id"`
	State      string             `json:"provisioning_status"`
	IPv4       string             `json:"vip_address"`
	IPv6       string             `json:"ipv6_vip_address"`
	IPv6Subnet string             `json:"ipv6_vip_virsubnet_id"`
	Zones      []string           `json:"availability_zone_list"`
	Subnets    []string           `json:"elb_virsubnet_ids"`
	EIPs       []huaweiLBEIP      `json:"eips"`
	PublicIPs  []huaweiLBPublicIP `json:"publicips"`
	Tags       []*tag
}

func (c *Client) huaweiLoadBalancers(ctx context.Context, region, cursor, id string) (provider.Page, error) {
	if cursor != "" && !huaweiResourceID.MatchString(cursor) {
		return provider.Page{}, &provider.Failure{Kind: provider.ExpiredCursor}
	}
	query := url.Values{"limit": {strconv.Itoa(loadBalancerPageSize)}}
	if cursor != "" {
		query.Set("marker", cursor)
	}
	if id != "" {
		if !huaweiResourceID.MatchString(id) {
			return provider.Page{}, &provider.Failure{Kind: provider.Unsupported}
		}
		query.Set("id", id)
		query.Set("limit", "2")
	}
	endpoint := url.URL{Scheme: "https", Host: "elb." + region + ".myhuaweicloud.com",
		Path: "/v3/" + c.profile.ProjectID + "/elb/loadbalancers", RawQuery: query.Encode()}
	var result struct {
		Rows []huaweiLoadBalancer `json:"loadbalancers"`
		Page *huaweiPageInfo      `json:"page_info"`
	}
	if err := c.readHuawei(ctx, endpoint, &result); err != nil {
		return provider.Page{}, err
	}
	n := len(result.Rows)
	if result.Rows == nil || n > loadBalancerPageSize || result.Page == nil || result.Page.Count == nil || *result.Page.Count != n {
		return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	next := result.Page.Next
	if next != "" && (n == 0 || result.Rows[n-1].ID != next || next == cursor) {
		return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	page := provider.Page{Objects: make([]provider.Object, 0, n), Next: next}
	for _, row := range result.Rows {
		if row.Project != c.profile.ProjectID {
			return provider.Page{}, &provider.Failure{Kind: provider.Unauthorized}
		}
		if !huaweiResourceID.MatchString(row.ID) || row.ID == cursor {
			return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		object, err := row.asset()
		if err != nil {
			return provider.Page{}, err
		}
		page.Objects = append(page.Objects, object)
	}
	return page, nil
}

func (row huaweiLoadBalancer) asset() (provider.Object, error) {
	if row.VPC != "" && !huaweiResourceID.MatchString(row.VPC) {
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	byID := make(map[string]string, len(row.PublicIPs)+len(row.EIPs))
	allocated := row.PublicIPs
	for _, eip := range row.EIPs {
		allocated = append(allocated, huaweiLBPublicIP{ID: eip.ID, Address: eip.Address})
	}
	for _, ip := range allocated {
		if !huaweiResourceID.MatchString(ip.ID) || ip.Address == "" {
			return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		if existing, ok := byID[ip.ID]; ok && existing != ip.Address {
			return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		byID[ip.ID] = ip.Address
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	typ := "private"
	var public []string
	for _, id := range ids {
		public = append(public, byID[id])
	}
	if len(public) != 0 {
		typ = "dual"
	}
	object, err := loadBalancerObject(row.ID, row.Name, row.State, "huawei-elb", typ, "", []string{row.IPv4, row.IPv6}, public, row.Tags)
	if err != nil {
		return provider.Object{}, err
	}
	object.Link(provider.Network, row.VPC, "in_network")
	for _, id := range append(row.Subnets, row.IPv6Subnet) {
		if id != "" && !huaweiResourceID.MatchString(id) {
			return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
		}
		object.Link(provider.Subnet, id, "in_subnet")
	}
	for _, zone := range row.Zones {
		object.Link(provider.Zone, zone, "in_zone")
	}
	for _, id := range ids {
		object.Link(provider.ElasticIP, id, "uses_elastic_ip")
	}
	return object, nil
}
