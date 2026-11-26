package provider

import (
	"context"
	"regexp"
)

type InstanceRetirement struct {
	InstanceID   string
	SystemDiskID string
}

type InstanceRetirer interface {
	PrepareInstanceRetirement(context.Context, Scope, InstanceRetirement) (func(context.Context) (string, error), error)
	ObserveInstanceRetirement(context.Context, Scope, string) (ActionResult, error)
}

var tencentRetirementInstance = regexp.MustCompile(`^ins-[A-Za-z0-9]{1,64}$`)
var tencentRetirementDisk = regexp.MustCompile(`^disk-[A-Za-z0-9]{1,64}$`)

func (target InstanceRetirement) ValidFor(vendor string) bool {
	return vendor == TencentCVM && tencentRetirementInstance.MatchString(target.InstanceID) &&
		tencentRetirementDisk.MatchString(target.SystemDiskID)
}
