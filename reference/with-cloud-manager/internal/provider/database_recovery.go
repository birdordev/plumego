package provider

import (
	"context"
	"regexp"
	"time"
)

type DatabaseRecoveryPlacement struct {
	VPC           string `json:"vpc_id"`
	Subnet        string `json:"subnet_id"`
	SecurityGroup string `json:"security_group_id"`
	Zone          string `json:"zone"`
	Flavor        string `json:"flavor"`
	StorageType   string `json:"storage_type"`
	StorageGiB    int    `json:"storage_gib"`
	CPU           int    `json:"cpu,omitempty"`
	MemoryMiB     int    `json:"memory_mib,omitempty"`
}

type DatabaseRecovery struct {
	SourceID    string    `json:"source_id"`
	Name        string    `json:"name"`
	RestoreTime time.Time `json:"restore_time"`
	DatabaseRecoveryPlacement
}

type DatabaseRecoverer interface {
	PrepareDatabaseRecovery(context.Context, Scope, DatabaseRecovery, string) (func(context.Context) (LaunchReceipt, error), error)
	ObserveDatabaseRecovery(context.Context, Scope, DatabaseRecovery, string, LaunchReceipt) (Object, error)
}

var recoveryName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{3,63}$`)
var recoveryUUID = regexp.MustCompile(`^[a-f0-9]{8}(-[a-f0-9]{4}){3}-[a-f0-9]{12}$`)
var recoveryCode = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,95}$`)
var huaweiRecoveryInstanceID = regexp.MustCompile(`^[a-f0-9]{32}in[0-9]{2}$`)
var alibabaRecoveryInstanceID = regexp.MustCompile(`^rm-[a-z0-9]{1,124}$`)
var alibabaRecoveryName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{3,63}$`)
var recoveryVPCID = regexp.MustCompile(`^vpc-[a-z0-9]{1,123}$`)
var alibabaRecoverySubnet = regexp.MustCompile(`^vsw-[a-z0-9]{1,123}$`)
var tencentRecoveryInstanceID = regexp.MustCompile(`^cdb-[a-z0-9]{1,123}$`)
var tencentRecoverySubnet = regexp.MustCompile(`^subnet-[a-z0-9]{1,120}$`)
var tencentRecoveryGroup = regexp.MustCompile(`^sg-[a-z0-9]{1,124}$`)

func ValidDatabaseRecoveryInstanceID(vendor, id string) bool {
	switch vendor {
	case HuaweiECS:
		return huaweiRecoveryInstanceID.MatchString(id)
	case AlibabaECS:
		return alibabaRecoveryInstanceID.MatchString(id)
	case TencentCVM:
		return tencentRecoveryInstanceID.MatchString(id)
	default:
		return false
	}
}

func (placement DatabaseRecoveryPlacement) ValidFor(vendor string) bool {
	if !recoveryCode.MatchString(placement.Zone) || !recoveryCode.MatchString(placement.Flavor) {
		return false
	}
	if vendor != TencentCVM && (placement.CPU != 0 || placement.MemoryMiB != 0) {
		return false
	}
	switch vendor {
	case HuaweiECS:
		return recoveryUUID.MatchString(placement.VPC) && recoveryUUID.MatchString(placement.Subnet) &&
			recoveryUUID.MatchString(placement.SecurityGroup) && placement.StorageType == "CLOUDSSD" &&
			placement.StorageGiB >= 20 && placement.StorageGiB <= 10000 && placement.StorageGiB%10 == 0
	case AlibabaECS:
		return recoveryVPCID.MatchString(placement.VPC) && alibabaRecoverySubnet.MatchString(placement.Subnet) &&
			placement.SecurityGroup == "" && placement.StorageType == "cloud_essd" &&
			placement.StorageGiB >= 20 && placement.StorageGiB <= 64000 && placement.StorageGiB%5 == 0
	case TencentCVM:
		return recoveryVPCID.MatchString(placement.VPC) && tencentRecoverySubnet.MatchString(placement.Subnet) &&
			tencentRecoveryGroup.MatchString(placement.SecurityGroup) &&
			(placement.Flavor == "UNIVERSAL" || placement.Flavor == "EXCLUSIVE") && placement.StorageType == "local_ssd" &&
			placement.CPU > 0 && placement.CPU <= 256 && placement.MemoryMiB > 0 && placement.MemoryMiB <= 4*1024*1024 &&
			placement.StorageGiB > 0 && placement.StorageGiB <= 64000
	default:
		return false
	}
}

func (spec DatabaseRecovery) ValidFor(vendor string) bool {
	at := spec.RestoreTime.UTC()
	placement := spec.DatabaseRecoveryPlacement
	if at.Year() < 1970 || at.Year() > 9999 || !ValidDatabaseRecoveryInstanceID(vendor, spec.SourceID) || !placement.ValidFor(vendor) {
		return false
	}
	if vendor == AlibabaECS {
		return alibabaRecoveryName.MatchString(spec.Name) && at.Nanosecond() == 0
	}
	if vendor == TencentCVM {
		return len(spec.Name) <= 60 && recoveryName.MatchString(spec.Name) && at.Nanosecond() == 0
	}
	return recoveryName.MatchString(spec.Name) && at.Nanosecond()%int(time.Millisecond) == 0
}

func SupportsDatabaseRecovery(vendor string, db DatabaseDetails) bool {
	return db.Engine == "mysql" &&
		((vendor == HuaweiECS && db.Product == HuaweiRDS) || (vendor == AlibabaECS && db.Product == AlibabaRDS) ||
			(vendor == TencentCVM && db.Product == TencentCDB))
}

func (window DatabaseRestoreWindow) Contains(at time.Time) bool {
	return !at.Before(window.Start) && !at.After(window.End)
}
