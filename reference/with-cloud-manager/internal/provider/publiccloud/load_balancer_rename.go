package publiccloud

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"with-cloud-manager/internal/provider"
)

func (c *Client) PrepareLoadBalancerRename(ctx context.Context, scope provider.Scope, target provider.LoadBalancerRename) (func(context.Context) (string, error), error) {
	if target.TargetGroupID != "" {
		return c.prepareLoadBalancerTargetGroupRename(ctx, scope, target)
	}
	if target.ListenerID != "" {
		return c.prepareLoadBalancerListenerRename(ctx, scope, target)
	}
	if !c.profile.AllowLoadBalancerRename {
		return nil, &provider.Failure{Kind: provider.Unsupported}
	}
	item, err := c.readLoadBalancerRenameTarget(ctx, scope, target)
	if err != nil {
		if provider.IsFailure(err, provider.Absent) {
			err = &provider.Failure{Kind: provider.PreconditionFailed}
		}
		return nil, err
	}
	if item.Name != target.CurrentName || !loadBalancerReady(target.Product, item.Status) {
		return nil, &provider.Failure{Kind: provider.PreconditionFailed}
	}
	return func(ctx context.Context) (string, error) {
		switch c.name {
		case provider.TencentCVM:
			return c.renameTencentLoadBalancer(ctx, scope.Region, target)
		case provider.AlibabaECS:
			return c.renameAlibabaLoadBalancer(ctx, scope.Region, target)
		case provider.HuaweiECS:
			return c.renameHuaweiLoadBalancer(ctx, scope.Region, target)
		default:
			return "", &provider.Failure{Kind: provider.Unsupported}
		}
	}, nil
}

func (c *Client) ObserveLoadBalancerRename(ctx context.Context, scope provider.Scope, target provider.LoadBalancerRename, receipt string) (provider.ActionResult, error) {
	if target.TargetGroupID != "" {
		return c.observeLoadBalancerTargetGroupRename(ctx, scope, target, receipt)
	}
	if target.ListenerID != "" {
		return c.observeLoadBalancerListenerRename(ctx, scope, target, receipt)
	}
	item, err := c.readLoadBalancerRenameTarget(ctx, scope, target)
	if err != nil {
		return "", err
	}
	if receipt != "" {
		if !resourceID.MatchString(receipt) {
			return "", &provider.Failure{Kind: provider.InvalidResponse}
		}
		var status provider.ActionResult
		switch target.Product {
		case "tencent-clb":
			status, err = c.tencentLoadBalancerRenameTask(ctx, scope.Region, receipt)
		case "alibaba-nlb":
			status, err = c.alibabaNLBJob(ctx, scope.Region, receipt)
		default:
			status = provider.ActionSucceeded
		}
		if err != nil || status != provider.ActionSucceeded {
			return status, err
		}
	}
	if item.Name == target.Name && loadBalancerReady(target.Product, item.Status) {
		return provider.ActionSucceeded, nil
	}
	return provider.ActionPending, nil
}

func (c *Client) readLoadBalancerRenameTarget(ctx context.Context, scope provider.Scope, target provider.LoadBalancerRename) (provider.Object, error) {
	if scope.Kind != provider.LoadBalancer || !target.ValidFor(c.name) {
		return provider.Object{}, &provider.Failure{Kind: provider.Unsupported}
	}
	item, err := c.observeLoadBalancer(ctx, scope, target.ID)
	if err != nil {
		return provider.Object{}, err
	}
	if item.LoadBalancer.Product != target.Product {
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return item, nil
}

func loadBalancerReady(product, status string) bool {
	if product == "tencent-clb" {
		return status == "1"
	}
	return strings.EqualFold(status, "active")
}

func (c *Client) renameTencentLoadBalancer(ctx context.Context, region string, target provider.LoadBalancerRename) (string, error) {
	body, err := json.Marshal(struct{ LoadBalancerId, LoadBalancerName string }{target.ID, target.Name})
	if err != nil {
		return "", err
	}
	var result struct{ RequestId string }
	if err := c.tencent(ctx, "clb", region, "ModifyLoadBalancerAttributes", "2018-03-17", body, &result); err != nil {
		return "", err
	}
	return loadBalancerRenameReceipt(result.RequestId)
}

func (c *Client) tencentLoadBalancerRenameTask(ctx context.Context, region, receipt string) (provider.ActionResult, error) {
	body, err := json.Marshal(struct{ TaskId string }{receipt})
	if err != nil {
		return "", err
	}
	var result struct{ Status *int }
	if err := c.tencent(ctx, "clb", region, "DescribeTaskStatus", "2018-03-17", body, &result); err != nil {
		return "", err
	}
	if result.Status != nil {
		switch *result.Status {
		case 0:
			return provider.ActionSucceeded, nil
		case 1:
			return provider.ActionFailed, nil
		case 2:
			return provider.ActionPending, nil
		}
	}
	return "", &provider.Failure{Kind: provider.InvalidResponse}
}

func (c *Client) renameAlibabaLoadBalancer(ctx context.Context, region string, target provider.LoadBalancerRename) (string, error) {
	query := url.Values{"LoadBalancerId": {target.ID}, "LoadBalancerName": {target.Name}}
	host, action, version := alibabaClassicLBEndpoint(region), "SetLoadBalancerName", "2014-05-15"
	call := c.alibaba
	switch target.Product {
	case "alibaba-alb":
		host, version = alibabaModernLBEndpoint(region, alibabaALB)
		action = "UpdateLoadBalancerAttribute"
	case "alibaba-nlb":
		host, version = alibabaModernLBEndpoint(region, alibabaNLB)
		action = "UpdateLoadBalancerAttribute"
		call = c.alibabaForm
	}
	if target.Product != "alibaba-alb" {
		query.Set("RegionId", region)
	}
	var result struct{ Code, RequestId, JobId string }
	if err := call(ctx, host, action, version, query, &result); err != nil {
		return "", err
	}
	if result.Code != "" {
		return "", cloudFailure(result.Code)
	}
	if target.Product == "alibaba-nlb" {
		return loadBalancerRenameReceipt(result.JobId)
	}
	return loadBalancerRenameReceipt(result.RequestId)
}

func (c *Client) alibabaNLBJob(ctx context.Context, region, receipt string) (provider.ActionResult, error) {
	host, version := alibabaModernLBEndpoint(region, alibabaNLB)
	var result struct{ Code, Status string }
	query := url.Values{"RegionId": {region}, "JobId": {receipt}}
	if err := c.alibaba(ctx, host, "GetJobStatus", version, query, &result); err != nil {
		return "", err
	}
	if result.Code != "" {
		return "", cloudFailure(result.Code)
	}
	return alibabaLoadBalancerJobStatus(result.Status)
}

func alibabaLoadBalancerJobStatus(status string) (provider.ActionResult, error) {
	switch strings.ToLower(status) {
	case "succeeded":
		return provider.ActionSucceeded, nil
	case "failed":
		return provider.ActionFailed, nil
	case "processing":
		return provider.ActionPending, nil
	default:
		return "", &provider.Failure{Kind: provider.InvalidResponse}
	}
}

func (c *Client) renameHuaweiLoadBalancer(ctx context.Context, region string, target provider.LoadBalancerRename) (string, error) {
	type update struct {
		Name string `json:"name"`
	}
	body, err := json.Marshal(struct {
		LoadBalancer update `json:"loadbalancer"`
	}{update{Name: target.Name}})
	if err != nil {
		return "", err
	}
	u := url.URL{Scheme: "https", Host: "elb." + region + ".myhuaweicloud.com",
		Path: "/v3/" + c.profile.ProjectID + "/elb/loadbalancers/" + target.ID}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u.String(), bytes.NewReader(body))
	if err != nil {
		return "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Auth-Token", c.profile.IAMToken)
	req.GetBody = nil
	var result struct {
		RequestID    string `json:"request_id"`
		LoadBalancer struct {
			ID, Name  string
			ProjectID string `json:"project_id"`
		} `json:"loadbalancer"`
	}
	if err := c.request(req, &result); err != nil {
		return "", err
	}
	if result.LoadBalancer.ID != target.ID || result.LoadBalancer.Name != target.Name || result.LoadBalancer.ProjectID != c.profile.ProjectID {
		return "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	return loadBalancerRenameReceipt(result.RequestID)
}

func loadBalancerRenameReceipt(receipt string) (string, error) {
	if !resourceID.MatchString(receipt) {
		return "", &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	return receipt, nil
}
