package publiccloud

import (
	"context"
	"net/netip"
	"net/url"

	"with-cloud-manager/internal/provider"
)

func (c *Client) createHuaweiSubnet(ctx context.Context, region string, spec provider.NetworkSpec, description string) (provider.LaunchReceipt, error) {
	cidr, err := netip.ParsePrefix(spec.CIDR)
	if err != nil {
		return provider.LaunchReceipt{}, &provider.Failure{Kind: provider.CreationBlocked}
	}
	type subnet struct {
		Name        string `json:"name"`
		CIDR        string `json:"cidr"`
		Description string `json:"description"`
		Gateway     string `json:"gateway_ip"`
		VPC         string `json:"vpc_id"`
		Zone        string `json:"availability_zone,omitempty"`
		IPv6        bool   `json:"ipv6_enable"`
		DHCP        bool   `json:"dhcp_enable"`
	}
	payload := struct {
		Subnet subnet `json:"subnet"`
	}{Subnet: subnet{Name: spec.Name, CIDR: spec.CIDR, Description: description, Gateway: cidr.Addr().Next().String(),
		VPC: spec.ParentID, Zone: spec.Zone, DHCP: true}}
	var result struct {
		RequestID string               `json:"request_id"`
		Subnet    huaweiNetworkReceipt `json:"subnet"`
	}
	if err := c.postHuaweiNetwork(ctx, region, "/v1/"+c.profile.ProjectID+"/subnets", payload, &result); err != nil {
		return provider.LaunchReceipt{}, err
	}
	return c.huaweiNetworkReceipt(result.RequestID, result.Subnet)
}

func (c *Client) findHuaweiSubnet(ctx context.Context, region string, spec provider.NetworkSpec, id, description string) (provider.Object, error) {
	query := url.Values{"limit": {"2"}, "vpc_id": {spec.ParentID}}
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	if id == "" {
		query.Set("description", description)
	} else {
		if !huaweiResourceID.MatchString(id) {
			return provider.Object{}, invalid
		}
		query.Set("id", id)
	}
	endpoint := url.URL{Scheme: "https", Host: "vpc." + region + ".myhuaweicloud.com",
		Path: "/v3/" + c.profile.ProjectID + "/vpc/virsubnets", RawQuery: query.Encode()}
	var result struct {
		Subnets []struct {
			ID, Name, Status, Description string
			Project                       string `json:"project_id"`
			Parent                        string `json:"vpc_id"`
			Zone                          string `json:"zone_id"`
			Tags                          []*tag
			CIDRs                         []struct {
				ID, CIDR string
				Version  string `json:"ip_version"`
				Gateway  string `json:"gateway_ip"`
				DHCP     *bool  `json:"enable_dhcp"`
			} `json:"subnet_cidrs"`
		} `json:"virsubnets"`
		PageInfo *huaweiPageInfo `json:"page_info"`
	}
	if err := c.readHuawei(ctx, endpoint, &result); err != nil {
		return provider.Object{}, err
	}
	if result.Subnets == nil || !result.PageInfo.isCompleteSingleton(len(result.Subnets)) {
		return provider.Object{}, invalid
	}
	if len(result.Subnets) == 0 {
		return provider.Object{}, &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	row := result.Subnets[0]
	if row.Project != c.profile.ProjectID {
		return provider.Object{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	if !huaweiResourceID.MatchString(row.ID) || (id != "" && row.ID != id) || row.Parent != spec.ParentID {
		return provider.Object{}, invalid
	}
	if row.Status == "UNKNOWN" {
		return provider.Object{}, &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	cidr, err := netip.ParsePrefix(spec.CIDR)
	if err != nil || row.Status != "ACTIVE" || row.Name != spec.Name || row.Description != description ||
		(spec.Zone != "" && row.Zone != spec.Zone) || len(row.CIDRs) != 1 {
		return provider.Object{}, invalid
	}
	address := row.CIDRs[0]
	if !huaweiResourceID.MatchString(address.ID) || address.Version != "4" || address.CIDR != spec.CIDR ||
		address.Gateway != cidr.Addr().Next().String() || address.DHCP == nil || !*address.DHCP {
		return provider.Object{}, invalid
	}
	return subnetObject(spec, row.ID, row.Status, row.Tags), nil
}
