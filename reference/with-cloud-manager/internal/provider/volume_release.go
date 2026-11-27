package provider

import "context"

type VolumeRelease struct {
	ID      string
	SizeGiB int
}

func (target VolumeRelease) ValidFor(vendor string) bool {
	return target.SizeGiB > 0 && validPublicVolumeID(vendor, target.ID)
}

type VolumeReleaser interface {
	PrepareVolumeRelease(context.Context, Scope, VolumeRelease) (func(context.Context) (string, error), error)
	ObserveVolumeRelease(context.Context, Scope, VolumeRelease, string) (ActionResult, error)
}
