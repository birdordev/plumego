package cloudtest

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"with-cloud-manager/internal/provider"
)

func (f publicCloudResourceFixture) serveZones(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	fail := func() bool {
		t.Errorf("incorrect zone inventory request %s %s", r.Host, r.URL.Path)
		w.WriteHeader(http.StatusBadRequest)
		return true
	}
	id := f.id(provider.Zone)
	var response any
	switch {
	case r.Host == "cvm.tencentcloudapi.com" && r.Header.Get("X-TC-Action") == "DescribeZones":
		body, err := io.ReadAll(r.Body)
		if err != nil || string(body) != "{}" || !validFixtureTencentSignature(r, body, f.key, f.secret) ||
			r.Header.Get("X-TC-Region") != f.region || r.Header.Get("X-TC-Token") != f.session ||
			r.Header.Get("X-TC-Version") != "2017-03-12" {
			return fail()
		}
		response = map[string]any{"Response": map[string]any{"TotalCount": 1, "ZoneSet": []any{map[string]string{
			"ZoneId": "100001", "Zone": id, "ZoneName": "fixture-zone", "ZoneState": "AVAILABLE",
		}}}}
	case r.Host == "ecs.cn-hangzhou.aliyuncs.com" && r.Header.Get("X-ACS-Action") == "DescribeZones":
		params := r.URL.Query()
		if len(params) != 1 || params.Get("RegionId") != f.region ||
			!validFixtureAlibabaSignature(r, f.key, f.secret) || r.Header.Get("X-ACS-Version") != "2014-05-26" {
			return fail()
		}
		response = map[string]any{"Zones": map[string]any{"Zone": []any{map[string]string{
			"ZoneId": id, "LocalName": "fixture-zone",
		}}}}
	case r.Host == "ecs.cn-north-4.myhuaweicloud.com" && r.URL.Path == "/v1/"+f.project+"/availability-zones":
		if r.Method != http.MethodGet || r.URL.RawQuery != "" || r.Header.Get("X-Auth-Token") != f.token {
			return fail()
		}
		response = map[string]any{"availability_zones": []any{map[string]string{
			"availability_zone_id": id, "alias": "fixture-zone", "type": "Center", "mode": "shared",
		}}}
	default:
		return false
	}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		t.Error(err)
	}
	return true
}
