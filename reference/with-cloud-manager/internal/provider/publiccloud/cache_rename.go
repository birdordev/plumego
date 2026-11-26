package publiccloud

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"with-cloud-manager/internal/provider"
)

func (c *Client) PrepareCacheRename(ctx context.Context, scope provider.Scope, change provider.CacheRename) (func(context.Context) (string, error), error) {
	if !c.profile.AllowCacheRename {
		return nil, &provider.Failure{Kind: provider.Unsupported}
	}
	item, err := c.readCacheRenameTarget(ctx, scope, change)
	if provider.IsFailure(err, provider.Absent) {
		return nil, &provider.Failure{Kind: provider.PreconditionFailed}
	}
	if err != nil {
		return nil, err
	}
	if item.Name != change.CurrentName || !cacheReady(change.Product, item.Status) {
		return nil, &provider.Failure{Kind: provider.PreconditionFailed}
	}
	return func(ctx context.Context) (string, error) {
		return c.renameCache(ctx, scope.Region, change)
	}, nil
}

func (c *Client) ObserveCacheRename(ctx context.Context, scope provider.Scope, change provider.CacheRename) (provider.ActionResult, error) {
	item, err := c.readCacheRenameTarget(ctx, scope, change)
	if err != nil {
		return "", err
	}
	if item.Name == change.Name && cacheReady(change.Product, item.Status) {
		return provider.ActionSucceeded, nil
	}
	return provider.ActionPending, nil
}

func (c *Client) readCacheRenameTarget(ctx context.Context, scope provider.Scope, change provider.CacheRename) (provider.Object, error) {
	if scope.Kind != provider.CacheInstance || !change.ValidFor(c.name) {
		return provider.Object{}, &provider.Failure{Kind: provider.Unsupported}
	}
	item, err := c.observeCache(ctx, scope, change.ID)
	if err != nil {
		return provider.Object{}, err
	}
	if item.ID != change.ID || item.Cache == nil || item.Cache.Product != change.Product || item.Cache.Engine != "redis" {
		return provider.Object{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	return item, nil
}

func cacheReady(product, status string) bool {
	switch product {
	case provider.TencentRedis:
		return status == "2"
	case provider.AlibabaTair:
		return strings.EqualFold(status, "normal")
	case provider.HuaweiDCS:
		return strings.EqualFold(status, "running")
	default:
		return false
	}
}

func (c *Client) renameCache(ctx context.Context, region string, change provider.CacheRename) (string, error) {
	var result struct{ Code, RequestId string }
	var err error
	switch c.name {
	case provider.TencentCVM:
		body, marshalErr := json.Marshal(struct {
			Operation                  string
			InstanceIds, InstanceNames []string
		}{"rename", []string{change.ID}, []string{change.Name}})
		if marshalErr != nil {
			return "", marshalErr
		}
		err = c.tencent(ctx, "redis", region, "ModifyInstance", "2018-04-12", body, &result)
	case provider.AlibabaECS:
		fields := url.Values{"InstanceId": {change.ID}, "InstanceName": {change.Name}}
		err = c.alibaba(ctx, alibabaCacheEndpoint(region), "ModifyInstanceAttribute", "2015-01-01", fields, &result)
	case provider.HuaweiECS:
		endpoint := url.URL{Scheme: "https", Host: "dcs." + region + ".myhuaweicloud.com",
			Path: "/v2/" + c.profile.ProjectID + "/instances/" + change.ID}
		return c.putHuaweiResourceName(ctx, endpoint, change.Name, http.StatusNoContent)
	default:
		return "", &provider.Failure{Kind: provider.Unsupported}
	}
	if err != nil {
		return "", err
	}
	if result.Code != "" {
		return "", cloudFailure(result.Code)
	}
	if !resourceID.MatchString(result.RequestId) {
		return "", &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	return result.RequestId, nil
}
