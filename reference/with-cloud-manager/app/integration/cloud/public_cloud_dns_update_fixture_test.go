package cloudtest

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"

	"with-cloud-manager/app/integration/testsupport"
	"with-cloud-manager/internal/provider"
)

type dnsUpdateFixture struct {
	*dnsStatusFixture
	drift        atomic.Bool
	loseReceipt  atomic.Bool
	pendingRead  atomic.Bool
	seenPending  atomic.Bool
	healthCheck  atomic.Bool
	updateWrites atomic.Int32
}

func (f *dnsUpdateFixture) serveUpdate(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	zone := "/v2/zones/" + f.id
	record := "/v2.1/zones/" + f.id + "/recordsets/" + f.recordID()
	tc := r.Header.Get("X-TC-Action")
	ali := r.Header.Get("X-ACS-Action")
	switch {
	case tc == "ModifyRecordFields" || ali == "UpdateDomainRecord" || (r.Method == http.MethodPut && r.URL.Path == record):
		f.writeUpdate(t, w, r)
	case tc == "DescribeRecord" || ali == "DescribeDomainRecordInfo" || r.URL.Path == record:
		f.readUpdatedRecord(t, w, r)
	case f.vendor == provider.HuaweiECS && r.URL.Path == zone:
		if r.Method != http.MethodGet || r.Header.Get("X-Auth-Token") != fixtureHuaweiIAM || r.Host != "dns.cn-north-4.myhuaweicloud.com" {
			t.Error("unscoped DNS zone read")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		testsupport.WriteJSON(t, w, map[string]string{"id": f.id, "name": "example.test.", "zone_type": "public", "project_id": fixtureHuaweiProject})
	default:
		f.serveDNS(t, w, r)
	}
}

func (f *dnsUpdateFixture) writeUpdate(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	recordID := f.recordID()
	var update dnsFixtureUpdateRecord
	var valid bool
	switch f.vendor {
	case provider.TencentCVM:
		body, err := io.ReadAll(r.Body)
		var input map[string]json.RawMessage
		var fields []struct{ Key, Value string }
		valid = err == nil && json.Unmarshal(body, &input) == nil && len(input) == 4 &&
			json.Unmarshal(input["FieldList"], &fields) == nil && len(fields) == 2 &&
			fields[0].Key == "value" && fields[1].Key == "ttl" &&
			string(input["DomainId"]) == f.id && string(input["RecordId"]) == recordID && string(input["Domain"]) == `"example.test"` &&
			r.Method == http.MethodPost && r.Host == "dnspod.tencentcloudapi.com" && r.Header.Get("X-TC-Version") == "2021-03-23" &&
			r.Header.Get("X-TC-Token") == fixtureLaunchSession && validFixtureTencentSignature(r, body, fixtureLaunchKey, fixtureLaunchSecret)
		if valid {
			update.values = []string{fields[0].Value}
			update.ttl, _ = strconv.Atoi(fields[1].Value)
		}
	case provider.AlibabaECS:
		q := r.URL.Query()
		valid = len(q) == 6 && q.Get("RecordId") == recordID && q.Get("RR") == "www-100" &&
			q.Get("Type") == "A" && q.Get("Line") == "default" && r.Host == "alidns.aliyuncs.com" && r.Method == http.MethodPost &&
			r.Header.Get("X-ACS-Version") == "2015-01-09" && r.Header.Get("X-ACS-Security-Token") == fixtureLaunchSession &&
			validFixtureAlibabaSignature(r, fixtureLaunchKey, fixtureLaunchSecret)
		update.values = []string{q.Get("Value")}
		update.ttl, _ = strconv.Atoi(q.Get("TTL"))
	case provider.HuaweiECS:
		var input struct {
			Name, Type string
			TTL        int
			Records    []string
		}
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		valid = decoder.Decode(&input) == nil && input.Name == "www-100.example.test." && input.Type == "A" &&
			r.Host == "dns.cn-north-4.myhuaweicloud.com" && r.Header.Get("X-Auth-Token") == fixtureHuaweiIAM &&
			r.Header.Get("Content-Type") == "application/json"
		update.values, update.ttl = input.Records, input.TTL
	}
	if !valid || len(update.values) == 0 || update.ttl <= 0 {
		t.Error("invalid native DNS record update")
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	f.updateWrites.Add(1)
	f.lastUpdate.Store(&update)
	if f.loseReceipt.Load() {
		f.drift.Store(true)
		panic(http.ErrAbortHandler)
	}
	switch f.vendor {
	case provider.TencentCVM:
		testsupport.WriteJSON(t, w, map[string]any{"Response": map[string]any{"RecordId": 101, "RequestId": "dns-tc-update"}})
	case provider.AlibabaECS:
		testsupport.WriteJSON(t, w, map[string]string{"RecordId": recordID, "RequestId": "dns-ali-update"})
	case provider.HuaweiECS:
		f.pendingRead.Store(true)
		w.WriteHeader(http.StatusAccepted)
		testsupport.WriteJSON(t, w, map[string]string{"id": recordID, "zone_id": f.id, "project_id": fixtureHuaweiProject})
	}
}

func (f *dnsUpdateFixture) readUpdatedRecord(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	state := f.lastUpdate.Load()
	if state == nil {
		state = &dnsFixtureUpdateRecord{values: []string{"192.0.2.42"}, ttl: 60}
	}
	if f.drift.Load() {
		state = &dnsFixtureUpdateRecord{values: []string{"192.0.2.250"}, ttl: state.ttl}
	}
	recordID := f.recordID()
	switch f.vendor {
	case provider.TencentCVM:
		body, err := io.ReadAll(r.Body)
		if err != nil || !validFixtureTencentSignature(r, body, fixtureLaunchKey, fixtureLaunchSecret) {
			t.Error("invalid Tencent DNS read signature")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		domain, _ := strconv.Atoi(f.id)
		testsupport.WriteJSON(t, w, map[string]any{"Response": map[string]any{"RecordInfo": map[string]any{
			"Id": 101, "DomainId": domain, "SubDomain": "www-100", "RecordType": "A", "RecordLineId": "90=0",
			"Enabled": 1, "TTL": state.ttl, "Value": state.values[0],
		}}})
	case provider.AlibabaECS:
		if !validFixtureAlibabaSignature(r, fixtureLaunchKey, fixtureLaunchSecret) || r.URL.Query().Get("RecordId") != recordID {
			t.Error("invalid Alibaba DNS read signature or target")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		testsupport.WriteJSON(t, w, map[string]any{"DomainId": f.id, "RecordId": recordID, "RR": "www-100", "Type": "A",
			"Status": "Enable", "Line": "default", "TTL": state.ttl, "Value": state.values[0], "Locked": false})
	case provider.HuaweiECS:
		if r.Method != http.MethodGet || r.Header.Get("X-Auth-Token") != fixtureHuaweiIAM {
			t.Error("unscoped Huawei DNS record read")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		status, health := "ACTIVE", ""
		if f.pendingRead.Swap(false) {
			f.seenPending.Store(true)
			status = "PENDING_UPDATE"
		}
		if f.healthCheck.Load() {
			health = "fixture-health-check"
		}
		testsupport.WriteJSON(t, w, map[string]any{"id": recordID, "zone_id": f.id, "project_id": fixtureHuaweiProject,
			"zone_name": "example.test.", "name": "www-100.example.test.", "type": "A", "ttl": state.ttl, "records": state.values,
			"status": status, "default": false, "line": "default_view", "health_check_id": health})
	}
}
