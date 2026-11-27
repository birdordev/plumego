package publiccloud

import (
	"context"
	"slices"

	"with-cloud-manager/internal/provider"
)

const restoreIntentTag = "plumego-cmp-restore"

func (c *Client) checkSnapshotRestoreScope(scope provider.Scope, spec provider.SnapshotRestoreSpec, token string) error {
	if scope.Provider != c.name || scope.RemoteIdentity != c.identity {
		return &provider.Failure{Kind: provider.Unauthorized}
	}
	if scope.Kind != provider.Volume || !slices.Contains(c.profile.Regions, scope.Region) ||
		!launchTokenPattern.MatchString(token) || !spec.ValidFor(c.name, scope.Region) {
		return &provider.Failure{Kind: provider.CreationBlocked}
	}
	return nil
}

func (c *Client) PrepareSnapshotRestore(ctx context.Context, scope provider.Scope, spec provider.SnapshotRestoreSpec, token string) (func(context.Context) (provider.LaunchReceipt, error), error) {
	if err := c.checkSnapshotRestoreScope(scope, spec, token); err != nil {
		return nil, err
	}
	if spec.SizeGiB > c.profile.SnapshotRestoreMaxGiB {
		return nil, &provider.Failure{Kind: provider.CreationBlocked}
	}
	state, err := c.readSnapshotState(ctx, scope.Region, spec.SnapshotTarget)
	if err != nil {
		return nil, err
	}
	if !state.ready || state.size <= 0 || state.size > int64(spec.SizeGiB) ||
		state.encrypted == nil || *state.encrypted != spec.Encrypted ||
		(c.name == provider.HuaweiECS && state.zone != "" && state.zone != spec.Zone) {
		return nil, &provider.Failure{Kind: provider.CreationBlocked}
	}
	return c.prepareVolumeOrder(scope, volumeOrder{
		spec: spec.Volume(), tagKey: restoreIntentTag, snapshot: spec.ID,
	}, token), nil
}
