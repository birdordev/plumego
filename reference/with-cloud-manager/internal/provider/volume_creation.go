package provider

import (
	"context"
	"regexp"
	"strings"
)

const MaxPublicVolumeGiB = 32768

var volumeZone = regexp.MustCompile(`^[a-z][a-z0-9-]{2,63}$`)

type VolumeSpec struct {
	Name      string `json:"name"`
	Zone      string `json:"zone"`
	DiskType  string `json:"disk_type"`
	SizeGiB   int    `json:"size_gib"`
	Encrypted bool   `json:"encrypted"`
}

func (s VolumeSpec) ValidFor(vendor, region string) bool {
	return len(s.Name) <= 60 && validVolumePlacement(s, vendor, region) &&
		(vendor != AlibabaECS || s.SizeGiB >= 20)
}

func validVolumePlacement(s VolumeSpec, vendor, region string) bool {
	if !snapshotName.MatchString(s.Name) || !volumeZone.MatchString(s.Zone) ||
		!strings.HasPrefix(s.Zone, region) || s.Zone == region || s.SizeGiB < 10 || s.SizeGiB > MaxPublicVolumeGiB {
		return false
	}
	switch vendor {
	case TencentCVM:
		return s.DiskType == "CLOUD_PREMIUM" || s.DiskType == "CLOUD_SSD" || s.DiskType == "CLOUD_BSSD"
	case AlibabaECS:
		return s.DiskType == "cloud_essd" || s.DiskType == "cloud_ssd" || s.DiskType == "cloud_efficiency"
	case HuaweiECS:
		return s.DiskType == "SAS" || s.DiskType == "SSD" || s.DiskType == "GPSSD"
	default:
		return false
	}
}

type VolumeCreator interface {
	PrepareVolumeCreation(context.Context, Scope, VolumeSpec, string) (func(context.Context) (LaunchReceipt, error), error)
	FindCreatedVolume(context.Context, Scope, VolumeSpec, string, string) (Object, error)
}
