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

const (
	alibabaMemberChange = "UpdateServerGroupServersAttribute"
	alibabaMemberDetach = "RemoveServersFromServerGroup"
)

type backendMemberRead struct {
	weight         int
	ready, present bool
	bindingKey     string
	setWeight      func(context.Context, int) (string, error)
	detach         func(context.Context) (string, error)
}

func (c *Client) PrepareLoadBalancerBackendWeight(ctx context.Context, scope provider.Scope, target provider.LoadBalancerBackendWeight) (func(context.Context) (string, error), error) {
	if !c.profile.AllowLoadBalancerBackendWeight || !target.ValidFor(c.name) {
		return nil, &provider.Failure{Kind: provider.Unsupported}
	}
	found, err := c.readLoadBalancerBackendMember(ctx, scope, target.LoadBalancerBackendTarget, "")
	if provider.IsFailure(err, provider.Absent) || (err == nil && (!found.present || !found.ready || found.weight != target.Current)) {
		return nil, &provider.Failure{Kind: provider.PreconditionFailed}
	}
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) (string, error) {
		return found.setWeight(ctx, target.Desired)
	}, nil
}

func (c *Client) ObserveLoadBalancerBackendWeight(ctx context.Context, scope provider.Scope, target provider.LoadBalancerBackendWeight, receipt string) (provider.ActionResult, error) {
	if !target.ValidFor(c.name) {
		return "", &provider.Failure{Kind: provider.Unsupported}
	}
	found, err := c.readLoadBalancerBackendMember(ctx, scope, target.LoadBalancerBackendTarget, "")
	if err != nil {
		return "", err
	}
	if status, err := c.observeBackendMemberJob(ctx, scope.Region, target.Product, target.GroupID, receipt, alibabaMemberChange); err != nil || status != provider.ActionSucceeded {
		return status, err
	}
	if found.present && found.ready && found.weight == target.Desired {
		return provider.ActionSucceeded, nil
	}
	return provider.ActionPending, nil
}

func (c *Client) observeBackendMemberJob(ctx context.Context, region string, product, groupID, receipt, alibabaAction string) (provider.ActionResult, error) {
	if receipt == "" {
		return provider.ActionSucceeded, nil
	}
	if !resourceID.MatchString(receipt) {
		return "", invalidLoadBalancerBackend()
	}
	switch product {
	case "tencent-clb":
		return c.tencentLoadBalancerRenameTask(ctx, region, receipt)
	case "alibaba-alb":
		return c.alibabaALBJob(ctx, region, groupID, receipt, "servergroup", alibabaAction)
	case "alibaba-nlb":
		return c.alibabaNLBJob(ctx, region, receipt)
	}
	return provider.ActionSucceeded, nil
}

func (c *Client) readLoadBalancerBackendMember(ctx context.Context, scope provider.Scope, target provider.LoadBalancerBackendTarget, bindingKey string) (backendMemberRead, error) {
	if !target.ValidFor(c.name) {
		return backendMemberRead{}, &provider.Failure{Kind: provider.Unsupported}
	}
	group, err := c.readExclusiveLoadBalancerGroup(ctx, scope, target.ParentID, target.Product, target.GroupID)
	if err != nil {
		return backendMemberRead{}, err
	}
	if !group.supportsMemberWeight {
		return backendMemberRead{}, &provider.Failure{Kind: provider.PreconditionFailed}
	}
	reader := newLoadBalancerBackendRead(target.Secret)
	var found backendMemberRead
	switch c.name {
	case provider.TencentCVM:
		found, err = c.readTencentBackendMember(ctx, scope.Region, target, bindingKey, reader)
	case provider.AlibabaECS:
		found, err = c.readAlibabaBackendMember(ctx, scope.Region, target, bindingKey, reader)
	case provider.HuaweiECS:
		found, err = c.readHuaweiBackendMember(ctx, scope.Region, target, bindingKey, reader)
	default:
		err = &provider.Failure{Kind: provider.Unsupported}
	}
	found.ready = (!found.present || found.ready) && group.ready
	return found, err
}

func (c *Client) readTencentBackendMember(ctx context.Context, region string, target provider.LoadBalancerBackendTarget, bindingKey string, reader *loadBalancerBackendRead) (backendMemberRead, error) {
	rows, err := c.tencentLoadBalancerMemberRows(ctx, region, target.GroupID, reader)
	if err != nil {
		return backendMemberRead{}, err
	}
	var found *tencentLBTargetGroupMember
	selectors := make(map[netip.AddrPort]int, len(rows))
	for _, row := range rows {
		if row.TargetGroupId != target.GroupID {
			return backendMemberRead{}, invalidLoadBalancerBackend()
		}
		key, err := reader.endpointKey(row.InstanceId, row.address(), row.Port)
		if err != nil {
			return backendMemberRead{}, err
		}
		addr, err := netip.ParseAddr(row.address())
		var selector netip.AddrPort
		if err == nil && row.Port > 0 && row.Port <= 65535 {
			selector = netip.AddrPortFrom(addr.Unmap(), uint16(row.Port))
			selectors[selector]++
		}
		if bindingKey != "" && !selector.IsValid() {
			return backendMemberRead{}, invalidLoadBalancerBackend()
		}
		if bindingKey != "" {
			if reader.keyFor("tencent-binding\x00"+selector.String()) == bindingKey {
				return backendMemberRead{present: true, ready: true}, nil
			}
			continue
		}
		if key == target.MemberKey {
			if found != nil || !row.editableWeight() || !selector.IsValid() {
				return backendMemberRead{}, invalidLoadBalancerBackend()
			}
			found = &row
		}
	}
	if found == nil {
		return backendMemberRead{}, nil
	}
	address, err := netip.ParseAddr(found.address())
	if err != nil || selectors[netip.AddrPortFrom(address.Unmap(), uint16(found.Port))] != 1 {
		return backendMemberRead{}, invalidLoadBalancerBackend()
	}
	selector := netip.AddrPortFrom(address.Unmap(), uint16(found.Port))
	send := func(ctx context.Context, action string, weight *int) (string, error) {
		member := struct {
			BindIP string
			Port   int
			Weight *int `json:",omitempty"`
		}{selector.Addr().String(), found.Port, weight}
		payload := map[string]any{"TargetGroupId": target.GroupID, "TargetGroupInstances": []any{member}}
		body, err := json.Marshal(payload)
		if err != nil {
			return "", err
		}
		var result struct{ RequestId string }
		if err := c.tencent(ctx, "clb", region, action, "2018-03-17", body, &result); err != nil {
			return "", err
		}
		return loadBalancerRenameReceipt(result.RequestId)
	}
	return backendMemberRead{weight: *found.Weight, ready: true, present: true, bindingKey: reader.keyFor("tencent-binding\x00" + selector.String()),
		setWeight: func(ctx context.Context, weight int) (string, error) {
			return send(ctx, "ModifyTargetGroupInstancesWeight", &weight)
		},
		detach: func(ctx context.Context) (string, error) { return send(ctx, "DeregisterTargetGroupInstances", nil) },
	}, nil
}

func (c *Client) readAlibabaBackendMember(ctx context.Context, region string, target provider.LoadBalancerBackendTarget, bindingKey string, reader *loadBalancerBackendRead) (backendMemberRead, error) {
	product := alibabaLoadBalancerGroupProduct(target.Product)
	rows, err := c.alibabaLoadBalancerMemberRows(ctx, region, product, target.GroupID, reader)
	if err != nil {
		return backendMemberRead{}, err
	}
	selectorKey := func(row alibabaLBServer) string {
		id := row.ServerId
		if row.ServerType == "Ip" {
			if addr, err := netip.ParseAddr(id); err == nil && addr.Zone() == "" {
				id = addr.Unmap().String()
			}
		}
		return reader.keyFor("alibaba-binding\x00" + row.ServerType + "\x00" + id + "\x00" + strconv.Itoa(row.Port))
	}
	selectors := make(map[string]int, len(rows))
	var found *alibabaLBServer
	for _, row := range rows {
		if row.ServerGroupId != target.GroupID {
			return backendMemberRead{}, invalidLoadBalancerBackend()
		}
		key, err := reader.endpointKey(row.ServerId, row.ServerIp, row.Port)
		if err != nil {
			return backendMemberRead{}, err
		}
		selector := selectorKey(row)
		selectors[selector]++
		if bindingKey != "" {
			if row.ServerType == "" || row.ServerId == "" {
				return backendMemberRead{}, invalidLoadBalancerBackend()
			}
			if selector == bindingKey {
				return backendMemberRead{present: true, ready: true}, nil
			}
			continue
		}
		if key == target.MemberKey {
			if found != nil || !row.editableWeight() {
				return backendMemberRead{}, invalidLoadBalancerBackend()
			}
			found = &row
		}
	}
	if found == nil {
		return backendMemberRead{}, nil
	}
	selector := selectorKey(*found)
	if found.Port > 65535 || selectors[selector] != 1 || found.Status == "" {
		return backendMemberRead{}, invalidLoadBalancerBackend()
	}
	send := func(ctx context.Context, action string, weight *int) (string, error) {
		host, version := alibabaModernLBEndpoint(region, product)
		fields := url.Values{"Servers.1.ServerId": {found.ServerId}, "Servers.1.ServerType": {found.ServerType}, "Servers.1.Port": {strconv.Itoa(found.Port)}}
		if found.ServerIp != "" && (action == alibabaMemberChange || found.ServerType == "Eni") {
			fields.Set("Servers.1.ServerIp", found.ServerIp)
		}
		if weight != nil {
			fields.Set("Servers.1.Weight", strconv.Itoa(*weight))
		}
		query := url.Values{"ServerGroupId": {target.GroupID}}
		var form string
		switch {
		case product == alibabaNLB:
			fields.Set("ServerGroupId", target.GroupID)
			fields.Set("RegionId", region)
			query = nil
			form = fields.Encode()
		case action == alibabaMemberDetach:
			for key, values := range fields {
				query[key] = values
			}
		default:
			form = fields.Encode()
		}
		var result struct{ Code, JobId, ServerGroupId string }
		if err := c.alibabaRPC(ctx, host, action, version, query, form, &result); err != nil {
			return "", err
		}
		if result.Code != "" {
			return "", cloudFailure(result.Code)
		}
		if action == alibabaMemberDetach && product == alibabaNLB && result.ServerGroupId != target.GroupID {
			return "", invalidLoadBalancerBackend()
		}
		return loadBalancerRenameReceipt(result.JobId)
	}
	return backendMemberRead{weight: *found.Weight, ready: found.Status == "Available", present: true, bindingKey: selector,
		setWeight: func(ctx context.Context, weight int) (string, error) { return send(ctx, alibabaMemberChange, &weight) },
		detach:    func(ctx context.Context) (string, error) { return send(ctx, alibabaMemberDetach, nil) },
	}, nil
}

func (c *Client) readHuaweiBackendMember(ctx context.Context, region string, target provider.LoadBalancerBackendTarget, bindingKey string, reader *loadBalancerBackendRead) (backendMemberRead, error) {
	rows, err := c.huaweiLoadBalancerMemberRows(ctx, region, target.GroupID, reader)
	if err != nil {
		return backendMemberRead{}, err
	}
	var found *huaweiELBMember
	ids := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if _, present := ids[row.ID]; present {
			return backendMemberRead{}, invalidLoadBalancerBackend()
		}
		ids[row.ID] = struct{}{}
		key, err := reader.endpointKey(row.ID, "", row.Port)
		if err != nil {
			return backendMemberRead{}, err
		}
		if bindingKey != "" {
			if reader.keyFor("huawei-binding\x00"+row.ID) == bindingKey {
				return backendMemberRead{present: true, ready: true}, nil
			}
			continue
		}
		if key == target.MemberKey {
			if found != nil || row.Weight == nil || row.Port < 1 || row.Port > 65535 {
				return backendMemberRead{}, invalidLoadBalancerBackend()
			}
			found = &row
		}
	}
	if found == nil {
		return backendMemberRead{}, nil
	}
	endpoint := c.huaweiTargetGroupEndpoint(region, target.GroupID)
	endpoint.Path += "/members/" + found.ID
	return backendMemberRead{weight: *found.Weight, ready: true, present: true, bindingKey: reader.keyFor("huawei-binding\x00" + found.ID),
		detach: func(ctx context.Context) (string, error) {
			req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint.String(), nil)
			if err != nil {
				return "", invalidLoadBalancerBackend()
			}
			req.Header.Set("X-Auth-Token", c.profile.IAMToken)
			return "", c.request(req, nil, http.StatusNoContent)
		},
		setWeight: func(ctx context.Context, weight int) (string, error) {
			body, err := json.Marshal(map[string]map[string]int{"member": {"weight": weight}})
			if err != nil {
				return "", err
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint.String(), bytes.NewReader(body))
			if err != nil {
				return "", invalidLoadBalancerBackend()
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Auth-Token", c.profile.IAMToken)
			req.GetBody = nil
			var result struct {
				RequestID string `json:"request_id"`
				Member    huaweiELBMember
			}
			if err := c.request(req, &result); err != nil {
				return "", err
			}
			member := result.Member
			if member.ID != found.ID || member.ProjectID != c.profile.ProjectID || member.Port != found.Port ||
				member.Weight == nil || *member.Weight != weight {
				return "", invalidLoadBalancerBackend()
			}
			return loadBalancerRenameReceipt(result.RequestID)
		}}, nil
}
