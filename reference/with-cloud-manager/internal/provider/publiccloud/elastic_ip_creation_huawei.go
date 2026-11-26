package publiccloud

import (
	"context"
	"net/url"

	"with-cloud-manager/internal/provider"
)

func (c *Client) createHuaweiElasticIP(ctx context.Context, region string, spec provider.NetworkSpec, marker string) (provider.LaunchReceipt, error) {
	type request struct {
		Alias       string                   `json:"alias"`
		Description string                   `json:"description"`
		Type        string                   `json:"type"`
		IPVersion   int                      `json:"ip_version"`
		Pool        string                   `json:"publicip_pool_name"`
		Bandwidth   huaweiElasticIPBandwidth `json:"bandwidth"`
	}
	payload := struct {
		PublicIP request `json:"publicip"`
	}{request{
		Alias: spec.Name, Description: marker, Type: "EIP", IPVersion: 4, Pool: spec.AddressPool,
		Bandwidth: huaweiElasticIPBandwidth{Size: spec.BandwidthMbps, ShareType: "PER", ChargeMode: "traffic", Name: spec.Name},
	}}
	var result struct {
		RequestID string          `json:"request_id"`
		PublicIP  huaweiElasticIP `json:"publicip"`
	}
	endpoint := c.huaweiElasticIPEndpoint(region)
	if err := c.postHuaweiNetwork(ctx, region, endpoint.Path, payload, &result); err != nil {
		return provider.LaunchReceipt{}, err
	}
	return c.huaweiNetworkReceipt(result.RequestID, huaweiNetworkReceipt{ID: result.PublicIP.ID, Tenant: result.PublicIP.Project})
}

func (c *Client) readCreatedHuaweiElasticIP(ctx context.Context, region, id, marker string) (huaweiElasticIP, error) {
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	if id != "" {
		if !huaweiResourceID.MatchString(id) {
			return huaweiElasticIP{}, invalid
		}
		endpoint := c.huaweiElasticIPEndpoint(region)
		endpoint.Path += "/" + id
		var result struct {
			PublicIP huaweiElasticIP `json:"publicip"`
		}
		if err := c.readHuawei(ctx, endpoint, &result); err != nil {
			return huaweiElasticIP{}, err
		}
		if result.PublicIP.ID != id {
			return huaweiElasticIP{}, invalid
		}
		return result.PublicIP, nil
	}
	query := url.Values{"description": {marker}}
	result, err := c.readHuaweiElasticIPs(ctx, region, query)
	if err != nil {
		return huaweiElasticIP{}, err
	}
	if len(result.Rows) == 0 {
		return huaweiElasticIP{}, &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	if len(result.Rows) != 1 {
		return huaweiElasticIP{}, invalid
	}
	if result.Page.Next != "" {
		query.Set("marker", result.Page.Next)
		tail, err := c.readHuaweiElasticIPs(ctx, region, query)
		if err != nil {
			return huaweiElasticIP{}, err
		}
		if len(tail.Rows) != 0 || tail.Page.Next != "" {
			return huaweiElasticIP{}, invalid
		}
	}
	return result.Rows[0], nil
}

func (c *Client) findHuaweiElasticIP(ctx context.Context, region string, spec provider.NetworkSpec, id, marker string) (provider.Object, error) {
	row, err := c.readCreatedHuaweiElasticIP(ctx, region, id, marker)
	if err != nil {
		return provider.Object{}, err
	}
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	if row.Project != c.profile.ProjectID {
		return provider.Object{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	if !huaweiResourceID.MatchString(row.ID) || row.Description != marker {
		return provider.Object{}, invalid
	}
	if row.Status == "PENDING_CREATE" || row.Status == "NOTIFYING" {
		return provider.Object{}, &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	if row.Status != "DOWN" || row.Type != "EIP" || row.IPVersion != 4 || row.IPv6 != "" ||
		row.Alias != spec.Name || row.Pool != spec.AddressPool || row.BillingInfo != "" ||
		row.AssociatedID != "" || row.AssociatedType != "" || row.Vnic != nil ||
		row.Bandwidth == nil || row.Bandwidth.Size != spec.BandwidthMbps || row.Bandwidth.ChargeMode != "traffic" ||
		row.Bandwidth.ShareType != "PER" || row.Bandwidth.BillingInfo != "" {
		return provider.Object{}, invalid
	}
	return observedIPv4Allocation(row.ID, row.Alias, row.Status, huaweiElasticIPTags(row.Tags), row.IPv4)
}
