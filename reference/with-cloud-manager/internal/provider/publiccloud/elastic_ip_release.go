package publiccloud

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"with-cloud-manager/internal/provider"
)

func (c *Client) PrepareElasticIPRelease(ctx context.Context, scope provider.Scope, target provider.ElasticIPTarget) (provider.ElasticIPRelease, error) {
	if !c.profile.AllowElasticIPRelease {
		return nil, &provider.Failure{Kind: provider.DeletionDisabled}
	}
	if err := c.checkElasticIPTarget(scope, target); err != nil {
		return nil, err
	}
	state, err := c.readElasticIPState(ctx, scope.Region, target)
	if err != nil {
		return nil, err
	}
	if !state.releasable {
		return nil, &provider.Failure{Kind: provider.DeletionBlocked}
	}
	return func(ctx context.Context) (string, error) {
		return c.releaseElasticIP(ctx, scope.Region, target.ID)
	}, nil
}

func (c *Client) ObserveElasticIPRelease(ctx context.Context, scope provider.Scope, target provider.ElasticIPTarget) (provider.ActionResult, error) {
	if err := c.checkElasticIPTarget(scope, target); err != nil {
		return provider.ActionPending, err
	}
	state, err := c.readElasticIPState(ctx, scope.Region, target)
	if err != nil || state.status != "" {
		return provider.ActionPending, err
	}
	return provider.ActionSucceeded, nil
}

func (c *Client) checkElasticIPTarget(scope provider.Scope, target provider.ElasticIPTarget) error {
	if scope.Provider != c.name || scope.RemoteIdentity != c.identity {
		return &provider.Failure{Kind: provider.Unauthorized}
	}
	validID := validLaunchID(target.ID, "eip-")
	if c.name == provider.HuaweiECS {
		validID = huaweiResourceID.MatchString(target.ID)
	}
	if scope.Kind != provider.ElasticIP || !slices.Contains(c.profile.Regions, scope.Region) || !validID || !target.ValidIPv4() {
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	return nil
}

type elasticIPState struct {
	status, instance, port, network                string
	unbound, bindingReady, preservesIP, releasable bool
}

func (c *Client) readElasticIPState(ctx context.Context, region string, target provider.ElasticIPTarget) (elasticIPState, error) {
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	switch c.name {
	case provider.TencentCVM:
		list, err := c.readTencentElasticIPs(ctx, region, tencentElasticIPQuery{Limit: 2, AddressIds: []string{target.ID}})
		if err != nil {
			return elasticIPState{}, err
		}
		rows := list.AddressSet
		if len(rows) > 1 || list.TotalCount == nil || *list.TotalCount != len(rows) {
			return elasticIPState{}, invalid
		}
		if len(rows) == 0 {
			return elasticIPState{}, nil
		}
		row := rows[0]
		if row.AddressId != target.ID || row.AddressIp != target.Address || row.AddressType != "EIP" || row.AddressStatus == "" {
			return elasticIPState{}, invalid
		}
		state := elasticIPState{status: row.AddressStatus,
			unbound:      row.AddressStatus == "UNBIND" && row.InstanceId == "" && row.NetworkInterfaceId == "",
			bindingReady: row.IsArrears != nil && !*row.IsArrears && row.IsBlocked != nil && !*row.IsBlocked,
			preservesIP:  row.CascadeRelease != nil && !*row.CascadeRelease,
		}
		if row.AddressStatus == "BIND" && row.InstanceType == "CVM" && validLaunchID(row.InstanceId, "ins-") {
			state.instance = row.InstanceId
		}
		state.releasable = state.unbound && state.bindingReady && row.BandwidthPackageId == "" && row.InternetChargeType == tencentElasticIPCharge
		return state, nil
	case provider.AlibabaECS:
		query := url.Values{"AllocationId": {target.ID}, "PageNumber": {"1"}, "PageSize": {"10"}}
		list, err := c.readAlibabaElasticIPs(ctx, region, query)
		if err != nil {
			return elasticIPState{}, err
		}
		rows := list.EipAddresses.EipAddress
		if next, err := list.next(1, 10, len(rows)); err != nil || next != "" || len(rows) > 1 {
			return elasticIPState{}, invalid
		}
		if len(rows) == 0 {
			return elasticIPState{}, nil
		}
		row := rows[0]
		if row.RegionId != region {
			return elasticIPState{}, &provider.Failure{Kind: provider.Unauthorized}
		}
		if row.AllocationId != target.ID || row.IpAddress != target.Address || row.Status == "" {
			return elasticIPState{}, invalid
		}
		state := elasticIPState{status: row.Status,
			unbound: row.Status == "Available" && row.InstanceId == "" && row.VpcId == "",
			bindingReady: row.ServiceManaged != nil && *row.ServiceManaged == 0 &&
				row.SegmentInstanceId == "" && row.BusinessStatus == "Normal",
			preservesIP: true,
		}
		if row.Status == "InUse" && row.InstanceType == "EcsInstance" && strings.TrimSpace(row.InstanceRegionId) == region && validLaunchID(row.InstanceId, "i-") {
			state.instance = row.InstanceId
		}
		state.releasable = state.unbound && state.bindingReady && row.DeletionProtection != nil && !*row.DeletionProtection &&
			row.BandwidthPackageId == "" && row.ChargeType == "PostPaid"
		return state, nil
	default:
		query := url.Values{"id": {target.ID}, "fields": {"id", "project_id", "type", "ip_version", "status", "public_ip_address",
			"public_ipv6_address", "associate_instance_id", "associate_instance_type", "vnic", "bandwidth", "billing_info", "lock_status"}}
		list, err := c.readHuaweiElasticIPs(ctx, region, query)
		if err != nil {
			return elasticIPState{}, err
		}
		if list.Page.Next != "" || len(list.Rows) > 1 {
			return elasticIPState{}, invalid
		}
		if len(list.Rows) == 0 {
			return elasticIPState{}, nil
		}
		row := list.Rows[0]
		if row.Project != c.profile.ProjectID {
			return elasticIPState{}, &provider.Failure{Kind: provider.Unauthorized}
		}
		if row.ID != target.ID || row.IPv4 != target.Address || row.Type != "EIP" || row.IPVersion != 4 || row.IPv6 != "" || row.Status == "" {
			return elasticIPState{}, invalid
		}
		state := elasticIPState{status: row.Status,
			unbound:      row.Status == "DOWN" && row.AssociatedID == "" && row.AssociatedType == "" && row.Vnic == nil,
			bindingReady: row.LockStatus == "", preservesIP: true,
		}
		if row.Status == "ACTIVE" && row.AssociatedType == "PORT" && row.Vnic != nil && row.AssociatedID == row.Vnic.PortID &&
			huaweiResourceID.MatchString(row.Vnic.PortID) && huaweiResourceID.MatchString(row.Vnic.DeviceID) &&
			huaweiResourceID.MatchString(row.Vnic.VpcID) && strings.HasPrefix(row.Vnic.DeviceOwner, "compute:") {
			state.instance, state.port, state.network = row.Vnic.DeviceID, row.Vnic.PortID, row.Vnic.VpcID
		}
		state.releasable = state.unbound && state.bindingReady && row.BillingInfo == "" && row.Bandwidth != nil &&
			row.Bandwidth.BillingInfo == "" && row.Bandwidth.ShareType == "PER"
		return state, nil
	}
}

func (c *Client) releaseElasticIP(ctx context.Context, region, id string) (string, error) {
	switch c.name {
	case provider.TencentCVM:
		body, err := json.Marshal(struct{ AddressIds []string }{[]string{id}})
		if err != nil {
			return "", err
		}
		var result struct{ RequestId string }
		if err := c.tencent(ctx, "vpc", region, "ReleaseAddresses", "2017-03-12", body, &result); err != nil {
			return "", err
		}
		return operationReceipt(result.RequestId)
	case provider.AlibabaECS:
		query := url.Values{"RegionId": {region}, "AllocationId": {id}}
		var result struct{ RequestId, Code string }
		if err := c.alibaba(ctx, alibabaVPCHost(region), "ReleaseEipAddress", "2016-04-28", query, &result); err != nil {
			return "", err
		}
		if result.Code != "" {
			return "", cloudFailure(result.Code)
		}
		return operationReceipt(result.RequestId)
	default:
		endpoint := c.huaweiElasticIPEndpoint(region)
		endpoint.Path += "/" + id
		req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint.String(), nil)
		if err != nil {
			return "", &provider.Failure{Kind: provider.InvalidResponse}
		}
		req.Header.Set("X-Auth-Token", c.profile.IAMToken)
		return "", c.request(req, nil, http.StatusNoContent)
	}
}
