package main

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"with-cloud-manager/internal/provider"
)

func (f publicCloudResourceFixture) serveVolumes(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	var result any
	fail := func() bool {
		t.Errorf("incorrect storage inventory request %s %s", r.Host, r.URL.Path)
		w.WriteHeader(http.StatusBadRequest)
		return true
	}
	id := f.id(provider.Volume)
	switch {
	case r.Host == "cbs.tencentcloudapi.com":
		body, err := io.ReadAll(r.Body)
		if err != nil || !validFixtureTencentSignature(r, body, f.key, f.secret) ||
			r.Header.Get("X-TC-Region") != f.region || r.Header.Get("X-TC-Token") != f.session ||
			r.Header.Get("X-TC-Action") != "DescribeDisks" || r.Header.Get("X-TC-Version") != "2017-03-12" {
			return fail()
		}
		var page struct{ Offset, Limit *int }
		if json.Unmarshal(body, &page) != nil || page.Offset == nil || page.Limit == nil || *page.Limit != 100 ||
			(*page.Offset != 0 && *page.Offset != 1) {
			return fail()
		}
		row := map[string]any{"DiskId": id, "DiskName": "fixture-volume", "DiskState": "ATTACHED",
			"InstanceId": "ins-1", "InstanceIdList": []string{"ins-1", "ins-2"}, "Placement": map[string]string{"Zone": f.region + "-1"}}
		if *page.Offset == 1 {
			row = map[string]any{"DiskId": "disk-2", "DiskState": "UNATTACHED"}
		}
		result = map[string]any{"Response": map[string]any{"TotalCount": 2, "DiskSet": []any{row}}}
	case r.Host == "ecs.cn-hangzhou.aliyuncs.com" && r.Header.Get("X-ACS-Action") == "DescribeDisks":
		const next = "disk+next/page=2"
		q := r.URL.Query()
		cursor := q.Get("NextToken")
		if (cursor != "" && cursor != next) || q.Get("MaxResults") != "100" || q.Get("RegionId") != f.region ||
			!validFixtureAlibabaSignature(r, f.key, f.secret) || r.Header.Get("X-ACS-Version") != "2014-05-26" {
			return fail()
		}
		token := next
		row := map[string]any{"DiskId": id, "DiskName": "fixture-volume", "Status": "In_use", "RegionId": f.region, "ZoneId": f.id(provider.Zone),
			"InstanceId": "i-1", "MountInstances": map[string]any{"MountInstance": []any{
				map[string]string{"InstanceId": "i-1"}, map[string]string{"InstanceId": "i-2"},
			}},
		}
		if cursor != "" {
			token = ""
			row = map[string]any{"DiskId": "d-2", "Status": "Available", "RegionId": f.region}
		}
		result = map[string]any{"NextToken": token, "Disks": map[string]any{"Disk": []any{row}}}
	case r.Host == "evs.cn-north-4.myhuaweicloud.com":
		path := "/v2/" + f.project
		q := r.URL.Query()
		marker := q.Get("marker")
		if r.Method != http.MethodGet || r.Header.Get("X-Auth-Token") != f.token ||
			r.URL.Path != path+"/cloudvolumes/detail" || q.Get("limit") != "100" || (marker != "" && marker != id) {
			return fail()
		}
		row := map[string]any{"id": id, "name": "fixture-volume", "status": "in-use", "os-vol-tenant-attr:tenant_id": f.project,
			"availability_zone": f.id(provider.Zone),
			"attachments": []any{
				map[string]string{"server_id": "00000000-0000-4000-8000-000000000001"},
				map[string]string{"server_id": "00000000-0000-4000-8000-000000000002"},
			},
		}
		links := []any{}
		if marker == "" {
			links = append(links, map[string]string{"rel": "next", "href": "https://volume.internal.example:8776" + path + "/volumes/detail?limit=100&marker=" + id})
		} else {
			row = map[string]any{"id": "40000000-0000-4000-8000-000000000002", "status": "available", "os-vol-tenant-attr:tenant_id": f.project}
		}
		result = map[string]any{"volumes": []any{row}, "volumes_links": links}
	default:
		return false
	}
	if err := json.NewEncoder(w).Encode(result); err != nil {
		t.Error(err)
	}
	return true
}
