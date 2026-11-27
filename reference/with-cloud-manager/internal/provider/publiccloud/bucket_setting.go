package publiccloud

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"with-cloud-manager/internal/provider"
)

func readBucketSetting[T ~string](ctx context.Context, c *Client, scope provider.Scope, id, region, query, obsAction string, read func(*http.Request) (T, error)) (T, error) {
	if scope.Provider != c.name || scope.RemoteIdentity != c.identity {
		return "", &provider.Failure{Kind: provider.Unauthorized}
	}
	name, _, _ := strings.Cut(id, "@")
	if scope.Kind != provider.ObjectBucket || scope.Region != provider.ObjectBucketGlobalScope ||
		!provider.ValidBucketAssetID(id) || strings.Contains(name, ".") || !slices.Contains(c.profile.Regions, region) {
		return "", &provider.Failure{Kind: provider.Unsupported}
	}
	if err := c.checkBucketIncarnation(ctx, scope, id, region); err != nil {
		return "", err
	}
	req, err := c.bucketSettingRequest(ctx, name, region, query, obsAction)
	if err != nil {
		return "", err
	}
	state, err := read(req)
	if err != nil {
		return "", err
	}
	if err := c.checkBucketIncarnation(ctx, scope, id, region); err != nil {
		return "", err
	}
	return state, nil
}

func (c *Client) checkBucketIncarnation(ctx context.Context, scope provider.Scope, id, location string) error {
	item, err := c.observeBucket(ctx, scope, id)
	if err != nil {
		return err
	}
	if item.Bucket.Location != location {
		return &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	return nil
}

func (c *Client) bucketSettingRequest(ctx context.Context, name, region, query, obsAction string) (*http.Request, error) {
	endpoint := url.URL{Scheme: "https", Path: "/", RawQuery: query}
	switch c.name {
	case provider.TencentCVM:
		endpoint.Host = name + ".cos." + region + ".myqcloud.com"
	case provider.AlibabaECS:
		endpoint.Host = name + ".oss-" + region + ".aliyuncs.com"
	case provider.HuaweiECS:
		endpoint.Host = name + ".obs." + region + ".myhuaweicloud.com"
	default:
		return nil, &provider.Failure{Kind: provider.Unsupported}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	switch c.name {
	case provider.TencentCVM:
		c.signCOS(req, query, now)
	case provider.AlibabaECS:
		c.signOSS(req, region, "/"+name+"/", now)
	case provider.HuaweiECS:
		key, err := c.obsScopedCredential(ctx, obsAction, name)
		if err != nil {
			return nil, err
		}
		signOBS(req, key, "/"+name+"/?"+query, time.Now().UTC())
	}
	return req, nil
}
