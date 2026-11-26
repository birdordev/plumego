package provider

import (
	"context"
	"regexp"
	"strings"
)

type SnapshotDetails struct {
	Class   string `json:"class,omitempty"`
	SizeGiB int64  `json:"size_gib,omitempty"`
}

func (o Object) ValidSnapshot() bool {
	if o.Snapshot == nil || o.Snapshot.SizeGiB < 0 || len(o.Snapshot.Class) > 64 ||
		strings.ContainsRune(o.Snapshot.Class, 0) || o.Tags != nil || len(o.PublicAddresses) != 0 || len(o.Links) > 2 {
		return false
	}
	zone := false
	for index, link := range o.Links {
		if link.Kind == Volume && link.Relation == "snapshot_of" && index == 0 {
			continue
		}
		if link.Kind != Zone || link.Relation != "in_zone" || zone {
			return false
		}
		zone = true
	}
	return true
}

type SnapshotSpec struct {
	Name          string `json:"name"`
	VolumeID      string `json:"volume_id"`
	AllowAttached bool   `json:"allow_attached,omitempty"`
}

var (
	snapshotName     = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]{1,63}$`)
	snapshotVolumeID = regexp.MustCompile(`^[a-zA-Z0-9]{1,120}$`)
	snapshotEVSID    = regexp.MustCompile(`^[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}$`)
)

func (s SnapshotSpec) ValidFor(vendor string) bool {
	name := strings.ToLower(s.Name)
	if !snapshotName.MatchString(s.Name) || strings.HasPrefix(name, "autobk_snapshot_") ||
		strings.HasPrefix(name, "manualbk_snapshot_") {
		return false
	}
	switch vendor {
	case TencentCVM:
		suffix, ok := strings.CutPrefix(s.VolumeID, "disk-")
		return ok && snapshotVolumeID.MatchString(suffix)
	case AlibabaECS:
		suffix, ok := strings.CutPrefix(s.VolumeID, "d-")
		return ok && snapshotVolumeID.MatchString(suffix)
	case HuaweiECS:
		return snapshotEVSID.MatchString(s.VolumeID)
	default:
		return false
	}
}

type SnapshotCreator interface {
	PrepareSnapshotCreation(context.Context, Scope, SnapshotSpec, string) (func(context.Context) (LaunchReceipt, error), error)
	FindCreatedSnapshot(context.Context, Scope, SnapshotSpec, string, string) (Object, error)
}

type SnapshotTarget struct {
	ID, VolumeID, Class string
}

func (s SnapshotTarget) ValidFor(vendor string) bool {
	switch vendor {
	case TencentCVM:
		snapshot, snapshotOK := strings.CutPrefix(s.ID, "snap-")
		volume, volumeOK := strings.CutPrefix(s.VolumeID, "disk-")
		return s.Class == "PRIVATE_SNAPSHOT" && snapshotOK && volumeOK &&
			snapshotVolumeID.MatchString(snapshot) && snapshotVolumeID.MatchString(volume)
	case AlibabaECS:
		snapshot, snapshotOK := strings.CutPrefix(s.ID, "s-")
		volume, volumeOK := strings.CutPrefix(s.VolumeID, "d-")
		class := strings.ToLower(s.Class)
		return (class == "standard" || class == "flash" || class == "archive") && snapshotOK && volumeOK &&
			snapshotVolumeID.MatchString(snapshot) && snapshotVolumeID.MatchString(volume)
	case HuaweiECS:
		return (s.Class == "standard" || s.Class == "flash") && snapshotEVSID.MatchString(s.ID) && snapshotEVSID.MatchString(s.VolumeID)
	default:
		return false
	}
}

type SnapshotDeleter interface {
	PrepareSnapshotDeletion(context.Context, Scope, SnapshotTarget) (func(context.Context) (string, error), error)
	ObserveSnapshotDeletion(context.Context, Scope, SnapshotTarget) (ActionResult, error)
}

type SnapshotRestoreSpec struct {
	SnapshotTarget
	Name, Zone, DiskType string
	SizeGiB              int
	Encrypted            bool
}

var restoreZone = regexp.MustCompile(`^[a-z][a-z0-9-]{2,63}$`)

func (s SnapshotRestoreSpec) ValidFor(vendor, region string) bool {
	if !s.SnapshotTarget.ValidFor(vendor) || !snapshotName.MatchString(s.Name) || !restoreZone.MatchString(s.Zone) ||
		!strings.HasPrefix(s.Zone, region) || s.Zone == region || s.SizeGiB < 10 || s.SizeGiB > 32768 {
		return false
	}
	switch vendor {
	case TencentCVM:
		return s.DiskType == "CLOUD_PREMIUM" || s.DiskType == "CLOUD_SSD" || s.DiskType == "CLOUD_BSSD"
	case AlibabaECS:
		return !strings.EqualFold(s.Class, "archive") &&
			(s.DiskType == "cloud_essd" || s.DiskType == "cloud_ssd" || s.DiskType == "cloud_efficiency")
	case HuaweiECS:
		return s.DiskType == "SAS" || s.DiskType == "SSD" || s.DiskType == "GPSSD"
	default:
		return false
	}
}

type SnapshotRestorer interface {
	PrepareSnapshotRestore(context.Context, Scope, SnapshotRestoreSpec, string) (func(context.Context) (LaunchReceipt, error), error)
	FindRestoredVolume(context.Context, Scope, SnapshotRestoreSpec, string, string) (Object, error)
}

func (s SnapshotTarget) ValidForRollback(vendor string) bool {
	return s.ValidFor(vendor) && !strings.EqualFold(s.Class, "archive")
}

type SnapshotRollbackOperator interface {
	PrepareSnapshotRollback(context.Context, Scope, SnapshotTarget) (func(context.Context) (string, error), error)
	ObserveSnapshotRollback(context.Context, Scope, SnapshotTarget, string) (ActionResult, error)
}
