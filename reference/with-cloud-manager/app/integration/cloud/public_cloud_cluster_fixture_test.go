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

const clusterFixtureSecret = "fixture-cluster-control-plane.invalid"

const (
	clusterNormal int32 = iota
	clusterRateLimit
	clusterBadScope
	clusterNotFound
	clusterWrongLookup
	clusterForbidden
	clusterIncomplete
)

type clusterFixture struct {
	vpcCreationFixture
	command, id, product, clusterType string
	limit                             int
	firstReads                        atomic.Int32
	mode                              atomic.Int32
	changed, missing                  atomic.Bool
}

func (f *clusterFixture) assetID(index int) string {
	if index == 0 {
		return f.id
	}
	if f.vendor == provider.HuaweiECS {
		return fmt.Sprintf("70000000-0000-4000-9000-%012d", index)
	}
	return f.id + "-" + strconv.Itoa(index)
}

func (f *clusterFixture) network() string {
	if f.vendor == provider.HuaweiECS {
		return "70000000-0000-4000-8000-000000000011"
	}
	return "vpc-fixture"
}

func (f *clusterFixture) checkRequest(r *http.Request) (string, int, bool) {
	query := r.URL.Query()
	switch f.vendor {
	case provider.TencentCVM:
		var input struct {
			Offset, Limit int
			ClusterIds    []string
		}
		body, err := io.ReadAll(r.Body)
		valid := err == nil && json.Unmarshal(body, &input) == nil && len(input.ClusterIds) <= 1 && r.Method == http.MethodPost &&
			r.Host == "tke.tencentcloudapi.com" && r.Header.Get("X-TC-Region") == f.region &&
			r.Header.Get("X-TC-Action") == "DescribeClusters" && r.Header.Get("X-TC-Version") == "2018-05-25" &&
			r.Header.Get("X-TC-Token") == fixtureLaunchSession && validFixtureTencentSignature(r, body, fixtureLaunchKey, fixtureLaunchSecret) &&
			input.Limit == f.limit
		if len(input.ClusterIds) == 1 {
			return input.ClusterIds[0], input.Offset, valid
		}
		return "", input.Offset, valid
	case provider.AlibabaECS:
		page, err := strconv.Atoi(query.Get("page_number"))
		valid := err == nil && query.Get("page_size") == strconv.Itoa(f.limit) &&
			query.Get("region_id") == f.region && r.Method == http.MethodGet &&
			r.Host == "cs."+f.region+".aliyuncs.com" && r.URL.Path == "/api/v1/clusters" &&
			r.Header.Get("X-ACS-Action") == "DescribeClustersV1" && r.Header.Get("X-ACS-Version") == "2015-12-15" &&
			r.Header.Get("X-ACS-Security-Token") == fixtureLaunchSession && validFixtureAlibabaRequestSignature(r, fixtureLaunchKey, fixtureLaunchSecret)
		return query.Get("cluster_id"), (page - 1) * f.limit, valid
	case provider.HuaweiECS:
		return "", 0, r.Method == http.MethodGet && r.Host == "cce."+f.region+".myhuaweicloud.com" &&
			r.URL.Path == "/api/v3/projects/"+fixtureHuaweiProject+"/clusters" && query.Get("detail") == "true" &&
			r.Header.Get("X-Auth-Token") == fixtureHuaweiIAM && len(query) == 1
	default:
		return "", 0, false
	}
}

func (f *clusterFixture) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if r.Host == "sts.tencentcloudapi.com" && f.tencent(t, w, r) ||
		r.Host == "sts.aliyuncs.com" && f.alibaba(t, w, r) ||
		r.URL.Path == "/v3/auth/tokens" && f.huawei(t, w, r) {
		return
	}
	id, offset, valid := f.checkRequest(r)
	if !valid || (offset != 0 && offset != f.limit) || (id != "" && (id != f.id || offset != 0)) {
		t.Errorf("unexpected cluster request (%s, %s, %d)", r.Host, r.Method, offset)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	mode := f.mode.Load()
	if id == "" && offset == 0 {
		f.firstReads.Add(1)
	}
	if offset != 0 || f.vendor == provider.HuaweiECS || id != "" {
		switch mode {
		case clusterRateLimit:
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		case clusterNotFound:
			w.WriteHeader(http.StatusNotFound)
			return
		case clusterForbidden:
			w.WriteHeader(http.StatusForbidden)
			return
		case clusterIncomplete:
			f.writePage(t, w, offset, 0, nil)
			return
		}
	}

	rows := make([]map[string]any, 0, f.limit)
	total := f.limit + 1
	missing := f.missing.Load()
	if id != "" {
		switch {
		case mode == clusterWrongLookup:
			rows = append(rows, f.row(1, false))
		case !missing:
			rows = append(rows, f.row(0, false))
		}
		f.writePage(t, w, 0, len(rows), rows)
		return
	}
	if missing {
		total--
	}
	end := min(total, offset+f.limit)
	if f.vendor == provider.HuaweiECS {
		end = total
	}
	for position := offset; position < end; position++ {
		index := position
		if missing {
			index++
		}
		badScope := mode == clusterBadScope && position+1 == end && (offset != 0 || f.vendor == provider.HuaweiECS)
		rows = append(rows, f.row(index, badScope))
	}
	f.writePage(t, w, offset, total, rows)
}

func (f *clusterFixture) writePage(t *testing.T, w http.ResponseWriter, offset, total int, rows []map[string]any) {
	t.Helper()
	var result map[string]any
	switch f.vendor {
	case provider.TencentCVM:
		result = map[string]any{"Response": map[string]any{"RequestId": "cluster-fixture", "TotalCount": total, "Clusters": rows}}
	case provider.AlibabaECS:
		result = map[string]any{"page_info": map[string]int{"page_number": offset/f.limit + 1, "page_size": f.limit, "total_count": total}, "clusters": rows}
	case provider.HuaweiECS:
		result = map[string]any{"kind": "Cluster", "apiVersion": "v3", "items": rows}
	}
	if err := json.NewEncoder(w).Encode(result); err != nil {
		t.Errorf("fixture encoding: %v", err)
	}
}

func (f *clusterFixture) row(index int, badScope bool) map[string]any {
	id, name := f.assetID(index), fmt.Sprintf("fixture-cluster-%03d", index)
	version, nodes := "1.30.4", 0
	if index == 0 && f.changed.Load() {
		version, nodes = "1.31.2", 3
	}
	switch f.vendor {
	case provider.TencentCVM:
		if badScope {
			id = "wrong-kind"
		}
		return map[string]any{"ClusterId": id, "ClusterName": name, "ClusterStatus": "Running", "ClusterType": f.clusterType,
			"ClusterVersion": version, "ClusterNodeNum": nodes, "ClusterNetworkSettings": map[string]string{"VpcId": f.network()},
			"ClusterExternalEndpoint": clusterFixtureSecret}
	case provider.AlibabaECS:
		region := f.region
		if badScope {
			region = "cn-outside"
		}
		return map[string]any{"cluster_id": id, "name": name, "state": "running", "cluster_type": f.clusterType, "region_id": region,
			"current_version": version, "size": nodes, "vpc_id": f.network(), "master_url": clusterFixtureSecret}
	default:
		project := fixtureHuaweiProject
		if badScope {
			project = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		}
		return map[string]any{
			"metadata": map[string]any{"uid": id, "name": name, "projectId": project,
				"annotations": map[string]string{"totalNodesNumber": strconv.Itoa(nodes), "ignoredCredential": clusterFixtureSecret}},
			"spec":   map[string]any{"type": f.clusterType, "version": version, "hostNetwork": map[string]string{"vpc": f.network()}},
			"status": map[string]any{"phase": "Available", "endpoints": []map[string]string{{"url": clusterFixtureSecret}}},
		}
	}
}
