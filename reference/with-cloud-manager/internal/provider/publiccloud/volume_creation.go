package publiccloud

import (
	"context"
	"slices"

	"with-cloud-manager/internal/provider"
)

const volumeCreationTag = "plumego-cmp-volume"

func (c *Client) checkVolumeCreationScope(scope provider.Scope, spec provider.VolumeSpec, token string) error {
	if scope.Provider != c.name || scope.RemoteIdentity != c.identity {
		return &provider.Failure{Kind: provider.Unauthorized}
	}
	if scope.Kind != provider.Volume || !slices.Contains(c.profile.Regions, scope.Region) ||
		!launchTokenPattern.MatchString(token) || !spec.ValidFor(c.name, scope.Region) {
		return &provider.Failure{Kind: provider.CreationBlocked}
	}
	return nil
}

func (c *Client) PrepareVolumeCreation(_ context.Context, scope provider.Scope, spec provider.VolumeSpec, token string) (func(context.Context) (provider.LaunchReceipt, error), error) {
	if err := c.checkVolumeCreationScope(scope, spec, token); err != nil {
		return nil, err
	}
	if spec.SizeGiB > c.profile.VolumeCreateMaxGiB ||
		(c.name == provider.HuaweiECS && spec.Encrypted && !huaweiResourceID.MatchString(c.profile.HuaweiVolumeKMSKeyID)) {
		return nil, &provider.Failure{Kind: provider.CreationBlocked}
	}
	return c.prepareVolumeOrder(scope, volumeOrder{spec: spec, tagKey: volumeCreationTag}, token), nil
}

func (c *Client) FindCreatedVolume(ctx context.Context, scope provider.Scope, spec provider.VolumeSpec, token, id string) (provider.Object, error) {
	if err := c.checkVolumeCreationScope(scope, spec, token); err != nil {
		return provider.Object{}, err
	}
	return c.findProvisionedVolume(ctx, scope, volumeLookup{
		id: id, order: volumeOrder{spec: spec, tagKey: volumeCreationTag, marker: volumeOrderMarker(scope, volumeCreationTag, token)},
	})
}
