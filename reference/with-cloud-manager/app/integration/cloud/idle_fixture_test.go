package cloudtest

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"with-cloud-manager/app/integration/testsupport"
	"with-cloud-manager/internal/provider"
)

type idleCloudFixture struct {
	vpcCreationFixture
	instance string
	mode     atomic.Int32
	calls    atomic.Int32
}

const (
	idlePartial int32 = iota + 1
	idleActive
	idleThrottled
	idleForeign
)

func (f *idleCloudFixture) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if r.Host == "sts.tencentcloudapi.com" && f.tencent(t, w, r) ||
		r.Host == "sts.aliyuncs.com" && f.alibaba(t, w, r) ||
		r.URL.Path == "/v3/auth/tokens" && f.huawei(t, w, r) {
		return
	}
	mode := f.mode.Load()
	f.calls.Add(1)
	if mode == idleThrottled {
		w.Header().Set("Retry-After", "80")
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	valid := false
	switch f.vendor {
	case provider.TencentCVM:
		valid = f.serveTencent(t, w, r, mode)
	case provider.AlibabaECS:
		valid = f.serveAlibaba(t, w, r, mode)
	case provider.HuaweiECS:
		valid = f.serveHuawei(t, w, r, mode)
	}
	if !valid {
		t.Error("unexpected utilization request")
		w.WriteHeader(http.StatusBadRequest)
	}
}

func (f *idleCloudFixture) serveTencent(t *testing.T, w http.ResponseWriter, r *http.Request, mode int32) bool {
	body, err := io.ReadAll(r.Body)
	if err != nil || r.Host != "monitor.tencentcloudapi.com" || r.Method != http.MethodPost ||
		r.Header.Get("X-TC-Action") != "GetMonitorData" || r.Header.Get("X-TC-Region") != f.region ||
		r.Header.Get("X-TC-Version") != "2018-07-24" || !validFixtureTencentSignature(r, body, fixtureLaunchKey, fixtureLaunchSecret) {
		return false
	}
	var input struct {
		Namespace, MetricName, StartTime, EndTime string
		Period, SpecifyStatistics                 int
		Instances                                 []struct {
			Dimensions []struct{ Name, Value string }
		}
	}
	if json.Unmarshal(body, &input) != nil || input.Namespace != "QCE/CVM" || input.Period != 3600 || input.SpecifyStatistics != 2 ||
		len(input.Instances) != 1 || len(input.Instances[0].Dimensions) != 1 ||
		input.Instances[0].Dimensions[0].Name != "InstanceId" || input.Instances[0].Dimensions[0].Value != f.instance {
		return false
	}
	from, end, ok := fixtureUsageWindow(input.StartTime, input.EndTime)
	if !ok || end.Sub(from) != provider.IdleWindow {
		return false
	}
	value := 0.01
	switch input.MetricName {
	case "BaseCpuUsage":
		value = fixtureCPU(mode)
	case "LanIntraffic", "LanOuttraffic", "WanIntraffic", "WanOuttraffic":
	default:
		return false
	}
	var stamps []int64
	var values []any
	for stamp := end; !stamp.Before(from); stamp = stamp.Add(-time.Hour) {
		stamps = append(stamps, stamp.Unix())
		if mode == idlePartial && input.MetricName == "WanOuttraffic" && stamp.Equal(from) {
			values = append(values, nil)
		} else {
			values = append(values, value)
		}
	}
	testsupport.WriteJSON(t, w, map[string]any{"Response": map[string]any{
		"Period": 3600, "MetricName": input.MetricName, "DataPoints": []any{map[string]any{
			"Dimensions": []map[string]string{{"Name": "InstanceId", "Value": f.fixtureID(mode)}},
			"Timestamps": stamps, "MaxValues": values,
		}},
	}})
	return true
}

func (f *idleCloudFixture) serveAlibaba(t *testing.T, w http.ResponseWriter, r *http.Request, mode int32) bool {
	if r.Host != "ecs."+f.region+".aliyuncs.com" || r.Method != http.MethodPost ||
		r.Header.Get("X-ACS-Action") != "DescribeInstanceMonitorData" || r.Header.Get("X-ACS-Version") != "2014-05-26" ||
		!validFixtureAlibabaSignature(r, fixtureLaunchKey, fixtureLaunchSecret) {
		return false
	}
	query := r.URL.Query()
	from, end, ok := fixtureUsageWindow(query.Get("StartTime"), query.Get("EndTime"))
	if !ok || end.Sub(from) > 60*time.Hour || query.Get("InstanceId") != f.instance ||
		query.Get("RegionId") != f.region || query.Get("Period") != "600" {
		return false
	}
	rows := make([]map[string]any, 0, int(end.Sub(from)/(10*time.Minute))+1)
	for stamp := from; !stamp.After(end); stamp = stamp.Add(10 * time.Minute) {
		row := map[string]any{"InstanceId": f.fixtureID(mode), "TimeStamp": stamp.Format(time.RFC3339), "CPU": fixtureCPU(mode),
			"InternetRX": 4800, "InternetTX": 4800, "IntranetRX": 4800, "IntranetTX": 4800}
		if mode == idlePartial && stamp.Equal(from) {
			delete(row, "IntranetTX")
		}
		rows = append(rows, row)
	}
	testsupport.WriteJSON(t, w, map[string]any{"MonitorData": map[string]any{"InstanceMonitorData": rows}})
	return true
}

func (f *idleCloudFixture) serveHuawei(t *testing.T, w http.ResponseWriter, r *http.Request, mode int32) bool {
	if r.Host != "ces."+f.region+".myhuaweicloud.com" || r.Method != http.MethodPost ||
		r.URL.Path != "/V1.0/"+fixtureHuaweiProject+"/batch-query-metric-data" ||
		r.Header.Get("X-Auth-Token") != fixtureHuaweiIAM {
		return false
	}
	var input struct {
		Period, Filter string
		From, To       int64
		Metrics        []struct {
			Namespace  string
			Metric     string `json:"metric_name"`
			Dimensions []struct{ Name, Value string }
		}
	}
	if json.NewDecoder(r.Body).Decode(&input) != nil || input.Period != "3600" || input.Filter != "max" ||
		input.To-input.From != provider.IdleWindow.Milliseconds() || len(input.Metrics) != 3 {
		return false
	}
	known := map[string]bool{"cpu_util": true, "network_vm_bandwidth_in": true, "network_vm_bandwidth_out": true}
	metrics := make([]map[string]any, 0, 3)
	for _, item := range input.Metrics {
		if !known[item.Metric] || item.Namespace != "SYS.ECS" || len(item.Dimensions) != 1 ||
			item.Dimensions[0].Name != "instance_id" || item.Dimensions[0].Value != f.instance {
			return false
		}
		delete(known, item.Metric)
		unit, value := "Byte/s", 500.0
		if item.Metric == "cpu_util" {
			unit, value = "%", fixtureCPU(mode)
		}
		points := make([]map[string]any, 0, 168)
		for stamp := input.From; stamp < input.To; stamp += time.Hour.Milliseconds() {
			if mode != idlePartial || item.Metric != "network_vm_bandwidth_in" || stamp != input.From {
				points = append(points, map[string]any{"timestamp": stamp, "max": value})
			}
		}
		metrics = append(metrics, map[string]any{"namespace": "SYS.ECS", "metric_name": item.Metric,
			"unit": unit, "dimensions": []map[string]string{{"name": "instance_id", "value": f.fixtureID(mode)}}, "datapoints": points})
	}
	testsupport.WriteJSON(t, w, map[string]any{"metrics": metrics})
	return true
}

func fixtureCPU(mode int32) float64 {
	if mode == idleActive {
		return 90
	}
	return 2
}

func (f *idleCloudFixture) fixtureID(mode int32) string {
	if mode == idleForeign {
		return "foreign-" + strconv.Itoa(len(f.instance))
	}
	return f.instance
}

func fixtureUsageWindow(from, end string) (time.Time, time.Time, bool) {
	begin, a := time.Parse(time.RFC3339, from)
	finish, b := time.Parse(time.RFC3339, end)
	return begin, finish, a == nil && b == nil && begin.Before(finish) && begin.Equal(begin.Truncate(time.Hour)) && finish.Equal(finish.Truncate(time.Hour))
}
