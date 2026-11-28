package cloudtest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"

	"with-cloud-manager/app/integration/testsupport"
	"with-cloud-manager/internal/provider"
)

type databaseFixture struct {
	vpcCreationFixture
	command, id, product string
	firstReads           atomic.Int32
	lateMode             atomic.Int32
	observation          atomic.Int32
	changed, missing     atomic.Bool
	pointRow             func(string) map[string]any
}

const (
	fixtureDatabaseVersion = "10.0.fixture"
	fixtureDatabaseNext    = "11.0.fixture"
	fixtureDatabaseHost    = "database-connection.example.test"
	fixtureDatabaseUser    = "database-principal-fixture"
)

func (f *databaseFixture) databaseVPC() string {
	if f.vendor == provider.HuaweiECS {
		return "70000000-0000-4000-8000-000000000011"
	}
	return "vpc-fixture"
}

func (f *databaseFixture) serveDatabase(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if r.Host == "sts.tencentcloudapi.com" && f.tencent(t, w, r) ||
		r.Host == "sts.aliyuncs.com" && f.alibaba(t, w, r) ||
		r.URL.Path == "/v3/auth/tokens" && f.huawei(t, w, r) {
		return
	}
	var family, id string
	var offset int
	var valid bool
	query := r.URL.Query()
	switch f.vendor {
	case provider.TencentCVM:
		family, id, offset, valid = f.tencentDatabaseFixture(r)
	case provider.AlibabaECS:
		family, id = provider.AlibabaRDS, query.Get("DBInstanceId")
		number, err := strconv.Atoi(query.Get("PageNumber"))
		offset = (number - 1) * 50
		valid = err == nil && r.Host == "rds.aliyuncs.com" && r.Method == http.MethodPost && query.Get("PageSize") == "50" &&
			query.Get("RegionId") == f.region && r.Header.Get("X-ACS-Action") == "DescribeDBInstances" &&
			r.Header.Get("X-ACS-Version") == "2014-08-15" && r.Header.Get("X-ACS-Security-Token") == fixtureLaunchSession &&
			validFixtureAlibabaSignature(r, fixtureLaunchKey, fixtureLaunchSecret)
	case provider.HuaweiECS:
		family, id = provider.HuaweiRDS, query.Get("id")
		var err error
		offset, err = strconv.Atoi(query.Get("offset"))
		valid = err == nil && r.Method == http.MethodGet && r.Host == "rds."+f.region+".myhuaweicloud.com" &&
			r.URL.Path == "/v3/"+fixtureHuaweiProject+"/instances" && query.Get("limit") == "50" &&
			r.Header.Get("X-Auth-Token") == fixtureHuaweiIAM
	}
	if !valid || (offset != 0 && offset != 50) || (id != "" && (id != f.id || offset != 0 || family != f.product)) {
		t.Errorf("unexpected database request (%s, %s, %d)", r.Method, family, offset)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	rows := make([]map[string]any, 0, 50)
	total := 51
	targetRegion := f.region
	if id != "" && f.pointRow != nil {
		total = 1
		rows = append(rows, f.pointRow(family))
	} else if id != "" {
		switch f.observation.Load() {
		case 0:
			w.WriteHeader(http.StatusNotFound)
			return
		case 1:
			total = 1
			rows = append(rows, f.databaseRow(family, f.id+"-wrong", "wrong", "13", targetRegion))
		case 2:
			total = 0
		}
	} else if family == f.product {
		if offset == 0 {
			f.firstReads.Add(1)
		}
		if offset == 50 {
			switch f.lateMode.Load() {
			case 1:
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			case 2:
				targetRegion = "cn-outside"
			}
		}
		if f.missing.Load() {
			total--
		}
		for position := offset; position < min(total, offset+50); position++ {
			index := position
			if f.missing.Load() {
				index++
			}
			version := fixtureDatabaseVersion
			if index == 0 && f.changed.Load() {
				version = fixtureDatabaseNext
			}
			assetID := f.id
			if index != 0 {
				assetID += fmt.Sprintf("-%03d", index)
			}
			rows = append(rows, f.databaseRow(family, assetID, fmt.Sprintf("fixture-db-%03d", index), version, targetRegion))
		}
	} else {
		total = 1
		assetID := "postgres-db-fixture"
		if family == provider.TencentSQLServer {
			assetID = "mssql-db-fixture"
		}
		rows = append(rows, f.databaseRow(family, assetID, assetID, "15", targetRegion))
	}

	switch family {
	case provider.TencentCDB, provider.TencentPostgreSQL, provider.TencentSQLServer:
		field := map[string]string{provider.TencentCDB: "Items", provider.TencentPostgreSQL: "DBInstanceSet", provider.TencentSQLServer: "DBInstances"}[family]
		testsupport.WriteJSON(t, w, map[string]any{"Response": map[string]any{field: rows, "TotalCount": total}})
	case provider.AlibabaRDS:
		testsupport.WriteJSON(t, w, map[string]any{"Items": map[string]any{"DBInstance": rows}, "PageNumber": offset/50 + 1,
			"PageRecordCount": len(rows), "TotalRecordCount": total})
	case provider.HuaweiRDS:
		testsupport.WriteJSON(t, w, map[string]any{"instances": rows, "total_count": total})
	}
}

func (f *databaseFixture) tencentDatabaseFixture(r *http.Request) (string, string, int, bool) {
	body, err := io.ReadAll(r.Body)
	var input struct {
		Limit, Offset              int
		InstanceIds, InstanceIdSet []string
		Filters                    []struct {
			Name   string
			Values []string
		}
	}
	if err != nil || json.Unmarshal(body, &input) != nil || input.Limit != 50 ||
		r.Method != http.MethodPost || r.Header.Get("X-TC-Region") != f.region ||
		r.Header.Get("X-TC-Token") != fixtureLaunchSession || r.Header.Get("X-TC-Action") != "DescribeDBInstances" ||
		!validFixtureTencentSignature(r, body, fixtureLaunchKey, fixtureLaunchSecret) {
		return "", "", 0, false
	}
	version := r.Header.Get("X-TC-Version")
	switch r.Host {
	case "cdb.tencentcloudapi.com":
		id := ""
		if len(input.InstanceIds) == 1 {
			id = input.InstanceIds[0]
		}
		return provider.TencentCDB, id, input.Offset, version == "2017-03-20" && len(input.InstanceIds) <= 1
	case "postgres.tencentcloudapi.com":
		id := ""
		if len(input.Filters) == 1 && input.Filters[0].Name == "db-instance-id" && len(input.Filters[0].Values) == 1 {
			id = input.Filters[0].Values[0]
		}
		return provider.TencentPostgreSQL, id, input.Offset, version == "2017-03-12" && (len(input.Filters) == 0 || id != "")
	case "sqlserver.tencentcloudapi.com":
		id := ""
		if len(input.InstanceIdSet) == 1 {
			id = input.InstanceIdSet[0]
		}
		return provider.TencentSQLServer, id, input.Offset, version == "2018-03-28" && len(input.InstanceIdSet) <= 1
	default:
		return "", "", 0, false
	}
}

func (f *databaseFixture) databaseRow(family, id, name, version, region string) map[string]any {
	switch family {
	case provider.TencentCDB:
		return map[string]any{"InstanceId": id, "InstanceName": name, "Status": 1, "EngineVersion": version, "Region": region,
			"UniqVpcId": f.databaseVPC(), "WanDomain": fixtureDatabaseHost, "Vip": "192.0.2.99"}
	case provider.TencentPostgreSQL:
		return map[string]any{"DBInstanceId": id, "DBInstanceName": name, "DBInstanceStatus": "running", "DBEngine": "postgresql",
			"DBVersion": version, "Region": region, "VpcId": f.databaseVPC(), "NetworkAccessList": []string{fixtureDatabaseHost}}
	case provider.TencentSQLServer:
		return map[string]any{"InstanceId": id, "Name": name, "Status": 2, "Version": version, "Region": region,
			"UniqVpcId": f.databaseVPC(), "Uid": fixtureDatabaseHost}
	case provider.AlibabaRDS:
		return map[string]any{"DBInstanceId": id, "DBInstanceDescription": name, "DBInstanceStatus": "Running", "Engine": "MySQL",
			"EngineVersion": version, "RegionId": region, "VpcId": f.databaseVPC(), "ConnectionString": fixtureDatabaseHost}
	default:
		return map[string]any{"id": id, "name": name, "status": "ACTIVE", "region": region, "vpc_id": f.databaseVPC(),
			"datastore": map[string]any{"type": "MySQL", "version": version}, "private_dns_names": []string{fixtureDatabaseHost}, "db_user_name": fixtureDatabaseUser}
	}
}
