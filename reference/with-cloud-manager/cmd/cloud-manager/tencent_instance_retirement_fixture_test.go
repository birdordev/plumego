package main

import (
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"sync/atomic"
	"testing"

	"with-cloud-manager/internal/provider"
)

const (
	retirementInstance = "ins-retire"
	retirementDisk     = "disk-retire"
	retirementReceipt  = "fixture-tencent-retirement-receipt"
)

type retirementFixture struct {
	vpcCreationFixture
	state      atomic.Value
	writes     atomic.Int32
	charge     atomic.Value
	protected  atomic.Bool
	extraDisk  atomic.Bool
	dropReply  atomic.Bool
	statusMode atomic.Int32
	pauseDisk  atomic.Bool
	diskRead   chan struct{}
	resumeDisk chan struct{}
}

func (f *retirementFixture) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if r.Host == "sts.tencentcloudapi.com" && f.tencent(t, w, r) {
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil || r.Method != http.MethodPost || r.URL.RequestURI() != "/" ||
		r.Header.Get("X-TC-Version") != "2017-03-12" || r.Header.Get("X-TC-Region") != f.region ||
		r.Header.Get("X-TC-Token") != fixtureLaunchSession || !validFixtureTencentSignature(r, body, fixtureLaunchKey, fixtureLaunchSecret) {
		t.Error("invalid Tencent retirement signature or scope")
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var payload struct {
		InstanceIds             []string
		ReleasePrepaidDataDisks *bool
		Filters                 []struct {
			Name   string
			Values []string
		}
		Limit int
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Error("invalid retirement request body")
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	action := r.Header.Get("X-TC-Action")
	cvm := r.Host == "cvm.tencentcloudapi.com" && slices.Equal(payload.InstanceIds, []string{retirementInstance})
	send := func(response any) { writeCloudFixtureJSON(t, w, map[string]any{"Response": response}) }
	switch {
	case action == "DescribeInstances" && cvm:
		state := f.state.Load().(string)
		items := []map[string]any{}
		if state != "absent" {
			items = append(items, map[string]any{"InstanceId": retirementInstance, "InstanceState": state,
				"InstanceChargeType": f.charge.Load(), "DisableApiTermination": f.protected.Load(),
				"SystemDisk": map[string]string{"DiskId": retirementDisk}, "DataDisks": []any{},
				"Placement": map[string]string{"Zone": f.region + "-1"}})
		}
		send(map[string]any{"InstanceSet": items, "TotalCount": len(items)})
	case action == "DescribeDisks" && r.Host == "cbs.tencentcloudapi.com" && payload.Limit == 2 &&
		len(payload.Filters) == 1 && payload.Filters[0].Name == "instance-id" && slices.Equal(payload.Filters[0].Values, []string{retirementInstance}):
		if f.pauseDisk.CompareAndSwap(true, false) {
			close(f.diskRead)
			select {
			case <-f.resumeDisk:
			case <-r.Context().Done():
				return
			}
		}
		count := 1
		if f.extraDisk.Load() {
			count = 2
		}
		items := []any{map[string]any{"DiskId": retirementDisk, "DiskUsage": "SYSTEM_DISK", "DiskState": "ATTACHED", "InstanceId": retirementInstance,
			"Placement": map[string]string{"Zone": f.region + "-1"}}}
		if count == 2 {
			items = append(items, map[string]any{"DiskId": "disk-other"})
		}
		send(map[string]any{"DiskSet": items, "TotalCount": count})
	case action == "TerminateInstances" && cvm && payload.ReleasePrepaidDataDisks != nil && !*payload.ReleasePrepaidDataDisks:
		f.writes.Add(1)
		if f.dropReply.Load() {
			panic(http.ErrAbortHandler)
		}
		f.state.Store("SHUTDOWN")
		send(map[string]string{"RequestId": retirementReceipt})
	case action == "DescribeInstancesStatus" && cvm:
		rows := []map[string]string{}
		switch f.statusMode.Load() {
		case 1:
			rows = append(rows, map[string]string{"InstanceId": retirementInstance, "InstanceState": "TERMINATING"})
		case 2:
			w.WriteHeader(http.StatusTooManyRequests)
			return
		case 3:
			rows = append(rows, map[string]string{"InstanceId": "ins-other", "InstanceState": "TERMINATING"})
		}
		send(map[string]any{"InstanceStatusSet": rows, "TotalCount": len(rows)})
	default:
		t.Errorf("unexpected retirement protocol action %s", action)
		w.WriteHeader(http.StatusBadRequest)
	}
}

func newRetirementFixture() *retirementFixture {
	f := &retirementFixture{
		vpcCreationFixture: vpcCreationFixture{vendor: provider.TencentCVM, account: "100000000001", region: "ap-guangzhou"},
		diskRead:           make(chan struct{}), resumeDisk: make(chan struct{}),
	}
	f.state.Store("STOPPED")
	f.charge.Store("POSTPAID_BY_HOUR")
	return f
}
