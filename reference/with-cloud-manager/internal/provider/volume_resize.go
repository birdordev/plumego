package provider

import "context"

type VolumeResize struct {
	ID         string
	CurrentGiB int
	TargetGiB  int
}

func (change VolumeResize) Valid() bool {
	return change.CurrentGiB > 0 && change.TargetGiB > change.CurrentGiB && change.TargetGiB <= MaxPublicVolumeGiB
}

type VolumeResizer interface {
	PrepareVolumeResize(context.Context, Scope, VolumeResize) (func(context.Context) (string, error), error)
	ObserveVolumeResize(context.Context, Scope, VolumeResize, string) (ActionResult, error)
}
