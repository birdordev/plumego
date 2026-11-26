package provider

import (
	"regexp"
	"strings"
	"time"
)

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
