package publiccloud

import (
	"context"
	"regexp"
	"slices"
	"strings"

	"with-cloud-manager/internal/provider"
)

var huaweiDataDevice = regexp.MustCompile(`^/dev/sd[f-p]$`)

type volumeEvidence struct {
	instanceZone, volumeZone                               string
	available, attached, safe, retained, instanceHasVolume bool
	hosts                                                  []string
}

func (e volumeEvidence) matches(instance string, attached bool) bool {
	if !e.safe || e.instanceZone == "" || e.instanceZone != e.volumeZone {
		return false
	}
	if attached {
		return e.attached && e.retained && e.instanceHasVolume && slices.Equal(e.hosts, []string{instance})
	}
	return e.available && !e.instanceHasVolume && len(e.hosts) == 0
}

func (c *Client) checkVolumeChange(scope provider.Scope, change provider.VolumeChange) error {
	if err := c.checkInstanceScope(scope, change.InstanceID); err != nil {
		return err
	}
	volume := change.VolumeID
	ok := false
	switch c.name {
	case provider.TencentCVM:
		ok = strings.HasPrefix(volume, "disk-") && len(volume) > len("disk-") && change.Device == ""
	case provider.AlibabaECS:
		ok = strings.HasPrefix(volume, "d-") && len(volume) > len("d-") && change.Device == ""
	case provider.HuaweiECS:
		ok = huaweiResourceID.MatchString(volume) && (change.Device == "" || (change.Attach && huaweiDataDevice.MatchString(change.Device)))
	}
	if !ok || !resourceID.MatchString(volume) {
		return &provider.Failure{Kind: provider.VolumeBlocked}
	}
	return nil
}

func (c *Client) PrepareVolume(ctx context.Context, scope provider.Scope, change provider.VolumeChange) (func(context.Context) (string, error), error) {
	if err := c.checkVolumeChange(scope, change); err != nil {
		return nil, err
	}
	if !c.profile.AllowVolumeChanges {
		return nil, &provider.Failure{Kind: provider.VolumeBlocked}
	}
	evidence, err := c.volumeEvidence(ctx, scope.Region, change)
	if err != nil {
		return nil, err
	}
	if !evidence.matches(change.InstanceID, !change.Attach) {
		return nil, &provider.Failure{Kind: provider.VolumeBlocked}
	}
	return func(ctx context.Context) (string, error) {
		switch c.name {
		case provider.TencentCVM:
			return c.tencentVolumeChange(ctx, scope.Region, change)
		case provider.AlibabaECS:
			return c.alibabaVolumeChange(ctx, scope.Region, change)
		case provider.HuaweiECS:
			return c.huaweiVolumeChange(ctx, scope.Region, change)
		default:
			return "", &provider.Failure{Kind: provider.Unsupported}
		}
	}, nil
}

func (c *Client) ApplyVolume(ctx context.Context, scope provider.Scope, change provider.VolumeChange) (string, error) {
	send, err := c.PrepareVolume(ctx, scope, change)
	if err != nil {
		return "", err
	}
	return send(ctx)
}

func (c *Client) ObserveVolume(ctx context.Context, scope provider.Scope, change provider.VolumeChange) (bool, error) {
	if err := c.checkVolumeChange(scope, change); err != nil {
		return false, err
	}
	evidence, err := c.volumeEvidence(ctx, scope.Region, change)
	if err != nil {
		return false, err
	}
	return evidence.matches(change.InstanceID, change.Attach), nil
}

func (c *Client) volumeEvidence(ctx context.Context, region string, change provider.VolumeChange) (volumeEvidence, error) {
	switch c.name {
	case provider.TencentCVM:
		return c.tencentVolumeEvidence(ctx, region, change)
	case provider.AlibabaECS:
		return c.alibabaVolumeEvidence(ctx, region, change)
	case provider.HuaweiECS:
		return c.huaweiVolumeEvidence(ctx, region, change)
	default:
		return volumeEvidence{}, &provider.Failure{Kind: provider.Unsupported}
	}
}

func distinctVolumeHosts(hosts []string) []string {
	slices.Sort(hosts)
	return slices.Compact(slices.DeleteFunc(hosts, func(id string) bool { return id == "" }))
}
