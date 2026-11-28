package publiccloud

import (
	"bytes"
	"context"
	"io"
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
		!provider.ValidBucketSettingAssetID(id) || !slices.Contains(c.profile.Regions, region) {
		return "", &provider.Failure{Kind: provider.Unsupported}
	}
	if err := c.checkBucketIncarnation(ctx, scope, id, region); err != nil {
		return "", err
	}
	req, err := c.bucketSettingRequest(ctx, http.MethodGet, name, region, query, obsAction, nil)
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

func prepareBucketSetting[T ~string](ctx context.Context, c *Client, scope provider.Scope, id, location string,
	expected T, read func(context.Context, provider.Scope, string, string) (T, error),
	query, obsAction string, body []byte, successStatus int,
) (func(context.Context) (string, error), error) {
	current, err := read(ctx, scope, id, location)
	if provider.IsFailure(err, provider.Absent) {
		return nil, &provider.Failure{Kind: provider.PreconditionFailed}
	}
	if err != nil {
		return nil, err
	}
	if current != expected {
		return nil, &provider.Failure{Kind: provider.PreconditionFailed}
	}
	name, _, _ := strings.Cut(id, "@")
	return func(ctx context.Context) (string, error) {
		req, err := c.bucketSettingRequest(ctx, http.MethodPut, name, location, query, obsAction, bytes.NewReader(body))
		if err != nil {
			return "", err
		}
		req.GetBody = nil
		if err := c.checkBucketIncarnation(ctx, scope, id, location); err != nil {
			return "", err
		}
		return c.putBucketSetting(req, successStatus)
	}, nil
}

func (c *Client) putBucketSetting(req *http.Request, successStatus int) (string, error) {
	res, err := c.openObjectStorageXML(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != successStatus {
		return "", cloudHTTPFailure(res)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 4097))
	if err != nil || len(body) > 4096 || len(bytes.TrimSpace(body)) != 0 {
		return "", &provider.Failure{Kind: provider.OutcomeUnknown}
	}
	header := "X-Cos-Request-Id"
	switch c.name {
	case provider.AlibabaECS:
		header = "X-Oss-Request-Id"
	case provider.HuaweiECS:
		header = "X-Obs-Request-Id"
	}
	if receipt := res.Header.Get(header); receipt != "" {
		return receipt, nil
	}
	return "", &provider.Failure{Kind: provider.OutcomeUnknown}
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

func (c *Client) bucketSettingRequest(ctx context.Context, method, name, region, query, obsAction string, body io.Reader) (*http.Request, error) {
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
	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/xml")
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
