package cloudtest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"

	"with-cloud-manager/internal/provider"
)

const cacheFixtureConnection = "sensitive-cache-connection.example.test"

type cacheFixture struct {
	vpcCreationFixture
	command, id, product string
	firstReads           atomic.Int32
	lateMode             atomic.Int32
	observation          atomic.Int32
	changed, missing     atomic.Bool
}

func (f *cacheFixture) assetID(index int) string {
	if index == 0 {
		return f.id
	}
	if f.vendor == provider.HuaweiECS {
		return fmt.Sprintf("70000000-0000-4000-9000-%012d", index)
	}
	return f.id + "-" + strconv.Itoa(index)
}

func (f *cacheFixture) network() string {
	if f.vendor == provider.HuaweiECS {
		return "70000000-0000-4000-8000-000000000011"
	}
	return "vpc-fixture"
}

func (f *cacheFixture) request(r *http.Request) (string, int, bool) {
	query := r.URL.Query()
	switch f.vendor {
	case provider.TencentCVM:
		var input struct {
			Offset, Limit       int
			InstanceId, OrderBy string
		}
		body, err := io.ReadAll(r.Body)
		valid := err == nil && json.Unmarshal(body, &input) == nil && r.Method == http.MethodPost &&
			r.Host == "redis.tencentcloudapi.com" && r.Header.Get("X-TC-Region") == f.region &&
			r.Header.Get("X-TC-Action") == "DescribeInstances" && r.Header.Get("X-TC-Version") == "2018-04-12" &&
			r.Header.Get("X-TC-Token") == fixtureLaunchSession && validFixtureTencentSignature(r, body, fixtureLaunchKey, fixtureLaunchSecret) &&
			input.Limit == 50 && input.OrderBy == "createtime"
		return input.InstanceId, input.Offset, valid
	case provider.AlibabaECS:
		page, err := strconv.Atoi(query.Get("PageNumber"))
		valid := err == nil && query.Get("PageSize") == "50" && query.Get("RegionId") == f.region &&
			r.Method == http.MethodPost && r.Host == "r-kvstore."+f.region+".aliyuncs.com" &&
			r.Header.Get("X-ACS-Action") == "DescribeInstances" && r.Header.Get("X-ACS-Version") == "2015-01-01" &&
			r.Header.Get("X-ACS-Security-Token") == fixtureLaunchSession && validFixtureAlibabaSignature(r, fixtureLaunchKey, fixtureLaunchSecret)
		return query.Get("InstanceIds"), (page - 1) * 50, valid
	case provider.HuaweiECS:
		offset, err := strconv.Atoi(query.Get("offset"))
		valid := err == nil && query.Get("limit") == "50" && query.Get("include_failure") == "true" &&
			r.Method == http.MethodGet && r.Host == "dcs."+f.region+".myhuaweicloud.com" &&
			r.URL.Path == "/v2/"+fixtureHuaweiProject+"/instances" && r.Header.Get("X-Auth-Token") == fixtureHuaweiIAM
		return query.Get("instance_id"), offset, valid
	default:
		return "", 0, false
	}
}

func (f *cacheFixture) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if r.Host == "sts.tencentcloudapi.com" && f.tencent(t, w, r) ||
		r.Host == "sts.aliyuncs.com" && f.alibaba(t, w, r) ||
		r.URL.Path == "/v3/auth/tokens" && f.huawei(t, w, r) {
		return
	}
	id, offset, valid := f.request(r)
	if !valid || (offset != 0 && offset != 50) || (id != "" && (id != f.id || offset != 0)) {
		t.Errorf("unexpected cache request (%s, %s, %d)", r.Host, r.Method, offset)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if id != "" {
		f.serveLookup(t, w)
		return
	}
	badScope := false
	if offset == 0 {
		f.firstReads.Add(1)
	} else {
		switch f.lateMode.Load() {
		case 1:
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		case 2:
			badScope = true
		}
	}
	total := 51
	if f.missing.Load() {
		total--
	}
	rows := make([]map[string]any, 0, 50)
	for position := offset; position < min(total, offset+50); position++ {
		index := position
		if f.missing.Load() {
			index++
		}
		rows = append(rows, f.row(index, badScope))
	}
	f.writePage(t, w, offset, total, rows)
}

func (f *cacheFixture) serveLookup(t *testing.T, w http.ResponseWriter) {
	switch f.observation.Load() {
	case 0:
		w.WriteHeader(http.StatusNotFound)
	case 1:
		f.writePage(t, w, 0, 1, []map[string]any{f.row(2, false)})
	case 2:
		f.writePage(t, w, 0, 0, []map[string]any{})
	case 3:
		f.writePage(t, w, 0, 1, []map[string]any{f.row(0, false)})
	default:
		w.WriteHeader(http.StatusForbidden)
	}
}

func (f *cacheFixture) writePage(t *testing.T, w http.ResponseWriter, offset, total int, rows []map[string]any) {
	t.Helper()
	var result map[string]any
	switch f.vendor {
	case provider.TencentCVM:
		result = map[string]any{"Response": map[string]any{"RequestId": "test-cache", "TotalCount": total, "InstanceSet": rows}}
	case provider.AlibabaECS:
		result = map[string]any{"PageNumber": offset/50 + 1, "PageSize": 50, "TotalCount": total, "Instances": map[string]any{"KVStoreInstance": rows}}
	case provider.HuaweiECS:
		result = map[string]any{"instances": rows, "instance_num": total}
	}
	if err := json.NewEncoder(w).Encode(result); err != nil {
		t.Errorf("fixture encoding: %v", err)
	}
}

func (f *cacheFixture) row(index int, badScope bool) map[string]any {
	id, name, region, project := f.assetID(index), fmt.Sprintf("fixture-cache-%03d", index), f.region, fixtureHuaweiProject
	version, capacity := "6.0", 128
	if index == 0 && f.changed.Load() {
		version, capacity = "7.0", 256
	}
	if badScope {
		region, project = "cn-outside", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	}
	switch f.vendor {
	case provider.TencentCVM:
		row := map[string]any{"InstanceId": id, "InstanceName": name, "Status": 2, "Type": 16, "Size": capacity, "RegionId": 1,
			"Engine": "Redis", "CurrentRedisVersion": version, "UniqVpcId": f.network(), "WanIp": cacheFixtureConnection}
		if badScope {
			row["RegionId"] = 4
		}
		return row
	case provider.AlibabaECS:
		return map[string]any{"InstanceId": id, "InstanceName": name, "InstanceStatus": "Normal", "InstanceType": "Tair", "Capacity": capacity,
			"EngineVersion": version, "RegionId": region, "VpcId": f.network(), "ConnectionDomain": cacheFixtureConnection}
	default:
		return map[string]any{"instance_id": id, "name": name, "status": "RUNNING", "engine": "Redis", "max_memory": capacity,
			"engine_version": version, "project_id": project, "vpc_id": f.network(), "domain_name": cacheFixtureConnection}
	}
}
