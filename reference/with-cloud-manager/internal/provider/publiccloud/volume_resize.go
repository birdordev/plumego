package publiccloud

import (
	"context"
	"slices"
	"strings"

	"with-cloud-manager/internal/provider"
)

func (c *Client) checkVolumeResize(scope provider.Scope, change provider.VolumeResize) error {
	if scope.Provider != c.name || scope.RemoteIdentity != c.identity {
		return &provider.Failure{Kind: provider.Unauthorized}
	}
	if scope.Kind != provider.Volume || !slices.Contains(c.profile.Regions, scope.Region) ||
		!change.Valid() || !resourceID.MatchString(change.ID) {
		return &provider.Failure{Kind: provider.Unsupported}
	}
	switch c.name {
	case provider.TencentCVM:
		if strings.HasPrefix(change.ID, "disk-") && len(change.ID) > len("disk-") {
			return nil
		}
	case provider.AlibabaECS:
		if strings.HasPrefix(change.ID, "d-") && len(change.ID) > len("d-") {
			return nil
		}
	case provider.HuaweiECS:
		if huaweiResourceID.MatchString(change.ID) {
			return nil
		}
	}
	return &provider.Failure{Kind: provider.Unsupported}
}

func (c *Client) PrepareVolumeResize(ctx context.Context, scope provider.Scope, change provider.VolumeResize) (func(context.Context) (string, error), error) {
	if err := c.checkVolumeResize(scope, change); err != nil {
		return nil, err
	}
	if !c.profile.AllowVolumeResize {
		return nil, &provider.Failure{Kind: provider.Unsupported}
	}
	evidence, err := c.inspectDetachedVolume(ctx, scope.Region, change.ID)
	if err != nil {
		return nil, err
	}
	if !evidence.safe || !evidence.available || evidence.size == nil || *evidence.size != change.CurrentGiB {
		return nil, &provider.Failure{Kind: provider.PreconditionFailed}
	}
	return func(ctx context.Context) (string, error) {
		switch c.name {
		case provider.TencentCVM:
			return c.resizeTencentDisk(ctx, scope.Region, change)
		case provider.AlibabaECS:
			return c.resizeAlibabaDisk(ctx, scope.Region, change)
		default:
			return c.resizeHuaweiDisk(ctx, scope.Region, change)
		}
	}, nil
}

func (c *Client) ObserveVolumeResize(ctx context.Context, scope provider.Scope, change provider.VolumeResize, receipt string) (provider.ActionResult, error) {
	if err := c.checkVolumeResize(scope, change); err != nil {
		return provider.ActionPending, err
	}
	if c.name == provider.HuaweiECS && receipt != "" {
		state, err := c.observeHuaweiVolumeJob(ctx, scope.Region, receipt, "extendVolume", change.ID, change.TargetGiB)
		if err != nil || state != provider.ActionSucceeded {
			return state, err
		}
	}
	evidence, err := c.inspectDetachedVolume(ctx, scope.Region, change.ID)
	if err != nil {
		return provider.ActionPending, err
	}
	if !evidence.safe || evidence.size == nil || *evidence.size < change.CurrentGiB || *evidence.size > change.TargetGiB {
		return provider.ActionFailed, nil
	}
	if evidence.available && *evidence.size == change.TargetGiB {
		return provider.ActionSucceeded, nil
	}
	return provider.ActionPending, nil
}
