package publiccloud

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"

	"with-cloud-manager/internal/provider"
)

const alibabaMemberAttach = "AddServersToServerGroup"

type backendAttachmentEndpoint struct {
	address netip.Addr
	subnet  string
}

func (c *Client) PrepareLoadBalancerBackendAttach(ctx context.Context, scope provider.Scope, target provider.LoadBalancerBackendAttach) (provider.LoadBalancerBackendPlan, error) {
	if !c.profile.AllowLoadBalancerBackendAttach {
		return provider.LoadBalancerBackendPlan{}, &provider.Failure{Kind: provider.Unsupported}
	}
	endpoint, group, err := c.resolveBackendAttachment(ctx, scope, target)
	if err != nil {
		return provider.LoadBalancerBackendPlan{}, err
	}
	if !group.ready {
		return provider.LoadBalancerBackendPlan{}, &provider.Failure{Kind: provider.PreconditionFailed}
	}
	member, err := c.observeBackendAttachment(ctx, scope.Region, target, endpoint)
	if err != nil {
		return provider.LoadBalancerBackendPlan{}, err
	}
	if member.Status != "" {
		return provider.LoadBalancerBackendPlan{}, &provider.Failure{Kind: provider.PreconditionFailed}
	}
	return provider.LoadBalancerBackendPlan{BindingKey: backendAttachmentKey(target, endpoint), Apply: func(ctx context.Context) (string, error) {
		switch c.name {
		case provider.TencentCVM:
			return c.attachTencentBackend(ctx, scope.Region, target, endpoint)
		case provider.AlibabaECS:
			return c.attachAlibabaBackend(ctx, scope.Region, target, endpoint)
		case provider.HuaweiECS:
			return c.attachHuaweiBackend(ctx, scope.Region, target, endpoint)
		default:
			return "", &provider.Failure{Kind: provider.Unsupported}
		}
	}}, nil
}

func (c *Client) ObserveLoadBalancerBackendAttach(ctx context.Context, scope provider.Scope, target provider.LoadBalancerBackendAttach, bindingKey, receipt string) (provider.LoadBalancerBackendAttached, error) {
	endpoint, group, err := c.resolveBackendAttachment(ctx, scope, target)
	if err != nil {
		return provider.LoadBalancerBackendAttached{}, err
	}
	if bindingKey == "" || backendAttachmentKey(target, endpoint) != bindingKey {
		return provider.LoadBalancerBackendAttached{}, &provider.Failure{Kind: provider.PreconditionFailed}
	}
	member, err := c.observeBackendAttachment(ctx, scope.Region, target, endpoint)
	if err != nil {
		return provider.LoadBalancerBackendAttached{}, err
	}
	status, err := c.observeBackendMemberJob(ctx, scope.Region, target.Product, target.GroupID, receipt, alibabaMemberAttach)
	if err != nil || status != provider.ActionSucceeded {
		return provider.LoadBalancerBackendAttached{Status: status}, err
	}
	if !group.ready || member.Status != provider.ActionSucceeded {
		return provider.LoadBalancerBackendAttached{Status: provider.ActionPending}, nil
	}
	return member, nil
}

func backendAttachmentKey(target provider.LoadBalancerBackendAttach, endpoint backendAttachmentEndpoint) string {
	return newLoadBalancerBackendRead(target.Secret).keyFor("attach\x00" + target.Product + "\x00" + target.GroupID + "\x00" +
		target.InstanceID + "\x00" + endpoint.address.String() + "\x00" + endpoint.subnet + "\x00" + strconv.Itoa(target.Port))
}

func (c *Client) resolveBackendAttachment(ctx context.Context, scope provider.Scope, target provider.LoadBalancerBackendAttach) (backendAttachmentEndpoint, exclusiveLoadBalancerGroup, error) {
	if !target.ValidFor(c.name) {
		return backendAttachmentEndpoint{}, exclusiveLoadBalancerGroup{}, &provider.Failure{Kind: provider.Unsupported}
	}
	group, err := c.readExclusiveLoadBalancerGroup(ctx, scope, target.ParentID, target.Product, target.GroupID)
	if err != nil {
		return backendAttachmentEndpoint{}, group, err
	}
	if group.network == "" || !group.supportsMemberWeight || (c.name == provider.AlibabaECS && group.backendType != "Instance") ||
		(c.name == provider.HuaweiECS && group.backendType == "ip") {
		return backendAttachmentEndpoint{}, group, &provider.Failure{Kind: provider.PreconditionFailed}
	}
	var endpoint backendAttachmentEndpoint
	switch c.name {
	case provider.TencentCVM:
		endpoint, err = c.readTencentBackendEndpoint(ctx, scope.Region, target.InstanceID, group.network)
	case provider.AlibabaECS:
		parent, readErr := c.observeLoadBalancer(ctx, scope, target.ParentID)
		if readErr != nil {
			return endpoint, group, readErr
		}
		if parent.LoadBalancer.Product != target.Product || matchingBackendNetwork(parent, group.network) == "" {
			return endpoint, group, &provider.Failure{Kind: provider.PreconditionFailed}
		}
		endpoint, err = c.readAlibabaBackendEndpoint(ctx, scope.Region, target.InstanceID, group.network)
	case provider.HuaweiECS:
		endpoint, err = c.readHuaweiBackendEndpoint(ctx, scope.Region, target.InstanceID, group.network)
	}
	return endpoint, group, err
}

func privateBackendIPv4(values []string) (netip.Addr, error) {
	var candidate netip.Addr
	for _, text := range values {
		address, err := netip.ParseAddr(text)
		if err != nil || address.Zone() != "" {
			return netip.Addr{}, invalidLoadBalancerBackend()
		}
		if !address.Is4() {
			continue
		}
		if !address.IsGlobalUnicast() || candidate.IsValid() {
			return netip.Addr{}, &provider.Failure{Kind: provider.PreconditionFailed}
		}
		candidate = address
	}
	if !candidate.IsValid() {
		return netip.Addr{}, &provider.Failure{Kind: provider.PreconditionFailed}
	}
	return candidate, nil
}

func (c *Client) readTencentBackendEndpoint(ctx context.Context, region, id, vpc string) (backendAttachmentEndpoint, error) {
	if !validLaunchID(id, "ins-") || !validLaunchID(vpc, "vpc-") {
		return backendAttachmentEndpoint{}, &provider.Failure{Kind: provider.PreconditionFailed}
	}
	row, err := c.describeTencentInstance(ctx, region, id)
	if err != nil {
		return backendAttachmentEndpoint{}, err
	}
	if row.VirtualPrivateCloud.VpcId != vpc || row.InstanceState != "RUNNING" {
		return backendAttachmentEndpoint{}, &provider.Failure{Kind: provider.PreconditionFailed}
	}
	addr, err := privateBackendIPv4(row.PrivateIpAddresses)
	return backendAttachmentEndpoint{address: addr}, err
}

func (c *Client) readAlibabaBackendEndpoint(ctx context.Context, region, id, vpc string) (backendAttachmentEndpoint, error) {
	if !validLaunchID(id, "i-") || !validLaunchID(vpc, "vpc-") {
		return backendAttachmentEndpoint{}, &provider.Failure{Kind: provider.PreconditionFailed}
	}
	row, err := c.describeAlibabaInstance(ctx, region, id)
	if err != nil {
		return backendAttachmentEndpoint{}, err
	}
	if row.VpcAttributes.VpcId != vpc || row.Status != "Running" {
		return backendAttachmentEndpoint{}, &provider.Failure{Kind: provider.PreconditionFailed}
	}
	addr, err := privateBackendIPv4(row.VpcAttributes.PrivateIpAddress.IpAddress)
	return backendAttachmentEndpoint{address: addr}, err
}

func (c *Client) readHuaweiBackendEndpoint(ctx context.Context, region, id, vpc string) (backendAttachmentEndpoint, error) {
	if !huaweiResourceID.MatchString(id) || !huaweiResourceID.MatchString(vpc) {
		return backendAttachmentEndpoint{}, &provider.Failure{Kind: provider.PreconditionFailed}
	}
	row, err := c.describeHuaweiInstance(ctx, region, id)
	if err != nil {
		return backendAttachmentEndpoint{}, err
	}
	if row.Metadata.VPC != vpc || row.Status != "ACTIVE" {
		return backendAttachmentEndpoint{}, &provider.Failure{Kind: provider.PreconditionFailed}
	}
	u := url.URL{Scheme: "https", Host: "ecs." + region + ".myhuaweicloud.com", Path: "/v1/" + c.profile.ProjectID + "/cloudservers/" + id + "/os-interface"}
	var result struct {
		Ports []struct {
			ID       string `json:"port_id"`
			State    string `json:"port_state"`
			FixedIPs []struct {
				Address string `json:"ip_address"`
				Subnet  string `json:"subnet_id"`
			} `json:"fixed_ips"`
		} `json:"interfaceAttachments"`
	}
	if err := c.readHuawei(ctx, u, &result); err != nil {
		return backendAttachmentEndpoint{}, err
	}
	var endpoint backendAttachmentEndpoint
	for _, port := range result.Ports {
		if !huaweiResourceID.MatchString(port.ID) {
			return endpoint, invalidLoadBalancerBackend()
		}
		for _, fixedIP := range port.FixedIPs {
			address, err := netip.ParseAddr(fixedIP.Address)
			if err != nil || address.Zone() != "" {
				return endpoint, invalidLoadBalancerBackend()
			}
			if !address.Is4() {
				continue
			}
			if port.State != "ACTIVE" || !address.IsGlobalUnicast() || !huaweiResourceID.MatchString(fixedIP.Subnet) || endpoint.address.IsValid() {
				return endpoint, &provider.Failure{Kind: provider.PreconditionFailed}
			}
			endpoint = backendAttachmentEndpoint{address: address, subnet: fixedIP.Subnet}
		}
	}
	if !endpoint.address.IsValid() {
		return endpoint, &provider.Failure{Kind: provider.PreconditionFailed}
	}
	return endpoint, nil
}

func (c *Client) observeBackendAttachment(ctx context.Context, region string, target provider.LoadBalancerBackendAttach, endpoint backendAttachmentEndpoint) (provider.LoadBalancerBackendAttached, error) {
	reader := newLoadBalancerBackendRead(target.Secret)
	var selected provider.LoadBalancerBackendAttached
	record := func(id, address string, weight *int, ready bool) error {
		if selected.Status != "" {
			return invalidLoadBalancerBackend()
		}
		key, err := reader.endpointKey(id, address, target.Port)
		if err != nil {
			return err
		}
		if ready && weight != nil && *weight != 0 {
			return invalidLoadBalancerBackend()
		}
		selected.Status = provider.ActionPending
		if ready && weight != nil {
			selected.Status, selected.MemberKey = provider.ActionSucceeded, key
		}
		return nil
	}
	switch c.name {
	case provider.TencentCVM:
		rows, err := c.tencentLoadBalancerMemberRows(ctx, region, target.GroupID, reader)
		if err != nil {
			return selected, err
		}
		for _, row := range rows {
			if row.TargetGroupId != target.GroupID {
				return selected, invalidLoadBalancerBackend()
			}
			if row.Port != target.Port {
				continue
			}
			address, err := netip.ParseAddr(row.address())
			if err != nil || address.Zone() != "" {
				return selected, invalidLoadBalancerBackend()
			}
			if row.InstanceId != target.InstanceID && address.Unmap() != endpoint.address {
				continue
			}
			if row.InstanceId != target.InstanceID || address.Unmap() != endpoint.address {
				return selected, invalidLoadBalancerBackend()
			}
			if err := record(row.InstanceId, row.address(), row.Weight, true); err != nil {
				return selected, err
			}
		}
	case provider.AlibabaECS:
		rows, err := c.alibabaLoadBalancerMemberRows(ctx, region, alibabaLoadBalancerGroupProduct(target.Product), target.GroupID, reader)
		if err != nil {
			return selected, err
		}
		for _, row := range rows {
			if row.ServerGroupId != target.GroupID {
				return selected, invalidLoadBalancerBackend()
			}
			if row.Port != target.Port {
				continue
			}
			address, err := netip.ParseAddr(row.ServerIp)
			if err != nil || address.Zone() != "" {
				return selected, invalidLoadBalancerBackend()
			}
			if row.ServerId != target.InstanceID && address.Unmap() != endpoint.address {
				continue
			}
			if row.ServerId != target.InstanceID || row.ServerType != "Ecs" || address.Unmap() != endpoint.address {
				return selected, invalidLoadBalancerBackend()
			}
			if err := record(row.ServerId, row.ServerIp, row.Weight, row.Status == "Available"); err != nil {
				return selected, err
			}
		}
	case provider.HuaweiECS:
		rows, err := c.huaweiLoadBalancerMemberRows(ctx, region, target.GroupID, reader)
		if err != nil {
			return selected, err
		}
		for _, row := range rows {
			if row.Port != target.Port {
				continue
			}
			address, err := netip.ParseAddr(row.Address)
			if err != nil || address.Zone() != "" {
				return selected, invalidLoadBalancerBackend()
			}
			if address.Unmap() != endpoint.address {
				continue
			}
			if row.Subnet != endpoint.subnet {
				return selected, invalidLoadBalancerBackend()
			}
			if err := record(row.ID, "", row.Weight, true); err != nil {
				return selected, err
			}
		}
	}
	return selected, nil
}

func (c *Client) attachTencentBackend(ctx context.Context, region string, target provider.LoadBalancerBackendAttach, endpoint backendAttachmentEndpoint) (string, error) {
	member := struct {
		BindIP       string
		Port, Weight int
	}{endpoint.address.String(), target.Port, 0}
	body, err := json.Marshal(map[string]any{"TargetGroupId": target.GroupID, "TargetGroupInstances": []any{member}})
	if err != nil {
		return "", err
	}
	var response struct{ RequestId string }
	if err := c.tencent(ctx, "clb", region, "RegisterTargetGroupInstances", "2018-03-17", body, &response); err != nil {
		return "", err
	}
	return loadBalancerRenameReceipt(response.RequestId)
}

func (c *Client) attachAlibabaBackend(ctx context.Context, region string, target provider.LoadBalancerBackendAttach, endpoint backendAttachmentEndpoint) (string, error) {
	product := alibabaLoadBalancerGroupProduct(target.Product)
	host, version := alibabaModernLBEndpoint(region, product)
	query := url.Values{"ServerGroupId": {target.GroupID}}
	fields := url.Values{"Servers.1.ServerId": {target.InstanceID}, "Servers.1.ServerType": {"Ecs"},
		"Servers.1.ServerIp": {endpoint.address.String()}, "Servers.1.Port": {strconv.Itoa(target.Port)}, "Servers.1.Weight": {"0"}}
	if product == alibabaNLB {
		fields.Set("ServerGroupId", target.GroupID)
		fields.Set("RegionId", region)
		query = nil
	}
	var response struct{ Code, JobId, ServerGroupId string }
	if err := c.alibabaRPC(ctx, host, alibabaMemberAttach, version, query, fields.Encode(), &response); err != nil {
		return "", err
	}
	if response.Code != "" {
		return "", cloudFailure(response.Code)
	}
	if product == alibabaNLB && response.ServerGroupId != target.GroupID {
		return "", invalidLoadBalancerBackend()
	}
	return loadBalancerRenameReceipt(response.JobId)
}

func (c *Client) attachHuaweiBackend(ctx context.Context, region string, target provider.LoadBalancerBackendAttach, endpoint backendAttachmentEndpoint) (string, error) {
	payload := struct {
		Member struct {
			Address string `json:"address"`
			Subnet  string `json:"subnet_cidr_id"`
			Port    int    `json:"protocol_port"`
			Weight  int    `json:"weight"`
		} `json:"member"`
	}{}
	payload.Member.Address, payload.Member.Subnet, payload.Member.Port = endpoint.address.String(), endpoint.subnet, target.Port
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	u := c.huaweiTargetGroupEndpoint(region, target.GroupID)
	u.Path += "/members"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return "", invalidLoadBalancerBackend()
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Auth-Token", c.profile.IAMToken)
	req.GetBody = nil
	var response struct {
		RequestID string `json:"request_id"`
		Member    huaweiELBMember
	}
	if err := c.request(req, &response, http.StatusCreated); err != nil {
		return "", err
	}
	row := response.Member
	if !huaweiResourceID.MatchString(row.ID) || row.ProjectID != c.profile.ProjectID || row.Address != endpoint.address.String() ||
		row.Subnet != endpoint.subnet || row.Port != target.Port || row.Weight == nil || *row.Weight != 0 {
		return "", invalidLoadBalancerBackend()
	}
	return loadBalancerRenameReceipt(response.RequestID)
}
