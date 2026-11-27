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
	ObserveInstanceRetirement(context.Context, Scope, InstanceRetirement, string) (ActionResult, error)
}

var tencentRetirementInstance = regexp.MustCompile(`^ins-[A-Za-z0-9]{1,64}$`)
var tencentRetirementDisk = regexp.MustCompile(`^disk-[A-Za-z0-9]{1,64}$`)
var alibabaRetirementInstance = regexp.MustCompile(`^i-[A-Za-z0-9]{1,64}$`)
var alibabaRetirementDisk = regexp.MustCompile(`^d-[A-Za-z0-9]{1,64}$`)

func (target InstanceRetirement) Valid() bool {
	return target.ValidFor(TencentCVM) || target.ValidFor(AlibabaECS) || target.ValidFor(HuaweiECS)
}

func (target InstanceRetirement) ValidFor(vendor string) bool {
	switch vendor {
	case TencentCVM:
		return tencentRetirementInstance.MatchString(target.InstanceID) && tencentRetirementDisk.MatchString(target.SystemDiskID)
	case AlibabaECS:
		return alibabaRetirementInstance.MatchString(target.InstanceID) && alibabaRetirementDisk.MatchString(target.SystemDiskID)
	case HuaweiECS:
		return target.InstanceID != target.SystemDiskID && snapshotEVSID.MatchString(target.InstanceID) && snapshotEVSID.MatchString(target.SystemDiskID)
	default:
		return false
	}
}
