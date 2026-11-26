package provider

import "math"

const (
	TencentRedis = "tencent-redis"
	AlibabaTair  = "alibaba-tair"
	HuaweiDCS    = "huawei-dcs"
)

// CacheDetails 是托管缓存的资产投影，容量按 MiB 表示。
type CacheDetails struct {
	Product     string  `json:"product"`
	Engine      string  `json:"engine"`
	Version     string  `json:"version,omitempty"`
	CapacityMiB float64 `json:"capacity_mib,omitempty"`
}

func (o Object) ValidCache() bool {
	cache := o.Cache
	if cache == nil || !o.validRegionalTopology() || o.Tags != nil || o.Snapshot != nil ||
		o.LoadBalancer != nil || o.Database != nil || o.Bucket != nil || o.Cluster != nil {
		return false
	}
	switch cache.Engine {
	case "redis", "memcached":
	case "ckv":
		if cache.Product != TencentRedis {
			return false
		}
	default:
		return false
	}
	switch cache.Product {
	case TencentRedis, AlibabaTair, HuaweiDCS:
	default:
		return false
	}
	if (cache.Version != "" && !engineVersion.MatchString(cache.Version)) ||
		math.IsNaN(cache.CapacityMiB) || math.IsInf(cache.CapacityMiB, 0) || cache.CapacityMiB < 0 {
		return false
	}
	return true
}
