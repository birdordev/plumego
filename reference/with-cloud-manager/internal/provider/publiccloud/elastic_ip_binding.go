package publiccloud

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/netip"
	"net/url"

	"with-cloud-manager/internal/provider"
)

func (c *Client) checkElasticIPBinding(scope provider.Scope, target provider.ElasticIPBinding) error {
	if err := c.checkElasticIPTarget(scope, target.ElasticIPTarget); err != nil {
		return err
	}
	valid := target.PortID == "" && validLaunchID(target.InstanceID, "ins-")
	switch c.name {
	case provider.AlibabaECS:
		valid = target.PortID == "" && validLaunchID(target.InstanceID, "i-")
	case provider.HuaweiECS:
		valid = huaweiResourceID.MatchString(target.InstanceID) && huaweiResourceID.MatchString(target.PortID)
	}
	if !valid || !resourceID.MatchString(target.ClientToken) || len(target.ClientToken) > 64 {
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	return nil
}

func (c *Client) PrepareElasticIPBinding(ctx context.Context, scope provider.Scope, target provider.ElasticIPBinding) (func(context.Context) (string, error), error) {
	if !c.profile.AllowElasticIPBinding {
		return nil, &provider.Failure{Kind: provider.Unsupported}
	}
	if err := c.checkElasticIPBinding(scope, target); err != nil {
		return nil, err
	}
	network, err := c.verifyElasticIPInstance(ctx, scope.Region, target)
	if err != nil {
		return nil, err
	}
	state, err := c.readElasticIPState(ctx, scope.Region, target.ElasticIPTarget)
	if err != nil {
		return nil, err
	}
	if !state.bindingReady || !state.preservesIP ||
		(target.Bind && !state.unbound) || (!target.Bind && !state.boundTo(target)) ||
		(!target.Bind && network != "" && network != state.network) {
		return nil, &provider.Failure{Kind: provider.DeletionBlocked}
	}
	return func(ctx context.Context) (string, error) {
		return c.changeElasticIPBinding(ctx, scope.Region, target)
	}, nil
}

func (state elasticIPState) boundTo(target provider.ElasticIPBinding) bool {
	return state.instance == target.InstanceID && state.port == target.PortID
}

func (c *Client) ObserveElasticIPBinding(ctx context.Context, scope provider.Scope, target provider.ElasticIPBinding) (provider.Object, error) {
	if err := c.checkElasticIPBinding(scope, target); err != nil {
		return provider.Object{}, err
	}
	state, err := c.readElasticIPState(ctx, scope.Region, target.ElasticIPTarget)
	if err != nil {
		return provider.Object{}, err
	}
	matchesInstance := state.boundTo(target)
	if (target.Bind && matchesInstance) || (!target.Bind && state.unbound) {
		object := provider.Object{ID: target.ID, Status: state.status, PublicAddresses: []string{target.Address}}
		if target.Bind {
			object.Link(provider.Instance, target.InstanceID, "bound_to")
			if state.network != "" {
				object.Link(provider.Network, state.network, "in_network")
			}
		}
		return object, nil
	}
	switch state.status {
	case "BINDING", "UNBINDING", "Associating", "Unassociating", "PENDING_UPDATE":
		return provider.Object{}, &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	if state.unbound || matchesInstance {
		return provider.Object{}, &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
}

func (c *Client) verifyElasticIPInstance(ctx context.Context, region string, target provider.ElasticIPBinding) (string, error) {
	blocked := &provider.Failure{Kind: provider.DeletionBlocked}
	switch c.name {
	case provider.TencentCVM:
		row, err := c.describeTencentInstance(ctx, region, target.InstanceID)
		if err != nil {
			return "", err
		}
		if (row.InstanceState != "RUNNING" && row.InstanceState != "STOPPED") ||
			(target.Bind && len(row.PublicIpAddresses) != 0) {
			return "", blocked
		}
		return "", nil
	case provider.AlibabaECS:
		row, err := c.describeAlibabaInstance(ctx, region, target.InstanceID)
		if err != nil {
			return "", err
		}
		if (row.Status != "Running" && row.Status != "Stopped") || !validLaunchID(row.VpcAttributes.VpcId, "vpc-") ||
			len(row.OperationLocks.LockReason) != 0 ||
			(target.Bind && (len(row.PublicIpAddress.IpAddress) != 0 || row.EipAddress.AllocationId != "" || row.EipAddress.IpAddress != "")) {
			return "", blocked
		}
		return "", nil
	default:
		return c.verifyHuaweiElasticIPPort(ctx, region, target)
	}
}

func (c *Client) verifyHuaweiElasticIPPort(ctx context.Context, region string, target provider.ElasticIPBinding) (string, error) {
	instance, err := c.describeHuaweiInstance(ctx, region, target.InstanceID)
	if err != nil {
		return "", err
	}
	blocked := &provider.Failure{Kind: provider.DeletionBlocked}
	if instance.Locked == nil || *instance.Locked || (instance.Status != "ACTIVE" && instance.Status != "SHUTOFF") ||
		!huaweiResourceID.MatchString(instance.Metadata.VPC) {
		return "", blocked
	}
	endpoint := url.URL{Scheme: "https", Host: "ecs." + region + ".myhuaweicloud.com",
		Path: "/v1/" + c.profile.ProjectID + "/cloudservers/" + instance.ID + "/os-interface"}
	var result struct {
		Ports []struct {
			ID       string `json:"port_id"`
			Status   string `json:"port_state"`
			FixedIPs []struct {
				Address string `json:"ip_address"`
			} `json:"fixed_ips"`
		} `json:"interfaceAttachments"`
	}
	if err := c.readHuawei(ctx, endpoint, &result); err != nil {
		return "", err
	}
	matches, ipv4 := 0, 0
	for _, port := range result.Ports {
		if port.ID != target.PortID {
			continue
		}
		if port.Status != "ACTIVE" {
			return "", blocked
		}
		matches++
		for _, ip := range port.FixedIPs {
			address, err := netip.ParseAddr(ip.Address)
			if err != nil {
				return "", &provider.Failure{Kind: provider.InvalidResponse}
			}
			if address.Is4() && address.IsGlobalUnicast() {
				ipv4++
			}
		}
	}
	if matches != 1 || ipv4 != 1 {
		return "", blocked
	}
	if target.Bind {
		list, err := c.readHuaweiElasticIPs(ctx, region, url.Values{"vnic.port_id": {target.PortID}, "fields": {"id"}})
		if err != nil {
			return "", err
		}
		if len(list.Rows) != 0 || list.Page.Next != "" {
			return "", blocked
		}
	}
	return instance.Metadata.VPC, nil
}

func (c *Client) changeElasticIPBinding(ctx context.Context, region string, target provider.ElasticIPBinding) (string, error) {
	switch c.name {
	case provider.TencentCVM:
		action := "DisassociateAddress"
		input := struct {
			AddressId  string
			InstanceId string `json:",omitempty"`
		}{AddressId: target.ID}
		if target.Bind {
			action, input.InstanceId = "AssociateAddress", target.InstanceID
		}
		body, err := json.Marshal(input)
		if err != nil {
			return "", err
		}
		var result struct{ RequestId string }
		if err := c.tencent(ctx, "vpc", region, action, "2017-03-12", body, &result); err != nil {
			return "", err
		}
		return operationReceipt(result.RequestId)
	case provider.AlibabaECS:
		action := "UnassociateEipAddress"
		if target.Bind {
			action = "AssociateEipAddress"
		}
		query := url.Values{"RegionId": {region}, "AllocationId": {target.ID}, "InstanceType": {"EcsInstance"},
			"InstanceId": {target.InstanceID}, "ClientToken": {target.ClientToken}}
		var result struct{ RequestId, Code string }
		if err := c.alibaba(ctx, alibabaVPCHost(region), action, "2016-04-28", query, &result); err != nil {
			return "", err
		}
		if result.Code != "" {
			return "", cloudFailure(result.Code)
		}
		return operationReceipt(result.RequestId)
	default:
		return c.changeHuaweiElasticIPBinding(ctx, region, target)
	}
}

func (c *Client) changeHuaweiElasticIPBinding(ctx context.Context, region string, target provider.ElasticIPBinding) (string, error) {
	endpoint := c.huaweiElasticIPEndpoint(region)
	endpoint.Path += "/" + target.ID
	var body []byte
	if target.Bind {
		endpoint.Path += "/associate-instance"
		var err error
		body, err = json.Marshal(map[string]map[string]string{"publicip": {
			"associate_instance_type": "PORT", "associate_instance_id": target.PortID,
		}})
		if err != nil {
			return "", err
		}
	} else {
		endpoint.Path += "/disassociate-instance"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	if target.Bind {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Auth-Token", c.profile.IAMToken)
	var result struct {
		RequestID string          `json:"request_id"`
		PublicIP  huaweiElasticIP `json:"publicip"`
	}
	if err := c.request(req, &result); err != nil {
		return "", err
	}
	if result.PublicIP.Project != c.profile.ProjectID || result.PublicIP.ID != target.ID || result.PublicIP.IPv4 != target.Address {
		return "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	return operationReceipt(result.RequestID)
}
