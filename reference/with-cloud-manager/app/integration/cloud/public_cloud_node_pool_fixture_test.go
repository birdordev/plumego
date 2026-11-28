package cloudtest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"with-cloud-manager/internal/provider"
)

const fixtureNodePoolSecret = "node-pool-script-fixture-sensitive.invalid"

const (
	poolsHealthy int32 = iota
	poolsThrottled
	poolsWrongCluster
	poolsDuplicate
	poolsEmpty
)

type nodePoolFixture struct {
	cluster *clusterFixture
	mode    atomic.Int32
	changed atomic.Bool
}

func (f *nodePoolFixture) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if !strings.HasSuffix(r.URL.Path, "/nodepools") && r.Header.Get("X-TC-Action") != "DescribeNodePools" {
		f.cluster.serve(t, w, r)
		return
	}
	offset, valid := f.checkRequest(r)
	if !valid || (offset != 0 && offset != 50) {
		t.Errorf("unexpected node pool request (%s, %s, %d)", r.Host, r.Method, offset)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	mode := f.mode.Load()
	if mode == poolsThrottled && (f.cluster.vendor != provider.TencentCVM || offset == 50) {
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	total := 51
	if mode == poolsEmpty {
		total = 0
	}
	stop := total
	if f.cluster.vendor == provider.TencentCVM {
		stop = min(total, offset+50)
	}
	rows := make([]map[string]any, 0, stop-offset)
	for index := offset; index < stop; index++ {
		bad := mode == poolsWrongCluster && index == stop-1
		id := index
		if mode == poolsDuplicate && index == total-1 {
			id = 0
		}
		rows = append(rows, f.row(id, bad))
	}
	var result map[string]any
	switch f.cluster.vendor {
	case provider.TencentCVM:
		result = map[string]any{"Response": map[string]any{"NodePools": rows, "TotalCount": total, "RequestId": "pool-fixture"}}
	case provider.AlibabaECS:
		result = map[string]any{"nodepools": rows}
	case provider.HuaweiECS:
		result = map[string]any{"kind": "List", "apiVersion": "v3", "items": rows}
	}
	if err := json.NewEncoder(w).Encode(result); err != nil {
		t.Errorf("fixture encoding: %v", err)
	}
}

func (f *nodePoolFixture) checkRequest(r *http.Request) (int, bool) {
	region, cluster := f.cluster.region, f.cluster.id
	switch f.cluster.vendor {
	case provider.TencentCVM:
		var input struct {
			ClusterId     string
			Offset, Limit int
		}
		body, err := io.ReadAll(r.Body)
		valid := err == nil && json.Unmarshal(body, &input) == nil && r.Host == "tke.tencentcloudapi.com" &&
			r.Header.Get("X-TC-Region") == region && r.Header.Get("X-TC-Action") == "DescribeNodePools" &&
			r.Header.Get("X-TC-Version") == "2022-05-01" && r.Header.Get("X-TC-Token") == fixtureLaunchSession &&
			validFixtureTencentSignature(r, body, fixtureLaunchKey, fixtureLaunchSecret) &&
			input.ClusterId == cluster && input.Limit == 50
		return input.Offset, valid
	case provider.AlibabaECS:
		return 0, r.Method == http.MethodGet && r.Host == "cs."+region+".aliyuncs.com" &&
			r.URL.Path == "/clusters/"+cluster+"/nodepools" && r.URL.RawQuery == "" &&
			r.Header.Get("X-ACS-Action") == "DescribeClusterNodePools" && r.Header.Get("X-ACS-Version") == "2015-12-15" &&
			r.Header.Get("X-ACS-Security-Token") == fixtureLaunchSession && validFixtureAlibabaRequestSignature(r, fixtureLaunchKey, fixtureLaunchSecret)
	case provider.HuaweiECS:
		return 0, r.Method == http.MethodGet && r.Host == "cce."+region+".myhuaweicloud.com" &&
			r.URL.Path == "/api/v3/projects/"+fixtureHuaweiProject+"/clusters/"+cluster+"/nodepools" &&
			r.URL.RawQuery == "showDefaultNodePool=true" && r.Header.Get("X-Auth-Token") == fixtureHuaweiIAM
	default:
		return 0, false
	}
}

func (f *nodePoolFixture) row(index int, badScope bool) map[string]any {
	id, name := fmt.Sprintf("np-pool-%03d", index), fmt.Sprintf("pool-%03d", index)
	if index == 0 && f.changed.Load() {
		name = "latest-pool"
	}
	region, cluster, project := f.cluster.region, f.cluster.id, fixtureHuaweiProject
	if badScope {
		region, cluster, project = "cn-outside", "cls-other", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	}
	switch f.cluster.vendor {
	case provider.TencentCVM:
		if index == 1 {
			return map[string]any{"ClusterId": cluster, "NodePoolId": id, "Name": name, "Type": "Native",
				"LifeState": "Running", "Native": map[string]any{"EnableAutoscaling": true, "Replicas": 2,
					"ReadyReplicas": 1, "Scaling": map[string]int{"MinReplicas": 1, "MaxReplicas": 4}}}
		}
		return map[string]any{
			"ClusterId": cluster, "NodePoolId": id, "Name": name, "Type": "Regular", "LifeState": "normal",
			"Regular": map[string]any{"DesiredNodesNum": 0, "MinNodesNum": 0, "MaxNodesNum": 10, "AutoscalingGroupStatus": "disabled",
				"NodeCountSummary":         map[string]any{"AutoscalingAdded": map[string]int{"Total": 0}, "ManuallyAdded": map[string]int{"Total": 0}},
				"InstanceAdvancedSettings": map[string]string{"UserScript": fixtureNodePoolSecret}},
		}
	case provider.AlibabaECS:
		return map[string]any{
			"nodepool_info": map[string]string{"nodepool_id": id, "name": name, "type": "ess", "region_id": region, "cluster_id": cluster},
			"status":        map[string]any{"state": "active", "total_nodes": 0},
			"auto_scaling":  map[string]any{"enable": false, "min_instances": 0, "max_instances": 10},
			"scaling_group": map[string]any{"desired_size": 0, "user_data": fixtureNodePoolSecret},
		}
	default:
		return map[string]any{
			"metadata": map[string]string{"uid": id, "name": name, "clusterId": cluster, "projectId": project},
			"spec": map[string]any{"type": "vm", "initialNodeCount": 0,
				"autoscaling":  map[string]any{"enable": false, "minNodeCount": 0, "maxNodeCount": 10},
				"nodeTemplate": map[string]string{"userData": fixtureNodePoolSecret}},
			"status": map[string]any{"currentNode": 0},
		}
	}
}
