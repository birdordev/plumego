package cloudtest

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"with-cloud-manager/app/integration/testsupport"
	"with-cloud-manager/internal/provider"
)

func (f *dnsDeletionFixture) serveDeletion(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	action := r.Header.Get("X-TC-Action")
	aliAction := r.Header.Get("X-ACS-Action")
	huaweiZone := "/v2/zones/" + f.id
	var deleting, valid bool
	switch {
	case action == "DeleteRecord":
		deleting = true
		body, err := io.ReadAll(r.Body)
		var input map[string]json.RawMessage
		valid = err == nil && r.Host == "dnspod.tencentcloudapi.com" && r.Method == http.MethodPost &&
			r.Header.Get("X-TC-Version") == "2021-03-23" && r.Header.Get("X-TC-Token") == fixtureLaunchSession &&
			validFixtureTencentSignature(r, body, fixtureLaunchKey, fixtureLaunchSecret) &&
			json.Unmarshal(body, &input) == nil && len(input) == 3 &&
			string(input["DomainId"]) == f.id && string(input["RecordId"]) == f.recordID() && string(input["Domain"]) == `"example.test"`
	case aliAction == "DeleteDomainRecord":
		deleting = true
		query := r.URL.Query()
		valid = r.Host == "alidns.aliyuncs.com" && r.Method == http.MethodPost && len(query) == 1 && query.Get("RecordId") == f.recordID() &&
			r.Header.Get("X-ACS-Version") == "2015-01-09" && r.Header.Get("X-ACS-Security-Token") == fixtureLaunchSession &&
			validFixtureAlibabaSignature(r, fixtureLaunchKey, fixtureLaunchSecret)
	case r.Method == http.MethodDelete:
		deleting = true
		valid = r.Host == "dns.cn-north-4.myhuaweicloud.com" && r.Header.Get("X-Auth-Token") == fixtureHuaweiIAM &&
			r.URL.Path == huaweiZone+"/recordsets/"+f.recordID() && r.URL.RawQuery == ""
	case r.Method == http.MethodGet && r.URL.Path == "/v2.1/zones/"+f.id+"/recordsets/"+f.recordID() &&
		r.Host == "dns.cn-north-4.myhuaweicloud.com":
		if r.Header.Get("X-Auth-Token") != fixtureHuaweiIAM {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		state, check := "ACTIVE", ""
		if f.lastDisabled.Load() {
			state = "DISABLE"
		}
		if f.attachedHealthCheck.Load() {
			check = "fixture-health-check"
		}
		testsupport.WriteJSON(t, w, map[string]any{"id": f.recordID(), "name": "www-100.example.test.", "type": "A",
			"status": state, "ttl": 60, "records": []string{"192.0.2.42"}, "zone_id": f.id, "zone_name": "example.test.",
			"project_id": fixtureHuaweiProject, "default": false, "line": "default_view", "health_check_id": check})
		return
	case r.Method == http.MethodGet && r.URL.Path == huaweiZone && r.Host == "dns.cn-north-4.myhuaweicloud.com":
		if r.Header.Get("X-Auth-Token") != fixtureHuaweiIAM {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		testsupport.WriteJSON(t, w, map[string]string{"id": f.id, "name": "example.test.", "zone_type": "public", "project_id": fixtureHuaweiProject})
		return
	}
	if deleting {
		if !valid || !f.lastDisabled.Load() {
			t.Errorf("unsafe DNS deletion: %s %s", r.Method, r.Host)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.deletions.Add(1)
		f.created.Store(&dnsFixtureCreatedRecord{DNSRecordSpec: provider.DNSRecordSpec{
			Name: "www-100.example.test", Type: "A", Value: "192.0.2.45", TTL: 60,
		}})
		f.lastDeleted.Store(true)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = conn.Close()
		return
	}
	if f.lastDeleted.Load() && f.lateDeletionPage(r, huaweiZone) {
		switch f.latePages.Add(1) {
		case 2:
			w.WriteHeader(http.StatusNotFound)
			return
		case 4:
			w.WriteHeader(http.StatusForbidden)
			return
		}
	}
	f.serveStatus(t, w, r)
}

func (f *dnsDeletionFixture) lateDeletionPage(r *http.Request, huaweiZone string) bool {
	switch f.vendor {
	case provider.TencentCVM:
		if r.Header.Get("X-TC-Action") != "DescribeRecordList" {
			return false
		}
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		var input struct{ Offset int }
		return json.Unmarshal(body, &input) == nil && input.Offset == 100
	case provider.AlibabaECS:
		return r.Header.Get("X-ACS-Action") == "DescribeDomainRecords" && r.URL.Query().Get("PageNumber") == "2"
	default:
		return r.URL.Path == huaweiZone+"/recordsets" && r.URL.Query().Get("offset") == "100"
	}
}
