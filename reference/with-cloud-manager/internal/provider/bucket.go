package provider

import (
	"context"
	"regexp"
	"strings"
	"time"
)

type BucketVersioning string

const (
	BucketVersioningDisabled  BucketVersioning = "disabled"
	BucketVersioningEnabled   BucketVersioning = "enabled"
	BucketVersioningSuspended BucketVersioning = "suspended"
)

func (state BucketVersioning) Valid() bool {
	return state == BucketVersioningDisabled || state == BucketVersioningEnabled || state == BucketVersioningSuspended
}

type BucketVersioningReader interface {
	ReadBucketVersioning(context.Context, Scope, string, string) (BucketVersioning, error)
}

type BucketEncryption string

const (
	BucketEncryptionUnconfigured BucketEncryption = "unconfigured"
	BucketEncryptionAES256       BucketEncryption = "aes256"
	BucketEncryptionSM4          BucketEncryption = "sm4"
	BucketEncryptionKMS          BucketEncryption = "kms"
)

func (state BucketEncryption) Valid() bool {
	switch state {
	case BucketEncryptionUnconfigured, BucketEncryptionAES256, BucketEncryptionSM4, BucketEncryptionKMS:
		return true
	default:
		return false
	}
}

type BucketEncryptionReader interface {
	ReadBucketEncryption(context.Context, Scope, string, string) (BucketEncryption, error)
}

const ObjectBucketGlobalScope = "global"

const (
	TencentCOS = "tencent-cos"
	AlibabaOSS = "alibaba-oss"
	HuaweiOBS  = "huawei-obs"
)

type BucketDetails struct {
	Product      string    `json:"product"`
	Location     string    `json:"location"`
	StorageClass string    `json:"storage_class,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

var bucketName = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,77}[a-z0-9]$`)
var bucketLocation = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
var bucketClass = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,39}$`)

// BucketAssetID 将对象桶的创建批次纳入资产标识，隔离同名重建。
func BucketAssetID(name string, created time.Time) string {
	return name + "@" + created.UTC().Format(time.RFC3339Nano)
}

func ValidBucketAssetID(id string) bool {
	name, stamp, found := strings.Cut(id, "@")
	date, err := time.Parse(time.RFC3339Nano, stamp)
	return found && bucketName.MatchString(name) && err == nil && !date.IsZero() && BucketAssetID(name, date) == id
}

func (o Object) ValidBucket() bool {
	bucket := o.Bucket
	if bucket == nil || o.Status != "observed" || !ValidBucketAssetID(o.ID) || o.ID != BucketAssetID(o.Name, bucket.CreatedAt) ||
		!bucketLocation.MatchString(bucket.Location) || (bucket.StorageClass != "" && !bucketClass.MatchString(bucket.StorageClass)) ||
		len(o.Links) != 0 || len(o.PublicAddresses) != 0 || o.Tags != nil || o.Database != nil || o.Snapshot != nil || o.LoadBalancer != nil || o.Cache != nil || o.Cluster != nil {
		return false
	}
	switch bucket.Product {
	case TencentCOS, AlibabaOSS, HuaweiOBS:
		return true
	default:
		return false
	}
}
