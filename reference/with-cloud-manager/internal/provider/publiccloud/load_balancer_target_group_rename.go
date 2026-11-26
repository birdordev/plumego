package publiccloud

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"

	"with-cloud-manager/internal/provider"
)

func (c *Client) prepareLoadBalancerTargetGroupRename(ctx context.Context, scope provider.Scope, target provider.LoadBalancerRename) (func(context.Context) (string, error), error) {
	if !c.profile.AllowLoadBalancerTargetGroupRename {
		return nil, &provider.Failure{Kind: provider.Unsupported}
	}
	name, ready, err := c.readLoadBalancerTargetGroup(ctx, scope, target)
	if provider.IsFailure(err, provider.Absent) || (err == nil && (!ready || name != target.CurrentName)) {
		return nil, &provider.Failure{Kind: provider.PreconditionFailed}
	}
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) (string, error) {
		switch c.name {
		case provider.TencentCVM:
			return c.renameTencentTargetGroup(ctx, scope.Region, target)
		case provider.AlibabaECS:
			return c.renameAlibabaTargetGroup(ctx, scope.Region, target)
		case provider.HuaweiECS:
			return c.renameHuaweiTargetGroup(ctx, scope.Region, target)
		default:
			return "", &provider.Failure{Kind: provider.Unsupported}
		}
	}, nil
}

func (c *Client) observeLoadBalancerTargetGroupRename(ctx context.Context, scope provider.Scope, target provider.LoadBalancerRename, receipt string) (provider.ActionResult, error) {
	name, ready, err := c.readLoadBalancerTargetGroup(ctx, scope, target)
	if err != nil {
		return "", err
	}
	if receipt != "" {
		if !resourceID.MatchString(receipt) {
			return "", &provider.Failure{Kind: provider.InvalidResponse}
		}
		status := provider.ActionSucceeded
		switch target.Product {
		case "alibaba-alb":
			status, err = c.alibabaALBJob(ctx, scope.Region, target.TargetGroupID, receipt, "servergroup", "UpdateServerGroupAttribute")
		case "alibaba-nlb":
			status, err = c.alibabaNLBJob(ctx, scope.Region, receipt)
		}
		if err != nil || status != provider.ActionSucceeded {
			return status, err
		}
	}
	if ready && name == target.Name {
		return provider.ActionSucceeded, nil
	}
	return provider.ActionPending, nil
}

func (c *Client) readLoadBalancerTargetGroup(ctx context.Context, scope provider.Scope, target provider.LoadBalancerRename) (string, bool, error) {
	if !target.ValidFor(c.name) {
		return "", false, &provider.Failure{Kind: provider.Unsupported}
	}
	group, err := c.readExclusiveLoadBalancerGroup(ctx, scope, target.ID, target.Product, target.TargetGroupID)
	return group.name, group.ready, err
}

type exclusiveLoadBalancerGroup struct {
	network, backendType string
	name                 string
	ready                bool
	supportsMemberWeight bool
}

func (c *Client) readExclusiveLoadBalancerGroup(ctx context.Context, scope provider.Scope, parentID, product, groupID string) (exclusiveLoadBalancerGroup, error) {
	if scope.Provider != c.name || scope.RemoteIdentity != c.identity {
		return exclusiveLoadBalancerGroup{}, &provider.Failure{Kind: provider.Unauthorized}
	}
	if scope.Kind != provider.LoadBalancer || !slices.Contains(c.profile.Regions, scope.Region) {
		return exclusiveLoadBalancerGroup{}, &provider.Failure{Kind: provider.Unsupported}
	}
	if c.name == provider.AlibabaECS {
		return c.readAlibabaTargetGroup(ctx, scope.Region, parentID, product, groupID)
	}
	parent, err := c.observeLoadBalancer(ctx, scope, parentID)
	if err != nil {
		return exclusiveLoadBalancerGroup{}, err
	}
	if parent.LoadBalancer.Product != product {
		return exclusiveLoadBalancerGroup{}, invalidLoadBalancerBackend()
	}
	ready := loadBalancerReady(product, parent.Status)
	switch c.name {
	case provider.TencentCVM:
		rows, err := c.tencentLoadBalancerGroupRows(ctx, scope.Region, []string{groupID})
		if err != nil {
			return exclusiveLoadBalancerGroup{}, err
		}
		group := rows[0]
		if group.TargetGroupId != groupID || !group.exclusiveTo(parentID) {
			return exclusiveLoadBalancerGroup{}, invalidLoadBalancerBackend()
		}
		return exclusiveLoadBalancerGroup{name: group.TargetGroupName, ready: ready, supportsMemberWeight: !group.FullListenSwitch, network: matchingBackendNetwork(parent, group.VpcId)}, nil
	case provider.HuaweiECS:
		if !huaweiResourceID.MatchString(groupID) {
			return exclusiveLoadBalancerGroup{}, &provider.Failure{Kind: provider.Unsupported}
		}
		var result huaweiPoolRenameResult
		if err := c.readHuawei(ctx, c.huaweiTargetGroupEndpoint(scope.Region, groupID), &result); err != nil {
			return exclusiveLoadBalancerGroup{}, err
		}
		if !result.matches(c.profile.ProjectID, parentID, groupID) {
			return exclusiveLoadBalancerGroup{}, invalidLoadBalancerBackend()
		}
		return exclusiveLoadBalancerGroup{name: *result.Pool.Name, ready: ready, supportsMemberWeight: huaweiWeightedAlgorithm(result.Pool.Algorithm), network: matchingBackendNetwork(parent, result.Pool.VPC), backendType: result.Pool.Type}, nil
	default:
		return exclusiveLoadBalancerGroup{}, &provider.Failure{Kind: provider.Unsupported}
	}
}

func matchingBackendNetwork(parent provider.Object, groupVPC string) string {
	for _, link := range parent.Links {
		if link.Kind == provider.Network && link.Relation == "in_network" && (groupVPC == "" || groupVPC == link.ID) {
			return link.ID
		}
	}
	return ""
}

func (c *Client) renameTencentTargetGroup(ctx context.Context, region string, target provider.LoadBalancerRename) (string, error) {
	body, err := json.Marshal(struct{ TargetGroupId, TargetGroupName string }{target.TargetGroupID, target.Name})
	if err != nil {
		return "", err
	}
	var result struct{ RequestId string }
	if err := c.tencent(ctx, "clb", region, "ModifyTargetGroupAttribute", "2018-03-17", body, &result); err != nil {
		return "", err
	}
	return loadBalancerRenameReceipt(result.RequestId)
}

func alibabaLoadBalancerGroupProduct(product string) alibabaLBProduct {
	if product == "alibaba-nlb" {
		return alibabaNLB
	}
	return alibabaALB
}

func (c *Client) readAlibabaTargetGroup(ctx context.Context, region, parentID, productName, groupID string) (exclusiveLoadBalancerGroup, error) {
	product := alibabaLoadBalancerGroupProduct(productName)
	rows, err := c.alibabaModernLBGroupRows(ctx, region, product, []string{groupID})
	if err != nil {
		return exclusiveLoadBalancerGroup{}, err
	}
	group := rows[0]
	if group.ServerGroupId != groupID || (group.RegionId != "" && group.RegionId != region) ||
		!slices.Equal(group.RelatedLoadBalancerIds, []string{parentID}) || group.ServerGroupStatus == "" {
		return exclusiveLoadBalancerGroup{}, invalidLoadBalancerBackend()
	}
	return exclusiveLoadBalancerGroup{name: group.ServerGroupName, ready: group.ServerGroupStatus == "Available", supportsMemberWeight: !group.AnyPortEnabled, network: group.VpcId, backendType: group.ServerGroupType}, nil
}

func (c *Client) renameAlibabaTargetGroup(ctx context.Context, region string, target provider.LoadBalancerRename) (string, error) {
	product := alibabaLoadBalancerGroupProduct(target.Product)
	host, version := alibabaModernLBEndpoint(region, product)
	query := url.Values{"ServerGroupId": {target.TargetGroupID}, "ServerGroupName": {target.Name}}
	call := c.alibaba
	if product == alibabaNLB {
		query.Set("RegionId", region)
		call = c.alibabaForm
	}
	var result struct{ Code, JobId, ServerGroupId string }
	if err := call(ctx, host, "UpdateServerGroupAttribute", version, query, &result); err != nil {
		return "", err
	}
	if result.Code != "" {
		return "", cloudFailure(result.Code)
	}
	if (product == alibabaNLB || result.ServerGroupId != "") && result.ServerGroupId != target.TargetGroupID {
		return "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	return loadBalancerRenameReceipt(result.JobId)
}

type huaweiPoolRenameResult struct {
	RequestID string `json:"request_id"`
	Pool      struct {
		VPC           string `json:"vpc_id"`
		Type          string `json:"type"`
		ID            string
		Name          *string
		ProjectID     string                 `json:"project_id"`
		LoadBalancers []huaweiELBResourceRef `json:"loadbalancers"`
		Algorithm     string                 `json:"lb_algorithm"`
	}
}

func (r huaweiPoolRenameResult) matches(project, parentID, groupID string) bool {
	return r.Pool.ID == groupID && r.Pool.Name != nil && r.Pool.ProjectID == project &&
		slices.Equal(r.Pool.LoadBalancers, []huaweiELBResourceRef{{ID: parentID}})
}

func (c *Client) huaweiTargetGroupEndpoint(region, id string) url.URL {
	return url.URL{Scheme: "https", Host: "elb." + region + ".myhuaweicloud.com", Path: "/v3/" + c.profile.ProjectID + "/elb/pools/" + id}
}

func (c *Client) renameHuaweiTargetGroup(ctx context.Context, region string, target provider.LoadBalancerRename) (string, error) {
	body, err := json.Marshal(map[string]map[string]string{"pool": {"name": target.Name}})
	if err != nil {
		return "", err
	}
	endpoint := c.huaweiTargetGroupEndpoint(region, target.TargetGroupID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Auth-Token", c.profile.IAMToken)
	req.GetBody = nil
	var result huaweiPoolRenameResult
	if err := c.request(req, &result); err != nil {
		return "", err
	}
	if !result.matches(c.profile.ProjectID, target.ID, target.TargetGroupID) || *result.Pool.Name != target.Name {
		return "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	return loadBalancerRenameReceipt(result.RequestID)
}
