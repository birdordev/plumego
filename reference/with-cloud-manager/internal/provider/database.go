package provider

import (
	"regexp"
)

const (
	TencentCDB        = "tencent-cdb"
	TencentPostgreSQL = "tencent-postgresql"
	TencentSQLServer  = "tencent-sqlserver"
	AlibabaRDS        = "alibaba-rds"
	HuaweiRDS         = "huawei-rds"
)

// DatabaseDetails 是托管数据库的盘点投影。连接信息不属于资产库存。
type DatabaseDetails struct {
	Product string `json:"product"`
	Engine  string `json:"engine"`
	Version string `json:"version,omitempty"`
}

var databaseEngine = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)
var engineVersion = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+ -]{0,95}$`)

func (o Object) ValidDatabase() bool {
	db := o.Database
	if db == nil || !o.validRegionalTopology() || !databaseEngine.MatchString(db.Engine) ||
		(db.Version != "" && !engineVersion.MatchString(db.Version)) ||
		o.Snapshot != nil || o.LoadBalancer != nil || o.Cache != nil || o.Cluster != nil {
		return false
	}
	switch db.Product {
	case TencentCDB:
		if db.Engine != "mysql" {
			return false
		}
	case TencentPostgreSQL:
		if db.Engine != "postgresql" {
			return false
		}
	case TencentSQLServer:
		if db.Engine != "sqlserver" {
			return false
		}
	case AlibabaRDS, HuaweiRDS:
	default:
		return false
	}
	return true
}
