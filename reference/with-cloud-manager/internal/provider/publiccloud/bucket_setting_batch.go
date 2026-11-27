package publiccloud

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"

	"with-cloud-manager/internal/provider"
)

func (c *Client) ReadBucketVersioningBatch(ctx context.Context, scope provider.Scope, targets []provider.BucketSettingTarget) ([]provider.BucketSettingResult[provider.BucketVersioning], error) {
	return readBucketSettingBatch(ctx, c, scope, targets, "versioning", "GetBucketVersioning", c.getBucketVersioning)
}

func (c *Client) ReadBucketEncryptionBatch(ctx context.Context, scope provider.Scope, targets []provider.BucketSettingTarget) ([]provider.BucketSettingResult[provider.BucketEncryption], error) {
	return readBucketSettingBatch(ctx, c, scope, targets, "encryption", "GetEncryptionConfiguration", c.getBucketEncryption)
}

func readBucketSettingBatch[T ~string](ctx context.Context, c *Client, scope provider.Scope, targets []provider.BucketSettingTarget,
	query, action string, read func(*http.Request) (T, error),
) ([]provider.BucketSettingResult[T], error) {
	if scope.Provider != c.name || scope.RemoteIdentity != c.identity {
		return nil, &provider.Failure{Kind: provider.Unauthorized}
	}
	if scope.Kind != provider.ObjectBucket || scope.Region != provider.ObjectBucketGlobalScope || len(targets) > provider.BucketSettingBatchLimit {
		return nil, &provider.Failure{Kind: provider.Unsupported}
	}

	results := make([]provider.BucketSettingResult[T], len(targets))
	eligible := make(map[string]string, len(targets))
	for i, target := range targets {
		name, _, _ := strings.Cut(target.ID, "@")
		if !provider.ValidBucketAssetID(target.ID) || strings.Contains(name, ".") || !slices.Contains(c.profile.Regions, target.Location) {
			results[i].Err = &provider.Failure{Kind: provider.Unsupported}
			continue
		}
		eligible[target.ID] = target.Location
	}
	before, err := c.matchBucketIncarnations(ctx, scope, eligible)
	if err != nil {
		return nil, err
	}
	observed := make(map[string]string, len(before))
	var throttle error
	for i, target := range targets {
		if results[i].Err != nil {
			continue
		}
		if !before[target.ID] {
			results[i].Err = &provider.Failure{Kind: provider.OutcomeUnknown}
			continue
		}
		name, _, _ := strings.Cut(target.ID, "@")
		req, err := c.bucketSettingRequest(ctx, name, target.Location, query, action)
		if err == nil {
			results[i].State, err = read(req)
		}
		if provider.IsFailure(err, provider.Throttled) {
			throttle, results = err, results[:i]
			break
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		results[i].Err = err
		if err == nil {
			observed[target.ID] = target.Location
		}
	}
	after, err := c.matchBucketIncarnations(ctx, scope, observed)
	if err != nil {
		return nil, errors.Join(throttle, err)
	}
	for i, result := range results {
		if result.Err == nil && !after[targets[i].ID] {
			results[i] = provider.BucketSettingResult[T]{Err: &provider.Failure{Kind: provider.OutcomeUnknown}}
		}
	}
	return results, throttle
}

func (c *Client) matchBucketIncarnations(ctx context.Context, scope provider.Scope, targets map[string]string) (map[string]bool, error) {
	matches := make(map[string]bool, len(targets))
	if len(targets) == 0 {
		return matches, nil
	}
	var cursor string
	seen := make(map[string]bool)
	for range bucketObservationPages {
		page, err := c.discoverBuckets(ctx, scope, cursor)
		if err != nil {
			return nil, err
		}
		for _, item := range page.Objects {
			if location, ok := targets[item.ID]; ok && location == item.Bucket.Location {
				matches[item.ID] = true
			}
		}
		if len(matches) == len(targets) || page.Next == "" {
			return matches, nil
		}
		if seen[page.Next] {
			break
		}
		seen[page.Next] = true
		cursor = page.Next
	}
	return nil, &provider.Failure{Kind: provider.InvalidResponse}
}
