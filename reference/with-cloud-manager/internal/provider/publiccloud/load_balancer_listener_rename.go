package publiccloud

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"with-cloud-manager/internal/provider"
)

func (c *Client) prepareLoadBalancerListenerRename(ctx context.Context, scope provider.Scope, target provider.LoadBalancerRename) (func(context.Context) (string, error), error) {
	if !c.profile.AllowLoadBalancerListenerRename {
		return nil, &provider.Failure{Kind: provider.Unsupported}
	}
	name, ready, err := c.readLoadBalancerListener(ctx, scope, target)
	if provider.IsFailure(err, provider.Absent) || (err == nil && (!ready || name != target.CurrentName)) {
		return nil, &provider.Failure{Kind: provider.PreconditionFailed}
	}
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) (string, error) {
		switch c.name {
		case provider.TencentCVM:
			return c.renameTencentListener(ctx, scope.Region, target)
		case provider.AlibabaECS:
			return c.renameAlibabaListener(ctx, scope.Region, target)
		default:
			return c.renameHuaweiListener(ctx, scope.Region, target)
		}
	}, nil
}

func (c *Client) observeLoadBalancerListenerRename(ctx context.Context, scope provider.Scope, target provider.LoadBalancerRename, receipt string) (provider.ActionResult, error) {
	name, ready, err := c.readLoadBalancerListener(ctx, scope, target)
	if err != nil {
		return "", err
	}
	if receipt != "" {
		if !resourceID.MatchString(receipt) {
			return "", &provider.Failure{Kind: provider.InvalidResponse}
		}
		status := provider.ActionSucceeded
		switch target.Product {
		case "tencent-clb":
			status, err = c.tencentLoadBalancerRenameTask(ctx, scope.Region, receipt)
		case "alibaba-alb":
			status, err = c.alibabaALBJob(ctx, scope.Region, target.ListenerID, receipt, "listener", "UpdateListenerAttribute")
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

func (c *Client) readLoadBalancerListener(ctx context.Context, scope provider.Scope, target provider.LoadBalancerRename) (string, bool, error) {
	if scope.Provider != c.name || scope.RemoteIdentity != c.identity {
		return "", false, &provider.Failure{Kind: provider.Unauthorized}
	}
	if scope.Kind != provider.LoadBalancer || !target.ValidFor(c.name) || !slices.Contains(c.profile.Regions, scope.Region) {
		return "", false, &provider.Failure{Kind: provider.Unsupported}
	}
	switch c.name {
	case provider.TencentCVM:
		return c.readTencentListener(ctx, scope.Region, target)
	case provider.AlibabaECS:
		return c.readAlibabaListener(ctx, scope.Region, target)
	default:
		return c.readHuaweiListener(ctx, scope, target)
	}
}

func (c *Client) readTencentListener(ctx context.Context, region string, target provider.LoadBalancerRename) (string, bool, error) {
	if !validLaunchID(target.ListenerID, "lbl-") {
		return "", false, &provider.Failure{Kind: provider.Unsupported}
	}
	rows, next, err := c.tencentLoadBalancerRows(ctx, region, "", target.ID)
	if err != nil {
		return "", false, err
	}
	if next != "" || len(rows) > 1 || (len(rows) == 1 && (rows[0].LoadBalancerId != target.ID || rows[0].Forward == nil || rows[0].Status == nil)) {
		return "", false, &provider.Failure{Kind: provider.InvalidResponse}
	}
	if len(rows) == 0 {
		return "", false, &provider.Failure{Kind: provider.Absent}
	}
	if *rows[0].Forward != 1 {
		return "", false, &provider.Failure{Kind: provider.Unsupported}
	}
	body, err := json.Marshal(struct {
		LoadBalancerId string
		ListenerIds    []string
	}{target.ID, []string{target.ListenerID}})
	if err != nil {
		return "", false, err
	}
	var result struct {
		TotalCount *int
		Listeners  []struct {
			ListenerId   string
			ListenerName *string
		}
	}
	if err := c.tencent(ctx, "clb", region, "DescribeListeners", "2018-03-17", body, &result); err != nil {
		return "", false, err
	}
	if result.TotalCount == nil || *result.TotalCount != len(result.Listeners) || len(result.Listeners) > 1 {
		return "", false, &provider.Failure{Kind: provider.InvalidResponse}
	}
	if len(result.Listeners) == 0 {
		return "", false, &provider.Failure{Kind: provider.Absent}
	}
	item := result.Listeners[0]
	if item.ListenerId != target.ListenerID || item.ListenerName == nil {
		return "", false, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return *item.ListenerName, *rows[0].Status == 1, nil
}

func (c *Client) renameTencentListener(ctx context.Context, region string, target provider.LoadBalancerRename) (string, error) {
	body, err := json.Marshal(struct{ LoadBalancerId, ListenerId, ListenerName string }{target.ID, target.ListenerID, target.Name})
	if err != nil {
		return "", err
	}
	var result struct{ RequestId string }
	if err := c.tencent(ctx, "clb", region, "ModifyListener", "2018-03-17", body, &result); err != nil {
		return "", err
	}
	return loadBalancerRenameReceipt(result.RequestId)
}

func (c *Client) readAlibabaListener(ctx context.Context, region string, target provider.LoadBalancerRename) (string, bool, error) {
	product := alibabaLBProduct(strings.TrimPrefix(target.Product, "alibaba-"))
	if !validAlibabaLoadBalancerListenerID(product, target.ListenerID) || !validLaunchID(target.ID, string(product)+"-") {
		return "", false, &provider.Failure{Kind: provider.Unsupported}
	}
	host, version := alibabaModernLBEndpoint(region, product)
	var result struct {
		Code, ListenerId, LoadBalancerId, RegionId, ListenerStatus string
		ListenerDescription                                        *string
	}
	query := url.Values{"ListenerId": {target.ListenerID}}
	if product == alibabaNLB {
		query.Set("RegionId", region)
	}
	if err := c.alibaba(ctx, host, "GetListenerAttribute", version, query, &result); err != nil {
		return "", false, err
	}
	if result.Code != "" {
		return "", false, cloudFailure(result.Code)
	}
	if result.ListenerId != target.ListenerID || result.LoadBalancerId != target.ID ||
		(product == alibabaNLB && result.RegionId != region) || result.ListenerDescription == nil || result.ListenerStatus == "" {
		return "", false, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return *result.ListenerDescription, result.ListenerStatus == "Running" || result.ListenerStatus == "Stopped", nil
}

func (c *Client) renameAlibabaListener(ctx context.Context, region string, target provider.LoadBalancerRename) (string, error) {
	product := alibabaLBProduct(strings.TrimPrefix(target.Product, "alibaba-"))
	host, version := alibabaModernLBEndpoint(region, product)
	query := url.Values{"ListenerId": {target.ListenerID}, "ListenerDescription": {target.Name}}
	call := c.alibaba
	if product == alibabaNLB {
		query.Set("RegionId", region)
		call = c.alibabaForm
	}
	var result struct{ Code, JobId string }
	if err := call(ctx, host, "UpdateListenerAttribute", version, query, &result); err != nil {
		return "", err
	}
	if result.Code != "" {
		return "", cloudFailure(result.Code)
	}
	return loadBalancerRenameReceipt(result.JobId)
}

func (c *Client) alibabaALBJob(ctx context.Context, region, id, receipt, resourceType, action string) (provider.ActionResult, error) {
	host, version := alibabaModernLBEndpoint(region, alibabaALB)
	query := url.Values{"JobIds.1": {receipt}, "ResourceIds.1": {id}, "ResourceType": {resourceType}, "ApiName": {action}}
	var result struct {
		Code, NextToken string
		Jobs            []struct{ Id, ApiName, ResourceId, ResourceType, Status string }
	}
	if err := c.alibaba(ctx, host, "ListAsynJobs", version, query, &result); err != nil {
		return "", err
	}
	if result.Code != "" {
		return "", cloudFailure(result.Code)
	}
	if result.NextToken != "" || len(result.Jobs) > 1 {
		return "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	if len(result.Jobs) == 0 {
		return provider.ActionPending, nil
	}
	job := result.Jobs[0]
	if job.Id != receipt || job.ApiName != action || job.ResourceId != id || job.ResourceType != resourceType {
		return "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	return alibabaLoadBalancerJobStatus(job.Status)
}

type huaweiListenerRenameResult struct {
	RequestID string `json:"request_id"`
	Listener  struct {
		ID            string
		Name          *string
		ProjectID     string                `json:"project_id"`
		LoadBalancers []struct{ ID string } `json:"loadbalancers"`
	} `json:"listener"`
}

func (c *Client) readHuaweiListener(ctx context.Context, scope provider.Scope, target provider.LoadBalancerRename) (string, bool, error) {
	if !resourceID.MatchString(target.ListenerID) {
		return "", false, &provider.Failure{Kind: provider.Unsupported}
	}
	item, err := c.readLoadBalancerRenameTarget(ctx, scope, target)
	if err != nil {
		return "", false, err
	}
	result, err := c.huaweiListenerRequest(ctx, scope.Region, http.MethodGet, target, nil)
	if err != nil {
		return "", false, err
	}
	return *result.Listener.Name, loadBalancerReady(target.Product, item.Status), nil
}

func (c *Client) renameHuaweiListener(ctx context.Context, region string, target provider.LoadBalancerRename) (string, error) {
	body, err := json.Marshal(map[string]map[string]string{"listener": {"name": target.Name}})
	if err != nil {
		return "", err
	}
	result, err := c.huaweiListenerRequest(ctx, region, http.MethodPut, target, body)
	if err != nil {
		return "", err
	}
	if *result.Listener.Name != target.Name {
		return "", &provider.Failure{Kind: provider.InvalidResponse}
	}
	return loadBalancerRenameReceipt(result.RequestID)
}

func (c *Client) huaweiListenerRequest(ctx context.Context, region, method string, target provider.LoadBalancerRename, body []byte) (huaweiListenerRenameResult, error) {
	var result huaweiListenerRenameResult
	u := url.URL{Scheme: "https", Host: "elb." + region + ".myhuaweicloud.com", Path: "/v3/" + c.profile.ProjectID + "/elb/listeners/" + target.ListenerID}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(body))
	if err != nil {
		return result, &provider.Failure{Kind: provider.InvalidResponse}
	}
	req.Header.Set("X-Auth-Token", c.profile.IAMToken)
	if method == http.MethodPut {
		req.Header.Set("Content-Type", "application/json")
		req.GetBody = nil
	}
	if err := c.request(req, &result); err != nil {
		return result, err
	}
	item := result.Listener
	if item.ID != target.ListenerID || item.Name == nil || item.ProjectID != c.profile.ProjectID ||
		len(item.LoadBalancers) != 1 || item.LoadBalancers[0].ID != target.ID {
		return result, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return result, nil
}
